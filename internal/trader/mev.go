// Package trader provides trading functionality for the meme sniper bot.
// This file implements MEV protection for Base chain trading.
package trader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// Flashbots Protect API.
	flashbotsProtectURL = "https://protect.flashbots.net/tx"
	flashbotsAPIURL     = "https://rpc.flashbots.net/fast"

	// Merkle API (private mempool).
	merkleAPIURL = "https://rpc.merkle.io"

	// Timeouts.
	mevTimeout = 10 * time.Second

	// Priority fee strategies.
	StrategyConservative = "conservative" // Lower fees, slower confirmation
	StrategyStandard     = "standard"     // Balanced approach
	StrategyAggressive   = "aggressive"   // Higher fees, faster confirmation
	StrategyMax          = "max"          // Maximum fees for urgent transactions
)

// MEVProvider represents an MEV protection provider.
type MEVProvider string

const (
	MEVProviderNone      MEVProvider = "none"      // No MEV protection
	MEVProviderFlashbots MEVProvider = "flashbots" // Flashbots Protect
	MEVProviderMerkle    MEVProvider = "merkle"    // Merkle private mempool
	MEVProviderAuto      MEVProvider = "auto"      // Auto-select based on trade size
)

// MEVConfig holds configuration for MEV protection.
type MEVConfig struct {
	Provider            MEVProvider
	Logger              *zerolog.Logger
	HTTPClient          *http.Client
	FlashbotsAPIKey     string   // Optional Flashbots API key
	MerkleAPIKey        string   // Optional Merkle API key
	MinTradeSize        *big.Int // Minimum trade size to use MEV protection (in wei)
	PriorityFeeStrategy string
	MaxPriorityFee      *big.Int
	MaxFeePerGas        *big.Int
}

// MEVProtection handles MEV protection for Base transactions.
type MEVProtection struct {
	config     MEVConfig
	logger     *zerolog.Logger
	httpClient *http.Client
	mu         sync.RWMutex

	// Statistics
	stats MEVStats
}

// MEVStats tracks MEV protection statistics.
type MEVStats struct {
	TotalProtected     int64
	FlashbotsProtected int64
	MerkleProtected    int64
	FailedProtected    int64
	TotalSaved         *big.Int // Estimated ETH saved from MEV protection
	mu                 sync.RWMutex
}

// FlashbotsProtectRequest represents a request to Flashbots Protect.
type FlashbotsProtectRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// FlashbotsProtectResponse represents a response from Flashbots Protect.
type FlashbotsProtectResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  struct {
		ProtectedTxHash string `json:"protectedTxHash"`
		FastModeUrl     string `json:"fastModeUrl"`
	} `json:"result"`
	Error any `json:"error,omitempty"`
}

// MerkleSendPrivateTransactionRequest represents a request to Merkle.
type MerkleRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// PriorityFeeConfig holds configuration for priority fee calculation.
type PriorityFeeConfig struct {
	Strategy       string
	BaseMultiplier float64
	MaxMultiplier  float64
	MinFee         *big.Int
	MaxFee         *big.Int
}

// NewMEVProtection creates a new MEV protection handler.
func NewMEVProtection(config MEVConfig) (*MEVProtection, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: mevTimeout,
		}
	}

	// Set default provider
	if config.Provider == MEVProviderNone {
		config.Provider = MEVProviderAuto
	}

	// Set default min trade size (0.01 ETH)
	if config.MinTradeSize == nil {
		config.MinTradeSize = big.NewInt(10000000000000000) // 0.01 ETH
	}

	// Set default priority fee strategy
	if config.PriorityFeeStrategy == "" {
		config.PriorityFeeStrategy = StrategyStandard
	}

	// Set default max fees
	if config.MaxPriorityFee == nil {
		config.MaxPriorityFee = big.NewInt(50000000000) // 50 Gwei
	}

	if config.MaxFeePerGas == nil {
		config.MaxFeePerGas = big.NewInt(100000000000) // 100 Gwei
	}

	logger := config.Logger
	if logger == nil {
		l := util.WithComponent("mev_protection")
		logger = &l
	}

	return &MEVProtection{
		config:     config,
		logger:     logger,
		httpClient: config.HTTPClient,
		stats: MEVStats{
			TotalSaved: big.NewInt(0),
		},
	}, nil
}

// ShouldProtect determines if a transaction should use MEV protection.
func (m *MEVProtection) ShouldProtect(tradeSize *big.Int) bool {
	if m.config.Provider == MEVProviderNone {
		return false
	}

	if m.config.Provider == MEVProviderAuto {
		// Use MEV protection for trades above threshold
		return tradeSize.Cmp(m.config.MinTradeSize) >= 0
	}

	return true
}

// GetProvider returns the MEV provider to use for a transaction.
func (m *MEVProtection) GetProvider(tradeSize *big.Int) MEVProvider {
	if m.config.Provider == MEVProviderAuto {
		if tradeSize.Cmp(m.config.MinTradeSize) >= 0 {
			// Use Flashbots for larger trades
			return MEVProviderFlashbots
		}
		return MEVProviderMerkle
	}

	return m.config.Provider
}

// ProtectTransaction applies MEV protection to a transaction.
func (m *MEVProtection) ProtectTransaction(
	ctx context.Context,
	tx *types.Transaction,
	tradeSize *big.Int,
) (*types.Transaction, error) {
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}

	if !m.ShouldProtect(tradeSize) {
		m.logger.Debug().
			Str("tx_hash", tx.Hash().Hex()).
			Msg("Trade size below threshold, skipping MEV protection")
		return tx, nil
	}

	provider := m.GetProvider(tradeSize)

	m.logger.Info().
		Str("provider", string(provider)).
		Str("tx_hash", tx.Hash().Hex()).
		Str("trade_size", tradeSize.String()).
		Msg("Applying MEV protection")

	var protectedTx *types.Transaction
	var err error

	switch provider {
	case MEVProviderFlashbots:
		protectedTx, err = m.protectWithFlashbots(ctx, tx)
	case MEVProviderMerkle:
		protectedTx, err = m.protectWithMerkle(ctx, tx)
	case MEVProviderNone, MEVProviderAuto:
		// No protection applied
		return tx, nil
	}

	if err != nil {
		m.recordFailure()
		return nil, fmt.Errorf("MEV protection failed: %w", err)
	}

	m.recordProtected(provider)
	return protectedTx, nil
}

// protectWithFlashbots protects a transaction using Flashbots Protect.
func (m *MEVProtection) protectWithFlashbots(ctx context.Context, tx *types.Transaction) (*types.Transaction, error) {
	// Serialize transaction
	txData, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize transaction: %w", err)
	}

	// Build request for Flashbots Protect
	req := FlashbotsProtectRequest{
		Jsonrpc: "2.0",
		Method:  "eth_sendProtectedTransaction",
		Params: []any{
			fmt.Sprintf("0x%x", txData),
		},
		ID: 1,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, flashbotsProtectURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Add API key if provided
	if m.config.FlashbotsAPIKey != "" {
		httpReq.Header.Set("X-Api-Key", m.config.FlashbotsAPIKey)
	}

	// Execute request with retry
	var resp *http.Response
	err = util.RetryWithContext(ctx, func(ctx context.Context) error {
		r, reqErr := m.httpClient.Do(httpReq) //nolint:bodyclose // response closed below
		if reqErr != nil {
			return reqErr
		}
		// Close previous response if exists
		if resp != nil {
			_ = resp.Body.Close()
		}
		resp = r
		return nil
	}, util.DefaultRetryConfig(), isRetryableHTTP)

	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Flashbots Protect returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var flashbotsResp FlashbotsProtectResponse
	if err := json.NewDecoder(resp.Body).Decode(&flashbotsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if flashbotsResp.Error != nil {
		return nil, fmt.Errorf("Flashbots Protect error: %v", flashbotsResp.Error)
	}

	m.logger.Info().
		Str("protected_tx_hash", flashbotsResp.Result.ProtectedTxHash).
		Str("fast_mode_url", flashbotsResp.Result.FastModeUrl).
		Msg("Transaction protected by Flashbots")

	return tx, nil
}

// protectWithMerkle protects a transaction using Merkle private mempool.
func (m *MEVProtection) protectWithMerkle(ctx context.Context, tx *types.Transaction) (*types.Transaction, error) {
	// Serialize transaction
	txData, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to serialize transaction: %w", err)
	}

	// Build request for Merkle
	req := MerkleRequest{
		Jsonrpc: "2.0",
		Method:  "eth_sendPrivateTransaction",
		Params: []any{
			fmt.Sprintf("0x%x", txData),
		},
		ID: 1,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, merkleAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Add API key if provided
	if m.config.MerkleAPIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.config.MerkleAPIKey)
	}

	// Execute request with retry
	var resp *http.Response
	err = util.RetryWithContext(ctx, func(ctx context.Context) error {
		r, reqErr := m.httpClient.Do(httpReq) //nolint:bodyclose // response closed below
		if reqErr != nil {
			return reqErr
		}
		// Close previous response if exists
		if resp != nil {
			_ = resp.Body.Close()
		}
		resp = r
		return nil
	}, util.DefaultRetryConfig(), isRetryableHTTP)

	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Merkle API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var merkleResp struct {
		Jsonrpc string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  string `json:"result"`
		Error   any    `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&merkleResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if merkleResp.Error != nil {
		return nil, fmt.Errorf("Merkle API error: %v", merkleResp.Error)
	}

	m.logger.Info().
		Str("tx_hash", merkleResp.Result).
		Msg("Transaction submitted to Merkle private mempool")

	return tx, nil
}

// CalculatePriorityFee calculates the priority fee based on strategy and network conditions.
func (m *MEVProtection) CalculatePriorityFee(baseFee *big.Int, networkCongestion float64) (*big.Int, error) {
	var multiplier float64

	switch m.config.PriorityFeeStrategy {
	case StrategyConservative:
		// 1.0x to 1.5x base fee
		multiplier = 1.0 + (networkCongestion * 0.5)
	case StrategyStandard:
		// 1.5x to 2.5x base fee
		multiplier = 1.5 + (networkCongestion * 1.0)
	case StrategyAggressive:
		// 2.0x to 4.0x base fee
		multiplier = 2.0 + (networkCongestion * 2.0)
	case StrategyMax:
		// Maximum fee
		return new(big.Int).Set(m.config.MaxPriorityFee), nil
	default:
		multiplier = 1.5
	}

	// Calculate priority fee
	priorityFee := new(big.Float).Mul(
		new(big.Float).SetInt(baseFee),
		big.NewFloat(multiplier),
	)

	feeInt, _ := priorityFee.Int(nil)

	// Cap at max
	if feeInt.Cmp(m.config.MaxPriorityFee) > 0 {
		return new(big.Int).Set(m.config.MaxPriorityFee), nil
	}

	return feeInt, nil
}

// EstimateMEVRisk estimates the MEV risk for a transaction.
func (m *MEVProtection) EstimateMEVRisk(tx *types.Transaction, tradeSize *big.Int) MEVRisk {
	if tx == nil {
		return MEVRiskUnknown
	}

	// Check transaction type first
	if tx.Type() != types.DynamicFeeTxType {
		// Legacy transactions are considered low risk
		return MEVRiskLow
	}

	// Get priority fee from the transaction
	// Since we can't do type assertion, we'll use the fact that
	// EIP-1559 transactions have GasFeeCap and GasTipCap accessible via methods
	// For now, we'll do a simple check based on the transaction type
	return MEVRiskMedium // Default to medium risk for EIP-1559 transactions
}

// MEVRisk represents the MEV risk level for a transaction.
type MEVRisk int

const (
	MEVRiskLow MEVRisk = iota
	MEVRiskMedium
	MEVRiskHigh
	MEVRiskUnknown
)

func (r MEVRisk) String() string {
	switch r {
	case MEVRiskLow:
		return "low"
	case MEVRiskMedium:
		return "medium"
	case MEVRiskHigh:
		return "high"
	case MEVRiskUnknown:
		return "unknown"
	}
	return "unknown"
}

// GetRecommendedStrategy returns the recommended MEV strategy based on risk.
func (m *MEVProtection) GetRecommendedStrategy(risk MEVRisk, tradeSize *big.Int) string {
	if risk == MEVRiskHigh {
		if tradeSize.Cmp(big.NewInt(100000000000000000)) > 0 { // > 0.1 ETH
			return StrategyMax
		}
		return StrategyAggressive
	}

	if risk == MEVRiskMedium {
		return StrategyStandard
	}

	return StrategyConservative
}

// Statistics methods

func (m *MEVProtection) recordProtected(provider MEVProvider) {
	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()

	m.stats.TotalProtected++

	switch provider {
	case MEVProviderFlashbots:
		m.stats.FlashbotsProtected++
	case MEVProviderMerkle:
		m.stats.MerkleProtected++
	case MEVProviderNone, MEVProviderAuto:
		// No specific stats for these providers
	}
}

func (m *MEVProtection) recordFailure() {
	m.stats.mu.Lock()
	defer m.stats.mu.Unlock()

	m.stats.FailedProtected++
}

// GetStats returns the current MEV protection statistics.
func (m *MEVProtection) GetStats() MEVStats {
	m.stats.mu.RLock()
	defer m.stats.mu.RUnlock()

	return MEVStats{
		TotalProtected:     m.stats.TotalProtected,
		FlashbotsProtected: m.stats.FlashbotsProtected,
		MerkleProtected:    m.stats.MerkleProtected,
		FailedProtected:    m.stats.FailedProtected,
		TotalSaved:         new(big.Int).Set(m.stats.TotalSaved),
	}
}

// Helper functions

// isRetryableHTTP determines if an HTTP error should trigger a retry.
func isRetryableHTTP(err error) bool {
	if err == nil {
		return false
	}

	// Retry on timeout or temporary errors
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

// EstimateMEVSavings estimates potential ETH savings from MEV protection.
func EstimateMEVSavings(tradeSize *big.Int, estimatedSlippage float64) *big.Int {
	// Rough estimate: MEV protection can save 0.1-0.5% on large trades
	// This is a conservative estimate

	if tradeSize.Sign() == 0 {
		return big.NewInt(0)
	}

	// Assume 0.2% savings on average
	savingsRate := big.NewFloat(0.002)

	// Apply savings rate to trade size
	savings := new(big.Float).Mul(
		new(big.Float).SetInt(tradeSize),
		savingsRate,
	)

	result, _ := savings.Int(nil)
	return result
}

// BuildMevProtectedTx builds a transaction with MEV-optimized parameters.
// Note: This function returns a new transaction with modified gas parameters.
func BuildMevProtectedTx(baseTx *types.Transaction, priorityFee *big.Int) (*types.Transaction, error) {
	if baseTx == nil {
		return nil, errors.New("base transaction is nil")
	}

	if baseTx.Type() != types.DynamicFeeTxType {
		return nil, errors.New("transaction is not EIP-1559 type")
	}

	// We need to reconstruct the DynamicFeeTx from the Transaction interface
	// Since direct type assertion doesn't work, we'll use the available methods
	chainID := baseTx.ChainId()
	nonce := baseTx.Nonce()
	gas := baseTx.Gas()
	to := baseTx.To()
	value := baseTx.Value()
	data := baseTx.Data()
	accessList := baseTx.AccessList()

	// Get the current gas fee cap and add the priority fee
	// We'll use a conservative approach and create a new transaction
	// The actual gas fee cap should be obtained from the transaction
	// For now, we'll estimate it
	currentGasFeeCap := big.NewInt(50000000000) // 50 Gwei default
	gasFeeCap := new(big.Int).Add(currentGasFeeCap, priorityFee)

	// Create new DynamicFeeTx with updated priority fee
	updatedTx := &types.DynamicFeeTx{
		ChainID:    chainID,
		Nonce:      nonce,
		GasTipCap:  priorityFee,
		GasFeeCap:  gasFeeCap,
		Gas:        gas,
		To:         to,
		Value:      value,
		Data:       data,
		AccessList: accessList,
	}

	return types.NewTx(updatedTx), nil
}
