#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting EBS Distributed System Test${NC}"
echo "======================================="

echo -e "${YELLOW}Cleaning up old processes...${NC}"
pkill -f "broker -port" 2>/dev/null
pkill -f "publisher" 2>/dev/null
pkill -f "subscriber" 2>/dev/null

sleep 3

echo -e "${YELLOW}Clearing port bindings...${NC}"
for port in 50051 50052 50053; do
    lsof -ti:$port | xargs kill -9 2>/dev/null || true
done

sleep 2

echo -e "${YELLOW}Building binaries...${NC}"
go build -o broker ./cmd/broker
go build -o publisher ./cmd/publisher
go build -o subscriber ./cmd/subscriber

echo -e "${GREEN}Starting Broker Network...${NC}"
echo "=========================="

echo -e "${BLUE}Starting Broker 1 (port 50051)...${NC}"
./broker -port 50051 2>&1 | sed 's/^/[BROKER-50051] /' &
BROKER1_PID=$!
sleep 1

echo -e "${BLUE}Starting Broker 2 (port 50052)...${NC}"
./broker -port 50052 2>&1 | sed 's/^/[BROKER-50052] /' &
BROKER2_PID=$!
sleep 1

echo -e "${BLUE}Starting Broker 3 (port 50053)...${NC}"
./broker -port 50053 2>&1 | sed 's/^/[BROKER-50053] /' &
BROKER3_PID=$!

echo -e "${YELLOW}Waiting for brokers to establish mesh connections...${NC}"
sleep 5

echo -e "${GREEN}Starting Subscribers...${NC}"
echo "====================="

echo -e "${BLUE}Starting Subscriber 1 (connecting to 50051)...${NC}"
./subscriber -broker localhost:50051 2>&1 | sed 's/^/[SUB-50051] /' &
SUB1_PID=$!
sleep 3 

echo -e "${BLUE}Starting Subscriber 2 (connecting to 50052)...${NC}"
./subscriber -broker localhost:50052 2>&1 | sed 's/^/[SUB-50052] /' &
SUB2_PID=$!
sleep 3  

echo -e "${BLUE}Starting Subscriber 3 (connecting to 50053)...${NC}"
./subscriber -broker localhost:50053 2>&1 | sed 's/^/[SUB-50053] /' &
SUB3_PID=$!
sleep 3  

echo -e "${GREEN}Starting Publisher...${NC}"
echo "=================="
./publisher 2>&1 | sed 's/^/[PUBLISHER] /' &
PUB_PID=$!

echo ""
echo -e "${GREEN}System Status:${NC}"
echo "============="
echo -e "Brokers:     ${BLUE}3 running${NC}"
echo -e "Subscribers: ${BLUE}3 running${NC}"
echo -e "Publisher:   ${BLUE}1 running${NC}"
echo ""
echo -e "${YELLOW}System running... Press Ctrl+C to stop${NC}"
echo ""
echo "Expected behavior:"
echo "1. Each subscriber connects to a different broker"
echo "2. Subscriptions are forwarded to owner brokers"
echo "3. Notifications are forwarded back to origin brokers"
echo "4. All subscribers receive their matching notifications"
echo ""
echo "Watch for error messages about missing subscribers."
echo "These should NOT appear with proper forwarding."
echo ""

cleanup() {
    echo -e "\n${RED}Shutting down...${NC}"
    
    kill $BROKER1_PID $BROKER2_PID $BROKER3_PID 2>/dev/null
    kill $SUB1_PID $SUB2_PID $SUB3_PID 2>/dev/null
    kill $PUB_PID 2>/dev/null
    
    sleep 1
    
    kill -9 $BROKER1_PID $BROKER2_PID $BROKER3_PID 2>/dev/null
    kill -9 $SUB1_PID $SUB2_PID $SUB3_PID 2>/dev/null
    kill -9 $PUB_PID 2>/dev/null
    
    echo -e "${GREEN}Cleanup complete${NC}"
    exit
}

trap cleanup INT

wait