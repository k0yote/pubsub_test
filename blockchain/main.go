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
	Hash        string             `json:"hash"`
	Status      TransactionStatus  `json:"status"`
	Request     TransactionRequest `json:"request"`
	SubmittedAt time.Time          `json:"submitted_at"`
	ConfirmedAt *time.Time         `json:"confirmed_at,omitempty"`
	ErrorMsg    string             `json:"error_message,omitempty"`
	Retries     int                `json:"retries"`
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
}

func NewBlockchainService(client *pubsub.Client) *BlockchainService {
	return &BlockchainService{
		client:       client,
		requestSub:   client.Subscription("blockchain-requests-sub"),
		resultTopic:  client.Topic("blockchain-results"),
		transactions: make(map[string]*TransactionRecord),
	}
}

// Synchronous transaction submission
func (bs *BlockchainService) SubmitTransaction(req TransactionRequest) (*TransactionResponse, error) {
	log.Printf("🔗 Submitting transaction: %s for user %s", req.EventType, req.UserID)

	// Simulate blockchain transaction submission
	txHash, err := bs.sendToBlockchain(req)
	if err != nil {
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
		log.Printf("✅ Transaction confirmed: %s", txHash)
	} else {
		record.Status = StatusFailed
		record.ErrorMsg = "Transaction reverted or block reorganization"
		record.ConfirmedAt = &now
		log.Printf("❌ Transaction failed: %s", txHash)
	}
	bs.mu.Unlock()

	// Publish final result via PubSub
	bs.publishFinalResult(record)
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
		return true // 100% success rate for debugging
	case TokenExchangeRequested:
		return true // 100% success rate for debugging
	default:
		return true // 100% success rate for debugging
	}
}

// Publish final result to PubSub
func (bs *BlockchainService) publishFinalResult(record *TransactionRecord) {
	log.Printf("🔄 Publishing final result for transaction: %s", record.Hash)
	ctx := context.Background()

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
		log.Printf("❌ Failed to marshal final result: %v", err)
		return
	}

	log.Printf("📤 Publishing result message: %s", string(data))

	publishResult := bs.resultTopic.Publish(ctx, &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"event_type":  result.EventType.String(),
			"user_id":     result.UserID,
			"campaign_id": result.CampaignID,
			"request_id":  result.RequestID,
			"tx_hash":     result.TransactionHash,
		},
	})

	_, err = publishResult.Get(ctx)
	if err != nil {
		log.Printf("❌ Failed to publish final result: %v", err)
		return
	}

	log.Printf("✅ Final result published successfully for user %s: %s (tx: %s)",
		result.UserID, result.EventType, result.TransactionHash)
}

// Legacy PubSub processing (for backward compatibility)
func (bs *BlockchainService) ProcessRequests(ctx context.Context) {
	log.Println("⛓️  Blockchain Service: Listening for legacy PubSub requests...")

	bs.requestSub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		log.Printf("📨 Received PubSub message: %s", string(msg.Data))

		var request BlockchainRequest
		if err := json.Unmarshal(msg.Data, &request); err != nil {
			log.Printf("❌ Failed to unmarshal legacy request: %v", err)
			log.Printf("Raw message data: %s", string(msg.Data))
			msg.Nack()
			return
		}

		log.Printf("📨 Parsed legacy request: %s for user %s (request_id: %s)",
			request.EventType, request.UserID, request.RequestID)

		// Convert to new format and process
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

		log.Printf("🔄 Converting legacy request to new format for processing...")

		// Submit transaction synchronously
		response, err := bs.SubmitTransaction(txReq)
		if err != nil {
			log.Printf("❌ Failed to process legacy request: %v", err)
			msg.Nack()
			return
		}

		log.Printf("✅ Legacy request processed successfully: tx_hash=%s, status=%s",
			response.TransactionHash, response.Status)
		msg.Ack()
	})
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
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Blockchain Service is healthy"))
}

func (bs *BlockchainService) handleStats(w http.ResponseWriter, r *http.Request) {
	bs.mu.RLock()
	totalTxs := len(bs.transactions)
	confirmedTxs := 0
	failedTxs := 0

	for _, record := range bs.transactions {
		switch record.Status {
		case StatusConfirmed:
			confirmedTxs++
		case StatusFailed:
			failedTxs++
		}
	}
	bs.mu.RUnlock()

	stats := map[string]interface{}{
		"service":              "blockchain",
		"status":               "running",
		"uptime":               time.Since(startTime).String(),
		"supported_operations": []string{"token_grant", "token_exchange"},
		"transaction_stats": map[string]int{
			"total":     totalTxs,
			"confirmed": confirmedTxs,
			"failed":    failedTxs,
			"pending":   totalTxs - confirmedTxs - failedTxs,
		},
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
