// Package trader provides trading functionality for the meme sniper bot.
// This file implements priority fee calculation for Solana transactions.
package trader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// Fee calculation constants.
	defaultBaseFee          = 5000   // 0.000005 SOL (lamports)
	defaultMicroLamport     = 1000   // 1 microlamport = 0.000001 SOL
	maxPriorityFeeCalc      = 100000 // 0.0001 SOL (microlamports)
	feeUpdateInterval       = 10 * time.Second
	feeHistorySize          = 100
	congestionThreshold     = 0.7 // 70% utilization
	highCongestionThreshold = 0.9 // 90% utilization

	// Helius fee API.
	heliusFeeAPI = "https://rpc.helius.xyz/?api-key=%s/v0/priority-fees"
)

// FeeEstimate represents a fee estimate for a transaction.
type FeeEstimate struct {
	// Total fee in micro-lamports
	TotalFeeMicroLamports int `json:"total_fee_micro_lamports"`

	// Per-account fees
	AccountFees []AccountFee `json:"account_fees,omitempty"`

	// Recommended fee levels
	Low      int `json:"low_fee"`
	Medium   int `json:"medium_fee"`
	High     int `json:"high_fee"`
	VeryHigh int `json:"very_high_fee"`

	// Network conditions
	NetworkUtilization float64 `json:"network_utilization"`
	EstimatedSlotTime  int64   `json:"estimated_slot_time_ms"`

	// Timestamp
	Timestamp time.Time `json:"timestamp"`
}

// AccountFee represents a fee for a specific account in a transaction.
type AccountFee struct {
	Account          string `json:"account"`
	FeeMicroLamports int    `json:"fee_micro_lamports"`
}

// FeeCalculator calculates priority fees for Solana transactions.
type FeeCalculator struct {
	httpClient *http.Client
	logger     *zerolog.Logger
	mu         sync.RWMutex

	// Configuration
	apiKey          string
	baseFee         uint64  // Base fee in lamports
	maxFee          uint64  // Max priority fee in micro-lamports
	multiplier      float64 // Multiplier for fee calculation
	congestionBased bool    // Whether to use congestion-based fee calculation

	// Fee history for trend analysis
	feeHistory []FeeEstimate
	lastUpdate time.Time
}

// FeeConfig holds configuration for the fee calculator.
type FeeConfig struct {
	HTTPClient      *http.Client
	Logger          *zerolog.Logger
	APIKey          string // Helius API key
	BaseFee         uint64 // Base fee in lamports
	MaxFee          uint64 // Max priority fee in micro-lamports
	Multiplier      float64
	CongestionBased bool
}

// NewFeeCalculator creates a new fee calculator.
func NewFeeCalculator(config FeeConfig) *FeeCalculator {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: 5 * time.Second,
		}
	}

	if config.BaseFee == 0 {
		config.BaseFee = defaultBaseFee
	}

	if config.MaxFee == 0 {
		config.MaxFee = maxPriorityFeeCalc
	}

	if config.Multiplier == 0 {
		config.Multiplier = 1.0
	}

	return &FeeCalculator{
		httpClient:      config.HTTPClient,
		logger:          config.Logger,
		apiKey:          config.APIKey,
		baseFee:         config.BaseFee,
		maxFee:          config.MaxFee,
		multiplier:      config.Multiplier,
		congestionBased: config.CongestionBased,
		feeHistory:      make([]FeeEstimate, 0, feeHistorySize),
		lastUpdate:      time.Time{},
	}
}

// CalculatePriorityFee calculates the priority fee for a transaction.
// Returns the fee in micro-lamports.
func (f *FeeCalculator) CalculatePriorityFee(ctx context.Context, accounts []string) (*FeeEstimate, error) {
	// Try to get fee estimate from API
	estimate, err := f.getFeeEstimate(ctx, accounts)
	if err != nil {
		// Fallback to basic calculation
		if f.logger != nil {
			f.logger.Warn().Err(err).Msg("Failed to get fee estimate from API, using fallback calculation")
		}
		return f.calculateFallbackFee(accounts)
	}

	// Apply multiplier and cap
	adjustedFee := min(int(float64(estimate.TotalFeeMicroLamports)*f.multiplier), int(f.maxFee))

	estimate.TotalFeeMicroLamports = adjustedFee

	// Update fee history
	f.updateFeeHistory(estimate)

	return estimate, nil
}

// getFeeEstimate gets a fee estimate from the Helius API.
func (f *FeeCalculator) getFeeEstimate(ctx context.Context, accounts []string) (*FeeEstimate, error) {
	if f.apiKey == "" {
		return nil, errors.New("no API key provided")
	}

	// Build request URL
	url := fmt.Sprintf(heliusFeeAPI, f.apiKey)

	// Build request body
	reqBody := map[string]any{
		"accounts":           accounts,
		"recommended":        true,
		"include_all_tokens": false,
	}

	// Marshal request
	_, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Execute request with retry
	var resp *http.Response
	err = util.RetryWithBackoff(func() error {
		r, e := f.httpClient.Do(req) //nolint:bodyclose // response closed below
		if e != nil {
			return e
		}
		// Close previous response if exists
		if resp != nil {
			_ = resp.Body.Close()
		}
		resp = r
		return nil
	}, 3, 500*time.Millisecond, 5*time.Second)

	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	// Parse response
	var estimate FeeEstimate
	if err := json.NewDecoder(resp.Body).Decode(&estimate); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	estimate.Timestamp = time.Now()

	return &estimate, nil
}

// calculateFallbackFee calculates a fee estimate without API access.
func (f *FeeCalculator) calculateFallbackFee(accounts []string) (*FeeEstimate, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Start with base fee
	fee := int(defaultMicroLamport)

	// Adjust based on account count (more accounts = higher fee)
	accountMultiplier := 1.0 + (float64(len(accounts)) * 0.1)
	fee = int(float64(fee) * accountMultiplier)

	// Apply multiplier
	fee = int(float64(fee) * f.multiplier)

	// Check recent fee history for congestion trends
	if len(f.feeHistory) > 0 {
		recentFees := f.feeHistory
		avgFee := 0
		for _, est := range recentFees {
			avgFee += est.TotalFeeMicroLamports
		}
		avgFee /= len(recentFees)

		// If recent fees are higher, increase our fee
		if avgFee > fee {
			fee = avgFee + int(float64(fee)*0.1) // Add 10% buffer
		}
	}

	// Cap at max
	if fee > int(f.maxFee) {
		fee = int(f.maxFee)
	}

	estimate := &FeeEstimate{
		TotalFeeMicroLamports: fee,
		Low:                   int(float64(fee) * 0.5),
		Medium:                fee,
		High:                  int(float64(fee) * 1.5),
		VeryHigh:              int(float64(fee) * 2.0),
		Timestamp:             time.Now(),
	}

	// Estimate network utilization based on fee level
	estimate.NetworkUtilization = f.estimateUtilization(fee)

	return estimate, nil
}

// estimateUtilization estimates network utilization based on fee level.
func (f *FeeCalculator) estimateUtilization(fee int) float64 {
	// Higher fees suggest higher congestion
	if fee < 1000 {
		return 0.3 // Low congestion
	} else if fee < 5000 {
		return 0.6 // Medium congestion
	} else if fee < 10000 {
		return 0.8 // High congestion
	}
	return 0.95 // Very high congestion
}

// updateFeeHistory updates the fee history with a new estimate.
func (f *FeeCalculator) updateFeeHistory(estimate *FeeEstimate) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.feeHistory = append(f.feeHistory, *estimate)
	f.lastUpdate = time.Now()

	// Keep only recent history
	if len(f.feeHistory) > feeHistorySize {
		f.feeHistory = f.feeHistory[1:]
	}
}

// GetRecommendedFee gets the recommended fee for a transaction.
// Returns the fee in micro-lamports.
func (f *FeeCalculator) GetRecommendedFee(ctx context.Context, priority FeePriority) (int, error) {
	estimate, err := f.CalculatePriorityFee(ctx, []string{})
	if err != nil {
		// Return default on error
		return defaultMicroLamport, nil
	}

	switch priority {
	case FeePriorityLow:
		return estimate.Low, nil
	case FeePriorityMedium:
		return estimate.Medium, nil
	case FeePriorityHigh:
		return estimate.High, nil
	case FeePriorityVeryHigh:
		return estimate.VeryHigh, nil
	default:
		return estimate.Medium, nil
	}
}

// FeePriority represents the desired fee priority level.
type FeePriority int

const (
	FeePriorityLow FeePriority = iota
	FeePriorityMedium
	FeePriorityHigh
	FeePriorityVeryHigh
)

// CalculateDynamicFee calculates a dynamic fee based on network conditions.
func (f *FeeCalculator) CalculateDynamicFee(ctx context.Context, accounts []string, targetSlotTime int64) (int, error) {
	estimate, err := f.CalculatePriorityFee(ctx, accounts)
	if err != nil {
		return defaultMicroLamport, nil
	}

	// If we want faster confirmation, use higher fee
	if targetSlotTime < 500 { // Less than 500ms
		return estimate.VeryHigh, nil
	} else if targetSlotTime < 1000 { // Less than 1s
		return estimate.High, nil
	} else if targetSlotTime < 2000 { // Less than 2s
		return estimate.Medium, nil
	}
	return estimate.Low, nil
}

// GetFeeHistory returns the recent fee history.
func (f *FeeCalculator) GetFeeHistory() []FeeEstimate {
	f.mu.RLock()
	defer f.mu.RUnlock()

	history := make([]FeeEstimate, len(f.feeHistory))
	copy(history, f.feeHistory)
	return history
}

// GetAverageFee calculates the average fee from recent history.
func (f *FeeCalculator) GetAverageFee() int {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.feeHistory) == 0 {
		return defaultMicroLamport
	}

	sum := 0
	for _, estimate := range f.feeHistory {
		sum += estimate.TotalFeeMicroLamports
	}
	return sum / len(f.feeHistory)
}

// IsNetworkCongested checks if the network is currently congested.
func (f *FeeCalculator) IsNetworkCongested() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.feeHistory) == 0 {
		return false
	}

	// Get most recent estimate
	recent := f.feeHistory[len(f.feeHistory)-1]
	return recent.NetworkUtilization > congestionThreshold
}

// IsHighCongestion checks if network congestion is very high.
func (f *FeeCalculator) IsHighCongestion() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if len(f.feeHistory) == 0 {
		return false
	}

	// Get most recent estimate
	recent := f.feeHistory[len(f.feeHistory)-1]
	return recent.NetworkUtilization > highCongestionThreshold
}

// GetBaseFee returns the base fee in lamports.
func (f *FeeCalculator) GetBaseFee() uint64 {
	return f.baseFee
}

// SetMultiplier sets the fee multiplier.
func (f *FeeCalculator) SetMultiplier(multiplier float64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Cap multiplier at reasonable range
	if multiplier < 0.1 {
		multiplier = 0.1
	} else if multiplier > 10.0 {
		multiplier = 10.0
	}

	f.multiplier = multiplier
}

// GetMultiplier returns the current fee multiplier.
func (f *FeeCalculator) GetMultiplier() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.multiplier
}

// EstimateTotalFee estimates the total fee for a transaction.
// Includes base fee + priority fee.
func (f *FeeCalculator) EstimateTotalFee(ctx context.Context, accounts []string) (uint64, error) {
	estimate, err := f.CalculatePriorityFee(ctx, accounts)
	if err != nil {
		// Use defaults on error
		return f.baseFee + uint64(defaultMicroLamport), nil
	}

	// Convert micro-lamports to lamports for priority fee
	priorityFeeLamports := uint64(math.Ceil(float64(estimate.TotalFeeMicroLamports) / 1000.0))

	return f.baseFee + priorityFeeLamports, nil
}

// CalculateFeeForAccounts calculates fees for specific writable accounts.
// This is useful for DEX transactions that write to multiple accounts.
func (f *FeeCalculator) CalculateFeeForAccounts(ctx context.Context, writableAccounts []string) (*FeeEstimate, error) {
	return f.CalculatePriorityFee(ctx, writableAccounts)
}

// MicroLamportsToLamports converts micro-lamports to lamports.
func MicroLamportsToLamports(microLamports int) float64 {
	return float64(microLamports) / 1000.0
}

// LamportsToMicroLamports converts lamports to micro-lamports.
func LamportsToMicroLamports(lamports uint64) int {
	return int(lamports * 1000)
}

// SolToLamports converts SOL to lamports.
func SolToLamports(sol float64) uint64 {
	return uint64(sol * 1_000_000_000)
}

// LamportsToSol converts lamports to SOL.
func LamportsToSol(lamports uint64) float64 {
	return float64(lamports) / 1_000_000_000
}

// EstimateFeeForSwap estimates the fee for a swap transaction.
func (f *FeeCalculator) EstimateFeeForSwap(ctx context.Context, routeComplexity int) (*SwapFeeEstimate, error) {
	// Estimate accounts based on route complexity
	// More complex routes = more accounts
	estimatedAccounts := 5 + (routeComplexity * 2)

	accounts := make([]string, estimatedAccounts)
	for i := range accounts {
		accounts[i] = fmt.Sprintf("account%d", i)
	}

	estimate, err := f.CalculatePriorityFee(ctx, accounts)
	if err != nil {
		// Fallback to basic estimate
		return &SwapFeeEstimate{
			BaseFeeLamports:          f.baseFee,
			PriorityFeeMicroLamports: defaultMicroLamport,
			TotalFeeLamports:         f.baseFee + uint64(defaultMicroLamport/1000),
			EstimatedSlotTime:        2000, // 2 seconds
			RecommendedPriority:      FeePriorityMedium,
		}, nil
	}

	// Determine recommended priority based on fee level
	var priority FeePriority
	networkUtil := estimate.NetworkUtilization
	if networkUtil < 0.5 {
		priority = FeePriorityLow
	} else if networkUtil < 0.7 {
		priority = FeePriorityMedium
	} else if networkUtil < 0.9 {
		priority = FeePriorityHigh
	} else {
		priority = FeePriorityVeryHigh
	}

	return &SwapFeeEstimate{
		BaseFeeLamports:          f.baseFee,
		PriorityFeeMicroLamports: estimate.TotalFeeMicroLamports,
		TotalFeeLamports:         f.baseFee + uint64(estimate.TotalFeeMicroLamports/1000),
		EstimatedSlotTime:        f.estimateSlotTime(estimate.TotalFeeMicroLamports),
		RecommendedPriority:      priority,
		FeeLevels: FeeLevels{
			Low:      estimate.Low,
			Medium:   estimate.Medium,
			High:     estimate.High,
			VeryHigh: estimate.VeryHigh,
		},
	}, nil
}

// SwapFeeEstimate represents a fee estimate for a swap transaction.
type SwapFeeEstimate struct {
	BaseFeeLamports          uint64      `json:"base_fee_lamports"`
	PriorityFeeMicroLamports int         `json:"priority_fee_micro_lamports"`
	TotalFeeLamports         uint64      `json:"total_fee_lamports"`
	EstimatedSlotTime        int64       `json:"estimated_slot_time_ms"`
	RecommendedPriority      FeePriority `json:"recommended_priority"`
	FeeLevels                FeeLevels   `json:"fee_levels"`
}

// FeeLevels represents different fee priority levels.
type FeeLevels struct {
	Low      int `json:"low"`
	Medium   int `json:"medium"`
	High     int `json:"high"`
	VeryHigh int `json:"very_high"`
}

// estimateSlotTime estimates the time to land a transaction based on fee.
func (f *FeeCalculator) estimateSlotTime(feeMicroLamports int) int64 {
	// Higher fees = faster landing
	// These are rough estimates based on typical Solana behavior
	if feeMicroLamports < 1000 {
		return 3000 // 3 seconds
	} else if feeMicroLamports < 5000 {
		return 2000 // 2 seconds
	} else if feeMicroLamports < 10000 {
		return 1000 // 1 second
	}
	return 500 // 0.5 seconds
}
