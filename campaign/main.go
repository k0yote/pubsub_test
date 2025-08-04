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
	"sync/atomic"
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

// Service Metrics for monitoring
type ServiceMetrics struct {
	RequestsSent         int64
	RequestsFailed       int64
	ResultsReceived      int64
	ResultsProcessed     int64
	ProcessingErrors     int64
	RESTCallsSuccess     int64
	RESTCallsFailed      int64
	PubSubPublishSuccess int64
	PubSubPublishFailed  int64
}

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
	LastUpdated     time.Time         `json:"last_updated"`
	PubSubMsgID     string            `json:"pubsub_msg_id,omitempty"`
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

	// Metrics
	metrics *ServiceMetrics

	// Health check
	lastHealthCheck time.Time
	isHealthy       bool
}

func NewCampaignService(client *pubsub.Client) *CampaignService {
	return &CampaignService{
		client:               client,
		notifications:        make(map[string][]BlockchainResult),
		pendingTxs:           make(map[string]*PendingTransaction),
		userTxs:              make(map[string][]string),
		blockchainServiceURL: "http://localhost:8081",
		httpClient:           &http.Client{Timeout: 10 * time.Second},
		metrics:              &ServiceMetrics{},
		isHealthy:            true,
	}
}

// Initialize PubSub resources with retry
func (cs *CampaignService) InitializePubSub(ctx context.Context) error {
	log.Println("🔧 Initializing PubSub resources...")

	// Setup topics with retry
	if err := cs.ensureTopicsExist(ctx); err != nil {
		return fmt.Errorf("failed to ensure topics: %v", err)
	}

	// Setup subscription with retry
	if err := cs.ensureSubscriptionExists(ctx); err != nil {
		return fmt.Errorf("failed to ensure subscription: %v", err)
	}

	// Initialize topic and subscription references
	cs.requestTopic = cs.client.Topic("blockchain-requests")
	cs.resultSub = cs.client.Subscription("blockchain-results-sub")

	// Configure subscription settings for better reliability
	cs.resultSub.ReceiveSettings = pubsub.ReceiveSettings{
		MaxOutstandingMessages: 100,
		NumGoroutines:          10,
		MaxExtension:           60 * time.Second,
	}

	log.Println("✅ PubSub resources initialized successfully")
	return nil
}

func (cs *CampaignService) ensureTopicsExist(ctx context.Context) error {
	topics := []string{"blockchain-requests", "blockchain-results"}

	for _, topicName := range topics {
		if err := cs.ensureTopicExists(ctx, topicName); err != nil {
			return err
		}
	}
	return nil
}

func (cs *CampaignService) ensureTopicExists(ctx context.Context, topicName string) error {
	topic := cs.client.Topic(topicName)

	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		exists, err := topic.Exists(ctx)
		if err != nil {
			log.Printf("⚠️  Retry %d: Error checking topic %s: %v", i+1, topicName, err)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		if exists {
			log.Printf("✅ Topic %s already exists", topicName)
			return nil
		}

		// Create topic
		_, err = cs.client.CreateTopic(ctx, topicName)
		if err != nil {
			log.Printf("⚠️  Retry %d: Error creating topic %s: %v", i+1, topicName, err)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		log.Printf("✅ Created topic: %s", topicName)
		return nil
	}

	return fmt.Errorf("failed to ensure topic %s after %d retries", topicName, maxRetries)
}

func (cs *CampaignService) ensureSubscriptionExists(ctx context.Context) error {
	subName := "blockchain-results-sub"
	sub := cs.client.Subscription(subName)

	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		exists, err := sub.Exists(ctx)
		if err != nil {
			log.Printf("⚠️  Retry %d: Error checking subscription %s: %v", i+1, subName, err)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		if exists {
			log.Printf("✅ Subscription %s already exists", subName)
			return nil
		}

		// Create subscription
		topic := cs.client.Topic("blockchain-results")
		_, err = cs.client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
			Topic:               topic,
			AckDeadline:         30 * time.Second,
			RetainAckedMessages: false,
			ExpirationPolicy:    time.Hour * 24 * 7, // 7 days
		})

		if err != nil {
			log.Printf("⚠️  Retry %d: Error creating subscription %s: %v", i+1, subName, err)
			time.Sleep(time.Second * time.Duration(i+1))
			continue
		}

		log.Printf("✅ Created subscription: %s", subName)
		return nil
	}

	return fmt.Errorf("failed to ensure subscription %s after %d retries", subName, maxRetries)
}

// Health check for services
func (cs *CampaignService) checkHealth(ctx context.Context) error {
	// Check PubSub connectivity
	topics := []string{"blockchain-requests", "blockchain-results"}
	for _, topicName := range topics {
		topic := cs.client.Topic(topicName)
		exists, err := topic.Exists(ctx)
		if err != nil || !exists {
			cs.isHealthy = false
			return fmt.Errorf("topic %s not healthy: exists=%v, err=%v", topicName, exists, err)
		}
	}

	// Check subscription
	if cs.resultSub != nil {
		exists, err := cs.resultSub.Exists(ctx)
		if err != nil || !exists {
			cs.isHealthy = false
			return fmt.Errorf("subscription not healthy: exists=%v, err=%v", exists, err)
		}
	}

	// Check blockchain service
	resp, err := cs.httpClient.Get(cs.blockchainServiceURL + "/health")
	if err != nil {
		cs.isHealthy = false
		return fmt.Errorf("blockchain service not reachable: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cs.isHealthy = false
		return fmt.Errorf("blockchain service unhealthy: status=%d", resp.StatusCode)
	}

	cs.isHealthy = true
	cs.lastHealthCheck = time.Now()
	return nil
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
	atomic.AddInt64(&cs.metrics.RequestsSent, 1)

	// Prepare request body
	body, err := json.Marshal(request)
	if err != nil {
		atomic.AddInt64(&cs.metrics.RequestsFailed, 1)
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	// Send to blockchain service via REST with retry
	var response TransactionResponse
	maxRetries := 3
	baseDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			delay := baseDelay * time.Duration(i)
			log.Printf("⚠️  Retry %d/%d after %v", i+1, maxRetries, delay)
			time.Sleep(delay)
		}

		url := fmt.Sprintf("%s/submit-transaction", cs.blockchainServiceURL)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := cs.httpClient.Do(req)
		if err != nil {
			log.Printf("⚠️  HTTP request failed: %v", err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("⚠️  Blockchain service returned error: %d", resp.StatusCode)
			continue
		}

		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			log.Printf("⚠️  Failed to decode response: %v", err)
			continue
		}

		// Success
		atomic.AddInt64(&cs.metrics.RESTCallsSuccess, 1)
		break
	}

	if response.TransactionHash == "" {
		atomic.AddInt64(&cs.metrics.RESTCallsFailed, 1)
		atomic.AddInt64(&cs.metrics.RequestsFailed, 1)
		return nil, fmt.Errorf("failed to submit transaction after %d retries", maxRetries)
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
		LastUpdated:     time.Now(),
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

	return cs.publishRequestWithRetry(ctx, request)
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

	return cs.publishRequestWithRetry(ctx, request)
}

func (cs *CampaignService) publishRequestWithRetry(ctx context.Context, request BlockchainRequest) error {
	maxRetries := 3
	baseDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		if err := cs.publishRequest(ctx, request); err != nil {
			atomic.AddInt64(&cs.metrics.PubSubPublishFailed, 1)

			if i < maxRetries-1 {
				delay := baseDelay * time.Duration(i+1)
				log.Printf("⚠️  Retry %d/%d: Failed to publish, retrying in %v: %v",
					i+1, maxRetries, delay, err)
				time.Sleep(delay)
				continue
			}

			atomic.AddInt64(&cs.metrics.RequestsFailed, 1)
			return fmt.Errorf("failed to publish after %d retries: %v", maxRetries, err)
		}

		atomic.AddInt64(&cs.metrics.PubSubPublishSuccess, 1)
		return nil
	}

	return fmt.Errorf("unexpected error in publish retry loop")
}

func (cs *CampaignService) publishRequest(ctx context.Context, request BlockchainRequest) error {
	atomic.AddInt64(&cs.metrics.RequestsSent, 1)

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %v", err)
	}

	log.Printf("📤 Publishing PubSub message: %s", string(data))

	// Create message with ID for tracking
	msgID := fmt.Sprintf("%s-%d", request.RequestID, time.Now().UnixNano())

	attributes := map[string]string{
		"event_type":  request.EventType.String(),
		"user_id":     request.UserID,
		"campaign_id": request.CampaignID,
		"request_id":  request.RequestID,
		"msg_id":      msgID,
	}

	// Use context with timeout
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result := cs.requestTopic.Publish(ctx, &pubsub.Message{
		ID:         msgID,
		Data:       data,
		Attributes: attributes,
	})

	serverID, err := result.Get(ctx)
	if err != nil {
		log.Printf("❌ Failed to publish PubSub message: %v", err)
		return fmt.Errorf("failed to publish message: %v", err)
	}

	log.Printf("✅ %s requested via PubSub for user %s (request: %s, server_id: %s)",
		request.EventType, request.UserID, request.RequestID, serverID)
	return nil
}

// Process final results from PubSub (both REST and legacy results come here)
func (cs *CampaignService) ProcessResults(ctx context.Context) {
	log.Println("📱 Campaign Service: Starting result processor...")

	// Verify subscription exists before starting
	exists, err := cs.resultSub.Exists(ctx)
	if !exists || err != nil {
		log.Printf("❌ Result subscription not ready: exists=%v, err=%v", exists, err)
		return
	}

	messageCount := int64(0)

	err = cs.resultSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		atomic.AddInt64(&messageCount, 1)
		atomic.AddInt64(&cs.metrics.ResultsReceived, 1)

		log.Printf("📨 [Result Message #%d] ID: %s, PublishTime: %v",
			messageCount, msg.ID, msg.PublishTime)

		// Log message attributes
		for k, v := range msg.Attributes {
			log.Printf("  Attribute: %s = %s", k, v)
		}

		// Process in goroutine to not block other messages
		go func() {
			processingStart := time.Now()

			var result BlockchainResult
			if err := json.Unmarshal(msg.Data, &result); err != nil {
				atomic.AddInt64(&cs.metrics.ProcessingErrors, 1)
				log.Printf("❌ Failed to unmarshal result: %v", err)
				log.Printf("Raw result data: %s", string(msg.Data))
				msg.Nack()
				return
			}

			log.Printf("📨 Processing blockchain result: %s for user %s (tx: %s)",
				result.EventType, result.UserID, result.TransactionHash)

			// Update pending transaction status
			cs.mu.Lock()
			if pending, exists := cs.pendingTxs[result.TransactionHash]; exists {
				log.Printf("🔄 Updating pending transaction: %s", result.TransactionHash)

				pending.LastUpdated = time.Now()
				pending.PubSubMsgID = msg.ID

				if result.EventType == TokenGrantCompleted || result.EventType == TokenExchangeCompleted {
					pending.Status = StatusConfirmed
					log.Printf("✅ Transaction confirmed: %s", result.TransactionHash)
				} else {
					pending.Status = StatusFailed
					log.Printf("❌ Transaction failed: %s (error: %s)",
						result.TransactionHash, result.ErrorMessage)
				}
			} else {
				log.Printf("⚠️  No pending transaction found for: %s (might be from another instance)",
					result.TransactionHash)
			}

			// Store final result for user notifications
			cs.notifications[result.UserID] = append(cs.notifications[result.UserID], result)
			notificationCount := len(cs.notifications[result.UserID])
			cs.mu.Unlock()

			log.Printf("📊 Stored notification for user %s (total: %d)", result.UserID, notificationCount)

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
					result.UserID, result.TransactionHash,
					*result.ExchangedFromAmount, *result.ExchangedToAmount)
				cs.updateUserBadge(result.UserID, result.CampaignID, "token_exchange")

			case TokenExchangeFailed:
				log.Printf("❌ Token exchange failed for user %s (tx: %s, error: %s)",
					result.UserID, result.TransactionHash, result.ErrorMessage)
				cs.handleFailure(result)
			}

			processingDuration := time.Since(processingStart)
			log.Printf("⏱️  Message processed in %v", processingDuration)

			atomic.AddInt64(&cs.metrics.ResultsProcessed, 1)
			msg.Ack()
			log.Printf("✅ Message acknowledged successfully")
		}()
	})

	if err != nil {
		log.Printf("❌ Error in Receive: %v", err)
	}
}

func (cs *CampaignService) updateUserBadge(userID, campaignID, operationType string) {
	// Update user badge and notification in database
	log.Printf("🏆 Database: Updated badge for user %s in campaign %s (operation: %s)",
		userID, campaignID, operationType)
	log.Printf("🔔 Notification: User %s can now see their %s result", userID, operationType)
	// Send push notification (simulated)
	log.Printf("📱 Push notification sent to user %s", userID)
}

func (cs *CampaignService) handleFailure(result BlockchainResult) {
	if result.RequestID != "" {
		log.Printf("🔄 Scheduling retry for request %s", result.RequestID)
		// Retry logic implementation would go here
		log.Printf("⚠️  Failure reason: %s", result.ErrorMessage)
	}
}

func (cs *CampaignService) GetUserNotifications(userID string) []BlockchainResult {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	notifications := make([]BlockchainResult, len(cs.notifications[userID]))
	copy(notifications, cs.notifications[userID])
	return notifications
}

func (cs *CampaignService) GetUserPendingTransactions(userID string) []*PendingTransaction {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var pending []*PendingTransaction
	if txHashes, exists := cs.userTxs[userID]; exists {
		for _, hash := range txHashes {
			if tx, exists := cs.pendingTxs[hash]; exists {
				// Create a copy to avoid race conditions
				txCopy := *tx
				pending = append(pending, &txCopy)
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
			log.Printf("❌ Token grant request failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := map[string]string{
			"status":  "accepted",
			"message": "Token grant request sent via PubSub",
			"method":  "pubsub",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		// Modern REST approach
		response, err := cs.RequestTokenGrantREST(ctx, userID, campaignID, amount)
		if err != nil {
			log.Printf("❌ Token grant request failed: %v", err)
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
			log.Printf("❌ Token exchange request failed: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := map[string]string{
			"status":  "accepted",
			"message": "Token exchange request sent via PubSub",
			"method":  "pubsub",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		// Modern REST approach
		response, err := cs.RequestTokenExchangeREST(ctx, userID, campaignID, fromTokenType, toTokenType, fromAmount, exchangeRate)
		if err != nil {
			log.Printf("❌ Token exchange request failed: %v", err)
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

	// Count by status
	statusCounts := map[string]int{
		"pending":   0,
		"confirmed": 0,
		"failed":    0,
	}

	for _, tx := range pendingTxs {
		statusCounts[string(tx.Status)]++
	}

	status := map[string]interface{}{
		"user_id":              userID,
		"notifications":        notifications,
		"pending_transactions": pendingTxs,
		"summary": map[string]interface{}{
			"total_notifications": len(notifications),
			"total_pending":       len(pendingTxs),
			"status_breakdown":    statusCounts,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (cs *CampaignService) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check service health
	if err := cs.checkHealth(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":             "healthy",
		"last_health_check":  cs.lastHealthCheck,
		"blockchain_service": cs.blockchainServiceURL,
	})
}

func (cs *CampaignService) handleStats(w http.ResponseWriter, r *http.Request) {
	cs.mu.RLock()
	totalNotifications := 0
	for _, notifications := range cs.notifications {
		totalNotifications += len(notifications)
	}
	uniqueUsers := len(cs.notifications)
	totalPendingTxs := len(cs.pendingTxs)
	cs.mu.RUnlock()

	stats := map[string]interface{}{
		"service":            "campaign",
		"status":             "running",
		"uptime":             time.Since(startTime).String(),
		"is_healthy":         cs.isHealthy,
		"last_health_check":  cs.lastHealthCheck,
		"blockchain_service": cs.blockchainServiceURL,
		"supported_methods":  []string{"rest", "pubsub"},
		"user_stats": map[string]interface{}{
			"unique_users":         uniqueUsers,
			"total_notifications":  totalNotifications,
			"pending_transactions": totalPendingTxs,
		},
		"request_metrics": map[string]interface{}{
			"requests_sent":   atomic.LoadInt64(&cs.metrics.RequestsSent),
			"requests_failed": atomic.LoadInt64(&cs.metrics.RequestsFailed),
			"rest_success":    atomic.LoadInt64(&cs.metrics.RESTCallsSuccess),
			"rest_failed":     atomic.LoadInt64(&cs.metrics.RESTCallsFailed),
			"pubsub_success":  atomic.LoadInt64(&cs.metrics.PubSubPublishSuccess),
			"pubsub_failed":   atomic.LoadInt64(&cs.metrics.PubSubPublishFailed),
		},
		"result_metrics": map[string]interface{}{
			"results_received":  atomic.LoadInt64(&cs.metrics.ResultsReceived),
			"results_processed": atomic.LoadInt64(&cs.metrics.ResultsProcessed),
			"processing_errors": atomic.LoadInt64(&cs.metrics.ProcessingErrors),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

var startTime time.Time

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

	log.Println("🚀 Campaign Service Starting...")

	// Initialize campaign service
	campaignService := NewCampaignService(client)

	// Wait a bit for blockchain service to be ready
	log.Println("⏳ Waiting for blockchain service to be ready...")
	time.Sleep(3 * time.Second)

	// Initialize PubSub resources with retry
	if err := campaignService.InitializePubSub(ctx); err != nil {
		log.Fatalf("Failed to initialize PubSub: %v", err)
	}

	// Start background health checker
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := campaignService.checkHealth(ctx); err != nil {
					log.Printf("⚠️  Health check failed: %v", err)
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start processing results in background
	go campaignService.ProcessResults(ctx)

	// Configure HTTP endpoints
	http.HandleFunc("/health", campaignService.handleHealth)
	http.HandleFunc("/stats", campaignService.handleStats)
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
	log.Println("  GET  /health - Service health check")
	log.Println("  GET  /stats - Service statistics")
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

	// Give ongoing operations time to complete
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Log final metrics
	log.Printf("📊 Final metrics:")
	log.Printf("  Requests sent: %d", atomic.LoadInt64(&campaignService.metrics.RequestsSent))
	log.Printf("  Requests failed: %d", atomic.LoadInt64(&campaignService.metrics.RequestsFailed))
	log.Printf("  Results received: %d", atomic.LoadInt64(&campaignService.metrics.ResultsReceived))
	log.Printf("  Results processed: %d", atomic.LoadInt64(&campaignService.metrics.ResultsProcessed))
	log.Printf("  REST calls successful: %d", atomic.LoadInt64(&campaignService.metrics.RESTCallsSuccess))
	log.Printf("  PubSub publishes successful: %d", atomic.LoadInt64(&campaignService.metrics.PubSubPublishSuccess))
}
