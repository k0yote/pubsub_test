#!/bin/bash

# 停止スクリプト - すべてのサービスを停止

echo "🛑 Stopping all services..."

# Read PIDs from files if they exist
if [ -f .pubsub.pid ]; then
    PUBSUB_PID=$(cat .pubsub.pid)
    kill $PUBSUB_PID 2>/dev/null && echo "✅ Stopped PubSub emulator (PID: $PUBSUB_PID)"
fi

if [ -f .blockchain.pid ]; then
    BLOCKCHAIN_PID=$(cat .blockchain.pid)
    kill $BLOCKCHAIN_PID 2>/dev/null && echo "✅ Stopped Blockchain service (PID: $BLOCKCHAIN_PID)"
fi

if [ -f .campaign.pid ]; then
    CAMPAIGN_PID=$(cat .campaign.pid)
    kill $CAMPAIGN_PID 2>/dev/null && echo "✅ Stopped Campaign service (PID: $CAMPAIGN_PID)"
fi

# Clean up by process name as backup
pkill -f "pubsub-emulator" 2>/dev/null
pkill -f "blockchain-service" 2>/dev/null
pkill -f "campaign-service" 2>/dev/null

# Remove PID files
rm -f .*.pid

echo "✅ All services stopped"