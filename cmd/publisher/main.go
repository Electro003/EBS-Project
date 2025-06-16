package main

import (
	"context"
	"log"
	"math/rand"
	"time"

	"EBS-PROJECT/config"
	pb "EBS-PROJECT/ebs-project/pkg/ebs"
	"EBS-PROJECT/internal/generator"
	"EBS-PROJECT/routing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	log.Println("Publisher starting...")

	cfg, err := config.LoadConfig("config/config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	brokerClients := make(map[string]pb.BrokerServiceClient)
	for _, addr := range cfg.BrokerAddresses {
		conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("Warning: did not connect to broker %s: %v", addr, err)
			continue
		}
		defer conn.Close()
		brokerClients[addr] = pb.NewBrokerServiceClient(conn)
	}

	if len(brokerClients) == 0 {
		log.Fatalf("Could not connect to any brokers. Exiting.")
	}

	log.Printf("Connected to %d brokers. Starting publication feed.", len(brokerClients))

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.EvaluationDurationSeconds)*time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Duration(cfg.PublicationIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	var publicationsSent int64
	for {
		select {
		case <-ticker.C:
			pub := generator.GeneratePublication(cfg, r)
			pub.CreatedAt = timestamppb.Now()
			ownerAddr := routing.GetOwnerNode(pub.City, cfg.BrokerAddresses)
			if client, ok := brokerClients[ownerAddr]; ok {
				_, err := client.Publish(context.Background(), pub)
				if err == nil {
					publicationsSent++
				}
			}

		case <-ctx.Done():
			log.Printf("Publication feed finished after %d seconds.", cfg.EvaluationDurationSeconds)
			log.Printf("Total publications sent: %d", publicationsSent)
			return
		}
	}
}
