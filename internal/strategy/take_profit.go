// Package strategy provides trading strategies for the meme sniper bot.
// This file implements take profit strategy with multiple tiers.
package strategy

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

const (
	// Default take profit constants.
	defaultMinProfitPercent = 5 // Minimum 5% profit to consider take profit
	defaultCheckInterval    = 30 * time.Second
)

// TakeProfitStrategy manages take profit logic for positions.
type TakeProfitStrategy struct {
	config *TakeProfitConfig
	logger *zerolog.Logger
	mu     sync.RWMutex

	// Track which tiers have been executed for each position
	executedTiers map[string]map[int]bool // positionID -> tier -> executed

	// Track highest price seen for each position
	highestPrices map[string]decimal.Decimal
}

// TakeProfitConfig holds configuration for take profit strategy.
type TakeProfitStrategyConfig struct {
	Config           *TakeProfitConfig
	Logger           *zerolog.Logger
	CheckInterval    time.Duration
	MinProfitPercent decimal.Decimal
}

// NewTakeProfitStrategy creates a new take profit strategy.
func NewTakeProfitStrategy(config TakeProfitStrategyConfig) *TakeProfitStrategy {
	tp := &TakeProfitStrategy{
		config:        config.Config,
		logger:        config.Logger,
		executedTiers: make(map[string]map[int]bool),
		highestPrices: make(map[string]decimal.Decimal),
	}

	// Set defaults
	if config.MinProfitPercent.IsZero() {
		config.MinProfitPercent = decimal.NewFromInt(defaultMinProfitPercent)
	}

	return tp
}

// Evaluate evaluates a position for take profit conditions.
func (tp *TakeProfitStrategy) Evaluate(snapshot *PositionSnapshot) (*StrategyResult, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot is nil")
	}

	if tp.config == nil || !tp.config.Enabled {
		return &StrategyResult{
			Action:      SignalTypeTakeProfit,
			ShouldTrade: false,
			Reason:      "take profit disabled",
			Timestamp:   time.Now(),
		}, nil
	}

	// Update highest price
	tp.updateHighestPrice(snapshot.PositionID, snapshot.CurrentPrice)

	// Calculate PnL percentage
	if snapshot.PnLPercent.IsNegative() || snapshot.PnLPercent.IsZero() {
		return &StrategyResult{
			Action:      SignalTypeTakeProfit,
			ShouldTrade: false,
			Reason:      "no profit to take",
			Timestamp:   time.Now(),
		}, nil
	}

	// Check each tier
	for _, tier := range tp.config.Tiers {
		if !tier.Enabled {
			continue
		}

		// Check if this tier has already been executed
		if tp.isTierExecuted(snapshot.PositionID, tier.Tier) {
			continue
		}

		// Check if profit meets tier threshold
		if snapshot.PnLPercent.GreaterThanOrEqual(tier.Percent) {
			// Calculate amount to sell
			amountToSell := snapshot.Amount.Mul(tier.SellPortion)

			if tp.logger != nil {
				tp.logger.Info().
					Str("position_id", snapshot.PositionID).
					Str("token", snapshot.TokenSymbol).
					Int("tier", tier.Tier).
					Str("pnl_percent", snapshot.PnLPercent.String()).
					Str("tier_percent", tier.Percent.String()).
					Str("sell_portion", tier.SellPortion.String()).
					Str("amount_to_sell", amountToSell.String()).
					Msg("Take profit tier triggered")
			}

			return &StrategyResult{
				Action:      SignalTypeTakeProfit,
				ShouldTrade: true,
				Amount:      amountToSell,
				Reason:      fmt.Sprintf("take profit tier %d at %s%% profit", tier.Tier, tier.Percent),
				Tier:        tier.Tier,
				Price:       snapshot.CurrentPrice,
				Timestamp:   time.Now(),
			}, nil
		}
	}

	return &StrategyResult{
		Action:      SignalTypeTakeProfit,
		ShouldTrade: false,
		Reason:      fmt.Sprintf("profit %s%% below next tier threshold", snapshot.PnLPercent),
		Timestamp:   time.Now(),
	}, nil
}

// EvaluateAll evaluates all positions and returns take profit signals.
func (tp *TakeProfitStrategy) EvaluateAll(snapshots []*PositionSnapshot) ([]*StrategyResult, error) {
	var results []*StrategyResult

	for _, snapshot := range snapshots {
		result, err := tp.Evaluate(snapshot)
		if err != nil {
			if tp.logger != nil {
				tp.logger.Error().
					Err(err).
					Str("position_id", snapshot.PositionID).
					Msg("Error evaluating position for take profit")
			}
			continue
		}

		if result.ShouldTrade {
			results = append(results, result)
		}
	}

	return results, nil
}

// MarkTierExecuted marks a tier as executed for a position.
func (tp *TakeProfitStrategy) MarkTierExecuted(positionID string, tier int) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.executedTiers[positionID] == nil {
		tp.executedTiers[positionID] = make(map[int]bool)
	}

	tp.executedTiers[positionID][tier] = true

	if tp.logger != nil {
		tp.logger.Debug().
			Str("position_id", positionID).
			Int("tier", tier).
			Msg("Take profit tier marked as executed")
	}
}

// isTierExecuted checks if a tier has been executed for a position.
func (tp *TakeProfitStrategy) isTierExecuted(positionID string, tier int) bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	if tiers, ok := tp.executedTiers[positionID]; ok {
		return tiers[tier]
	}
	return false
}

// GetExecutedTiers returns all executed tiers for a position.
func (tp *TakeProfitStrategy) GetExecutedTiers(positionID string) map[int]bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	result := make(map[int]bool)
	if tiers, ok := tp.executedTiers[positionID]; ok {
		maps.Copy(result, tiers)
	}

	return result
}

// ResetExecutedTiers resets executed tiers for a position.
func (tp *TakeProfitStrategy) ResetExecutedTiers(positionID string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	delete(tp.executedTiers, positionID)
	delete(tp.highestPrices, positionID)

	if tp.logger != nil {
		tp.logger.Debug().
			Str("position_id", positionID).
			Msg("Take profit tiers reset")
	}
}

// updateHighestPrice updates the highest price seen for a position.
func (tp *TakeProfitStrategy) updateHighestPrice(positionID string, price decimal.Decimal) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	currentHighest, ok := tp.highestPrices[positionID]
	if !ok || price.GreaterThan(currentHighest) {
		tp.highestPrices[positionID] = price
	}
}

// GetHighestPrice returns the highest price seen for a position.
func (tp *TakeProfitStrategy) GetHighestPrice(positionID string) decimal.Decimal {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	if price, ok := tp.highestPrices[positionID]; ok {
		return price
	}
	return decimal.Zero
}

// CalculateTargetPrice calculates the target price for a take profit tier.
func (tp *TakeProfitStrategy) CalculateTargetPrice(entryPrice decimal.Decimal, tier TakeProfitTier) decimal.Decimal {
	// Target = EntryPrice * (1 + Percent/100)
	multiplier := decimal.NewFromInt(1).Add(tier.Percent.Div(decimal.NewFromInt(100)))
	return entryPrice.Mul(multiplier)
}

// CalculateAllTargetPrices calculates all target prices for a position.
func (tp *TakeProfitStrategy) CalculateAllTargetPrices(entryPrice decimal.Decimal) map[int]decimal.Decimal {
	if tp.config == nil {
		return nil
	}

	targets := make(map[int]decimal.Decimal)

	for _, tier := range tp.config.Tiers {
		if tier.Enabled {
			targets[tier.Tier] = tp.CalculateTargetPrice(entryPrice, tier)
		}
	}

	return targets
}

// CreateSnapshot creates a PositionSnapshot from a wallet position.
func (tp *TakeProfitStrategy) CreateSnapshot(position any) (*PositionSnapshot, error) {
	// This is a placeholder - the actual implementation would extract from wallet.Position
	// The wallet package is imported in types.go, so we'd use that
	return nil, errors.New("implement snapshot creation from wallet.Position")
}

// ValidateConfig validates the take profit configuration.
func (tp *TakeProfitStrategy) ValidateConfig() error {
	if tp.config == nil || !tp.config.Enabled {
		return nil
	}

	if len(tp.config.Tiers) == 0 {
		return errors.New("take profit enabled but no tiers configured")
	}

	// Validate tiers
	tierMap := make(map[int]bool)
	for _, tier := range tp.config.Tiers {
		if tier.Tier <= 0 {
			return fmt.Errorf("tier must be positive, got %d", tier.Tier)
		}

		if tierMap[tier.Tier] {
			return fmt.Errorf("duplicate tier %d", tier.Tier)
		}
		tierMap[tier.Tier] = true

		if tier.Percent.IsZero() || tier.Percent.IsNegative() {
			return fmt.Errorf("tier %d: percent must be positive", tier.Tier)
		}

		if tier.SellPortion.IsZero() || tier.SellPortion.IsNegative() {
			return fmt.Errorf("tier %d: sell portion must be positive", tier.Tier)
		}

		if tier.SellPortion.GreaterThan(decimal.NewFromInt(1)) {
			return fmt.Errorf("tier %d: sell portion cannot exceed 1", tier.Tier)
		}
	}

	return nil
}

// UpdateConfig updates the take profit configuration.
func (tp *TakeProfitStrategy) UpdateConfig(config *TakeProfitConfig) error {
	if err := tp.validateConfigInternal(config); err != nil {
		return err
	}

	tp.mu.Lock()
	tp.config = config
	tp.mu.Unlock()

	if tp.logger != nil {
		tp.logger.Info().
			Interface("config", config).
			Msg("Take profit config updated")
	}

	return nil
}

// validateConfigInternal validates a configuration.
func (tp *TakeProfitStrategy) validateConfigInternal(config *TakeProfitConfig) error {
	if config == nil || !config.Enabled {
		return nil
	}

	if len(config.Tiers) == 0 {
		return errors.New("take profit enabled but no tiers configured")
	}

	for _, tier := range config.Tiers {
		if tier.Tier <= 0 {
			return fmt.Errorf("invalid tier number: %d", tier.Tier)
		}
		if tier.Percent.IsNegative() {
			return fmt.Errorf("tier %d: negative percent not allowed", tier.Tier)
		}
		if tier.SellPortion.LessThan(decimal.Zero) || tier.SellPortion.GreaterThan(decimal.NewFromInt(1)) {
			return fmt.Errorf("tier %d: sell portion must be between 0 and 1", tier.Tier)
		}
	}

	return nil
}

// GetNextTier returns the next unexecuted tier for a position.
func (tp *TakeProfitStrategy) GetNextTier(positionID string, currentProfitPercent decimal.Decimal) *TakeProfitTier {
	if tp.config == nil {
		return nil
	}

	var lowestUnexecuted *TakeProfitTier
	var lowestTierNum = -1

	for _, tier := range tp.config.Tiers {
		if !tier.Enabled {
			continue
		}

		if tp.isTierExecuted(positionID, tier.Tier) {
			continue
		}

		// Check if profit meets threshold
		if currentProfitPercent.GreaterThanOrEqual(tier.Percent) {
			if lowestTierNum == -1 || tier.Tier < lowestTierNum {
				lowestUnexecuted = &tier
				lowestTierNum = tier.Tier
			}
		}
	}

	return lowestUnexecuted
}

// GetProgress returns the progress through take profit tiers.
func (tp *TakeProfitStrategy) GetProgress(positionID string) (int, int) {
	if tp.config == nil {
		return 0, 0
	}

	executedCount := 0
	totalEnabled := 0

	for _, tier := range tp.config.Tiers {
		if tier.Enabled {
			totalEnabled++
			if tp.isTierExecuted(positionID, tier.Tier) {
				executedCount++
			}
		}
	}

	return executedCount, totalEnabled
}

// ShouldExitAll determines if all tiers have been executed.
func (tp *TakeProfitStrategy) ShouldExitAll(positionID string) bool {
	executed, total := tp.GetProgress(positionID)
	return total > 0 && executed >= total
}

// EstimateExitValue estimates the value if all take profit tiers were executed.
func (tp *TakeProfitStrategy) EstimateExitValue(
	entryValue decimal.Decimal,
	targetPrices map[int]decimal.Decimal,
) decimal.Decimal {
	if tp.config == nil {
		return decimal.Zero
	}

	totalExitValue := decimal.Zero

	for _, tier := range tp.config.Tiers {
		if !tier.Enabled {
			continue
		}

		targetPrice, ok := targetPrices[tier.Tier]
		if !ok {
			continue
		}

		// Calculate exit value for this tier
		// ExitValue = EntryValue * (TargetPrice / EntryPrice) * SellPortion
		tierExitValue := entryValue.Mul(targetPrice.Div(entryValue)).Mul(tier.SellPortion)
		totalExitValue = totalExitValue.Add(tierExitValue)
	}

	return totalExitValue
}

// GetConfig returns the current take profit configuration.
func (tp *TakeProfitStrategy) GetConfig() *TakeProfitConfig {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	return tp.config
}

// Cleanup removes tracking data for a position.
func (tp *TakeProfitStrategy) Cleanup(positionID string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	delete(tp.executedTiers, positionID)
	delete(tp.highestPrices, positionID)

	if tp.logger != nil {
		tp.logger.Debug().
			Str("position_id", positionID).
			Msg("Take profit data cleaned up")
	}
}
