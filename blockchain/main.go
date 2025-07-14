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
	TokenGrantRequested = "token.grant.requested"
	TokenGrantCompleted = "token.grant.completed"
	TokenGrantFailed    = "token.grant.failed"
)

// Message structures
type TokenGrantRequest struct {
	EventType   string    `json:"event_type"`
	UserID      string    `json:"user_id"`
	CampaignID  string    `json:"campaign_id"`
	TokenAmount int64     `json:"token_amount"`
	TokenType   string    `json:"token_type"`
	RequestID   string    `json:"request_id"`
	Timestamp   time.Time `json:"timestamp"`
	RetryCount  int       `json:"retry_count"`
}

type TokenGrantResult struct {
	EventType       string    `json:"event_type"`
	UserID          string    `json:"user_id"`
	CampaignID      string    `json:"campaign_id"`
	RequestID       string    `json:"request_id"`
	TransactionHash string    `json:"transaction_hash,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	ProcessedAt     time.Time `json:"processed_at"`
}

// BlockchainService handles ERC20 token minting and transaction processing
type BlockchainService struct {
	client      *pubsub.Client
	grantSub    *pubsub.Subscription
	resultTopic *pubsub.Topic
}

func NewBlockchainService(client *pubsub.Client) *BlockchainService {
	return &BlockchainService{
		client:      client,
		grantSub:    client.Subscription("token-grant-requests-sub"),
		resultTopic: client.Topic("token-grant-results"),
	}
}

func (bs *BlockchainService) ProcessTokenGrants(ctx context.Context) {
	log.Println("⛓️  Blockchain Service: Listening for token grant requests...")

	bs.grantSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		var request TokenGrantRequest
		if err := json.Unmarshal(msg.Data, &request); err != nil {
			log.Printf("Failed to unmarshal request: %v", err)
			msg.Nack()
			return
		}

		log.Printf("⚡ Processing token grant for user %s (amount: %d)",
			request.UserID, request.TokenAmount)

		// Simulate blockchain processing
		result := bs.processBlockchainTransaction(request)

		// Publish result
		bs.publishResult(ctx, result)

		msg.Ack()
	})
}

func (bs *BlockchainService) processBlockchainTransaction(request TokenGrantRequest) TokenGrantResult {
	// Process ERC20 token minting transaction
	log.Printf("🔨 Minting %d ERC20 tokens for user %s...", request.TokenAmount, request.UserID)

	// Simulate blockchain transaction processing time (1-3 seconds)
	time.Sleep(time.Duration(rand.Intn(3)+1) * time.Second)

	// Simulate transaction success/failure (90% success rate)
	success := rand.Float32() < 0.9

	result := TokenGrantResult{
		UserID:      request.UserID,
		CampaignID:  request.CampaignID,
		RequestID:   request.RequestID,
		Timestamp:   request.Timestamp,
		ProcessedAt: time.Now(),
	}

	if success {
		result.EventType = TokenGrantCompleted
		result.TransactionHash = fmt.Sprintf("0x%x", rand.Int63())
		log.Printf("✅ Transaction completed: %s", result.TransactionHash)
	} else {
		result.EventType = TokenGrantFailed
		result.ErrorMessage = "Insufficient gas or network congestion"
		log.Printf("❌ Transaction failed: %s", result.ErrorMessage)
	}

	return result
}

func (bs *BlockchainService) publishResult(ctx context.Context, result TokenGrantResult) {
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
		"service": "blockchain",
		"status":  "running",
		"uptime":  time.Since(startTime).String(),
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
	// Create required topics
	topics := []string{"token-grant-requests", "token-grant-results"}
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
	sub := client.Subscription("token-grant-requests-sub")
	exists, err := sub.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		_, err = client.CreateSubscription(ctx, "token-grant-requests-sub", pubsub.SubscriptionConfig{
			Topic:       client.Topic("token-grant-requests"),
			AckDeadline: 10 * time.Second,
		})
		if err != nil {
			return err
		}
		log.Printf("📬 Created subscription: token-grant-requests-sub")
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

	// Start processing token grants in background
	go blockchainService.ProcessTokenGrants(ctx)

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

	// Wait for interrupt signal to shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	log.Println("📴 Blockchain Service shutting down...")
}
