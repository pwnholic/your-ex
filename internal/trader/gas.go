// Package trader provides trading functionality for the meme sniper bot.
// This file implements gas estimation and optimization for Base chain.
package trader

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// Gas price update intervals.
	gasUpdateInterval     = 10 * time.Second
	gasPriceHistoryLength = 100

	// Gas estimation bounds.
	minGasLimit                = 21000
	maxGasLimit                = 10000000
	defaultGasEstimationBuffer = 1.2         // 20% buffer
	defaultGasBaseFee          = 30000000000 // 30 Gwei

	// Network congestion levels.
	congestionLow    = 0.3  // < 30% utilization
	congestionMedium = 0.6  // 30-60% utilization
	congestionHigh   = 0.85 // 60-85% utilization
)

// GasPriceTracker tracks gas prices over time for optimization.
type GasPriceTracker struct {
	logger *zerolog.Logger
	mu     sync.RWMutex

	// Price history
	baseFeeHistory []*big.Int
	tipHistory     []*big.Int
	maxHistory     int

	// Current prices
	currentBaseFee *big.Int
	currentTip     *big.Int
	currentMaxFee  *big.Int

	// Network status
	congestionLevel float64
	lastUpdate      time.Time
	updateInterval  time.Duration
}

// GasEstimator handles gas estimation and optimization.
type GasEstimator struct {
	tracker *GasPriceTracker
	client  GasPriceClient
	logger  *zerolog.Logger
	mu      sync.RWMutex

	// Configuration
	maxPriorityFee   *big.Int
	maxFeePerGas     *big.Int
	minPriorityFee   *big.Int
	estimationBuffer float64

	// Statistics
	stats GasStats
}

// GasStats tracks gas estimation statistics.
type GasStats struct {
	TotalEstimations    int64
	TotalOverEstimates  int64
	TotalUnderEstimates int64
	AverageGasUsed      uint64
	AverageGasPrice     *big.Int
	mu                  sync.RWMutex
}

// GasPriceClient defines the interface for fetching gas prices.
type GasPriceClient interface {
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
}

// EstimatorConfig holds configuration for the gas estimator.
type EstimatorConfig struct {
	Logger           *zerolog.Logger
	RPCClient        GasPriceClient
	MaxPriorityFee   *big.Int
	MaxFeePerGas     *big.Int
	MinPriorityFee   *big.Int
	EstimationBuffer float64
	UpdateInterval   time.Duration
}

// NewGasPriceTracker creates a new gas price tracker.
func NewGasPriceTracker(logger *zerolog.Logger) *GasPriceTracker {
	if logger == nil {
		l := util.WithComponent("gas_tracker")
		logger = &l
	}

	return &GasPriceTracker{
		logger:          logger,
		baseFeeHistory:  make([]*big.Int, 0, gasPriceHistoryLength),
		tipHistory:      make([]*big.Int, 0, gasPriceHistoryLength),
		maxHistory:      gasPriceHistoryLength,
		currentBaseFee:  big.NewInt(1000000000),  // 1 Gwei
		currentTip:      big.NewInt(100000000),   // 0.1 Gwei
		currentMaxFee:   big.NewInt(50000000000), // 50 Gwei
		congestionLevel: 0.5,
		lastUpdate:      time.Now(),
		updateInterval:  gasUpdateInterval,
	}
}

// NewGasEstimator creates a new gas estimator.
func NewGasEstimator(config EstimatorConfig) (*GasEstimator, error) {
	if config.RPCClient == nil {
		return nil, errors.New("RPC client is required")
	}

	if config.MaxPriorityFee == nil {
		config.MaxPriorityFee = big.NewInt(50000000000) // 50 Gwei
	}

	if config.MaxFeePerGas == nil {
		config.MaxFeePerGas = big.NewInt(100000000000) // 100 Gwei
	}

	if config.MinPriorityFee == nil {
		config.MinPriorityFee = big.NewInt(100000000) // 0.1 Gwei
	}

	if config.EstimationBuffer == 0 {
		config.EstimationBuffer = defaultGasEstimationBuffer
	}

	logger := config.Logger
	if logger == nil {
		l := util.WithComponent("gas_estimator")
		logger = &l
	}

	tracker := NewGasPriceTracker(logger)

	return &GasEstimator{
		tracker:          tracker,
		client:           config.RPCClient,
		logger:           logger,
		maxPriorityFee:   config.MaxPriorityFee,
		maxFeePerGas:     config.MaxFeePerGas,
		minPriorityFee:   config.MinPriorityFee,
		estimationBuffer: config.EstimationBuffer,
		stats: GasStats{
			AverageGasPrice: big.NewInt(0),
		},
	}, nil
}

// UpdatePrices updates the current gas prices from the network.
func (e *GasEstimator) UpdatePrices(ctx context.Context) error {
	header, err := e.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get latest header: %w", err)
	}

	if header.BaseFee == nil {
		return errors.New("header does not contain base fee")
	}

	e.tracker.mu.Lock()
	defer e.tracker.mu.Unlock()

	// Update base fee
	e.tracker.currentBaseFee = new(big.Int).Set(header.BaseFee)

	// Estimate tip based on recent blocks
	tip := e.estimateTipFromHistory(header)
	e.tracker.currentTip = tip

	// Calculate max fee
	e.tracker.currentMaxFee = e.calculateMaxFee(e.tracker.currentBaseFee, tip)

	// Update history
	e.tracker.baseFeeHistory = append(e.tracker.baseFeeHistory, new(big.Int).Set(e.tracker.currentBaseFee))
	e.tracker.tipHistory = append(e.tracker.tipHistory, new(big.Int).Set(tip))

	// Trim history
	if len(e.tracker.baseFeeHistory) > e.tracker.maxHistory {
		e.tracker.baseFeeHistory = e.tracker.baseFeeHistory[1:]
	}
	if len(e.tracker.tipHistory) > e.tracker.maxHistory {
		e.tracker.tipHistory = e.tracker.tipHistory[1:]
	}

	e.tracker.congestionLevel = e.calculateCongestionLevel()
	e.tracker.lastUpdate = time.Now()

	e.logger.Debug().
		Str("base_fee", e.tracker.currentBaseFee.String()).
		Str("tip", e.tracker.currentTip.String()).
		Str("max_fee", e.tracker.currentMaxFee.String()).
		Float64("congestion", e.tracker.congestionLevel).
		Msg("Gas prices updated")

	return nil
}

// estimateTipFromHistory estimates the priority fee tip from recent blocks.
func (e *GasEstimator) estimateTipFromHistory(header *types.Header) *big.Int {
	// Use a simple heuristic: 10-20% of base fee
	// In production, you would analyze actual tips from recent blocks

	tip := new(big.Int).Mul(header.BaseFee, big.NewInt(15))
	tip.Div(tip, big.NewInt(100))

	// Ensure minimum tip
	if tip.Cmp(e.minPriorityFee) < 0 {
		tip = new(big.Int).Set(e.minPriorityFee)
	}

	return tip
}

// calculateMaxFee calculates the max fee per gas.
func (e *GasEstimator) calculateMaxFee(baseFee, tip *big.Int) *big.Int {
	// maxFee = baseFee + 2 * tip
	// This allows for one block of base fee increase
	maxFee := new(big.Int).Add(baseFee, new(big.Int).Mul(tip, big.NewInt(2)))

	// Cap at configured maximum
	if maxFee.Cmp(e.maxFeePerGas) > 0 {
		return new(big.Int).Set(e.maxFeePerGas)
	}

	return maxFee
}

// calculateCongestionLevel calculates the network congestion level.
func (e *GasEstimator) calculateCongestionLevel() float64 {
	if len(e.tracker.baseFeeHistory) < 2 {
		return 0.5
	}

	// Analyze base fee trend
	// Increasing base fees indicate higher congestion
	trend := new(big.Int).Sub(
		e.tracker.baseFeeHistory[len(e.tracker.baseFeeHistory)-1],
		e.tracker.baseFeeHistory[0],
	)

	// Normalize to 0-1 range
	// Assume 5 Gwei change is significant
	change := new(big.Float).Quo(
		new(big.Float).SetInt(trend),
		big.NewFloat(5000000000),
	)

	congestion, _ := change.Float64()
	if congestion < 0 {
		congestion = 0
	}
	if congestion > 1 {
		congestion = 1
	}

	return congestion
}

// EstimateGas estimates the gas required for a transaction.
func (e *GasEstimator) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	// This would normally call eth_estimateGas via RPC
	// For now, return a reasonable default estimate

	var gasLimit uint64

	// Determine gas limit based on call type
	if len(msg.Data) > 0 {
		// Contract call
		if len(msg.Data) > 1000 {
			// Complex call (e.g., swap)
			gasLimit = 250000
		} else {
			// Simple call (e.g., approve)
			gasLimit = 60000
		}
	} else {
		// Simple transfer
		gasLimit = 21000
	}

	// Add estimation buffer
	gasLimit = min(
		// Cap at max
		uint64(float64(gasLimit)*e.estimationBuffer), maxGasLimit)

	e.stats.mu.Lock()
	e.stats.TotalEstimations++
	e.stats.AverageGasUsed = (e.stats.AverageGasUsed + gasLimit) / 2
	e.stats.mu.Unlock()

	return gasLimit, nil
}

// GetSuggestedGasPrice returns the suggested gas price parameters.
func (e *GasEstimator) GetSuggestedGasPrice() (baseFee, priorityFee, maxFee *big.Int) {
	e.tracker.mu.RLock()
	defer e.tracker.mu.RUnlock()

	return new(big.Int).Set(e.tracker.currentBaseFee),
		new(big.Int).Set(e.tracker.currentTip),
		new(big.Int).Set(e.tracker.currentMaxFee)
}

// GetSuggestedGasPriceForCongestion returns gas prices based on congestion level.
func (e *GasEstimator) GetSuggestedGasPriceForCongestion(urgency GasUrgency) (baseFee, priorityFee, maxFee *big.Int) {
	e.tracker.mu.RLock()
	baseFee = new(big.Int).Set(e.tracker.currentBaseFee)
	tip := new(big.Int).Set(e.tracker.currentTip)
	congestion := e.tracker.congestionLevel
	e.tracker.mu.RUnlock()

	// Adjust tip based on urgency and congestion
	var tipMultiplier float64

	switch urgency {
	case UrgencyLow:
		tipMultiplier = 1.0
	case UrgencyMedium:
		tipMultiplier = 1.5
	case UrgencyHigh:
		tipMultiplier = 2.0
	case UrgencyCritical:
		tipMultiplier = 3.0
	}

	// Apply congestion multiplier
	if congestion > congestionHigh {
		tipMultiplier *= 2.0
	} else if congestion > congestionMedium {
		tipMultiplier *= 1.5
	}

	priorityFee = new(big.Int).Mul(tip, big.NewInt(int64(tipMultiplier*100)))
	priorityFee.Div(priorityFee, big.NewInt(100))

	// Cap at max
	if priorityFee.Cmp(e.maxPriorityFee) > 0 {
		priorityFee = new(big.Int).Set(e.maxPriorityFee)
	}

	// Calculate max fee
	maxFee = e.calculateMaxFee(baseFee, priorityFee)

	return baseFee, priorityFee, maxFee
}

// GetCongestionLevel returns the current network congestion level.
func (e *GasEstimator) GetCongestionLevel() float64 {
	e.tracker.mu.RLock()
	defer e.tracker.mu.RUnlock()

	return e.tracker.congestionLevel
}

// EstimateTransactionFee estimates the total fee for a transaction.
func (e *GasEstimator) EstimateTransactionFee(gasLimit uint64, maxFeePerGas *big.Int) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), maxFeePerGas)
}

// OptimizeGasForSpeedup optimizes gas parameters for speeding up a transaction.
func (e *GasEstimator) OptimizeGasForSpeedup(originalMaxFee, originalTip *big.Int) (newMaxFee, newTip *big.Int) {
	// Increase by at least 10%
	multiplier := big.NewInt(110) // 10% increase
	divisor := big.NewInt(100)

	newTip = new(big.Int).Mul(originalTip, multiplier)
	newTip.Div(newTip, divisor)

	newMaxFee = new(big.Int).Mul(originalMaxFee, multiplier)
	newMaxFee.Div(newMaxFee, divisor)

	// Cap at max
	if newTip.Cmp(e.maxPriorityFee) > 0 {
		newTip = new(big.Int).Set(e.maxPriorityFee)
	}

	if newMaxFee.Cmp(e.maxFeePerGas) > 0 {
		newMaxFee = new(big.Int).Set(e.maxFeePerGas)
	}

	return newMaxFee, newTip
}

// GasUrgency represents the urgency level for a transaction.
type GasUrgency int

const (
	UrgencyLow GasUrgency = iota
	UrgencyMedium
	UrgencyHigh
	UrgencyCritical
)

func (u GasUrgency) String() string {
	switch u {
	case UrgencyLow:
		return "low"
	case UrgencyMedium:
		return "medium"
	case UrgencyHigh:
		return "high"
	case UrgencyCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseGasUrgency parses a string to GasUrgency.
func ParseGasUrgency(s string) GasUrgency {
	switch s {
	case "low":
		return UrgencyLow
	case "medium":
		return UrgencyMedium
	case "high":
		return UrgencyHigh
	case "critical":
		return UrgencyCritical
	default:
		return UrgencyMedium
	}
}

// GetAverageGasPrice returns the average gas price from history.
func (e *GasEstimator) GetAverageGasPrice() *big.Int {
	e.tracker.mu.RLock()
	defer e.tracker.mu.RUnlock()

	if len(e.tracker.baseFeeHistory) == 0 {
		return big.NewInt(defaultBaseFee)
	}

	sum := big.NewInt(0)
	for _, fee := range e.tracker.baseFeeHistory {
		sum.Add(sum, fee)
	}

	avg := new(big.Int).Div(sum, big.NewInt(int64(len(e.tracker.baseFeeHistory))))
	return avg
}

// GetGasPriceTrend returns the gas price trend (-1 to 1, negative = decreasing).
func (e *GasEstimator) GetGasPriceTrend() float64 {
	e.tracker.mu.RLock()
	defer e.tracker.mu.RUnlock()

	if len(e.tracker.baseFeeHistory) < 2 {
		return 0
	}

	oldest := e.tracker.baseFeeHistory[0]
	newest := e.tracker.baseFeeHistory[len(e.tracker.baseFeeHistory)-1]

	diff := new(big.Float).Sub(
		new(big.Float).SetInt(newest),
		new(big.Float).SetInt(oldest),
	)

	normalized := new(big.Float).Quo(diff, new(big.Float).SetInt(oldest))
	trend, _ := normalized.Float64()

	if trend < -1 {
		return -1
	}
	if trend > 1 {
		return 1
	}

	return trend
}

// ShouldWaitForLowerGas determines if it's worth waiting for lower gas prices.
func (e *GasEstimator) ShouldWaitForLowerGas(threshold *big.Int, maxWait time.Duration) bool {
	trend := e.GetGasPriceTrend()
	if trend >= 0 {
		// Prices are rising or stable, don't wait
		return false
	}

	// Prices are falling
	avgPrice := e.GetAverageGasPrice()

	// Check if current price is above threshold
	e.tracker.mu.RLock()
	currentPrice := e.tracker.currentBaseFee
	e.tracker.mu.RUnlock()

	if currentPrice.Cmp(threshold) < 0 {
		// Already below threshold
		return false
	}

	// Estimate time to reach threshold based on trend
	priceDiff := new(big.Float).Sub(
		new(big.Float).SetInt(currentPrice),
		new(big.Float).SetInt(threshold),
	)

	rate := new(big.Float).Mul(
		new(big.Float).SetInt(avgPrice),
		big.NewFloat(-trend), // Negative trend means falling
	)

	estimatedTime := new(big.Float).Quo(priceDiff, rate)

	// Convert to seconds (assuming trend is per 10 seconds)
	seconds, _ := estimatedTime.Float64()
	seconds *= 10

	if seconds < 0 {
		return false
	}

	return seconds <= float64(maxWait.Seconds())
}

// GetStats returns the gas estimator statistics.
func (e *GasEstimator) GetStats() GasStats {
	e.stats.mu.RLock()
	defer e.stats.mu.RUnlock()

	return GasStats{
		TotalEstimations:    e.stats.TotalEstimations,
		TotalOverEstimates:  e.stats.TotalOverEstimates,
		TotalUnderEstimates: e.stats.TotalUnderEstimates,
		AverageGasUsed:      e.stats.AverageGasUsed,
		AverageGasPrice:     new(big.Int).Set(e.stats.AverageGasPrice),
	}
}

// RecordActualGas records the actual gas used for comparison with estimates.
func (e *GasEstimator) RecordActualGas(estimated, actual uint64) {
	e.stats.mu.Lock()
	defer e.stats.mu.Unlock()

	if actual > estimated {
		e.stats.TotalUnderEstimates++
	} else if actual < estimated*8/10 { // More than 20% overestimate
		e.stats.TotalOverEstimates++
	}

	// Update average
	e.stats.AverageGasUsed = (e.stats.AverageGasUsed + actual) / 2
}

// EstimateGasForSwap estimates gas for a Uniswap swap.
func (e *GasEstimator) EstimateGasForSwap(isMultiHop bool) uint64 {
	baseGas := uint64(180000) // Base gas for Uniswap V3 swap

	if isMultiHop {
		baseGas += 50000 // Extra for multi-hop
	}

	// Add buffer
	gasLimit := min(uint64(float64(baseGas)*e.estimationBuffer), maxGasLimit)

	return gasLimit
}

// EstimateGasForApproval estimates gas for an ERC20 approval.
func (e *GasEstimator) EstimateGasForApproval() uint64 {
	baseGas := uint64(50000) // Base gas for ERC20 approve
	gasLimit := min(uint64(float64(baseGas)*e.estimationBuffer), maxGasLimit)

	return gasLimit
}

// EstimateGasForTransfer estimates gas for an ETH transfer.
func (e *GasEstimator) EstimateGasForTransfer() uint64 {
	return 21000 // Standard ETH transfer
}

// EstimateGasForTokenTransfer estimates gas for an ERC20 transfer.
func (e *GasEstimator) EstimateGasForTokenTransfer() uint64 {
	baseGas := uint64(65000)
	gasLimit := min(uint64(float64(baseGas)*e.estimationBuffer), maxGasLimit)

	return gasLimit
}

// GetPriceHistory returns the gas price history.
func (e *GasEstimator) GetPriceHistory() ([]*big.Int, []*big.Int) {
	e.tracker.mu.RLock()
	defer e.tracker.mu.RUnlock()

	baseFees := make([]*big.Int, len(e.tracker.baseFeeHistory))
	tips := make([]*big.Int, len(e.tracker.tipHistory))

	copy(baseFees, e.tracker.baseFeeHistory)
	copy(tips, e.tracker.tipHistory)

	return baseFees, tips
}

// WeiToGwei converts wei to Gwei.
func WeiToGwei(wei *big.Int) *big.Float {
	return new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e9))
}

// GweiToWei converts Gwei to wei.
func GweiToWei(gwei float64) *big.Int {
	wei := new(big.Float).Mul(big.NewFloat(gwei), big.NewFloat(1e9))
	result, _ := wei.Int(nil)
	return result
}
