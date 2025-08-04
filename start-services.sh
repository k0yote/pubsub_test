#!/bin/bash

# 起動スクリプト - PubSubエミュレータと両サービスを適切な順序で起動

echo "🚀 Starting services..."

# Kill any existing processes
echo "🔄 Cleaning up existing processes..."
pkill -f "blockchain-service" || true
pkill -f "campaign-service" || true
pkill -f "pubsub-emulator" || true

sleep 2

echo "📦 Ensuring Pub/Sub emulator is installed..."
gcloud components install pubsub-emulator --quiet || {
    echo "❌ Failed to install pubsub-emulator"
    exit 1
}

# Start PubSub emulator
echo "📡 Starting PubSub emulator..."
gcloud beta emulators pubsub start --project=test-project --host-port=localhost:8681 > pubsub-emulator.log 2>&1 &
PUBSUB_PID=$!

# Wait for emulator to be ready
echo "⏳ Waiting for PubSub emulator to be ready..."
sleep 5

# Test if emulator is running
if ! nc -z localhost 8681; then
    echo "❌ PubSub emulator failed to start!"
    exit 1
fi

echo "✅ PubSub emulator is ready!"

# Set environment variable
export PUBSUB_EMULATOR_HOST=localhost:8681

# Start Blockchain Service first (it creates the subscriptions)
echo "⛓️  Starting Blockchain Service..."
cd blockchain && go run main.go > blockchain-service.log 2>&1 &
BLOCKCHAIN_PID=$!

# Wait for Blockchain Service to initialize
echo "⏳ Waiting for Blockchain Service to initialize..."
sleep 5

# Check if Blockchain Service is running
if ! curl -s http://localhost:8081/health > /dev/null; then
    echo "❌ Blockchain Service failed to start!"
    kill $PUBSUB_PID
    exit 1
fi

echo "✅ Blockchain Service is ready!"

# Start Campaign Service
echo "🎯 Starting Campaign Service..."
cd campaign && go run main.go > campaign-service.log 2>&1 &
CAMPAIGN_PID=$!

# Wait for Campaign Service to initialize
echo "⏳ Waiting for Campaign Service to initialize..."
sleep 5

# Check if Campaign Service is running
if ! curl -s http://localhost:8080/health > /dev/null; then
    echo "❌ Campaign Service failed to start!"
    kill $PUBSUB_PID $BLOCKCHAIN_PID
    exit 1
fi

echo "✅ Campaign Service is ready!"

echo ""
echo "🎉 All services started successfully!"
echo ""
echo "📊 Service endpoints:"
echo "  Campaign Service:    http://localhost:8080"
echo "  Blockchain Service:  http://localhost:8081"
echo "  PubSub Emulator:     localhost:8681"
echo ""
echo "📝 Logs:"
echo "  tail -f pubsub-emulator.log"
echo "  tail -f blockchain-service.log"
echo "  tail -f campaign-service.log"
echo ""
echo "🛑 To stop all services: ./stop-services.sh"
echo ""
echo "Process IDs:"
echo "  PubSub Emulator: $PUBSUB_PID"
echo "  Blockchain Service: $BLOCKCHAIN_PID"
echo "  Campaign Service: $CAMPAIGN_PID"

# Save PIDs for stop script
echo $PUBSUB_PID > .pubsub.pid
echo $BLOCKCHAIN_PID > .blockchain.pid
echo $CAMPAIGN_PID > .campaign.pid

# Keep script running
echo ""
echo "Press Ctrl+C to stop all services..."
trap "kill $PUBSUB_PID $BLOCKCHAIN_PID $CAMPAIGN_PID; rm -f .*.pid; exit" INT TERM
wait