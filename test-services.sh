#!/bin/bash

# テストスクリプト - サービスの動作確認

echo "🧪 Testing services..."
echo ""

# Health checks
echo "📋 Health Checks:"
echo -n "  Campaign Service: "
curl -s http://localhost:8080/health | jq -r '.status' || echo "FAILED"

echo -n "  Blockchain Service: "
curl -s http://localhost:8081/health | jq -r '.status' || echo "FAILED"

echo ""
echo "📊 Service Statistics:"
echo "  Campaign Service stats:"
curl -s http://localhost:8080/stats | jq '.request_metrics' || echo "FAILED"

echo ""
echo "  Blockchain Service stats:"
curl -s http://localhost:8081/stats | jq '.pubsub_metrics' || echo "FAILED"

echo ""
echo "🔧 Testing Token Grant (REST):"
RESPONSE=$(curl -s -X POST "http://localhost:8080/token-request?user_id=user123&campaign_id=summer&amount=100")
echo "  Response: $RESPONSE"
TX_HASH=$(echo $RESPONSE | jq -r '.transaction_hash')
echo "  Transaction Hash: $TX_HASH"

echo ""
echo "⏳ Waiting 5 seconds for transaction to complete..."
sleep 5

echo ""
echo "📄 Checking transaction status:"
curl -s "http://localhost:8081/transaction-status?hash=$TX_HASH" | jq '.'

echo ""
echo "👤 Checking user status:"
curl -s "http://localhost:8080/status?user_id=user123" | jq '.summary'

echo ""
echo "🔧 Testing Token Exchange (PubSub):"
curl -s -X POST "http://localhost:8080/exchange-request?user_id=user456&campaign_id=winter&from_token_type=ERC20&to_token_type=GOLD&from_amount=50&exchange_rate=2.5&method=pubsub"

echo ""
echo "⏳ Waiting 5 seconds for PubSub processing..."
sleep 5

echo ""
echo "👤 Checking user456 status:"
curl -s "http://localhost:8080/status?user_id=user456" | jq '.summary'

echo ""
echo "✅ Test completed!"