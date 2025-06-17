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

// brokerServer holds the state for a broker node.
type brokerServer struct {
	pb.UnimplementedBrokerServiceServer
	mu                 sync.RWMutex
	address            string
	allBrokerAddresses []string
	routingKeyField    string
	brokerClients      map[string]pb.BrokerServiceClient
	brokerConnections  map[string]*grpc.ClientConn

	// Stores subscriptions that this broker is the "owner" of.
	simpleSubscriptions  map[string]*pb.SimpleSubscription
	complexSubscriptions map[string]*pb.ComplexSubscription

	windowBuffers map[string][]*pb.Publication
	windowSize    int

	// Maps a subscriber ID to the active gRPC stream for that subscriber.
	// This map only contains subscribers directly connected to THIS broker.
	subscriberStreams map[string]pb.BrokerService_SubscribeServer

	// Maps a subscriber ID to a list of its subscription IDs.
	// This is now populated on ALL brokers that hold subscriptions for the subscriber,
	// not just the origin broker. This enables efficient cleanup.
	subscriberToSimpleSubs  map[string][]string
	subscriberToComplexSubs map[string][]string
}

func newServer(addr string, cfg *config.Config) *brokerServer {
	s := &brokerServer{
		address:                 addr,
		allBrokerAddresses:      cfg.BrokerAddresses,
		routingKeyField:         cfg.RoutingKeyField,
		brokerClients:           make(map[string]pb.BrokerServiceClient),
		brokerConnections:       make(map[string]*grpc.ClientConn),
		simpleSubscriptions:     make(map[string]*pb.SimpleSubscription),
		complexSubscriptions:    make(map[string]*pb.ComplexSubscription),
		windowBuffers:           make(map[string][]*pb.Publication),
		windowSize:              cfg.WindowSize,
		subscriberStreams:       make(map[string]pb.BrokerService_SubscribeServer),
		subscriberToSimpleSubs:  make(map[string][]string),
		subscriberToComplexSubs: make(map[string][]string),
	}

	s.connectToPeers()

	return s
}

func (s *brokerServer) connectToPeers() {
	for _, addr := range s.allBrokerAddresses {
		if addr == s.address {
			continue // Don't connect to self
		}
		go s.managePeerConnection(addr)
	}
}

func (s *brokerServer) managePeerConnection(addr string) {
	for {
		s.mu.RLock()
		_, connected := s.brokerClients[addr]
		s.mu.RUnlock()

		if !connected {
			log.Printf("[%s] Attempting to connect to peer %s", s.address, addr)
			conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			if err != nil {
				log.Printf("[%s] Failed to connect to peer %s, will retry: %v", s.address, addr, err)
				time.Sleep(5 * time.Second) // Wait before retrying
				continue
			}

			s.mu.Lock()
			s.brokerClients[addr] = pb.NewBrokerServiceClient(conn)
			s.brokerConnections[addr] = conn
			s.mu.Unlock()
			log.Printf("[%s] Successfully connected to peer broker %s", s.address, addr)
		}
		time.Sleep(15 * time.Second)
	}
}

// getPeerClient retrieves a gRPC client for a peer, ensuring a connection exists.
func (s *brokerServer) getPeerClient(addr string) (pb.BrokerServiceClient, error) {
	s.mu.RLock()
	client, ok := s.brokerClients[addr]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no connection to peer %s", addr)
	}
	return client, nil
}

// Publish handles incoming publications from the publisher.
func (s *brokerServer) Publish(ctx context.Context, pub *pb.Publication) (*emptypb.Empty, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matchedLocal := 0
	matchedRemote := 0

	// 1. Match simple subscriptions
	for _, sub := range s.simpleSubscriptions {
		if matching.CheckSimpleMatch(pub, sub) {
			notif := &pb.Notification{
				Content:      &pb.Notification_Publication{Publication: pub},
				DispatchedAt: timestamppb.Now(),
			}

			if sub.OriginBroker == s.address {
				// Subscriber is local, send directly
				if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
					if err := stream.Send(notif); err == nil {
						matchedLocal++
					}
				}
			} else {
				// Subscriber is remote, forward to its origin broker
				matchedRemote++
				go s.forwardNotificationToBroker(sub.OriginBroker, sub.SubscriberId, notif)
			}
		}
	}

	// 2. Process complex subscriptions (window-based)
	for _, sub := range s.complexSubscriptions {
		if pub.City == sub.IdentityConstraint.Value.GetStringValue() {
			identityKey := fmt.Sprintf("%s:%s", sub.IdentityConstraint.Field, pub.City)

			s.mu.RUnlock() // Release read lock to acquire write lock
			s.mu.Lock()
			s.windowBuffers[identityKey] = append(s.windowBuffers[identityKey], pub)

			var windowToProcess []*pb.Publication
			if len(s.windowBuffers[identityKey]) == s.windowSize {
				windowToProcess = s.windowBuffers[identityKey]
				delete(s.windowBuffers, identityKey) // Tumbling window
			}
			s.mu.Unlock()
			s.mu.RLock()

			if windowToProcess != nil {
				if ok, msg := matching.CheckWindowMatch(windowToProcess, sub); ok {
					metaPub := &pb.MetaPublication{City: pub.City, Message: msg}
					notif := &pb.Notification{
						Content:      &pb.Notification_MetaPublication{MetaPublication: metaPub},
						DispatchedAt: timestamppb.Now(),
					}

					if sub.OriginBroker == s.address {
						if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
							stream.Send(notif)
						}
					} else {
						go s.forwardNotificationToBroker(sub.OriginBroker, sub.SubscriberId, notif)
					}
				}
			}
		}
	}

	if matchedLocal > 0 || matchedRemote > 0 {
		log.Printf("[%s] Publication for %s matched %d local and %d remote subscriptions",
			s.address, pub.City, matchedLocal, matchedRemote)
	}

	return &emptypb.Empty{}, nil
}

// forwardNotificationToBroker sends a matched notification to a subscriber's origin broker.
func (s *brokerServer) forwardNotificationToBroker(originBroker, subscriberId string, notif *pb.Notification) {
	client, err := s.getPeerClient(originBroker)
	if err != nil {
		log.Printf("[%s] Failed to get client for origin broker %s: %v", s.address, originBroker, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.ForwardNotification(ctx, &pb.ForwardedNotification{
		SubscriberId: subscriberId,
		Notification: notif,
	})

	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			log.Printf("[%s] Subscriber %s not found on origin broker %s. Triggering reactive cleanup.", s.address, subscriberId, originBroker)
			s.cleanupRemoteSubscriber(subscriberId)
		} else {
			log.Printf("[%s] Failed to forward notification to broker %s for subscriber %s: %v",
				s.address, originBroker, subscriberId, err)
		}
	}
}

// ForwardNotification is the RPC handler for receiving a forwarded notification from an owner broker.
func (s *brokerServer) ForwardNotification(ctx context.Context, req *pb.ForwardedNotification) (*emptypb.Empty, error) {
	s.mu.RLock()
	stream, ok := s.subscriberStreams[req.SubscriberId]
	s.mu.RUnlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "subscriber %s not found", req.SubscriberId)
	}

	if err := stream.Send(req.Notification); err != nil {
		log.Printf("[%s] Failed to send forwarded notification to subscriber %s: %v",
			s.address, req.SubscriberId, err)
		return nil, status.Errorf(codes.Internal, "failed to send notification: %v", err)
	}

	return &emptypb.Empty{}, nil
}

// RegisterSimpleSubscription handles a subscription request from a client.
func (s *brokerServer) RegisterSimpleSubscription(ctx context.Context, sub *pb.SimpleSubscription) (*emptypb.Empty, error) {
	key := sub.Constraints[0].Value.GetStringValue() // Assuming city is always the first constraint for routing
	ownerAddr := routing.GetOwnerNode(key, s.allBrokerAddresses)

	sub.OriginBroker = s.address // Tag the subscription with its origin

	if ownerAddr == s.address {
		return s.ForwardSimpleSubscription(ctx, sub)
	}

	client, err := s.getPeerClient(ownerAddr)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to get client for owner broker %s: %v", ownerAddr, err)
	}

	_, err = client.ForwardSimpleSubscription(ctx, sub)
	return &emptypb.Empty{}, err
}

// ForwardSimpleSubscription stores a subscription for which this broker is the owner.
func (s *brokerServer) ForwardSimpleSubscription(ctx context.Context, sub *pb.SimpleSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.simpleSubscriptions[sub.SubscriptionId] = sub
	s.subscriberToSimpleSubs[sub.SubscriberId] = append(s.subscriberToSimpleSubs[sub.SubscriberId], sub.SubscriptionId)

	log.Printf("[%s] Stored simple subscription %s for subscriber %s (origin: %s)",
		s.address, sub.SubscriptionId, sub.SubscriberId, sub.OriginBroker)
	return &emptypb.Empty{}, nil
}

// RegisterComplexSubscription handles a subscription request from a client.
func (s *brokerServer) RegisterComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	key := sub.IdentityConstraint.Value.GetStringValue()
	ownerAddr := routing.GetOwnerNode(key, s.allBrokerAddresses)

	sub.OriginBroker = s.address

	if ownerAddr == s.address {
		return s.ForwardComplexSubscription(ctx, sub)
	}

	client, err := s.getPeerClient(ownerAddr)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "failed to connect to owner broker %s: %v", ownerAddr, err)
	}

	_, err = client.ForwardComplexSubscription(ctx, sub)
	return &emptypb.Empty{}, err
}

// ForwardComplexSubscription stores a subscription for which this broker is the owner.
func (s *brokerServer) ForwardComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.complexSubscriptions[sub.SubscriptionId] = sub
	s.subscriberToComplexSubs[sub.SubscriberId] = append(s.subscriberToComplexSubs[sub.SubscriberId], sub.SubscriptionId)

	log.Printf("[%s] Stored complex subscription %s for subscriber %s (origin: %s)",
		s.address, sub.SubscriptionId, sub.SubscriberId, sub.OriginBroker)
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) Subscribe(req *pb.SubscriptionStreamRequest, stream pb.BrokerService_SubscribeServer) error {
	s.mu.Lock()
	s.subscriberStreams[req.SubscriberId] = stream
	s.mu.Unlock()

	log.Printf("[%s] Subscriber %s connected and stream established", s.address, req.SubscriberId)

	<-stream.Context().Done()

	s.mu.Lock()
	delete(s.subscriberStreams, req.SubscriberId)
	s.mu.Unlock()

	log.Printf("[%s] Subscriber %s disconnected.", s.address, req.SubscriberId)

	// PROACTIVE CLEANUP: Notify all other brokers that this subscriber is gone.
	s.cleanupLocalSubscriberAndNotifyPeers(req.SubscriberId)

	return nil
}

func (s *brokerServer) cleanupLocalSubscriberAndNotifyPeers(subscriberId string) {
	log.Printf("[%s] Broadcasting unsubscribe request for disconnected subscriber %s", s.address, subscriberId)

	req := &pb.UnsubscribeRequest{SubscriberId: subscriberId}

	s.mu.RLock()
	peers := s.brokerClients
	s.mu.RUnlock()

	for peerAddr, client := range peers {
		go func(addr string, c pb.BrokerServiceClient) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Unsubscribe(ctx, req); err != nil {
				log.Printf("[%s] Failed to send unsubscribe request to peer %s: %v", s.address, addr, err)
			}
		}(peerAddr, client)
	}
}

func (s *brokerServer) Unsubscribe(ctx context.Context, req *pb.UnsubscribeRequest) (*emptypb.Empty, error) {
	log.Printf("[%s] Received unsubscribe request for subscriber %s", s.address, req.SubscriberId)
	s.cleanupRemoteSubscriber(req.SubscriberId)
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) cleanupRemoteSubscriber(subscriberId string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	simpleSubIds, okSimple := s.subscriberToSimpleSubs[subscriberId]
	if okSimple {
		for _, subId := range simpleSubIds {
			delete(s.simpleSubscriptions, subId)
		}
		delete(s.subscriberToSimpleSubs, subscriberId)
		log.Printf("[%s] Cleaned up %d simple subscriptions for subscriber %s.", s.address, len(simpleSubIds), subscriberId)
	}

	complexSubIds, okComplex := s.subscriberToComplexSubs[subscriberId]
	if okComplex {
		for _, subId := range complexSubIds {
			delete(s.complexSubscriptions, subId)
		}
		delete(s.subscriberToComplexSubs, subscriberId)
		log.Printf("[%s] Cleaned up %d complex subscriptions for subscriber %s.", s.address, len(complexSubIds), subscriberId)
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

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
