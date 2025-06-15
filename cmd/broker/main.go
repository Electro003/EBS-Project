package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"

	"EBS-PROJECT/config"
	"EBS-PROJECT/internal/matching"
	pb "EBS-PROJECT/proto"
	"EBS-PROJECT/routing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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
		simpleSubscriptions:  make(map[string]*pb.SimpleSubscription),
		complexSubscriptions: make(map[string]*pb.ComplexSubscription),
		windowBuffers:        make(map[string][]*pb.Publication),
		windowSize:           cfg.WindowSize,
		subscriberStreams:    make(map[string]pb.BrokerService_SubscribeServer),
	}
	s.connectToPeers()
	return s
}

func (s *brokerServer) connectToPeers() {
	for _, addr := range s.allBrokerAddresses {
		if addr == s.address {
			continue
		}
		conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("[%s] Failed to connect to peer broker %s: %v", s.address, addr, err)
			continue
		}
		s.brokerClients[addr] = pb.NewBrokerServiceClient(conn)
		log.Printf("[%s] Connected to peer broker %s", s.address, addr)
	}
}

// Publish - apelat doar de publisher sau un alt nod catre broker-ul proprietar
func (s *brokerServer) Publish(ctx context.Context, pub *pb.Publication) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Potrivire pentru subscriptii simple
	for _, sub := range s.simpleSubscriptions {
		if matching.CheckSimpleMatch(pub, sub) {
			if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
				notif := &pb.Notification{Content: &pb.Notification_Publication{Publication: pub}, DispatchedAt: timestamppb.Now()}
				stream.Send(notif)
			}
		}
	}

	// 2. Procesare pentru subscriptii complexe (ferestre)
	for _, sub := range s.complexSubscriptions {
		if pub.City == sub.IdentityConstraint.Value.GetStringValue() {
			identityKey := fmt.Sprintf("%s:%s", sub.IdentityConstraint.Field, pub.City)
			s.windowBuffers[identityKey] = append(s.windowBuffers[identityKey], pub)
			if len(s.windowBuffers[identityKey]) == s.windowSize {
				window := s.windowBuffers[identityKey]
				if ok, msg := matching.CheckWindowMatch(window, sub); ok {
					if stream, ok := s.subscriberStreams[sub.SubscriberId]; ok {
						metaPub := &pb.MetaPublication{City: pub.City, Message: msg}
						notif := &pb.Notification{Content: &pb.Notification_MetaPublication{MetaPublication: metaPub}, DispatchedAt: timestamppb.Now()}
						stream.Send(notif)
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
	} else if client, ok := s.brokerClients[ownerAddr]; ok {
		client.ForwardSimpleSubscription(ctx, sub)
	}
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) RegisterComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	key := sub.IdentityConstraint.Value.GetStringValue()
	ownerAddr := routing.GetOwnerNode(key, s.allBrokerAddresses)

	if ownerAddr == s.address {
		s.ForwardComplexSubscription(ctx, sub)
	} else if client, ok := s.brokerClients[ownerAddr]; ok {
		client.ForwardComplexSubscription(ctx, sub)
	}
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) ForwardSimpleSubscription(ctx context.Context, sub *pb.SimpleSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.simpleSubscriptions[sub.SubscriptionId] = sub
	s.mu.Unlock()
	return &emptypb.Empty{}, nil
}

func (s *brokerServer) ForwardComplexSubscription(ctx context.Context, sub *pb.ComplexSubscription) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.complexSubscriptions[sub.SubscriptionId] = sub
	s.mu.Unlock()
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
