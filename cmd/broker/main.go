package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"EBS-PROJECT/config"
	pb "EBS-PROJECT/ebs-project/pkg/ebs"
	"EBS-PROJECT/internal/matching"
	"EBS-PROJECT/routing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type brokerServer struct {
	pb.UnimplementedBrokerServiceServer
	mu                   sync.RWMutex
	address              string
	allBrokerAddresses   []string
	routingKeyField      string
	brokerClients        map[string]pb.BrokerServiceClient
	brokerConnections    map[string]*grpc.ClientConn // Track connections for cleanup
	simpleSubscriptions  map[string]*pb.SimpleSubscription
	complexSubscriptions map[string]*pb.ComplexSubscription
	windowBuffers        map[string][]*pb.Publication
	windowSize           int
	subscriberStreams    map[string]pb.BrokerService_SubscribeServer
}

func newServer(addr string, cfg *config.Config) *brokerServer {
	s := &brokerServer{
		address:              addr,
		allBrokerAddresses:   cfg.BrokerAddresses,
		routingKeyField:      cfg.RoutingKeyField,
		brokerClients:        make(map[string]pb.BrokerServiceClient),
		brokerConnections:    make(map[string]*grpc.ClientConn),
		simpleSubscriptions:  make(map[string]*pb.SimpleSubscription),
		complexSubscriptions: make(map[string]*pb.ComplexSubscription),
		windowBuffers:        make(map[string][]*pb.Publication),
		windowSize:           cfg.WindowSize,
		subscriberStreams:    make(map[string]pb.BrokerService_SubscribeServer),
	}

	// Start initial connection attempts
	s.connectToPeers()

	// Start background reconnection manager
	s.startPeerConnectionManager()

	return s
}

// Initial connection attempt
func (s *brokerServer) connectToPeers() {
	for _, addr := range s.allBrokerAddresses {
		if addr == s.address {
			continue
		}
		s.connectToPeer(addr)
	}
}

// Connect to a specific peer and notify them
func (s *brokerServer) connectToPeer(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already connected
	if _, ok := s.brokerClients[addr]; ok {
		return true
	}

	// Try to connect
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[%s] Failed to connect to peer broker %s: %v", s.address, addr, err)
		return false
	}

	client := pb.NewBrokerServiceClient(conn)
	s.brokerClients[addr] = client
	s.brokerConnections[addr] = conn
	log.Printf("[%s] Connected to peer broker %s", s.address, addr)

	// Notify the peer about our existence
	go s.notifyPeer(addr, client)

	return true
}

// Notify peer broker about our connection
func (s *brokerServer) notifyPeer(peerAddr string, client pb.BrokerServiceClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	peerInfo := &pb.PeerInfo{
		Address:     s.address,
		ConnectedAt: timestamppb.Now(),
	}

	_, err := client.NotifyPeerConnection(ctx, peerInfo)
	if err != nil {
		log.Printf("[%s] Failed to notify peer %s about connection: %v", s.address, peerAddr, err)
	} else {
		log.Printf("[%s] Successfully notified peer %s about connection", s.address, peerAddr)
	}
}

// Handle peer connection notification
func (s *brokerServer) NotifyPeerConnection(ctx context.Context, req *pb.PeerInfo) (*emptypb.Empty, error) {
	peerAddr := req.Address
	log.Printf("[%s] Received connection notification from peer %s", s.address, peerAddr)

	// Establish reverse connection if we don't have one
	s.mu.RLock()
	_, hasConnection := s.brokerClients[peerAddr]
	s.mu.RUnlock()

	if !hasConnection {
		log.Printf("[%s] Establishing reverse connection to peer %s", s.address, peerAddr)
		s.connectToPeer(peerAddr)
	}

	return &emptypb.Empty{}, nil
}

// Background manager to handle reconnections
func (s *brokerServer) startPeerConnectionManager() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			for _, addr := range s.allBrokerAddresses {
				if addr == s.address {
					continue
				}

				s.mu.RLock()
				_, connected := s.brokerClients[addr]
				s.mu.RUnlock()

				if !connected {
					log.Printf("[%s] Attempting to reconnect to peer %s", s.address, addr)
					s.connectToPeer(addr)
				}
			}
		}
	}()
}

// Get or create connection to peer
func (s *brokerServer) getPeerClient(addr string) (pb.BrokerServiceClient, error) {
	s.mu.RLock()
	client, ok := s.brokerClients[addr]
	s.mu.RUnlock()

	if ok {
		return client, nil
	}

	// Try to connect
	if s.connectToPeer(addr) {
		s.mu.RLock()
		client = s.brokerClients[addr]
		s.mu.RUnlock()
		return client, nil
	}

	return nil, fmt.Errorf("failed to connect to peer %s", addr)
}

// Publish - called only by publisher or another node to the owner broker
func (s *brokerServer) Publish(ctx context.Context, pub *pb.Publication) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Match simple subscriptions
	for _, sub := range s.simpleSubscriptions {
		if matching.CheckSimpleMatch(pub, sub) {
			if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
				notif := &pb.Notification{
					Content:      &pb.Notification_Publication{Publication: pub},
					DispatchedAt: timestamppb.Now(),
				}
				err := stream.Send(notif)
				if err != nil {
					log.Printf("[%s] Failed to send notification to subscriber %s: %v",
						s.address, sub.SubscriberId, err)
				}
			}
		}
	}

	// 2. Process complex subscriptions (windows)
	for _, sub := range s.complexSubscriptions {
		if pub.City == sub.IdentityConstraint.Value.GetStringValue() {
			identityKey := fmt.Sprintf("%s:%s", sub.IdentityConstraint.Field, pub.City)
			s.windowBuffers[identityKey] = append(s.windowBuffers[identityKey], pub)

			if len(s.windowBuffers[identityKey]) == s.windowSize {
				window := s.windowBuffers[identityKey]
				if ok, msg := matching.CheckWindowMatch(window, sub); ok {
					if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
						metaPub := &pb.MetaPublication{City: pub.City, Message: msg}
						notif := &pb.Notification{
							Content:      &pb.Notification_MetaPublication{MetaPublication: metaPub},
							DispatchedAt: timestamppb.Now(),
						}
						err := stream.Send(notif)
						if err != nil {
							log.Printf("[%s] Failed to send meta notification to subscriber %s: %v",
								s.address, sub.SubscriberId, err)
						}
					}
				}
				delete(s.windowBuffers, identityKey)
			}
		}
	}

	return &emptypb.Empty{}, nil
}

func (s *brokerServer) RegisterSimpleSubscription(ctx context.Context, sub *pb.SimpleSubscription) (*emptypb.Empty, error) {
	if len(sub.Constraints) == 0 {
		return &emptypb.Empty{}, nil
	}

	key := sub.Constraints[0].Value.GetStringValue()
	ownerAddr := routing.GetOwnerNode(key, s.allBrokerAddresses)

	if ownerAddr == s.address {
		s.ForwardSimpleSubscription(ctx, sub)
	} else {
		client, err := s.getPeerClient(ownerAddr)
		if err != nil {
			log.Printf("[%s] Failed to forward simple subscription to %s: %v", s.address, ownerAddr, err)
			return nil, status.Errorf(codes.Unavailable, "failed to connect to owner broker: %v", err)
		}
		_, err = client.ForwardSimpleSubscription(ctx, sub)
		if err != nil {
			log.Printf("[%s] Failed to forward simple subscription to %s: %v", s.address, ownerAddr, err)
			return nil, err
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) RegisterComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	key := sub.IdentityConstraint.Value.GetStringValue()
	ownerAddr := routing.GetOwnerNode(key, s.allBrokerAddresses)

	if ownerAddr == s.address {
		s.ForwardComplexSubscription(ctx, sub)
	} else {
		client, err := s.getPeerClient(ownerAddr)
		if err != nil {
			log.Printf("[%s] Failed to forward complex subscription to %s: %v", s.address, ownerAddr, err)
			return nil, status.Errorf(codes.Unavailable, "failed to connect to owner broker: %v", err)
		}
		_, err = client.ForwardComplexSubscription(ctx, sub)
		if err != nil {
			log.Printf("[%s] Failed to forward complex subscription to %s: %v", s.address, ownerAddr, err)
			return nil, err
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) ForwardSimpleSubscription(ctx context.Context, sub *pb.SimpleSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.simpleSubscriptions[sub.SubscriptionId] = sub
	s.mu.Unlock()
	log.Printf("[%s] Stored simple subscription %s", s.address, sub.SubscriptionId)
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) ForwardComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.complexSubscriptions[sub.SubscriptionId] = sub
	s.mu.Unlock()
	log.Printf("[%s] Stored complex subscription %s", s.address, sub.SubscriptionId)
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) Subscribe(req *pb.SubscriptionStreamRequest, stream pb.BrokerService_SubscribeServer) error {
	s.mu.Lock()
	s.subscriberStreams[req.SubscriberId] = stream
	s.mu.Unlock()

	log.Printf("[%s] Subscriber %s connected.", s.address, req.SubscriberId)

	<-stream.Context().Done()

	s.mu.Lock()
	delete(s.subscriberStreams, req.SubscriberId)
	s.mu.Unlock()

	log.Printf("[%s] Subscriber %s disconnected.", s.address, req.SubscriberId)
	return nil
}

// Cleanup connections on shutdown
func (s *brokerServer) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for addr, conn := range s.brokerConnections {
		err := conn.Close()
		if err != nil {
			log.Printf("[%s] Error closing connection to %s: %v", s.address, addr, err)
		}
	}
}

func main() {
	var port string
	flag.StringVar(&port, "port", "50051", "The server port")
	flag.Parse()

	addr := "localhost:" + port

	cfg, err := config.LoadConfig("config/config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	broker := newServer(addr, cfg)
	pb.RegisterBrokerServiceServer(grpcServer, broker)

	log.Printf("Broker server listening at %v", lis.Addr())

	// Handle graceful shutdown
	defer broker.cleanup()

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
