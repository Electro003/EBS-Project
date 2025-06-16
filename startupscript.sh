#!/bin/bash

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting EBS Distributed System Test${NC}"
echo "======================================="

echo -e "${YELLOW}Cleaning up old processes...${NC}"
pkill -f "broker -port" 2>/dev/null
pkill -f "publisher" 2>/dev/null
pkill -f "subscriber" 2>/dev/null
sleep 2

echo -e "${YELLOW}Building binaries...${NC}"
go build -o broker ./cmd/broker
go build -o publisher ./cmd/publisher
go build -o subscriber ./cmd/subscriber

echo -e "${GREEN}Starting Broker 1 (port 50051)...${NC}"
./broker -port 50051 &
BROKER1_PID=$!
sleep 2

echo -e "${GREEN}Starting Broker 2 (port 50052)...${NC}"
./broker -port 50052 &
BROKER2_PID=$!
sleep 2

echo -e "${GREEN}Starting Broker 3 (port 50053)...${NC}"
./broker -port 50053 &
BROKER3_PID=$!
sleep 3

echo -e "${YELLOW}Checking broker connections...${NC}"
sleep 5

echo -e "${GREEN}Starting Subscribers...${NC}"
./subscriber -broker localhost:50051 &
SUB1_PID=$!
sleep 1

./subscriber -broker localhost:50052 &
SUB2_PID=$!
sleep 1

./subscriber -broker localhost:50053 &
SUB3_PID=$!
sleep 2

echo -e "${GREEN}Starting Publisher...${NC}"
./publisher &
PUB_PID=$!


trap 'echo -e "\n${RED}Shutting down...${NC}"; kill $BROKER1_PID $BROKER2_PID $BROKER3_PID $SUB1_PID $SUB2_PID $SUB3_PID $PUB_PID 2>/dev/null; exit' INT

wait