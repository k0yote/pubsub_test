#!/bin/bash

echo "🧪 Testing Unified Blockchain Services (Token Grant + Exchange)"
echo "============================================================="

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
test_endpoint "POST" "http://localhost:8080/token-request?user_id=alice&campaign_id=summer&amount=150" "Token Grant Request"

# Wait for processing
echo -e "\n${YELLOW}Waiting for blockchain processing...${NC}"
sleep 5

# Check user status
echo -e "\n${YELLOW}Checking user status...${NC}"
test_endpoint "GET" "http://localhost:8080/status?user_id=alice" "User Status Check"

# Test 4: Token Exchange Flow
echo -e "\n${YELLOW}=== Testing Token Exchange Flow ===${NC}"

# Send token exchange request
echo -e "\n${YELLOW}Sending token exchange request...${NC}"
test_endpoint "POST" "http://localhost:8080/exchange-request?user_id=bob&campaign_id=summer&from_token_type=ERC20&to_token_type=GOLD&from_amount=200&exchange_rate=1.5" "Token Exchange Request"

# Wait for processing
echo -e "\n${YELLOW}Waiting for exchange processing...${NC}"
sleep 6

# Check user status
echo -e "\n${YELLOW}Checking exchange result...${NC}"
test_endpoint "GET" "http://localhost:8080/status?user_id=bob" "Exchange Status Check"

# Test 5: Mixed Operations
echo -e "\n${YELLOW}=== Testing Mixed Operations ===${NC}"
users=("charlie" "david" "eve")
operations=("grant" "exchange" "grant")

for i in "${!users[@]}"; do
    user=${users[$i]}
    operation=${operations[$i]}
    
    echo -e "\n${YELLOW}Processing user: $user (operation: $operation)${NC}"
    
    if [ "$operation" = "grant" ]; then
        test_endpoint "POST" "http://localhost:8080/token-request?user_id=$user&campaign_id=mixed_test&amount=100" "Token Grant for $user"
    else
        test_endpoint "POST" "http://localhost:8080/exchange-request?user_id=$user&campaign_id=mixed_test&from_token_type=ERC20&to_token_type=SILVER&from_amount=50&exchange_rate=2.0" "Token Exchange for $user"
    fi
    
    sleep 2
done

# Wait for all processing
echo -e "\n${YELLOW}Waiting for all operations to complete...${NC}"
sleep 10

# Check all user statuses
echo -e "\n${YELLOW}Checking all user statuses:${NC}"
for user in "${users[@]}"; do
    test_endpoint "GET" "http://localhost:8080/status?user_id=$user" "Status for $user"
done

# Test 6: Stress Test - Multiple Concurrent Operations
echo -e "\n${YELLOW}=== Stress Test - Concurrent Operations ===${NC}"
echo -e "\n${YELLOW}Launching concurrent requests...${NC}"

# Launch multiple requests in parallel
for i in {1..5}; do
    echo "Launching grant request $i..."
    curl -s -X POST "http://localhost:8080/token-request?user_id=stress_user_$i&campaign_id=stress_test&amount=50" &
done

for i in {1..3}; do
    echo "Launching exchange request $i..."
    curl -s -X POST "http://localhost:8080/exchange-request?user_id=exchange_user_$i&campaign_id=stress_test&from_token_type=ERC20&to_token_type=DIAMOND&from_amount=75&exchange_rate=0.8" &
done

# Wait for all background processes
wait

echo -e "\n${YELLOW}Waiting for stress test processing...${NC}"
sleep 15

# Check stress test results
echo -e "\n${YELLOW}Checking stress test results:${NC}"
for i in {1..5}; do
    test_endpoint "GET" "http://localhost:8080/status?user_id=stress_user_$i" "Stress Grant User $i"
done

for i in {1..3}; do
    test_endpoint "GET" "http://localhost:8080/status?user_id=exchange_user_$i" "Stress Exchange User $i"
done

# Test 7: PubSub Message Monitoring
echo -e "\n${YELLOW}=== PubSub Message Monitoring ===${NC}"
echo -e "\n${YELLOW}Blockchain Requests Topic:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/topics/blockchain-requests" | jq '.'

echo -e "\n${YELLOW}Blockchain Results Topic:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/topics/blockchain-results" | jq '.'

echo -e "\n${YELLOW}Requests Subscription:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-requests-sub" | jq '.'

echo -e "\n${YELLOW}Results Subscription:${NC}"
curl -s "http://localhost:8681/v1/projects/test-project/subscriptions/blockchain-results-sub" | jq '.'

# Test 8: Error Handling Tests
echo -e "\n${YELLOW}=== Error Handling Tests ===${NC}"

# Test missing parameters
echo -e "\n${YELLOW}Testing missing parameters...${NC}"
test_endpoint "POST" "http://localhost:8080/token-request?user_id=error_test" "Missing Campaign ID"
test_endpoint "POST" "http://localhost:8080/exchange-request?user_id=error_test&campaign_id=error_test" "Missing Exchange Parameters"

# Test invalid parameters
echo -e "\n${YELLOW}Testing invalid parameters...${NC}"
test_endpoint "POST" "http://localhost:8080/token-request?user_id=invalid_test&campaign_id=invalid_test&amount=abc" "Invalid Amount Parameter"
test_endpoint "POST" "http://localhost:8080/exchange-request?user_id=invalid_test&campaign_id=invalid_test&from_token_type=ERC20&to_token_type=GOLD&from_amount=xyz&exchange_rate=abc" "Invalid Exchange Parameters"

echo -e "\n${GREEN}🎉 Testing completed!${NC}"
echo -e "\n${YELLOW}Services are running:${NC}"
echo "- Campaign Service: http://localhost:8080"
echo "- Blockchain Service: http://localhost:8081"
echo "- PubSub Emulator: http://localhost:8681"
echo ""
echo -e "${YELLOW}New Features Tested:${NC}"
echo "✅ Token Grant Operations"
echo "✅ Token Exchange Operations"
echo "✅ Mixed Operations"
echo "✅ Concurrent Processing"
echo "✅ Error Handling"
echo "✅ Unified PubSub Topics" 