# 🚀 Microservices Token Distribution System

**Production-grade microservices architecture** using PubSub for the gaming industry.
This system implements asynchronous ERC20 token distribution to campaign participants.

## 🏗️ Architecture

```
┌─────────────────┐    PubSub     ┌─────────────────┐
│                 │ ───────────── │                 │
│ Campaign Service│               │Blockchain Service│
│                 │ ←─────────── │                 │
│ Port: 8080     │               │ Port: 8081     │
└─────────────────┘               └─────────────────┘
         │                                 │
         │                                 │
         └─────────────────┬───────────────┘
                           │
                ┌─────────────────┐
                │                 │
                │ PubSub Emulator │
                │                 │
                │ Port: 8681     │
                └─────────────────┘
```

## 📦 Service Architecture

### 🎯 Campaign Service (`/campaign`)
- **Role**: Token grant request processing, result handling, notification management
- **Port**: 8080
- **Endpoints**:
  - `POST /token-request` - Token grant request
  - `GET /status` - User status check

### ⛓️ Blockchain Service (`/blockchain`)
- **Role**: ERC20 token minting, transaction processing
- **Port**: 8081
- **Endpoints**:
  - `GET /health` - Health check
  - `GET /stats` - Service statistics

### 📡 PubSub Topics & Subscriptions
- **Topics**:
  - `token-grant-requests` - Token grant requests
  - `token-grant-results` - Token grant results
- **Subscriptions**:
  - `token-grant-requests-sub` - For Blockchain Service
  - `token-grant-results-sub` - For Campaign Service

## 🚀 Getting Started

### 1. Prerequisites
```bash
# Start PubSub Emulator
docker-compose up -d

# Install dependencies (first time only)
cd campaign && go mod tidy
cd ../blockchain && go mod tidy
```

### 2. Service Startup

**Terminal 1 - Campaign Service**
```bash
cd campaign
go run main.go
```

**Terminal 2 - Blockchain Service**
```bash
cd blockchain
go run main.go
```

### 3. Run Tests

**Terminal 3 - Automated Tests**
```bash
./test_services.sh
```

## 🧪 Manual Testing

### Basic Operations

1. **Service Health Check**
```bash
curl http://localhost:8080/status?user_id=test
curl http://localhost:8081/health
```

2. **Token Grant Request**
```bash
curl -X POST "http://localhost:8080/token-request?user_id=alice&campaign_id=summer"
```

3. **Check Results**
```bash
curl "http://localhost:8080/status?user_id=alice"
```

### PubSub Monitoring

1. **List Topics**
```bash
curl "http://localhost:8681/v1/projects/test-project/topics"
```

2. **List Subscriptions**
```bash
curl "http://localhost:8681/v1/projects/test-project/subscriptions"
```

3. **Topic Details**
```bash
curl "http://localhost:8681/v1/projects/test-project/topics/token-grant-requests"
```

## 🔄 Processing Flow

1. **Campaign Service**: User participates in campaign
2. **PubSub**: Send message to `token-grant-requests` topic
3. **Blockchain Service**: Receive message, process ERC20 token minting
4. **PubSub**: Send result to `token-grant-results` topic
5. **Campaign Service**: Receive result, update database, notify user

## 📊 Message Format

### Token Grant Request
```json
{
  "event_type": "token.grant.requested",
  "user_id": "user123",
  "campaign_id": "summer_campaign",
  "token_amount": 100,
  "token_type": "ERC20",
  "request_id": "req_1234567890",
  "timestamp": "2024-07-15T04:30:00Z",
  "retry_count": 0
}
```

### Token Grant Result
```json
{
  "event_type": "token.grant.completed",
  "user_id": "user123",
  "campaign_id": "summer_campaign",
  "request_id": "req_1234567890",
  "transaction_hash": "0x1234567890abcdef",
  "timestamp": "2024-07-15T04:30:00Z",
  "processed_at": "2024-07-15T04:30:05Z"
}
```

## 🔧 Configuration

### Environment Variables
- `PUBSUB_EMULATOR_HOST`: localhost:8681
- `GCP_PROJECT_ID`: test-project

### Port Configuration
- Campaign Service: 8080
- Blockchain Service: 8081
- PubSub Emulator: 8681

### Docker Configuration
- **Dynamic Resource Creation**: Topics and subscriptions are automatically created at service startup
- **Health Check**: PubSub Emulator monitoring
- **Auto Recovery**: Automatic restart on failure

## 🎯 Production Considerations

### 1. Availability & Redundancy
- Multiple instances of each service
- Load balancer deployment
- Health check functionality

### 2. Monitoring & Logging
- Structured log output
- Metrics collection
- Distributed tracing

### 3. Security
- Authentication & authorization
- TLS communication
- Rate limiting

### 4. Scalability
- Horizontal scaling
- Distributed message processing
- Caching functionality

## 🛠️ Development & Operations

### Local Development

**Quick Start - Fastest Development Environment Setup**
```bash
# Development environment setup (batch)
make dev

# Start each service in separate terminals
make run-campaign    # Terminal 1
make run-blockchain  # Terminal 2
make test           # Terminal 3
```

**Individual Commands**
```bash
# Development environment setup
make dev             # PubSub Emulator + dependency installation

# Service startup
make run-campaign    # Campaign Service
make run-blockchain  # Blockchain Service

# Testing & monitoring
make test           # Run automated tests
make status         # Check service status
make monitor        # Check PubSub Topics/Subscriptions
make logs           # Check PubSub Emulator logs

# Stop & cleanup
make stop           # Stop all services
make clean          # Complete cleanup
```

### Available Make Tasks
```bash
make help           # Show all available commands
make dev            # Setup development environment
make build          # Install dependencies
make run-campaign   # Start Campaign Service
make run-blockchain # Start Blockchain Service
make test           # Run automated tests
make status         # Check service status
make monitor        # PubSub monitoring
make logs           # Check logs
make stop           # Stop all services
make clean          # Complete cleanup
```

### Deployment
```bash
# Docker build
docker build -t campaign-service ./campaign
docker build -t blockchain-service ./blockchain

# Kubernetes deploy
kubectl apply -f k8s/
```

## 📈 Performance Metrics

| Metric | Target | Notes |
|--------|--------|-------|
| Average Processing Time | < 3 seconds | Until token grant completion |
| Availability | 99.9% | Monthly downtime < 43 minutes |
| Throughput | 1000 req/sec | Peak time support |
| Error Rate | < 0.1% | Successful transaction rate |

## 📚 References

- [Google Cloud PubSub Documentation](https://cloud.google.com/pubsub/docs)
- [Microservices Design Patterns](./architecture_recommendations.md)
- [Event-Driven Architecture](https://microservices.io/patterns/data/event-driven-architecture.html)

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## 📝 License

This project is licensed under the MIT License.

---

**Happy Coding! 🎮⚡** 