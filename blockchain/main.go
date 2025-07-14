package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
)

// Event Types
const (
	// Token Grant Events
	TokenGrantRequested = "token.grant.requested"
	TokenGrantCompleted = "token.grant.completed"
	TokenGrantFailed    = "token.grant.failed"

	// Token Exchange Events
	TokenExchangeRequested = "token.exchange.requested"
	TokenExchangeCompleted = "token.exchange.completed"
	TokenExchangeFailed    = "token.exchange.failed"
)

// Unified request structure for both grant and exchange
type BlockchainRequest struct {
	EventType  string    `json:"event_type"`
	UserID     string    `json:"user_id"`
	CampaignID string    `json:"campaign_id"`
	RequestID  string    `json:"request_id"`
	Timestamp  time.Time `json:"timestamp"`
	RetryCount int       `json:"retry_count"`

	// Token Grant specific fields
	TokenAmount *int64 `json:"token_amount,omitempty"`
	TokenType   string `json:"token_type,omitempty"`

	// Token Exchange specific fields
	FromTokenType *string  `json:"from_token_type,omitempty"`
	ToTokenType   *string  `json:"to_token_type,omitempty"`
	ExchangeRate  *float64 `json:"exchange_rate,omitempty"`
	FromAmount    *int64   `json:"from_amount,omitempty"`
	ToAmount      *int64   `json:"to_amount,omitempty"`
}

// Unified result structure
type BlockchainResult struct {
	EventType       string    `json:"event_type"`
	UserID          string    `json:"user_id"`
	CampaignID      string    `json:"campaign_id"`
	RequestID       string    `json:"request_id"`
	TransactionHash string    `json:"transaction_hash,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	ProcessedAt     time.Time `json:"processed_at"`

	// Token Grant specific result fields
	GrantedAmount    *int64 `json:"granted_amount,omitempty"`
	GrantedTokenType string `json:"granted_token_type,omitempty"`

	// Token Exchange specific result fields
	ExchangedFromAmount *int64   `json:"exchanged_from_amount,omitempty"`
	ExchangedToAmount   *int64   `json:"exchanged_to_amount,omitempty"`
	ActualExchangeRate  *float64 `json:"actual_exchange_rate,omitempty"`
}

// BlockchainService handles both ERC20 token minting and exchange processing
type BlockchainService struct {
	client      *pubsub.Client
	requestSub  *pubsub.Subscription
	resultTopic *pubsub.Topic
}

func NewBlockchainService(client *pubsub.Client) *BlockchainService {
	return &BlockchainService{
		client:      client,
		requestSub:  client.Subscription("blockchain-requests-sub"),
		resultTopic: client.Topic("blockchain-results"),
	}
}

func (bs *BlockchainService) ProcessRequests(ctx context.Context) {
	log.Println("⛓️  Blockchain Service: Listening for blockchain requests...")

	bs.requestSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		var request BlockchainRequest
		if err := json.Unmarshal(msg.Data, &request); err != nil {
			log.Printf("Failed to unmarshal request: %v", err)
			msg.Nack()
			return
		}

		log.Printf("📨 Received request: %s for user %s", request.EventType, request.UserID)

		// Route to appropriate processor based on event type
		var result BlockchainResult
		switch request.EventType {
		case TokenGrantRequested:
			result = bs.processTokenGrant(request)
		case TokenExchangeRequested:
			result = bs.processTokenExchange(request)
		default:
			log.Printf("❌ Unknown event type: %s", request.EventType)
			msg.Nack()
			return
		}

		// Publish result
		bs.publishResult(ctx, result)
		msg.Ack()
	})
}

func (bs *BlockchainService) processTokenGrant(request BlockchainRequest) BlockchainResult {
	log.Printf("🔨 Processing token grant for user %s (amount: %d)",
		request.UserID, *request.TokenAmount)

	// Simulate blockchain transaction processing time (1-3 seconds)
	time.Sleep(time.Duration(rand.Intn(3)+1) * time.Second)

	// Simulate transaction success/failure (90% success rate)
	success := rand.Float32() < 0.9

	result := BlockchainResult{
		UserID:      request.UserID,
		CampaignID:  request.CampaignID,
		RequestID:   request.RequestID,
		Timestamp:   request.Timestamp,
		ProcessedAt: time.Now(),
	}

	if success {
		result.EventType = TokenGrantCompleted
		result.TransactionHash = fmt.Sprintf("0x%x", rand.Int63())
		result.GrantedAmount = request.TokenAmount
		result.GrantedTokenType = request.TokenType
		log.Printf("✅ Token grant completed: %s (amount: %d)", result.TransactionHash, *result.GrantedAmount)
	} else {
		result.EventType = TokenGrantFailed
		result.ErrorMessage = "Insufficient gas or network congestion"
		log.Printf("❌ Token grant failed: %s", result.ErrorMessage)
	}

	return result
}

func (bs *BlockchainService) processTokenExchange(request BlockchainRequest) BlockchainResult {
	log.Printf("🔄 Processing token exchange for user %s (%s -> %s)",
		request.UserID, *request.FromTokenType, *request.ToTokenType)

	// Simulate exchange processing time (2-4 seconds, longer than grant)
	time.Sleep(time.Duration(rand.Intn(3)+2) * time.Second)

	// Simulate exchange success/failure (85% success rate, slightly lower than grant)
	success := rand.Float32() < 0.85

	result := BlockchainResult{
		UserID:      request.UserID,
		CampaignID:  request.CampaignID,
		RequestID:   request.RequestID,
		Timestamp:   request.Timestamp,
		ProcessedAt: time.Now(),
	}

	if success {
		result.EventType = TokenExchangeCompleted
		result.TransactionHash = fmt.Sprintf("0x%x", rand.Int63())
		result.ExchangedFromAmount = request.FromAmount
		result.ExchangedToAmount = request.ToAmount
		result.ActualExchangeRate = request.ExchangeRate
		log.Printf("✅ Token exchange completed: %s (%d %s -> %d %s)",
			result.TransactionHash, *result.ExchangedFromAmount, *request.FromTokenType,
			*result.ExchangedToAmount, *request.ToTokenType)
	} else {
		result.EventType = TokenExchangeFailed
		result.ErrorMessage = "Exchange rate volatility or liquidity issues"
		log.Printf("❌ Token exchange failed: %s", result.ErrorMessage)
	}

	return result
}

func (bs *BlockchainService) publishResult(ctx context.Context, result BlockchainResult) {
	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("Failed to marshal result: %v", err)
		return
	}

	publishResult := bs.resultTopic.Publish(ctx, &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"event_type":  result.EventType,
			"user_id":     result.UserID,
			"campaign_id": result.CampaignID,
			"request_id":  result.RequestID,
		},
	})

	_, err = publishResult.Get(ctx)
	if err != nil {
		log.Printf("Failed to publish result: %v", err)
		return
	}

	log.Printf("📤 Result published for user %s: %s", result.UserID, result.EventType)
}

// HTTP handlers for testing
func (bs *BlockchainService) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Blockchain Service is healthy"))
}

func (bs *BlockchainService) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"service":              "blockchain",
		"status":               "running",
		"uptime":               time.Since(startTime).String(),
		"supported_operations": []string{"token_grant", "token_exchange"},
	}

	data, err := json.Marshal(stats)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

var startTime time.Time

// setupPubSubResources creates required topics and subscriptions
func setupPubSubResources(client *pubsub.Client, ctx context.Context) error {
	// Create unified topics
	topics := []string{"blockchain-requests", "blockchain-results"}
	for _, topicName := range topics {
		topic := client.Topic(topicName)
		exists, err := topic.Exists(ctx)
		if err != nil {
			return err
		}
		if !exists {
			_, err = client.CreateTopic(ctx, topicName)
			if err != nil {
				return err
			}
			log.Printf("📡 Created topic: %s", topicName)
		}
	}

	// Create subscription for processing requests
	sub := client.Subscription("blockchain-requests-sub")
	exists, err := sub.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		_, err = client.CreateSubscription(ctx, "blockchain-requests-sub", pubsub.SubscriptionConfig{
			Topic:       client.Topic("blockchain-requests"),
			AckDeadline: 15 * time.Second, // Longer deadline for exchange operations
		})
		if err != nil {
			return err
		}
		log.Printf("📬 Created subscription: blockchain-requests-sub")
	}

	return nil
}

func main() {
	startTime = time.Now()

	// Configure PubSub emulator endpoint
	os.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8681")

	ctx := context.Background()
	projectID := "test-project"

	client, err := pubsub.NewClient(ctx, projectID, option.WithoutAuthentication())
	if err != nil {
		log.Fatalf("Failed to create PubSub client: %v", err)
	}
	defer client.Close()

	log.Println("🚀 Blockchain Service Starting...")

	// Initialize PubSub resources
	if err := setupPubSubResources(client, ctx); err != nil {
		log.Fatalf("Failed to setup PubSub resources: %v", err)
	}

	// Initialize blockchain service
	blockchainService := NewBlockchainService(client)

	// Start processing requests in background
	go blockchainService.ProcessRequests(ctx)

	// Configure HTTP endpoints
	http.HandleFunc("/health", blockchainService.handleHealth)
	http.HandleFunc("/stats", blockchainService.handleStats)

	// Start HTTP server in background
	go func() {
		log.Println("📡 HTTP server starting on :8081")
		if err := http.ListenAndServe(":8081", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	log.Println("⛓️  Blockchain Service is ready!")
	log.Println("📊 Test endpoints:")
	log.Println("  GET /health")
	log.Println("  GET /stats")
	log.Println("🔧 Supported operations:")
	log.Println("  - Token Grant (ERC20 minting)")
	log.Println("  - Token Exchange (Token swapping)")

	// Wait for interrupt signal to shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	log.Println("📴 Blockchain Service shutting down...")
}
