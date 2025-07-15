package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
)

type EventType string

func (e EventType) String() string {
	return string(e)
}

// Event Types
const (
	// Token Grant Events
	TokenGrantRequested EventType = "token.grant.requested"
	TokenGrantCompleted EventType = "token.grant.completed"
	TokenGrantFailed    EventType = "token.grant.failed"

	// Token Exchange Events
	TokenExchangeRequested EventType = "token.exchange.requested"
	TokenExchangeCompleted EventType = "token.exchange.completed"
	TokenExchangeFailed    EventType = "token.exchange.failed"
)

// Transaction Status
type TransactionStatus string

const (
	StatusPending   TransactionStatus = "pending"
	StatusConfirmed TransactionStatus = "confirmed"
	StatusFailed    TransactionStatus = "failed"
)

// Synchronous transaction request/response structures for Blockchain Service API
type TransactionRequest struct {
	EventType  EventType `json:"event_type"`
	UserID     string    `json:"user_id"`
	CampaignID string    `json:"campaign_id"`
	RequestID  string    `json:"request_id"`

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

type TransactionResponse struct {
	TransactionHash string            `json:"transaction_hash"`
	Status          TransactionStatus `json:"status"`
	RequestID       string            `json:"request_id"`
	EstimatedTime   string            `json:"estimated_completion_time"`
	Message         string            `json:"message"`
}

// Legacy PubSub structures (for backward compatibility)
type BlockchainRequest struct {
	EventType  EventType `json:"event_type"`
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

type BlockchainResult struct {
	EventType       EventType `json:"event_type"`
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

// Pending transaction tracking
type PendingTransaction struct {
	TransactionHash string            `json:"transaction_hash"`
	Status          TransactionStatus `json:"status"`
	RequestID       string            `json:"request_id"`
	UserID          string            `json:"user_id"`
	CampaignID      string            `json:"campaign_id"`
	EventType       EventType         `json:"event_type"`
	SubmittedAt     time.Time         `json:"submitted_at"`
	EstimatedTime   string            `json:"estimated_completion_time"`
}

// CampaignService handles both token grant and exchange requests with hybrid approach
type CampaignService struct {
	client        *pubsub.Client
	requestTopic  *pubsub.Topic
	resultSub     *pubsub.Subscription
	notifications map[string][]BlockchainResult
	pendingTxs    map[string]*PendingTransaction // Track pending transactions by hash
	userTxs       map[string][]string            // Track user's transaction hashes
	mu            sync.RWMutex

	// Blockchain service configuration
	blockchainServiceURL string
	httpClient           *http.Client
}

func NewCampaignService(client *pubsub.Client) *CampaignService {
	return &CampaignService{
		client:               client,
		requestTopic:         client.Topic("blockchain-requests"),
		resultSub:            client.Subscription("blockchain-results-sub"),
		notifications:        make(map[string][]BlockchainResult),
		pendingTxs:           make(map[string]*PendingTransaction),
		userTxs:              make(map[string][]string),
		blockchainServiceURL: "http://localhost:8081",
		httpClient:           &http.Client{Timeout: 10 * time.Second},
	}
}

// Modern REST API approach for transaction submission
func (cs *CampaignService) RequestTokenGrantREST(ctx context.Context, userID, campaignID string, amount int64) (*TransactionResponse, error) {
	request := TransactionRequest{
		EventType:   TokenGrantRequested,
		UserID:      userID,
		CampaignID:  campaignID,
		TokenAmount: &amount,
		TokenType:   "ERC20",
		RequestID:   fmt.Sprintf("grant_%d", time.Now().UnixNano()),
	}

	return cs.submitTransactionREST(ctx, request)
}

func (cs *CampaignService) RequestTokenExchangeREST(ctx context.Context, userID, campaignID string, fromTokenType, toTokenType string, fromAmount int64, exchangeRate float64) (*TransactionResponse, error) {
	toAmount := int64(float64(fromAmount) * exchangeRate)

	request := TransactionRequest{
		EventType:     TokenExchangeRequested,
		UserID:        userID,
		CampaignID:    campaignID,
		FromTokenType: &fromTokenType,
		ToTokenType:   &toTokenType,
		ExchangeRate:  &exchangeRate,
		FromAmount:    &fromAmount,
		ToAmount:      &toAmount,
		RequestID:     fmt.Sprintf("exchange_%d", time.Now().UnixNano()),
	}

	return cs.submitTransactionREST(ctx, request)
}

func (cs *CampaignService) submitTransactionREST(ctx context.Context, request TransactionRequest) (*TransactionResponse, error) {
	log.Printf("🔗 Submitting %s via REST for user %s", request.EventType, request.UserID)

	// Prepare request body
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Send to blockchain service via REST
	url := fmt.Sprintf("%s/submit-transaction", cs.blockchainServiceURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cs.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to submit transaction: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blockchain service returned error: %d", resp.StatusCode)
	}

	var response TransactionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %v", err)
	}

	// Store pending transaction for tracking
	pending := &PendingTransaction{
		TransactionHash: response.TransactionHash,
		Status:          response.Status,
		RequestID:       response.RequestID,
		UserID:          request.UserID,
		CampaignID:      request.CampaignID,
		EventType:       request.EventType,
		SubmittedAt:     time.Now(),
		EstimatedTime:   response.EstimatedTime,
	}

	cs.mu.Lock()
	cs.pendingTxs[response.TransactionHash] = pending
	cs.userTxs[request.UserID] = append(cs.userTxs[request.UserID], response.TransactionHash)
	cs.mu.Unlock()

	log.Printf("✅ Transaction submitted via REST: %s (hash: %s, status: %s)",
		request.EventType, response.TransactionHash, response.Status)

	return &response, nil
}

// Legacy PubSub approach (for backward compatibility)
func (cs *CampaignService) RequestTokenGrant(ctx context.Context, userID, campaignID string, amount int64) error {
	request := BlockchainRequest{
		EventType:   TokenGrantRequested,
		UserID:      userID,
		CampaignID:  campaignID,
		TokenAmount: &amount,
		TokenType:   "ERC20",
		RequestID:   fmt.Sprintf("grant_%d", time.Now().UnixNano()),
		Timestamp:   time.Now(),
		RetryCount:  0,
	}

	return cs.publishRequest(ctx, request)
}

func (cs *CampaignService) RequestTokenExchange(ctx context.Context, userID, campaignID string, fromTokenType, toTokenType string, fromAmount int64, exchangeRate float64) error {
	toAmount := int64(float64(fromAmount) * exchangeRate)

	request := BlockchainRequest{
		EventType:     TokenExchangeRequested,
		UserID:        userID,
		CampaignID:    campaignID,
		FromTokenType: &fromTokenType,
		ToTokenType:   &toTokenType,
		ExchangeRate:  &exchangeRate,
		FromAmount:    &fromAmount,
		ToAmount:      &toAmount,
		RequestID:     fmt.Sprintf("exchange_%d", time.Now().UnixNano()),
		Timestamp:     time.Now(),
		RetryCount:    0,
	}

	return cs.publishRequest(ctx, request)
}

func (cs *CampaignService) publishRequest(ctx context.Context, request BlockchainRequest) error {
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	log.Printf("📤 Publishing PubSub message: %s", string(data))

	attributes := map[string]string{
		"event_type":  request.EventType.String(),
		"user_id":     request.UserID,
		"campaign_id": request.CampaignID,
		"request_id":  request.RequestID,
	}

	result := cs.requestTopic.Publish(ctx, &pubsub.Message{
		Data:       data,
		Attributes: attributes,
	})

	_, err = result.Get(ctx)
	if err != nil {
		log.Printf("❌ Failed to publish PubSub message: %v", err)
		return fmt.Errorf("failed to publish message: %v", err)
	}

	log.Printf("✅ %s requested via PubSub for user %s (request: %s)", request.EventType, request.UserID, request.RequestID)
	log.Printf("📊 Message published to topic: blockchain-requests")
	return nil
}

// Process final results from PubSub (both REST and legacy results come here)
func (cs *CampaignService) ProcessResults(ctx context.Context) {
	log.Println("📱 Campaign Service: Listening for blockchain results...")

	cs.resultSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		log.Printf("📨 Received PubSub result message: %s", string(msg.Data))

		var result BlockchainResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			log.Printf("❌ Failed to unmarshal result: %v", err)
			log.Printf("Raw result data: %s", string(msg.Data))
			msg.Nack()
			return
		}

		log.Printf("📨 Parsed blockchain result: %s for user %s (tx: %s)",
			result.EventType, result.UserID, result.TransactionHash)

		// Update pending transaction status if it exists
		cs.mu.Lock()
		if pending, exists := cs.pendingTxs[result.TransactionHash]; exists {
			log.Printf("🔄 Updating pending transaction: %s", result.TransactionHash)
			if result.EventType == TokenGrantCompleted || result.EventType == TokenExchangeCompleted {
				pending.Status = StatusConfirmed
				log.Printf("✅ Transaction confirmed: %s", result.TransactionHash)
			} else {
				pending.Status = StatusFailed
				log.Printf("❌ Transaction failed: %s", result.TransactionHash)
			}
		} else {
			log.Printf("⚠️ No pending transaction found for: %s", result.TransactionHash)
		}

		// Store final result for user notifications
		cs.notifications[result.UserID] = append(cs.notifications[result.UserID], result)
		log.Printf("📊 Stored notification for user %s (total: %d)", result.UserID, len(cs.notifications[result.UserID]))
		cs.mu.Unlock()

		// Process results based on event type
		switch result.EventType {
		case TokenGrantCompleted:
			log.Printf("✅ Token grant completed for user %s (tx: %s, amount: %d)",
				result.UserID, result.TransactionHash, *result.GrantedAmount)
			cs.updateUserBadge(result.UserID, result.CampaignID, "token_grant")
		case TokenGrantFailed:
			log.Printf("❌ Token grant failed for user %s (tx: %s, error: %s)",
				result.UserID, result.TransactionHash, result.ErrorMessage)
			cs.handleFailure(result)
		case TokenExchangeCompleted:
			log.Printf("✅ Token exchange completed for user %s (tx: %s, %d -> %d)",
				result.UserID, result.TransactionHash, *result.ExchangedFromAmount, *result.ExchangedToAmount)
			cs.updateUserBadge(result.UserID, result.CampaignID, "token_exchange")
		case TokenExchangeFailed:
			log.Printf("❌ Token exchange failed for user %s (tx: %s, error: %s)",
				result.UserID, result.TransactionHash, result.ErrorMessage)
			cs.handleFailure(result)
		}

		msg.Ack()
		log.Printf("✅ Message acknowledged successfully")
	})
}

func (cs *CampaignService) updateUserBadge(userID, campaignID, operationType string) {
	// Update user badge and notification in database
	log.Printf("🏆 Database: Updated badge for user %s in campaign %s (operation: %s)",
		userID, campaignID, operationType)
	log.Printf("🔔 Notification: User %s can now see their %s result", userID, operationType)
}

func (cs *CampaignService) handleFailure(result BlockchainResult) {
	if result.RequestID != "" {
		log.Printf("🔄 Scheduling retry for request %s", result.RequestID)
		// Retry logic implementation would go here
	}
}

func (cs *CampaignService) GetUserNotifications(userID string) []BlockchainResult {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.notifications[userID]
}

func (cs *CampaignService) GetUserPendingTransactions(userID string) []*PendingTransaction {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var pending []*PendingTransaction
	if txHashes, exists := cs.userTxs[userID]; exists {
		for _, hash := range txHashes {
			if tx, exists := cs.pendingTxs[hash]; exists {
				pending = append(pending, tx)
			}
		}
	}
	return pending
}

// HTTP handlers with hybrid approach support
func (cs *CampaignService) handleTokenRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.URL.Query().Get("user_id")
	campaignID := r.URL.Query().Get("campaign_id")
	amountStr := r.URL.Query().Get("amount")
	method := r.URL.Query().Get("method") // "rest" or "pubsub"

	if userID == "" || campaignID == "" {
		http.Error(w, "user_id and campaign_id are required", http.StatusBadRequest)
		return
	}

	amount := int64(100) // default amount
	if amountStr != "" {
		if parsed, err := strconv.ParseInt(amountStr, 10, 64); err == nil {
			amount = parsed
		}
	}

	ctx := context.Background()

	// Choose method (default to REST for better UX)
	if method == "pubsub" {
		// Legacy PubSub approach
		if err := cs.RequestTokenGrant(ctx, userID, campaignID, amount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Token grant request sent via PubSub"))
	} else {
		// Modern REST approach
		response, err := cs.RequestTokenGrantREST(ctx, userID, campaignID, amount)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func (cs *CampaignService) handleExchangeRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID := r.URL.Query().Get("user_id")
	campaignID := r.URL.Query().Get("campaign_id")
	fromTokenType := r.URL.Query().Get("from_token_type")
	toTokenType := r.URL.Query().Get("to_token_type")
	fromAmountStr := r.URL.Query().Get("from_amount")
	exchangeRateStr := r.URL.Query().Get("exchange_rate")
	method := r.URL.Query().Get("method") // "rest" or "pubsub"

	if userID == "" || campaignID == "" || fromTokenType == "" || toTokenType == "" {
		http.Error(w, "user_id, campaign_id, from_token_type, and to_token_type are required", http.StatusBadRequest)
		return
	}

	fromAmount := int64(100) // default amount
	if fromAmountStr != "" {
		if parsed, err := strconv.ParseInt(fromAmountStr, 10, 64); err == nil {
			fromAmount = parsed
		}
	}

	exchangeRate := 1.0 // default rate
	if exchangeRateStr != "" {
		if parsed, err := strconv.ParseFloat(exchangeRateStr, 64); err == nil {
			exchangeRate = parsed
		}
	}

	ctx := context.Background()

	// Choose method (default to REST for better UX)
	if method == "pubsub" {
		// Legacy PubSub approach
		if err := cs.RequestTokenExchange(ctx, userID, campaignID, fromTokenType, toTokenType, fromAmount, exchangeRate); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Token exchange request sent via PubSub"))
	} else {
		// Modern REST approach
		response, err := cs.RequestTokenExchangeREST(ctx, userID, campaignID, fromTokenType, toTokenType, fromAmount, exchangeRate)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func (cs *CampaignService) handleStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	notifications := cs.GetUserNotifications(userID)
	pendingTxs := cs.GetUserPendingTransactions(userID)

	status := map[string]interface{}{
		"user_id":              userID,
		"notifications":        notifications,
		"pending_transactions": pendingTxs,
		"total_notifications":  len(notifications),
		"pending_count":        len(pendingTxs),
	}

	data, err := json.Marshal(status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

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

	// Create subscription for processing results
	sub := client.Subscription("blockchain-results-sub")
	exists, err := sub.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		_, err = client.CreateSubscription(ctx, "blockchain-results-sub", pubsub.SubscriptionConfig{
			Topic:       client.Topic("blockchain-results"),
			AckDeadline: 10 * time.Second,
		})
		if err != nil {
			return err
		}
		log.Printf("📬 Created subscription: blockchain-results-sub")
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
	http.HandleFunc("/exchange-request", campaignService.handleExchangeRequest)
	http.HandleFunc("/status", campaignService.handleStatus)

	// Start HTTP server in background
	go func() {
		log.Println("📡 HTTP server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	log.Println("🎯 Campaign Service is ready!")
	log.Println("📊 Test endpoints (Hybrid REST + PubSub):")
	log.Println("  POST /token-request?user_id=user123&campaign_id=summer&amount=100")
	log.Println("  POST /token-request?user_id=user123&campaign_id=summer&amount=100&method=pubsub")
	log.Println("  POST /exchange-request?user_id=user123&campaign_id=summer&from_token_type=ERC20&to_token_type=GOLD&from_amount=100&exchange_rate=1.5")
	log.Println("  POST /exchange-request?user_id=user123&campaign_id=summer&from_token_type=ERC20&to_token_type=GOLD&from_amount=100&exchange_rate=1.5&method=pubsub")
	log.Println("  GET  /status?user_id=user123")
	log.Println("🔧 Default method: REST (immediate response + async final result)")
	log.Println("🔧 Legacy method: PubSub (add &method=pubsub)")

	// Wait for interrupt signal to shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	log.Println("📴 Campaign Service shutting down...")
}
