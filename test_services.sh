#!/bin/bash

echo "🧪 Testing Microservices PubSub System"
echo "======================================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test function
test_endpoint() {
    local method=$1
    local url=$2
    local description=$3
    
    echo -e "\n${YELLOW}Testing: $description${NC}"
    echo "Command: curl -X $method $url"
    
    if [ "$method" = "GET" ]; then
        response=$(curl -s -w "HTTPSTATUS:%{http_code}" "$url")
    else
        response=$(curl -s -w "HTTPSTATUS:%{http_code}" -X "$method" "$url")
    fi
    
    http_code=$(echo "$response" | tr -d '\n' | sed -e 's/.*HTTPSTATUS://')
    body=$(echo "$response" | sed -e 's/HTTPSTATUS\:.*//g')
    
    if [ "$http_code" -eq 200 ]; then
        echo -e "${GREEN}✅ Success (HTTP $http_code)${NC}"
        echo "Response: $body"
    else
        echo -e "${RED}❌ Failed (HTTP $http_code)${NC}"
        echo "Response: $body"
    fi
}

# Wait for services to start
echo -e "\n${YELLOW}Waiting for services to start...${NC}"
sleep 2

# Test 1: Check service health
echo -e "\n${YELLOW}=== Testing Service Health ===${NC}"
test_endpoint "GET" "http://localhost:8080/status?user_id=test" "Campaign Service Status"
test_endpoint "GET" "http://localhost:8081/health" "Blockchain Service Health"
test_endpoint "GET" "http://localhost:8081/stats" "Blockchain Service Stats"

# Test 2: PubSub Topics and Subscriptions
echo -e "\n${YELLOW}=== Testing PubSub Infrastructure ===${NC}"
echo -e "\n${YELLOW}Checking PubSub Topics:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/topics" | jq '.'

echo -e "\n${YELLOW}Checking PubSub Subscriptions:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/subscriptions" | jq '.'

# Test 3: Token Grant Flow
echo -e "\n${YELLOW}=== Testing Token Grant Flow ===${NC}"

# Send token grant request
echo -e "\n${YELLOW}Sending token grant request...${NC}"
test_endpoint "POST" "http://localhost:8080/token-request?user_id=user123&campaign_id=summer" "Token Grant Request"

# Wait for processing
echo -e "\n${YELLOW}Waiting for blockchain processing...${NC}"
sleep 5

# Check user status
echo -e "\n${YELLOW}Checking user status...${NC}"
test_endpoint "GET" "http://localhost:8080/status?user_id=user123" "User Status Check"

# Test 4: Multiple Users
echo -e "\n${YELLOW}=== Testing Multiple Users ===${NC}"
users=("alice" "bob" "charlie")
for user in "${users[@]}"; do
    echo -e "\n${YELLOW}Processing user: $user${NC}"
    test_endpoint "POST" "http://localhost:8080/token-request?user_id=$user&campaign_id=daily_quest" "Token Grant for $user"
    sleep 1
done

# Wait for all processing
echo -e "\n${YELLOW}Waiting for all transactions to process...${NC}"
sleep 8

# Check all user statuses
echo -e "\n${YELLOW}Checking all user statuses:${NC}"
for user in "${users[@]}"; do
    test_endpoint "GET" "http://localhost:8080/status?user_id=$user" "Status for $user"
done

# Test 5: PubSub Message Monitoring
echo -e "\n${YELLOW}=== PubSub Message Monitoring ===${NC}"
echo -e "\n${YELLOW}Token Grant Requests Topic:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/topics/token-grant-requests" | jq '.'

echo -e "\n${YELLOW}Token Grant Results Topic:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/topics/token-grant-results" | jq '.'

echo -e "\n${YELLOW}Requests Subscription:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/subscriptions/token-grant-requests-sub" | jq '.'

echo -e "\n${YELLOW}Results Subscription:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/subscriptions/token-grant-results-sub" | jq '.'

echo -e "\n${GREEN}🎉 Testing completed!${NC}"
echo -e "\n${YELLOW}Services are running:${NC}"
echo "- Campaign Service: http://localhost:8080"
echo "- Blockchain Service: http://localhost:8081"
echo "- PubSub Emulator: http://localhost:8681"
echo "- PubSub UI: http://localhost:7200" 