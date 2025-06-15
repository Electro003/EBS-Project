package main

import (
	"context"
	"flag"
	"io"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"EBS-PROJECT/config"
	"EBS-PROJECT/internal/generator"
	pb "EBS-PROJECT/proto"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type stats struct {
	simpleNotificationsRx  atomic.Int64
	complexNotificationsRx atomic.Int64
	totalLatencyNs         atomic.Int64
}

func main() {
	var brokerAddr string
	flag.StringVar(&brokerAddr, "broker", "localhost:50051", "Address of the broker to connect to")
	flag.Parse()

	subscriberID := uuid.New().String()
	log.Printf("Subscriber %s starting, connecting to %s", subscriberID, brokerAddr)

	cfg, err := config.LoadConfig("config/config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	conn, err := grpc.Dial(brokerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("[%s] did not connect: %v", subscriberID, err)
	}
	defer conn.Close()
	client := pb.NewBrokerServiceClient(conn)

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	simpleSubs, complexSubs := generator.GenerateSubscriptions(subscriberID, cfg.TotalSubscriptionsToGenerate/len(cfg.BrokerAddresses), cfg, r)

	for _, sub := range simpleSubs {
		sub.SubscriptionId = uuid.New().String()
		client.RegisterSimpleSubscription(context.Background(), sub)
	}
	for _, sub := range complexSubs {
		sub.SubscriptionId = uuid.New().String()
		client.RegisterComplexSubscription(context.Background(), sub)
	}
	log.Printf("[%s] Registered %d simple and %d complex subscriptions.", subscriberID, len(simpleSubs), len(complexSubs))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Subscribe(ctx, &pb.SubscriptionStreamRequest{SubscriberId: subscriberID})
	if err != nil {
		log.Fatalf("[%s] Failed to subscribe: %v", subscriberID, err)
	}

	stats := &stats{}
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			notif, err := stream.Recv()
			if err != nil {
				if status.Code(err) == codes.Canceled || err == io.EOF {
				} else {
					log.Printf("[%s] Error receiving notification: %v", subscriberID, err)
				}
				return
			}
			if pub := notif.GetPublication(); pub != nil {
				stats.simpleNotificationsRx.Add(1)
				latency := time.Since(pub.CreatedAt.AsTime())
				stats.totalLatencyNs.Add(latency.Nanoseconds())
			} else if metaPub := notif.GetMetaPublication(); metaPub != nil {
				stats.complexNotificationsRx.Add(1)
			}
		}
	}()

	log.Printf("[%s] Listening for notifications for %d seconds...", subscriberID, cfg.EvaluationDurationSeconds)
	time.Sleep(time.Duration(cfg.EvaluationDurationSeconds) * time.Second)

	log.Printf("[%s] Evaluation time finished. Shutting down stream.", subscriberID)
	cancel()
	wg.Wait()

	totalSimple := stats.simpleNotificationsRx.Load()
	log.Printf("--- STATS for %s ---", subscriberID)
	log.Printf("Total simple notifications received: %d", totalSimple)
	log.Printf("Total complex notifications received: %d", stats.complexNotificationsRx.Load())
	if totalSimple > 0 {
		avgLatency := time.Duration(stats.totalLatencyNs.Load() / totalSimple)
		log.Printf("Average latency: %v", avgLatency)
	}
	log.Printf("--- END STATS for %s ---", subscriberID)
}
