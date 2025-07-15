# Microservices Token Distribution System - Makefile

.PHONY: help dev build test clean run-campaign run-blockchain stop logs status

# Default target
help:
	@echo "🚀 Microservices Token Distribution System"
	@echo "==========================================="
	@echo ""
	@echo "Available targets:"
	@echo "  dev            - Setup development environment"
	@echo "  build          - Install dependencies for all services"
	@echo "  run-campaign   - Run Campaign Service"
	@echo "  run-blockchain - Run Blockchain Service"
	@echo "  test           - Run automated tests"
	@echo "  status         - Check service status"
	@echo "  logs           - Show PubSub emulator logs"
	@echo "  clean          - Stop all services and clean up"
	@echo "  stop           - Stop all services"
	@echo ""
	@echo "Usage: make <target>"

# Development environment setup
dev:
	@echo "🏗️  Setting up development environment..."
	@docker-compose up -d
	@echo "⏳ Waiting for PubSub emulator to be ready..."
	@sleep 3
	@echo "📦 Installing dependencies..."
	@cd campaign && go mod tidy
	@cd blockchain && go mod tidy
	@echo "✅ Development environment ready!"
	@echo ""
	@echo "Next steps:"
	@echo "  Terminal 1: make run-campaign"
	@echo "  Terminal 2: make run-blockchain"
	@echo "  Terminal 3: make test"

# Install dependencies
build:
	@echo "📦 Installing dependencies..."
	@cd campaign && go mod tidy
	@cd blockchain && go mod tidy
	@echo "✅ Dependencies installed!"

# Run Campaign Service
run-campaign:
	@echo "🎯 Starting Campaign Service..."
	@cd campaign && go run main.go

# Run Blockchain Service  
run-blockchain:
	@echo "⛓️  Starting Blockchain Service..."
	@cd blockchain && go run main.go

# Run automated tests
test:
	@echo "🧪 Running Hybrid REST + PubSub Test Suite..."
	@if [ ! -f ./test_services.sh ]; then \
		echo "❌ Test script not found!"; \
		exit 1; \
	fi
	@./test_services.sh

# Check service status
status:
	@echo "📊 Service Status:"
	@echo "=================="
	@echo ""
	@echo "🐳 Docker Services:"
	@docker-compose ps
	@echo ""
	@echo "🌐 HTTP Endpoints:"
	@echo "  Campaign Service:  http://localhost:8080/status?user_id=test"
	@echo "  Blockchain Service: http://localhost:8081/health"
	@echo "  Blockchain API:    http://localhost:8081/submit-transaction"
	@echo "  Transaction Status: http://localhost:8081/transaction-status?hash=<tx_hash>"
	@echo "  PubSub Emulator:   http://localhost:8681/v1/projects/test-project/topics"
	@echo ""
	@echo "🔍 Quick Health Check:"
	@curl -s http://localhost:8080/status?user_id=test > /dev/null 2>&1 && echo "✅ Campaign Service: Running" || echo "❌ Campaign Service: Not running"
	@curl -s http://localhost:8081/health > /dev/null 2>&1 && echo "✅ Blockchain Service: Running" || echo "❌ Blockchain Service: Not running"
	@curl -s http://localhost:8681/v1/projects/test-project/topics > /dev/null 2>&1 && echo "✅ PubSub Emulator: Running" || echo "❌ PubSub Emulator: Not running"

# Show PubSub emulator logs
logs:
	@echo "📜 PubSub Emulator Logs:"
	@docker-compose logs -f pubsub_emulator

# Stop all services
stop:
	@echo "🛑 Stopping all services..."
	@docker-compose down
	@echo "✅ All services stopped!"

# Clean up everything
clean: stop
	@echo "🧹 Cleaning up..."
	@docker-compose down --volumes --remove-orphans
	@docker system prune -f
	@echo "✅ Cleanup completed!"

# Quick development workflow
quick-start: dev
	@echo "🚀 Quick start completed!"
	@echo ""
	@echo "Services are running:"
	@echo "  📡 PubSub Emulator: http://localhost:8681"
	@echo ""
	@echo "Ready to start microservices:"
	@echo "  make run-campaign   (in Terminal 1)"
	@echo "  make run-blockchain (in Terminal 2)"
	@echo "  make test          (in Terminal 3)"

# Monitor PubSub topics
monitor:
	@echo "📡 PubSub Topics & Subscriptions:"
	@echo "================================"
	@echo ""
	@echo "📋 Topics:"
	@curl -s "http://localhost:8681/v1/projects/test-project/topics" | jq . 2>/dev/null || echo "No topics found or jq not installed"
	@echo ""
	@echo "📬 Subscriptions:"
	@curl -s "http://localhost:8681/v1/projects/test-project/subscriptions" | jq . 2>/dev/null || echo "No subscriptions found or jq not installed" 