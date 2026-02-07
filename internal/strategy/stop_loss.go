// Package strategy provides trading strategies for the meme sniper bot.
// This file implements stop loss strategy with trailing option.
package strategy

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

const (
	// Default stop loss constants.
	defaultStopLossPercent       = -50 // -50% loss
	defaultTrailingPercent       = 10  // 10% trailing distance
	defaultTrailingActivate      = 10  // Activate trailing after 10% profit
	defaultStopLossCheckInterval = 30 * time.Second
)

// StopLossStrategy manages stop loss logic for positions.
type StopLossStrategy struct {
	config *StopLossConfig
	logger *zerolog.Logger
	mu     sync.RWMutex

	// Track trailing stop state for each position
	trailingStates map[string]*TrailingState
	triggered      map[string]bool // Track which positions have triggered stop loss
}

// TrailingState holds the state for a trailing stop.
type TrailingState struct {
	Activated        bool            `json:"activated"`
	ActivationPrice  decimal.Decimal `json:"activationPrice"`
	StopPrice        decimal.Decimal `json:"stopPrice"`
	HighestPrice     decimal.Decimal `json:"highestPrice"`
	InitialStopPrice decimal.Decimal `json:"initialStopPrice"`
	LastUpdate       time.Time       `json:"lastUpdate"`
}

// StopLossStrategyConfig holds configuration for stop loss strategy.
type StopLossStrategyConfig struct {
	Config        *StopLossConfig
	Logger        *zerolog.Logger
	CheckInterval time.Duration
}

// NewStopLossStrategy creates a new stop loss strategy.
func NewStopLossStrategy(config StopLossStrategyConfig) *StopLossStrategy {
	sl := &StopLossStrategy{
		config:         config.Config,
		logger:         config.Logger,
		trailingStates: make(map[string]*TrailingState),
		triggered:      make(map[string]bool),
	}

	// Set defaults if not provided
	if sl.config != nil {
		if sl.config.Percent.IsZero() {
			sl.config.Percent = decimal.NewFromInt(defaultStopLossPercent)
		}
		if sl.config.Trailing && sl.config.TrailingPercent.IsZero() {
			sl.config.TrailingPercent = decimal.NewFromInt(defaultTrailingPercent)
		}
		if sl.config.Trailing && sl.config.TrailingActivate.IsZero() {
			sl.config.TrailingActivate = decimal.NewFromInt(defaultTrailingActivate)
		}
	}

	return sl
}

// Evaluate evaluates a position for stop loss conditions.
func (sl *StopLossStrategy) Evaluate(snapshot *PositionSnapshot) (*StrategyResult, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot is nil")
	}

	if sl.config == nil || !sl.config.Enabled {
		return &StrategyResult{
			Action:      SignalTypeStopLoss,
			ShouldTrade: false,
			Reason:      "stop loss disabled",
			Timestamp:   time.Now(),
		}, nil
	}

	// Check if stop loss already triggered for this position
	if sl.isTriggered(snapshot.PositionID) {
		return &StrategyResult{
			Action:      SignalTypeStopLoss,
			ShouldTrade: false,
			Reason:      "stop loss already triggered",
			Timestamp:   time.Now(),
		}, nil
	}

	// Calculate entry-based stop loss price
	entryStopPrice := sl.calculateEntryStopPrice(snapshot.EntryPrice)

	// Handle trailing stop if enabled
	if sl.config.Trailing {
		return sl.evaluateTrailing(snapshot, entryStopPrice)
	}

	// Regular stop loss check
	if snapshot.CurrentPrice.LessThanOrEqual(entryStopPrice) {
		// Stop loss triggered
		sl.markTriggered(snapshot.PositionID)

		if sl.logger != nil {
			sl.logger.Warn().
				Str("position_id", snapshot.PositionID).
				Str("token", snapshot.TokenSymbol).
				Str("current_price", snapshot.CurrentPrice.String()).
				Str("stop_price", entryStopPrice.String()).
				Str("pnl_percent", snapshot.PnLPercent.String()).
				Msg("Stop loss triggered")
		}

		return &StrategyResult{
			Action:      SignalTypeStopLoss,
			ShouldTrade: true,
			Amount:      snapshot.Amount, // Sell entire position
			Reason:      fmt.Sprintf("stop loss at %s%% loss", sl.config.Percent),
			Price:       snapshot.CurrentPrice,
			Timestamp:   time.Now(),
		}, nil
	}

	return &StrategyResult{
		Action:      SignalTypeStopLoss,
		ShouldTrade: false,
		Reason:      fmt.Sprintf("price %s above stop %s", snapshot.CurrentPrice, entryStopPrice),
		Timestamp:   time.Now(),
	}, nil
}

// evaluateTrailing evaluates trailing stop conditions.
func (sl *StopLossStrategy) evaluateTrailing(
	snapshot *PositionSnapshot,
	entryStopPrice decimal.Decimal,
) (*StrategyResult, error) {
	state := sl.getOrCreateTrailingState(snapshot.PositionID, snapshot.EntryPrice, entryStopPrice)

	// Update highest price
	if snapshot.CurrentPrice.GreaterThan(state.HighestPrice) {
		state.HighestPrice = snapshot.CurrentPrice
		state.LastUpdate = time.Now()
	}

	// Calculate profit percentage
	profitPercent := sl.calculateProfitPercent(snapshot.EntryPrice, snapshot.CurrentPrice)

	// Check if we should activate trailing
	activationThreshold := decimal.NewFromInt(1).Add(sl.config.TrailingActivate.Div(decimal.NewFromInt(100)))

	if !state.Activated && snapshot.CurrentPrice.GreaterThanOrEqual(snapshot.EntryPrice.Mul(activationThreshold)) {
		state.Activated = true
		state.ActivationPrice = snapshot.CurrentPrice

		if sl.logger != nil {
			sl.logger.Info().
				Str("position_id", snapshot.PositionID).
				Str("token", snapshot.TokenSymbol).
				Str("activation_price", state.ActivationPrice.String()).
				Str("profit_percent", profitPercent.String()).
				Msg("Trailing stop activated")
		}
	}

	// Update trailing stop price if activated
	if state.Activated {
		// Calculate new trailing stop price
		trailingDistance := decimal.NewFromInt(1).Sub(sl.config.TrailingPercent.Div(decimal.NewFromInt(100)))
		newStopPrice := state.HighestPrice.Mul(trailingDistance)

		// Only raise the stop, never lower it
		if newStopPrice.GreaterThan(state.StopPrice) {
			state.StopPrice = newStopPrice

			if sl.logger != nil {
				sl.logger.Debug().
					Str("position_id", snapshot.PositionID).
					Str("token", snapshot.TokenSymbol).
					Str("highest_price", state.HighestPrice.String()).
					Str("new_stop_price", state.StopPrice.String()).
					Msg("Trailing stop price updated")
			}
		}

		// Check if current price hit the trailing stop
		if snapshot.CurrentPrice.LessThanOrEqual(state.StopPrice) {
			sl.markTriggered(snapshot.PositionID)

			if sl.logger != nil {
				sl.logger.Warn().
					Str("position_id", snapshot.PositionID).
					Str("token", snapshot.TokenSymbol).
					Str("current_price", snapshot.CurrentPrice.String()).
					Str("stop_price", state.StopPrice.String()).
					Str("pnl_percent", snapshot.PnLPercent.String()).
					Msg("Trailing stop triggered")
			}

			return &StrategyResult{
				Action:      SignalTypeTrailing,
				ShouldTrade: true,
				Amount:      snapshot.Amount,
				Reason:      fmt.Sprintf("trailing stop at %s%%", sl.config.TrailingPercent),
				Price:       snapshot.CurrentPrice,
				Timestamp:   time.Now(),
			}, nil
		}

		return &StrategyResult{
			Action:      SignalTypeTrailing,
			ShouldTrade: false,
			Reason:      fmt.Sprintf("trailing: price %s above stop %s", snapshot.CurrentPrice, state.StopPrice),
			Timestamp:   time.Now(),
		}, nil
	}

	// Not activated yet, check entry-based stop
	if snapshot.CurrentPrice.LessThanOrEqual(entryStopPrice) {
		sl.markTriggered(snapshot.PositionID)

		if sl.logger != nil {
			sl.logger.Warn().
				Str("position_id", snapshot.PositionID).
				Str("token", snapshot.TokenSymbol).
				Str("current_price", snapshot.CurrentPrice.String()).
				Str("stop_price", entryStopPrice.String()).
				Str("pnl_percent", snapshot.PnLPercent.String()).
				Msg("Stop loss triggered (trailing not yet activated)")
		}

		return &StrategyResult{
			Action:      SignalTypeStopLoss,
			ShouldTrade: true,
			Amount:      snapshot.Amount,
			Reason:      fmt.Sprintf("stop loss at %s%% (trailing pending)", sl.config.Percent),
			Price:       snapshot.CurrentPrice,
			Timestamp:   time.Now(),
		}, nil
	}

	return &StrategyResult{
		Action:      SignalTypeStopLoss,
		ShouldTrade: false,
		Reason:      fmt.Sprintf("trailing pending at %s%% profit", sl.config.TrailingActivate),
		Timestamp:   time.Now(),
	}, nil
}

// calculateEntryStopPrice calculates the initial stop price based on entry price.
func (sl *StopLossStrategy) calculateEntryStopPrice(entryPrice decimal.Decimal) decimal.Decimal {
	// StopPrice = EntryPrice * (1 + Percent/100)
	// For -50%: EntryPrice * (1 - 0.5) = EntryPrice * 0.5
	multiplier := decimal.NewFromInt(1).Add(sl.config.Percent.Div(decimal.NewFromInt(100)))
	return entryPrice.Mul(multiplier)
}

// calculateProfitPercent calculates the profit percentage.
func (sl *StopLossStrategy) calculateProfitPercent(entryPrice, currentPrice decimal.Decimal) decimal.Decimal {
	if entryPrice.IsZero() {
		return decimal.Zero
	}
	return currentPrice.Sub(entryPrice).Div(entryPrice).Mul(decimal.NewFromInt(100))
}

// getOrCreateTrailingState gets or creates trailing state for a position.
func (sl *StopLossStrategy) getOrCreateTrailingState(
	positionID string,
	entryPrice, stopPrice decimal.Decimal,
) *TrailingState {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	state, ok := sl.trailingStates[positionID]
	if !ok {
		state = &TrailingState{
			Activated:        false,
			StopPrice:        stopPrice,
			HighestPrice:     entryPrice,
			InitialStopPrice: stopPrice,
			LastUpdate:       time.Now(),
		}
		sl.trailingStates[positionID] = state
	}

	return state
}

// markTriggered marks a position as having triggered stop loss.
func (sl *StopLossStrategy) markTriggered(positionID string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	sl.triggered[positionID] = true
}

// isTriggered checks if a position has triggered stop loss.
func (sl *StopLossStrategy) isTriggered(positionID string) bool {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	return sl.triggered[positionID]
}

// GetTrailingState returns the trailing state for a position.
func (sl *StopLossStrategy) GetTrailingState(positionID string) *TrailingState {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	state, ok := sl.trailingStates[positionID]
	if !ok {
		return nil
	}

	// Return a copy
	return &TrailingState{
		Activated:        state.Activated,
		ActivationPrice:  state.ActivationPrice,
		StopPrice:        state.StopPrice,
		HighestPrice:     state.HighestPrice,
		InitialStopPrice: state.InitialStopPrice,
		LastUpdate:       state.LastUpdate,
	}
}

// GetStopPrice returns the current stop price for a position.
func (sl *StopLossStrategy) GetStopPrice(positionID string, entryPrice decimal.Decimal) decimal.Decimal {
	state := sl.GetTrailingState(positionID)
	if state != nil && state.Activated {
		return state.StopPrice
	}

	// Return entry-based stop price
	return sl.calculateEntryStopPrice(entryPrice)
}

// Reset resets the stop loss state for a position.
func (sl *StopLossStrategy) Reset(positionID string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	delete(sl.triggered, positionID)
	delete(sl.trailingStates, positionID)

	if sl.logger != nil {
		sl.logger.Debug().
			Str("position_id", positionID).
			Msg("Stop loss state reset")
	}
}

// ValidateConfig validates the stop loss configuration.
func (sl *StopLossStrategy) ValidateConfig() error {
	if sl.config == nil || !sl.config.Enabled {
		return nil
	}

	// Validate stop loss percent
	if sl.config.Percent.IsZero() {
		return errors.New("stop loss percent cannot be zero")
	}

	if sl.config.Percent.GreaterThanOrEqual(decimal.Zero) {
		return fmt.Errorf("stop loss percent must be negative (loss), got %s", sl.config.Percent)
	}

	if sl.config.Percent.LessThan(decimal.NewFromInt(-100)) {
		return errors.New("stop loss percent cannot be less than -100%%")
	}

	// Validate trailing settings
	if sl.config.Trailing {
		if sl.config.TrailingPercent.IsZero() || sl.config.TrailingPercent.IsNegative() {
			return errors.New("trailing percent must be positive")
		}

		if sl.config.TrailingPercent.GreaterThan(decimal.NewFromInt(50)) {
			return fmt.Errorf("trailing percent too large: %s", sl.config.TrailingPercent)
		}

		if sl.config.TrailingActivate.IsZero() || sl.config.TrailingActivate.IsNegative() {
			return errors.New("trailing activation must be positive")
		}

		if sl.config.TrailingActivate.GreaterThan(decimal.NewFromInt(100)) {
			return errors.New("trailing activation cannot exceed 100%%")
		}
	}

	return nil
}

// UpdateConfig updates the stop loss configuration.
func (sl *StopLossStrategy) UpdateConfig(config *StopLossConfig) error {
	if err := sl.validateConfigInternal(config); err != nil {
		return err
	}

	sl.mu.Lock()
	sl.config = config
	sl.mu.Unlock()

	if sl.logger != nil {
		sl.logger.Info().
			Interface("config", config).
			Msg("Stop loss config updated")
	}

	return nil
}

// validateConfigInternal validates a configuration.
func (sl *StopLossStrategy) validateConfigInternal(config *StopLossConfig) error {
	if config == nil || !config.Enabled {
		return nil
	}

	if config.Percent.IsZero() {
		return errors.New("stop loss percent required")
	}

	if config.Percent.GreaterThanOrEqual(decimal.Zero) {
		return errors.New("stop loss percent must be negative")
	}

	if config.Trailing {
		if config.TrailingPercent.IsZero() || config.TrailingPercent.IsNegative() {
			return errors.New("invalid trailing percent")
		}
		if config.TrailingActivate.IsZero() || config.TrailingActivate.IsNegative() {
			return errors.New("invalid trailing activation")
		}
	}

	return nil
}

// GetConfig returns the current stop loss configuration.
func (sl *StopLossStrategy) GetConfig() *StopLossConfig {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	return sl.config
}

// EstimateLoss estimates the loss if stop loss is triggered.
func (sl *StopLossStrategy) EstimateLoss(entryValue decimal.Decimal) decimal.Decimal {
	if sl.config == nil {
		return decimal.Zero
	}

	// Loss = EntryValue * abs(Percent) / 100
	lossPercent := sl.config.Percent.Abs()
	return entryValue.Mul(lossPercent).Div(decimal.NewFromInt(100))
}

// IsTrailingActive checks if trailing is active for a position.
func (sl *StopLossStrategy) IsTrailingActive(positionID string) bool {
	state := sl.GetTrailingState(positionID)
	return state != nil && state.Activated
}

// GetDistanceToStop calculates the distance to stop loss as a percentage.
func (sl *StopLossStrategy) GetDistanceToStop(
	positionID string,
	currentPrice, entryPrice decimal.Decimal,
) decimal.Decimal {
	stopPrice := sl.GetStopPrice(positionID, entryPrice)

	if currentPrice.IsZero() || stopPrice.IsZero() {
		return decimal.Zero
	}

	distance := currentPrice.Sub(stopPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
	return distance
}

// Cleanup removes tracking data for a position.
func (sl *StopLossStrategy) Cleanup(positionID string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	delete(sl.triggered, positionID)
	delete(sl.trailingStates, positionID)

	if sl.logger != nil {
		sl.logger.Debug().
			Str("position_id", positionID).
			Msg("Stop loss data cleaned up")
	}
}

// GetAllStates returns all trailing states (for monitoring/debugging).
func (sl *StopLossStrategy) GetAllStates() map[string]*TrailingState {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	result := make(map[string]*TrailingState, len(sl.trailingStates))
	for k, v := range sl.trailingStates {
		result[k] = &TrailingState{
			Activated:        v.Activated,
			ActivationPrice:  v.ActivationPrice,
			StopPrice:        v.StopPrice,
			HighestPrice:     v.HighestPrice,
			InitialStopPrice: v.InitialStopPrice,
			LastUpdate:       v.LastUpdate,
		}
	}

	return result
}
