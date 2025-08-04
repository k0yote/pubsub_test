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

type ServiceMetrics struct {
	MessagesReceived    int64
	MessagesProcessed   int64
	MessagesPublished   int64
	PublishErrors       int64
	ProcessingErrors    int64
	TransactionsCreated int64
	TransactionsSuccess int64
	TransactionsFailed  int64
}

// Synchronous transaction request/response structures
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

// Transaction tracking
type TransactionRecord struct {
	Hash           string             `json:"hash"`
	Status         TransactionStatus  `json:"status"`
	Request        TransactionRequest `json:"request"`
	SubmittedAt    time.Time          `json:"submitted_at"`
	ConfirmedAt    *time.Time         `json:"confirmed_at,omitempty"`
	ErrorMsg       string             `json:"error_message,omitempty"`
	Retries        int                `json:"retries"`
	PubSubMsgID    string             `json:"pubsub_msg_id,omitempty"`
	PublishRetries int                `json:"publish_retries"`
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

// BlockchainService handles both synchronous and asynchronous blockchain operations
type BlockchainService struct {
	client      *pubsub.Client
	requestSub  *pubsub.Subscription
	resultTopic *pubsub.Topic

	// Transaction tracking
	transactions map[string]*TransactionRecord
	mu           sync.RWMutex

	metrics *ServiceMetrics

	// Health check
	lastHealthCheck time.Time
	isHealthy       bool
}

func NewBlockchainService(client *pubsub.Client) *BlockchainService {
	return &BlockchainService{
		client:          client,
		transactions:    make(map[string]*TransactionRecord),
		metrics:         &ServiceMetrics{},
		lastHealthCheck: time.Now(),
		isHealthy:       true,
	}
}

// Initialize PubSub resources with retry
func (bs *BlockchainService) InitializePubSub(ctx context.Context) error {
	log.Println("🔧 Initializing PubSub resources...")

	// Setup topics with retry
	if err := bs.ensureTopicsExist(ctx); err != nil {
		return fmt.Errorf("failed to ensure topics: %v", err)
	}

	// Setup subscription with retry
	if err := bs.ensureSubscriptionExists(ctx); err != nil {
		return fmt.Errorf("failed to ensure subscription: %v", err)
	}

	// Initialize topic and subscription references
	bs.resultTopic = bs.client.Topic("blockchain-results")
	bs.requestSub = bs.client.Subscription("blockchain-requests-sub")

	// Configure subscription settings for better reliability
	bs.requestSub.ReceiveSettings = pubsub.ReceiveSettings{
		MaxOutstandingMessages: 100,
		NumGoroutines:          10,
		MaxExtension:           60 * time.Second,
	}

	log.Println("✅ PubSub resources initialized successfully")
	return nil
}

func (bs *BlockchainService) ensureTopicsExist(ctx context.Context) error {
	topics := []string{"blockchain-requests", "blockchain-results"}

	for _, topicName := range topics {
		if err := bs.ensureTopicExists(ctx, topicName); err != nil {
			return err
		}
	}
	return nil
}

func (bs *BlockchainService) ensureTopicExists(ctx context.Context, topicName string) error {
	topic := bs.client.Topic(topicName)

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
		_, err = bs.client.CreateTopic(ctx, topicName)
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

func (bs *BlockchainService) ensureSubscriptionExists(ctx context.Context) error {
	subName := "blockchain-requests-sub"
	sub := bs.client.Subscription(subName)

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
		topic := bs.client.Topic("blockchain-requests")
		_, err = bs.client.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
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

// Health check for PubSub connectivity
func (bs *BlockchainService) checkPubSubHealth(ctx context.Context) error {
	// Check topics
	topics := []string{"blockchain-requests", "blockchain-results"}
	for _, topicName := range topics {
		topic := bs.client.Topic(topicName)
		exists, err := topic.Exists(ctx)
		if err != nil || !exists {
			bs.isHealthy = false
			return fmt.Errorf("topic %s not healthy: exists=%v, err=%v", topicName, exists, err)
		}
	}

	// Check subscription
	if bs.requestSub != nil {
		exists, err := bs.requestSub.Exists(ctx)
		if err != nil || !exists {
			bs.isHealthy = false
			return fmt.Errorf("subscription not healthy: exists=%v, err=%v", exists, err)
		}
	}

	bs.isHealthy = true
	bs.lastHealthCheck = time.Now()
	return nil
}

// Synchronous transaction submission
func (bs *BlockchainService) SubmitTransaction(req TransactionRequest) (*TransactionResponse, error) {
	log.Printf("🔗 Submitting transaction: %s for user %s", req.EventType, req.UserID)
	atomic.AddInt64(&bs.metrics.TransactionsCreated, 1)
	// Simulate blockchain transaction submission
	txHash, err := bs.sendToBlockchain(req)
	if err != nil {
		atomic.AddInt64(&bs.metrics.TransactionsFailed, 1)
		return nil, fmt.Errorf("failed to submit transaction: %v", err)
	}

	// Store transaction record
	record := &TransactionRecord{
		Hash:        txHash,
		Status:      StatusPending,
		Request:     req,
		SubmittedAt: time.Now(),
		Retries:     0,
	}

	bs.mu.Lock()
	bs.transactions[txHash] = record
	bs.mu.Unlock()

	// Start background monitoring for this transaction
	go bs.monitorTransaction(txHash)

	response := &TransactionResponse{
		TransactionHash: txHash,
		Status:          StatusPending,
		RequestID:       req.RequestID,
		EstimatedTime:   time.Now().Add(5 * time.Second).Format(time.RFC3339),
		Message:         "Transaction submitted successfully",
	}

	log.Printf("✅ Transaction submitted: %s (hash: %s)", req.EventType, txHash)
	return response, nil
}

// Simulate blockchain transaction submission
func (bs *BlockchainService) sendToBlockchain(req TransactionRequest) (string, error) {
	// Simulate network delay
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)

	// Simulate occasional submission failures (5% failure rate)
	if rand.Float32() < 0.05 {
		return "", fmt.Errorf("network error or insufficient gas")
	}

	// Generate realistic transaction hash
	txHash := fmt.Sprintf("0x%016x%016x", rand.Int63(), rand.Int63())
	return txHash, nil
}

// Background transaction monitoring
func (bs *BlockchainService) monitorTransaction(txHash string) {
	log.Printf("👀 Monitoring transaction: %s", txHash)

	// Simulate confirmation time (2-8 seconds)
	confirmationDelay := time.Duration(rand.Intn(6)+2) * time.Second
	time.Sleep(confirmationDelay)

	bs.mu.Lock()
	record, exists := bs.transactions[txHash]
	if !exists {
		bs.mu.Unlock()
		log.Printf("⚠️  Transaction record not found: %s", txHash)
		return
	}
	bs.mu.Unlock()

	// Simulate receipt polling - check if transaction succeeded
	success := bs.checkTransactionReceipt(txHash)

	bs.mu.Lock()
	now := time.Now()
	if success {
		record.Status = StatusConfirmed
		record.ConfirmedAt = &now
		atomic.AddInt64(&bs.metrics.TransactionsSuccess, 1)
		log.Printf("✅ Transaction confirmed: %s", txHash)
	} else {
		record.Status = StatusFailed
		record.ErrorMsg = "Transaction reverted or block reorganization"
		record.ConfirmedAt = &now
		atomic.AddInt64(&bs.metrics.TransactionsFailed, 1)
		log.Printf("❌ Transaction failed: %s", txHash)
	}
	bs.mu.Unlock()

	// Publish final result via PubSub with retry
	bs.publishFinalResultWithRetry(record)
}

// Simulate blockchain receipt checking
func (bs *BlockchainService) checkTransactionReceipt(txHash string) bool {
	// Simulate receipt polling
	time.Sleep(time.Duration(rand.Intn(1000)+500) * time.Millisecond)

	// Get the original request type to determine success rate
	bs.mu.RLock()
	record, exists := bs.transactions[txHash]
	bs.mu.RUnlock()

	if !exists {
		return false
	}

	// Different success rates for different operations (temporarily set to 100% for debugging)
	switch record.Request.EventType {
	case TokenGrantRequested:
		return rand.Float32() < 0.95 // 95% success rate for debugging
	case TokenExchangeRequested:
		return rand.Float32() < 0.90 // 90% success rate for debugging
	default:
		return rand.Float32() < 0.95 // 95% success rate for debugging
	}
}

// Publish final result with retry logic
func (bs *BlockchainService) publishFinalResultWithRetry(record *TransactionRecord) {
	maxRetries := 3
	baseDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		if err := bs.publishFinalResult(record); err != nil {
			atomic.AddInt64(&bs.metrics.PublishErrors, 1)
			record.PublishRetries++

			delay := baseDelay * time.Duration(i+1)
			log.Printf("⚠️  Retry %d/%d: Failed to publish result for tx %s: %v. Retrying in %v",
				i+1, maxRetries, record.Hash, err, delay)
			time.Sleep(delay)
			continue
		}

		atomic.AddInt64(&bs.metrics.MessagesPublished, 1)
		return
	}

	log.Printf("❌ Failed to publish result for tx %s after %d retries", record.Hash, maxRetries)
}

// Publish final result to PubSub
func (bs *BlockchainService) publishFinalResult(record *TransactionRecord) error {
	log.Printf("🔄 Publishing final result for transaction: %s", record.Hash)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var result BlockchainResult
	result.UserID = record.Request.UserID
	result.CampaignID = record.Request.CampaignID
	result.RequestID = record.Request.RequestID
	result.TransactionHash = record.Hash
	result.Timestamp = record.SubmittedAt
	result.ProcessedAt = time.Now()

	if record.Status == StatusConfirmed {
		switch record.Request.EventType {
		case TokenGrantRequested:
			result.EventType = TokenGrantCompleted
			result.GrantedAmount = record.Request.TokenAmount
			result.GrantedTokenType = record.Request.TokenType
			log.Printf("✅ Preparing grant completion result for user %s", result.UserID)
		case TokenExchangeRequested:
			result.EventType = TokenExchangeCompleted
			result.ExchangedFromAmount = record.Request.FromAmount
			result.ExchangedToAmount = record.Request.ToAmount
			result.ActualExchangeRate = record.Request.ExchangeRate
			log.Printf("✅ Preparing exchange completion result for user %s", result.UserID)
		}
	} else {
		switch record.Request.EventType {
		case TokenGrantRequested:
			result.EventType = TokenGrantFailed
			log.Printf("❌ Preparing grant failure result for user %s", result.UserID)
		case TokenExchangeRequested:
			result.EventType = TokenExchangeFailed
			log.Printf("❌ Preparing exchange failure result for user %s", result.UserID)
		}
		result.ErrorMessage = record.ErrorMsg
	}

	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %v", err)
	}

	log.Printf("📤 Publishing result message: %s", string(data))

	// Create message with ID for tracking
	msgID := fmt.Sprintf("%s-%s-%d", result.RequestID, result.TransactionHash, time.Now().UnixNano())

	publishResult := bs.resultTopic.Publish(ctx, &pubsub.Message{
		ID:   msgID,
		Data: data,
		Attributes: map[string]string{
			"event_type":  result.EventType.String(),
			"user_id":     result.UserID,
			"campaign_id": result.CampaignID,
			"request_id":  result.RequestID,
			"tx_hash":     result.TransactionHash,
			"msg_id":      msgID,
		},
	})

	serverID, err := publishResult.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to publish: %v", err)
	}

	log.Printf("✅ Final result published successfully for user %s: %s (tx: %s, server_id: %s)",
		result.UserID, result.EventType, result.TransactionHash, serverID)

	return nil
}

// Legacy PubSub processing (for backward compatibility)
func (bs *BlockchainService) ProcessRequests(ctx context.Context) {
	log.Println("⛓️  Blockchain Service: Starting request processor...")

	// Verify subscription exists before starting
	exists, err := bs.requestSub.Exists(ctx)
	if !exists || err != nil {
		log.Printf("❌ Request subscription not ready: exists=%v, err=%v", exists, err)
		return
	}

	messageCount := int64(0)

	err = bs.requestSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		atomic.AddInt64(&messageCount, 1)
		atomic.AddInt64(&bs.metrics.MessagesReceived, 1)

		log.Printf("📨 [PubSub Message #%d] ID: %s, PublishTime: %v",
			messageCount, msg.ID, msg.PublishTime)

		// Log message attributes
		for k, v := range msg.Attributes {
			log.Printf("  Attribute: %s = %s", k, v)
		}

		var request BlockchainRequest
		if err := json.Unmarshal(msg.Data, &request); err != nil {
			atomic.AddInt64(&bs.metrics.ProcessingErrors, 1)
			log.Printf("❌ Failed to unmarshal legacy request: %v", err)
			log.Printf("Raw message data: %s", string(msg.Data))
			msg.Nack()
			return
		}

		log.Printf("📨 Parsed legacy request: %s for user %s (request_id: %s)",
			request.EventType, request.UserID, request.RequestID)

		// Process the request asynchronously
		go func() {
			// Convert to new format
			txReq := TransactionRequest{
				EventType:     request.EventType,
				UserID:        request.UserID,
				CampaignID:    request.CampaignID,
				RequestID:     request.RequestID,
				TokenAmount:   request.TokenAmount,
				TokenType:     request.TokenType,
				FromTokenType: request.FromTokenType,
				ToTokenType:   request.ToTokenType,
				ExchangeRate:  request.ExchangeRate,
				FromAmount:    request.FromAmount,
				ToAmount:      request.ToAmount,
			}

			log.Printf("🔄 Processing PubSub request...")

			// Submit transaction
			response, err := bs.SubmitTransaction(txReq)
			if err != nil {
				atomic.AddInt64(&bs.metrics.ProcessingErrors, 1)
				log.Printf("❌ Failed to process request: %v", err)

				// Retry logic based on retry count
				if request.RetryCount < 3 {
					log.Printf("⚠️  Message will be retried (attempt %d/3)", request.RetryCount+1)
					msg.Nack()
				} else {
					log.Printf("❌ Message exceeded retry limit, acknowledging to prevent redelivery")
					msg.Ack() // Prevent infinite retries
				}
				return
			}

			// Update record with PubSub message ID
			bs.mu.Lock()
			if record, exists := bs.transactions[response.TransactionHash]; exists {
				record.PubSubMsgID = msg.ID
			}
			bs.mu.Unlock()

			atomic.AddInt64(&bs.metrics.MessagesProcessed, 1)
			log.Printf("✅ Request processed successfully: tx_hash=%s, status=%s",
				response.TransactionHash, response.Status)

			// Acknowledge only after successful processing
			msg.Ack()
		}()
	})

	if err != nil {
		log.Printf("❌ Error receiving messages: %v", err)
	}
}

// HTTP handlers
func (bs *BlockchainService) handleSubmitTransaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	response, err := bs.SubmitTransaction(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (bs *BlockchainService) handleTransactionStatus(w http.ResponseWriter, r *http.Request) {
	txHash := r.URL.Query().Get("hash")
	if txHash == "" {
		http.Error(w, "hash parameter is required", http.StatusBadRequest)
		return
	}

	bs.mu.RLock()
	record, exists := bs.transactions[txHash]
	bs.mu.RUnlock()

	if !exists {
		http.Error(w, "Transaction not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(record)
}

func (bs *BlockchainService) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check PubSub health
	if err := bs.checkPubSubHealth(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "unhealthy",
			"error":  err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "healthy",
		"last_health_check": bs.lastHealthCheck,
	})
}

func (bs *BlockchainService) handleStats(w http.ResponseWriter, r *http.Request) {
	bs.mu.RLock()
	totalTxs := len(bs.transactions)
	confirmedTxs := 0
	failedTxs := 0
	pendingTxs := 0

	for _, record := range bs.transactions {
		switch record.Status {
		case StatusConfirmed:
			confirmedTxs++
		case StatusFailed:
			failedTxs++
		case StatusPending:
			pendingTxs++
		}
	}
	bs.mu.RUnlock()

	stats := map[string]interface{}{
		"service":              "blockchain",
		"status":               "running",
		"uptime":               time.Since(startTime).String(),
		"is_healthy":           bs.isHealthy,
		"last_health_check":    bs.lastHealthCheck,
		"supported_operations": []string{"token_grant", "token_exchange"},
		"transaction_stats": map[string]interface{}{
			"total":     totalTxs,
			"confirmed": confirmedTxs,
			"failed":    failedTxs,
			"pending":   pendingTxs,
		},
		"pubsub_metrics": map[string]interface{}{
			"messages_received":  atomic.LoadInt64(&bs.metrics.MessagesReceived),
			"messages_processed": atomic.LoadInt64(&bs.metrics.MessagesProcessed),
			"messages_published": atomic.LoadInt64(&bs.metrics.MessagesPublished),
			"publish_errors":     atomic.LoadInt64(&bs.metrics.PublishErrors),
			"processing_errors":  atomic.LoadInt64(&bs.metrics.ProcessingErrors),
		},
		"blockchain_metrics": map[string]interface{}{
			"transactions_created": atomic.LoadInt64(&bs.metrics.TransactionsCreated),
			"transactions_success": atomic.LoadInt64(&bs.metrics.TransactionsSuccess),
			"transactions_failed":  atomic.LoadInt64(&bs.metrics.TransactionsFailed),
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

	log.Println("🚀 Blockchain Service Starting...")

	// Initialize blockchain service
	blockchainService := NewBlockchainService(client)

	// Initialize PubSub resources with retry
	if err := blockchainService.InitializePubSub(ctx); err != nil {
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
				if err := blockchainService.checkPubSubHealth(ctx); err != nil {
					log.Printf("⚠️  Health check failed: %v", err)
				}
				cancel()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start processing requests in background
	go blockchainService.ProcessRequests(ctx)

	// Configure HTTP endpoints
	http.HandleFunc("/health", blockchainService.handleHealth)
	http.HandleFunc("/stats", blockchainService.handleStats)
	http.HandleFunc("/submit-transaction", blockchainService.handleSubmitTransaction)
	http.HandleFunc("/transaction-status", blockchainService.handleTransactionStatus)

	// Start HTTP server in background
	go func() {
		log.Println("📡 HTTP server starting on :8081")
		if err := http.ListenAndServe(":8081", nil); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	log.Println("⛓️  Blockchain Service is ready!")
	log.Println("📊 Test endpoints:")
	log.Println("  GET  /health - Service health check")
	log.Println("  GET  /stats - Service statistics")
	log.Println("  POST /submit-transaction - Submit blockchain transaction")
	log.Println("  GET  /transaction-status?hash=<tx_hash> - Check transaction status")
	log.Println("🔧 Supported operations:")
	log.Println("  - Token Grant (ERC20 minting)")
	log.Println("  - Token Exchange (Token swapping)")

	// Wait for interrupt signal to shutdown gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	<-c

	log.Println("📴 Blockchain Service shutting down...")

	// Give ongoing operations time to complete
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Log final metrics
	log.Printf("📊 Final metrics:")
	log.Printf("  Messages received: %d", atomic.LoadInt64(&blockchainService.metrics.MessagesReceived))
	log.Printf("  Messages processed: %d", atomic.LoadInt64(&blockchainService.metrics.MessagesProcessed))
	log.Printf("  Messages published: %d", atomic.LoadInt64(&blockchainService.metrics.MessagesPublished))
	log.Printf("  Transactions created: %d", atomic.LoadInt64(&blockchainService.metrics.TransactionsCreated))
	log.Printf("  Transactions succeeded: %d", atomic.LoadInt64(&blockchainService.metrics.TransactionsSuccess))
	log.Printf("  Transactions failed: %d", atomic.LoadInt64(&blockchainService.metrics.TransactionsFailed))
}
