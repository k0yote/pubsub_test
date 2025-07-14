package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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

// CampaignService handles token grant requests and processes results
type CampaignService struct {
	client        *pubsub.Client
	grantTopic    *pubsub.Topic
	resultSub     *pubsub.Subscription
	notifications map[string][]TokenGrantResult
	mu            sync.RWMutex
}

func NewCampaignService(client *pubsub.Client) *CampaignService {
	return &CampaignService{
		client:        client,
		grantTopic:    client.Topic("token-grant-requests"),
		resultSub:     client.Subscription("token-grant-results-sub"),
		notifications: make(map[string][]TokenGrantResult),
	}
}

func (cs *CampaignService) RequestTokenGrant(ctx context.Context, userID, campaignID string, amount int64) error {
	request := TokenGrantRequest{
		EventType:   TokenGrantRequested,
		UserID:      userID,
		CampaignID:  campaignID,
		TokenAmount: amount,
		TokenType:   "ERC20",
		RequestID:   fmt.Sprintf("req_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		RetryCount:  0,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	result := cs.grantTopic.Publish(ctx, &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"event_type":  TokenGrantRequested,
			"user_id":     userID,
			"campaign_id": campaignID,
			"request_id":  request.RequestID,
		},
	})

	_, err = result.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to publish message: %v", err)
	}

	log.Printf("🎯 Token grant requested for user %s (amount: %d, request: %s)",
		userID, amount, request.RequestID)
	return nil
}

func (cs *CampaignService) ProcessResults(ctx context.Context) {
	log.Println("📱 Campaign Service: Listening for token grant results...")

	cs.resultSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		var result TokenGrantResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			log.Printf("Failed to unmarshal result: %v", err)
			msg.Nack()
			return
		}

		cs.mu.Lock()
		cs.notifications[result.UserID] = append(cs.notifications[result.UserID], result)
		cs.mu.Unlock()

		// Process token grant results and update user data
		switch result.EventType {
		case TokenGrantCompleted:
			log.Printf("✅ Token grant completed for user %s (tx: %s)",
				result.UserID, result.TransactionHash)
			cs.updateUserBadge(result.UserID, result.CampaignID)
		case TokenGrantFailed:
			log.Printf("❌ Token grant failed for user %s (error: %s)",
				result.UserID, result.ErrorMessage)
			cs.handleFailure(result)
		}

		msg.Ack()
	})
}

func (cs *CampaignService) updateUserBadge(userID, campaignID string) {
	// Update user badge and notification in database
	log.Printf("🏆 Database: Updated badge for user %s in campaign %s", userID, campaignID)
	log.Printf("🔔 Notification: User %s can now see their token reward", userID)
}

func (cs *CampaignService) handleFailure(result TokenGrantResult) {
	if result.RequestID != "" {
		log.Printf("🔄 Scheduling retry for request %s", result.RequestID)
		// Retry logic implementation would go here
	}
}

func (cs *CampaignService) GetUserNotifications(userID string) []TokenGrantResult {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.notifications[userID]
}

// HTTP handlers for testing
func (cs *CampaignService) handleTokenRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.URL.Query().Get("user_id")
	campaignID := r.URL.Query().Get("campaign_id")
	if userID == "" || campaignID == "" {
		http.Error(w, "user_id and campaign_id are required", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	if err := cs.RequestTokenGrant(ctx, userID, campaignID, 100); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Token grant request sent"))
}

func (cs *CampaignService) handleStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	notifications := cs.GetUserNotifications(userID)
	data, err := json.Marshal(notifications)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

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

	// Create subscription for processing results
	sub := client.Subscription("token-grant-results-sub")
	exists, err := sub.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		_, err = client.CreateSubscription(ctx, "token-grant-results-sub", pubsub.SubscriptionConfig{
			Topic:       client.Topic("token-grant-results"),
			AckDeadline: 10 * time.Second,
		})
		if err != nil {
			return err
		}
		log.Printf("📬 Created subscription: token-grant-results-sub")
	}

	return nil
}

func main() {
	// Configure PubSub emulator endpoint
	os.Setenv("PUBSUB_EMULATOR_HOST", "localhost:8681")

	ctx := context.Background()
	projectID := "test-project"

	client, err := pubsub.NewClient(ctx, projectID, option.WithoutAuthentication())
	if err != nil {
		log.Fatalf("Failed to create PubSub client: %v", err)
	}
	defer client.Close()

	log.Println("🚀 Campaign Service Starting...")

	// Initialize PubSub resources
	if err := setupPubSubResources(client, ctx); err != nil {
		log.Fatalf("Failed to setup PubSub resources: %v", err)
	}

	// Initialize campaign service
	campaignService := NewCampaignService(client)

	// Start processing results in background
	go campaignService.ProcessResults(ctx)

	// Configure HTTP endpoints
	http.HandleFunc("/token-request", campaignService.handleTokenRequest)
	http.HandleFunc("/status", campaignService.handleStatus)

	// Start HTTP server in background
	go func() {
		log.Println("📡 HTTP server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	log.Println("🎯 Campaign Service is ready!")
	log.Println("📊 Test endpoints:")
	log.Println("  POST /token-request?user_id=user123&campaign_id=summer")
	log.Println("  GET  /status?user_id=user123")

	// Wait for interrupt signal to shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	log.Println("📴 Campaign Service shutting down...")
}
