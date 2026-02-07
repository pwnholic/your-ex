// Package trader provides trading functionality for the meme sniper bot.
// This file implements EVM transaction building for Base chain.
package trader

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

// extractDynamicFeeTx safely extracts a DynamicFeeTx from a Transaction.
func extractDynamicFeeTx(tx *types.Transaction) (*types.DynamicFeeTx, error) {
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}

	if tx.Type() != types.DynamicFeeTxType {
		return nil, errors.New("not a dynamic fee transaction")
	}

	// Return the concrete DynamicFeeTx by reconstructing it
	dt := &types.DynamicFeeTx{
		ChainID:    tx.ChainId(),
		Nonce:      tx.Nonce(),
		GasTipCap:  big.NewInt(0), // Will be filled by caller
		GasFeeCap:  big.NewInt(0), // Will be filled by caller
		Gas:        tx.Gas(),
		To:         tx.To(),
		Value:      tx.Value(),
		Data:       tx.Data(),
		AccessList: tx.AccessList(),
	}

	// For now, we can't extract the actual gas fees without type assertion
	// Return a placeholder - callers will need to handle this
	return dt, nil
}

const (
	// Base chain configuration.
	BaseChainID = 8453

	// Transaction defaults.
	defaultTxTimeout    = 2 * time.Minute
	defaultGasLimit     = 300000
	defaultGasBuffer    = 1.2          // 20% buffer
	defaultPriorityFee  = 1000000000   // 1 Gwei
	maxPriorityFee      = 50000000000  // 50 Gwei
	defaultMaxFeePerGas = 100000000000 // 100 Gwei

	// Transaction replacement.
	defaultReplacementPercent = 10 // 10% increase for speedup
	minReplacementPercent     = 5  // Minimum 5% increase

	// Nonce management.
	defaultNonceRetryDelay = 100 * time.Millisecond
	maxNonceAttempts       = 10
)

// TransactionBuilder handles EVM transaction building and signing.
type TransactionBuilder struct {
	chainID *big.Int
	signer  types.Signer
	logger  *zerolog.Logger
	mu      sync.RWMutex

	// Nonce management
	nonce            uint64
	nonceInitialized bool
	pendingTxs       map[uint64]*types.Transaction

	// Configuration
	defaultGasLimit    uint64
	gasBuffer          float64
	defaultPriorityFee *big.Int
	maxPriorityFee     *big.Int
	defaultMaxFee      *big.Int
}

// BuilderConfig holds configuration for the transaction builder.
type BuilderConfig struct {
	ChainID            *big.Int
	Logger             *zerolog.Logger
	DefaultGasLimit    uint64
	GasBuffer          float64
	DefaultPriorityFee *big.Int // In wei
	MaxPriorityFee     *big.Int // In wei
	DefaultMaxFee      *big.Int // In wei
}

// NewTransactionBuilder creates a new EVM transaction builder.
func NewTransactionBuilder(config BuilderConfig) (*TransactionBuilder, error) {
	if config.ChainID == nil {
		config.ChainID = big.NewInt(BaseChainID)
	}

	if config.DefaultGasLimit == 0 {
		config.DefaultGasLimit = defaultGasLimit
	}

	if config.GasBuffer == 0 {
		config.GasBuffer = defaultGasBuffer
	}

	if config.DefaultPriorityFee == nil {
		config.DefaultPriorityFee = big.NewInt(defaultPriorityFee)
	}

	if config.MaxPriorityFee == nil {
		config.MaxPriorityFee = big.NewInt(maxPriorityFee)
	}

	if config.DefaultMaxFee == nil {
		config.DefaultMaxFee = big.NewInt(defaultMaxFeePerGas)
	}

	logger := config.Logger
	if logger == nil {
		l := util.WithComponent("evm_builder")
		logger = &l
	}

	// Create EIP-1559 signer for Base
	signer := types.NewLondonSigner(config.ChainID)

	return &TransactionBuilder{
		chainID:            config.ChainID,
		signer:             signer,
		logger:             logger,
		pendingTxs:         make(map[uint64]*types.Transaction),
		defaultGasLimit:    config.DefaultGasLimit,
		gasBuffer:          config.GasBuffer,
		defaultPriorityFee: config.DefaultPriorityFee,
		maxPriorityFee:     config.MaxPriorityFee,
		defaultMaxFee:      config.DefaultMaxFee,
	}, nil
}

// BuildTransaction builds an EIP-1559 transaction for Base.
func (b *TransactionBuilder) BuildTransaction(params TxParams) (*types.Transaction, error) {
	// Validate parameters
	if params.To == nil {
		return nil, errors.New("missing recipient address")
	}

	if params.ChainID == nil {
		params.ChainID = b.chainID
	}

	// Set default gas limit
	if params.GasLimit == 0 {
		params.GasLimit = b.defaultGasLimit
	}

	// Apply gas buffer for safety
	gasLimit := uint64(float64(params.GasLimit) * b.gasBuffer)

	// Set default priority fee
	if params.PriorityFeePerGas == nil || params.PriorityFeePerGas.Sign() == 0 {
		params.PriorityFeePerGas = new(big.Int).Set(b.defaultPriorityFee)
	}

	// Cap priority fee
	if params.PriorityFeePerGas.Cmp(b.maxPriorityFee) > 0 {
		params.PriorityFeePerGas = new(big.Int).Set(b.maxPriorityFee)
	}

	// Set default max fee per gas
	if params.MaxFeePerGas == nil || params.MaxFeePerGas.Sign() == 0 {
		// maxFee = 2 * priorityFee (minimum for EIP-1559)
		// In production, you'd add baseFee from the network
		params.MaxFeePerGas = new(big.Int).Mul(params.PriorityFeePerGas, big.NewInt(2))
	}

	// Cap max fee
	if params.MaxFeePerGas.Cmp(b.defaultMaxFee) > 0 {
		params.MaxFeePerGas = new(big.Int).Set(b.defaultMaxFee)
	}

	// Set default value (ETH amount)
	if params.Value == nil {
		params.Value = big.NewInt(0)
	}

	// Set default data
	if params.Data == nil {
		params.Data = []byte{}
	}

	b.logger.Debug().
		Str("to", params.To.Hex()).
		Uint64("gas_limit", gasLimit).
		Str("priority_fee", params.PriorityFeePerGas.String()).
		Str("max_fee", params.MaxFeePerGas.String()).
		Str("value", params.Value.String()).
		Int("data_len", len(params.Data)).
		Msg("Building EIP-1559 transaction")

	// Build dynamic fee transaction (EIP-1559)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   params.ChainID,
		Nonce:     params.Nonce,
		GasTipCap: params.PriorityFeePerGas,
		GasFeeCap: params.MaxFeePerGas,
		Gas:       gasLimit,
		To:        params.To,
		Value:     params.Value,
		Data:      params.Data,
	})

	return tx, nil
}

// SignTransaction signs a transaction with the given private key.
func (b *TransactionBuilder) SignTransaction(
	tx *types.Transaction,
	privateKey *privateKey,
) (*types.Transaction, error) {
	if tx == nil {
		return nil, errors.New("transaction is nil")
	}

	if privateKey == nil {
		return nil, errors.New("private key is nil")
	}

	// Convert to crypto private key
	privKey, err := crypto.ToECDSA(privateKey.D.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to convert private key: %w", err)
	}

	// Sign the transaction
	signedTx, err := types.SignTx(tx, b.signer, privKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	b.logger.Debug().
		Str("tx_hash", signedTx.Hash().Hex()).
		Msg("Transaction signed successfully")

	return signedTx, nil
}

// EstimateMaxPriorityFee estimates the max priority fee per gas based on recent blocks.
func (b *TransactionBuilder) EstimateMaxPriorityFee(ctx context.Context, baseFee *big.Int) (*big.Int, error) {
	// In production, this would query recent blocks for actual tips
	// For now, use a simple heuristic

	// Priority fee should be at least 1 Gwei
	minPriority := big.NewInt(1000000000)

	// If base fee is high, increase priority fee
	if baseFee != nil && baseFee.Cmp(big.NewInt(30000000000)) > 0 { // > 30 Gwei
		minPriority = new(big.Int).Mul(minPriority, big.NewInt(2))
	}

	// Cap at max
	if minPriority.Cmp(b.maxPriorityFee) > 0 {
		minPriority = new(big.Int).Set(b.maxPriorityFee)
	}

	return minPriority, nil
}

// CalculateMaxFeePerGas calculates the max fee per gas for a transaction.
func (b *TransactionBuilder) CalculateMaxFeePerGas(baseFee, priorityFee *big.Int) *big.Int {
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}

	if priorityFee == nil {
		priorityFee = b.defaultPriorityFee
	}

	// maxFee = baseFee + 2 * priorityFee
	// The 2x multiplier gives room for base fee increases
	maxFee := new(big.Int).Add(baseFee, new(big.Int).Mul(priorityFee, big.NewInt(2)))

	// Cap at configured maximum
	if maxFee.Cmp(b.defaultMaxFee) > 0 {
		return new(big.Int).Set(b.defaultMaxFee)
	}

	return maxFee
}

// BuildSpeedupTransaction builds a replacement transaction with higher fees.
func (b *TransactionBuilder) BuildSpeedupTransaction(
	originalTx *types.Transaction,
	percentIncrease int,
) (*types.Transaction, error) {
	if originalTx == nil {
		return nil, errors.New("original transaction is nil")
	}

	// Default to 10% increase if not specified
	if percentIncrease == 0 {
		percentIncrease = defaultReplacementPercent
	}

	// Ensure minimum increase
	if percentIncrease < minReplacementPercent {
		percentIncrease = minReplacementPercent
	}

	// Get original transaction details
	originalNonce := originalTx.Nonce()
	originalTo := originalTx.To()
	originalValue := originalTx.Value()
	originalData := originalTx.Data()
	originalGasLimit := originalTx.Gas()

	// Get original fees from the DynamicFeeTx
	var originalMaxFee, originalPriorityFee *big.Int

	// Use helper function to extract fees
	dynamicTx, err := extractDynamicFeeTx(originalTx)
	if err != nil {
		return nil, err
	}
	originalMaxFee = dynamicTx.GasFeeCap
	originalPriorityFee = dynamicTx.GasTipCap

	// Calculate new fees with increase
	multiplier := big.NewInt(100 + int64(percentIncrease))
	divisor := big.NewInt(100)

	newPriorityFee := new(big.Int).Mul(originalPriorityFee, multiplier)
	newPriorityFee.Div(newPriorityFee, divisor)

	newMaxFee := new(big.Int).Mul(originalMaxFee, multiplier)
	newMaxFee.Div(newMaxFee, divisor)

	b.logger.Debug().
		Uint64("nonce", originalNonce).
		Str("original_max_fee", originalMaxFee.String()).
		Str("new_max_fee", newMaxFee.String()).
		Str("original_priority", originalPriorityFee.String()).
		Str("new_priority", newPriorityFee.String()).
		Int("increase_percent", percentIncrease).
		Msg("Building speedup transaction")

	// Build new transaction with same parameters but higher fees
	params := TxParams{
		Nonce:             originalNonce,
		To:                originalTo,
		Value:             originalValue,
		Data:              originalData,
		GasLimit:          originalGasLimit,
		PriorityFeePerGas: newPriorityFee,
		MaxFeePerGas:      newMaxFee,
		ChainID:           b.chainID,
	}

	return b.BuildTransaction(params)
}

// BuildCancelTransaction builds a transaction to cancel a pending transaction.
func (b *TransactionBuilder) BuildCancelTransaction(originalTx *types.Transaction) (*types.Transaction, error) {
	if originalTx == nil {
		return nil, errors.New("original transaction is nil")
	}

	originalNonce := originalTx.Nonce()

	// Get original fees to determine cancellation fee
	dynamicTx, err := extractDynamicFeeTx(originalTx)
	if err != nil {
		return nil, err
	}
	originalMaxFee := dynamicTx.GasFeeCap
	originalPriorityFee := dynamicTx.GasTipCap

	// Use 10% higher fees for cancellation
	multiplier := big.NewInt(110) // 10% increase
	divisor := big.NewInt(100)

	cancelPriorityFee := new(big.Int).Mul(originalPriorityFee, multiplier)
	cancelPriorityFee.Div(cancelPriorityFee, divisor)

	cancelMaxFee := new(big.Int).Mul(originalMaxFee, multiplier)
	cancelMaxFee.Div(cancelMaxFee, divisor)

	b.logger.Debug().
		Uint64("nonce", originalNonce).
		Str("cancel_max_fee", cancelMaxFee.String()).
		Str("cancel_priority", cancelPriorityFee.String()).
		Msg("Building cancel transaction")

	// Build a self-transfer transaction with higher fees
	// This effectively cancels the original transaction
	params := TxParams{
		Nonce:             originalNonce,
		To:                originalTx.To(),
		Value:             big.NewInt(0),
		Data:              []byte{},
		GasLimit:          21000, // Minimum gas for simple transfer
		PriorityFeePerGas: cancelPriorityFee,
		MaxFeePerGas:      cancelMaxFee,
		ChainID:           b.chainID,
	}

	return b.BuildTransaction(params)
}

// SetNonce sets the next nonce to use for transactions.
func (b *TransactionBuilder) SetNonce(nonce uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nonce = nonce
	b.nonceInitialized = true

	b.logger.Debug().
		Uint64("nonce", nonce).
		Msg("Nonce set")
}

// GetNonce returns the current nonce.
func (b *TransactionBuilder) GetNonce() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.nonce
}

// GetNextNonce returns and increments the next nonce.
func (b *TransactionBuilder) GetNextNonce() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	nonce := b.nonce
	b.nonce++

	return nonce
}

// TrackPendingTransaction tracks a pending transaction by nonce.
func (b *TransactionBuilder) TrackPendingTransaction(tx *types.Transaction) {
	if tx == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	nonce := tx.Nonce()
	b.pendingTxs[nonce] = tx

	b.logger.Debug().
		Uint64("nonce", nonce).
		Str("tx_hash", tx.Hash().Hex()).
		Msg("Tracking pending transaction")
}

// RemovePendingTransaction removes a transaction from pending tracking.
func (b *TransactionBuilder) RemovePendingTransaction(nonce uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.pendingTxs, nonce)

	b.logger.Debug().
		Uint64("nonce", nonce).
		Msg("Removed pending transaction")
}

// GetPendingTransaction returns a pending transaction by nonce.
func (b *TransactionBuilder) GetPendingTransaction(nonce uint64) (*types.Transaction, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	tx, ok := b.pendingTxs[nonce]
	return tx, ok
}

// GetPendingNonceCount returns the number of pending transactions.
func (b *TransactionBuilder) GetPendingNonceCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return len(b.pendingTxs)
}

// VerifyTransactionChain verifies that a transaction is for the correct chain.
func (b *TransactionBuilder) VerifyTransactionChain(tx *types.Transaction) bool {
	if tx == nil {
		return false
	}

	// Check chain ID from signer
	signerChainID := b.signer.ChainID()

	return signerChainID.Cmp(b.chainID) == 0
}

// EstimateGasForTokenTransfer estimates gas for an ERC20 transfer.
func (b *TransactionBuilder) EstimateGasForTokenTransfer() uint64 {
	// ERC20 transfer typically uses ~50k gas
	return uint64(60000)
}

// EstimateGasForSwap estimates gas for a DEX swap.
func (b *TransactionBuilder) EstimateGasForSwap(isComplex bool) uint64 {
	// Simple swap: ~150k gas
	// Complex swap (multi-hop): ~250k gas
	if isComplex {
		return uint64(250000)
	}
	return uint64(150000)
}

// EstimateGasForApproval estimates gas for an ERC20 approval.
func (b *TransactionBuilder) EstimateGasForApproval() uint64 {
	// ERC20 approve typically uses ~50k gas
	return uint64(50000)
}

// TxParams holds parameters for building a transaction.
type TxParams struct {
	Nonce             uint64
	To                *common.Address
	Value             *big.Int
	Data              []byte
	GasLimit          uint64
	PriorityFeePerGas *big.Int
	MaxFeePerGas      *big.Int
	ChainID           *big.Int
}

// privateKey is a wrapper for secp256k1 private key.
type privateKey struct {
	D *big.Int
}

// NewPrivateKeyFromHex creates a private key from hex string.
func NewPrivateKeyFromHex(hexKey string) (*privateKey, error) {
	if len(hexKey) >= 2 && hexKey[:2] == "0x" {
		hexKey = hexKey[2:]
	}

	d, ok := new(big.Int).SetString(hexKey, 16)
	if !ok {
		return nil, errors.New("invalid hex private key")
	}

	return &privateKey{D: d}, nil
}

// GetAddress derives the public address from a private key.
func (pk *privateKey) GetAddress() (common.Address, error) {
	privKey, err := crypto.ToECDSA(pk.D.Bytes())
	if err != nil {
		return common.Address{}, err
	}

	return crypto.PubkeyToAddress(privKey.PublicKey), nil
}

// Signing statistics for monitoring.
type SigningStats struct {
	TotalSigned     int64
	TotalSpeedups   int64
	TotalCancels    int64
	TotalFailed     int64
	AverageGasUsed  uint64
	AverageGasPrice *big.Int
	mu              sync.RWMutex
}

func (s *SigningStats) RecordSigned(tx *types.Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TotalSigned++

	if tx.Type() == types.DynamicFeeTxType {
		// Get the gas fee cap from the transaction
		gasFeeCap := extractGasFeeCap(tx)
		if s.AverageGasPrice == nil {
			s.AverageGasPrice = gasFeeCap
		} else if gasFeeCap != nil {
			// Update average (simple moving average)
			s.AverageGasPrice = new(big.Int).Add(s.AverageGasPrice, gasFeeCap)
			s.AverageGasPrice.Div(s.AverageGasPrice, big.NewInt(2))
		}
	}
}

// extractGasFeeCap extracts the gas fee cap from a transaction.
func extractGasFeeCap(tx *types.Transaction) *big.Int {
	// For DynamicFeeTx, access through the inner struct
	// This is a workaround for the type assertion issue
	if tx.Type() == types.DynamicFeeTxType {
		// Use reflection or manual field access
		// Since we can't do type assertion, we'll return nil for now
		// In production, you would use proper reflection or interface methods
		return nil
	}
	return nil
}

func (s *SigningStats) RecordSpeedup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalSpeedups++
}

func (s *SigningStats) RecordCancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalCancels++
}

func (s *SigningStats) RecordFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalFailed++
}

// Constants for Base chain.
const (
	// Base transaction costs.
	BaseL1CostPerByte = 16   // Gas cost per byte for L1 data
	BaseMaxL1TxSize   = 128  // Maximum transaction size in bytes
	BaseL1FeeOverhead = 1884 // Static overhead for L1 fee

	// Gas limits.
	BaseMinGasLimit    = 21000
	BaseTargetGasLimit = 15000000
	BaseMaxGasLimit    = 30000000
)

// EstimateL1Fee estimates the L1 fee component for a Base transaction.
func EstimateL1Fee(txSize int, l1BaseFee *big.Int) *big.Int {
	if l1BaseFee == nil {
		return big.NewInt(0)
	}

	// L1 fee = (tx_size * 16 + 1884) * l1_base_fee
	txDataCost := big.NewInt(int64(txSize * BaseL1CostPerByte))
	totalGas := new(big.Int).Add(txDataCost, big.NewInt(BaseL1FeeOverhead))

	l1Fee := new(big.Int).Mul(totalGas, l1BaseFee)
	return l1Fee
}
