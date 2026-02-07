// Package strategy provides trading strategies for the meme sniper bot.
// This file implements trailing stop functionality.
package strategy

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// TrailingStop manages trailing stop logic for positions.
type TrailingStop struct {
	config *TrailingStopConfig
	logger *zerolog.Logger
	mu     sync.RWMutex

	// Track trailing state for each position
	states map[string]*TrailState
}

// TrailState holds the current state of a trailing stop.
type TrailState struct {
	PositionID       string          `json:"positionId"`
	TokenSymbol      string          `json:"tokenSymbol"`
	EntryPrice       decimal.Decimal `json:"entryPrice"`
	InitialStopPrice decimal.Decimal `json:"initialStopPrice"`
	CurrentStopPrice decimal.Decimal `json:"currentStopPrice"`
	HighestPrice     decimal.Decimal `json:"highestPrice"`
	Activated        bool            `json:"activated"`
	ActivationPrice  decimal.Decimal `json:"activationPrice"`
	ActivatedAt      *time.Time      `json:"activatedAt,omitempty"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	Triggered        bool            `json:"triggered"`
	TriggeredAt      *time.Time      `json:"triggeredAt,omitempty"`
}

// TrailingStopConfig holds configuration for trailing stop.
type TrailingStopConfig struct {
	Enabled           bool            `json:"enabled"           yaml:"enabled"`
	InitialPercent    decimal.Decimal `json:"initialPercent"    yaml:"initialPercent"`
	TrailingPercent   decimal.Decimal `json:"trailingPercent"   yaml:"trailingPercent"`
	ActivationPercent decimal.Decimal `json:"activationPercent" yaml:"activationPercent"`
}

// NewTrailingStop creates a new trailing stop manager.
func NewTrailingStop(config *TrailingStopConfig, logger *zerolog.Logger) *TrailingStop {
	return &TrailingStop{
		config: config,
		logger: logger,
		states: make(map[string]*TrailState),
	}
}

// Initialize initializes a trailing stop for a position.
func (ts *TrailingStop) Initialize(
	positionID string,
	tokenSymbol string,
	entryPrice decimal.Decimal,
) (*TrailState, error) {
	if ts.config == nil || !ts.config.Enabled {
		return nil, errors.New("trailing stop disabled")
	}

	if entryPrice.IsZero() || entryPrice.IsNegative() {
		return nil, fmt.Errorf("invalid entry price: %s", entryPrice)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Calculate initial stop price
	initialStop := ts.calculateInitialStop(entryPrice)

	state := &TrailState{
		PositionID:       positionID,
		TokenSymbol:      tokenSymbol,
		EntryPrice:       entryPrice,
		InitialStopPrice: initialStop,
		CurrentStopPrice: initialStop,
		HighestPrice:     entryPrice,
		Activated:        false,
		UpdatedAt:        time.Now(),
		Triggered:        false,
	}

	ts.states[positionID] = state

	if ts.logger != nil {
		ts.logger.Info().
			Str("position_id", positionID).
			Str("token", tokenSymbol).
			Str("entry_price", entryPrice.String()).
			Str("initial_stop", initialStop.String()).
			Msg("Trailing stop initialized")
	}

	return state, nil
}

// calculateInitialStop calculates the initial stop price.
func (ts *TrailingStop) calculateInitialStop(entryPrice decimal.Decimal) decimal.Decimal {
	if ts.config.InitialPercent.IsZero() {
		// Default: -10% below entry
		return entryPrice.Mul(decimal.NewFromFloat(0.9))
	}

	multiplier := decimal.NewFromInt(1).Add(ts.config.InitialPercent.Div(decimal.NewFromInt(100)))
	return entryPrice.Mul(multiplier)
}

// Update updates the trailing stop based on current price.
func (ts *TrailingStop) Update(
	positionID string,
	currentPrice decimal.Decimal,
) (*TrailState, error) {
	if currentPrice.IsZero() || currentPrice.IsNegative() {
		return nil, fmt.Errorf("invalid current price: %s", currentPrice)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	state, ok := ts.states[positionID]
	if !ok {
		return nil, fmt.Errorf("position %s not found", positionID)
	}

	// Don't update if already triggered
	if state.Triggered {
		return state, nil
	}

	state.UpdatedAt = time.Now()

	// Update highest price if current price is higher
	if currentPrice.GreaterThan(state.HighestPrice) {
		state.HighestPrice = currentPrice

		// Check if we should activate trailing
		if !state.Activated {
			activationPrice := state.EntryPrice.Mul(
				decimal.NewFromInt(1).Add(
					ts.config.ActivationPercent.Div(decimal.NewFromInt(100)),
				),
			)

			if currentPrice.GreaterThanOrEqual(activationPrice) {
				ts.activate(state, currentPrice)
			}
		}

		// If activated, update trailing stop
		if state.Activated {
			ts.updateStopPrice(state)
		}
	}

	// Check if stop loss is triggered
	if currentPrice.LessThanOrEqual(state.CurrentStopPrice) {
		return ts.trigger(state, currentPrice)
	}

	return state, nil
}

// activate activates trailing for a state.
func (ts *TrailingStop) activate(state *TrailState, currentPrice decimal.Decimal) {
	state.Activated = true
	state.ActivationPrice = currentPrice
	now := time.Now()
	state.ActivatedAt = &now

	if ts.logger != nil {
		ts.logger.Info().
			Str("position_id", state.PositionID).
			Str("token", state.TokenSymbol).
			Str("activation_price", currentPrice.String()).
			Str("activation_percent", ts.config.ActivationPercent.String()).
			Msg("Trailing stop activated")
	}
}

// updateStopPrice updates the stop price based on highest price.
func (ts *TrailingStop) updateStopPrice(state *TrailState) {
	// Calculate new stop price based on highest price
	trailingDistance := decimal.NewFromInt(1).Sub(
		ts.config.TrailingPercent.Div(decimal.NewFromInt(100)),
	)
	newStopPrice := state.HighestPrice.Mul(trailingDistance)

	// Only raise the stop, never lower it
	if newStopPrice.GreaterThan(state.CurrentStopPrice) {
		oldStop := state.CurrentStopPrice
		state.CurrentStopPrice = newStopPrice

		if ts.logger != nil {
			ts.logger.Debug().
				Str("position_id", state.PositionID).
				Str("token", state.TokenSymbol).
				Str("highest_price", state.HighestPrice.String()).
				Str("old_stop", oldStop.String()).
				Str("new_stop", newStopPrice.String()).
				Msg("Trailing stop price updated")
		}
	}
}

// trigger marks the trailing stop as triggered.
func (ts *TrailingStop) trigger(state *TrailState, currentPrice decimal.Decimal) (*TrailState, error) {
	state.Triggered = true
	now := time.Now()
	state.TriggeredAt = &now

	if ts.logger != nil {
		ts.logger.Warn().
			Str("position_id", state.PositionID).
			Str("token", state.TokenSymbol).
			Str("current_price", currentPrice.String()).
			Str("stop_price", state.CurrentStopPrice.String()).
			Str("highest_price", state.HighestPrice.String()).
			Msg("Trailing stop triggered")
	}

	return state, nil
}

// GetState returns the current state for a position.
func (ts *TrailingStop) GetState(positionID string) (*TrailState, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	state, ok := ts.states[positionID]
	if !ok {
		return nil, fmt.Errorf("position %s not found", positionID)
	}

	// Return a copy
	return &TrailState{
		PositionID:       state.PositionID,
		TokenSymbol:      state.TokenSymbol,
		EntryPrice:       state.EntryPrice,
		InitialStopPrice: state.InitialStopPrice,
		CurrentStopPrice: state.CurrentStopPrice,
		HighestPrice:     state.HighestPrice,
		Activated:        state.Activated,
		ActivationPrice:  state.ActivationPrice,
		ActivatedAt:      state.ActivatedAt,
		UpdatedAt:        state.UpdatedAt,
		Triggered:        state.Triggered,
		TriggeredAt:      state.TriggeredAt,
	}, nil
}

// IsTriggered checks if trailing stop has been triggered.
func (ts *TrailingStop) IsTriggered(positionID string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	state, ok := ts.states[positionID]
	return ok && state.Triggered
}

// IsActivated checks if trailing is activated for a position.
func (ts *TrailingStop) IsActivated(positionID string) bool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	state, ok := ts.states[positionID]
	return ok && state.Activated
}

// GetStopPrice returns the current stop price for a position.
func (ts *TrailingStop) GetStopPrice(positionID string) (decimal.Decimal, error) {
	state, err := ts.GetState(positionID)
	if err != nil {
		return decimal.Zero, err
	}

	return state.CurrentStopPrice, nil
}

// CalculateDistanceToStop calculates the percentage distance to stop price.
func (ts *TrailingStop) CalculateDistanceToStop(
	positionID string,
	currentPrice decimal.Decimal,
) (decimal.Decimal, error) {
	stopPrice, err := ts.GetStopPrice(positionID)
	if err != nil {
		return decimal.Zero, err
	}

	if currentPrice.IsZero() || stopPrice.IsZero() {
		return decimal.Zero, nil
	}

	distance := currentPrice.Sub(stopPrice).Div(currentPrice).Mul(decimal.NewFromInt(100))
	return distance, nil
}

// Remove removes a position from tracking.
func (ts *TrailingStop) Remove(positionID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	delete(ts.states, positionID)

	if ts.logger != nil {
		ts.logger.Debug().
			Str("position_id", positionID).
			Msg("Trailing stop removed")
	}
}

// Reset resets the trailing stop state for a position.
func (ts *TrailingStop) Reset(positionID string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	state, ok := ts.states[positionID]
	if !ok {
		return fmt.Errorf("position %s not found", positionID)
	}

	// Reset to initial state
	state.CurrentStopPrice = state.InitialStopPrice
	state.HighestPrice = state.EntryPrice
	state.Activated = false
	state.ActivationPrice = decimal.Zero
	state.ActivatedAt = nil
	state.Triggered = false
	state.TriggeredAt = nil
	state.UpdatedAt = time.Now()

	if ts.logger != nil {
		ts.logger.Info().
			Str("position_id", positionID).
			Msg("Trailing stop reset")
	}

	return nil
}

// GetAllStates returns all states (for monitoring).
func (ts *TrailingStop) GetAllStates() map[string]*TrailState {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make(map[string]*TrailState, len(ts.states))
	for k, v := range ts.states {
		result[k] = &TrailState{
			PositionID:       v.PositionID,
			TokenSymbol:      v.TokenSymbol,
			EntryPrice:       v.EntryPrice,
			InitialStopPrice: v.InitialStopPrice,
			CurrentStopPrice: v.CurrentStopPrice,
			HighestPrice:     v.HighestPrice,
			Activated:        v.Activated,
			ActivationPrice:  v.ActivationPrice,
			ActivatedAt:      v.ActivatedAt,
			UpdatedAt:        v.UpdatedAt,
			Triggered:        v.Triggered,
			TriggeredAt:      v.TriggeredAt,
		}
	}

	return result
}

// GetStats returns statistics about trailing stops.
func (ts *TrailingStop) GetStats() *TrailingStats {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	stats := &TrailingStats{
		TotalPositions: len(ts.states),
		ActivatedCount: 0,
		TriggeredCount: 0,
		PendingCount:   0,
	}

	for _, state := range ts.states {
		if state.Triggered {
			stats.TriggeredCount++
		} else if state.Activated {
			stats.ActivatedCount++
		} else {
			stats.PendingCount++
		}
	}

	return stats
}

// TrailingStats holds statistics about trailing stops.
type TrailingStats struct {
	TotalPositions int `json:"totalPositions"`
	ActivatedCount int `json:"activatedCount"`
	TriggeredCount int `json:"triggeredCount"`
	PendingCount   int `json:"pendingCount"`
}

// ValidateConfig validates the trailing stop configuration.
func (ts *TrailingStop) ValidateConfig() error {
	if ts.config == nil || !ts.config.Enabled {
		return nil
	}

	// Validate trailing percent
	if ts.config.TrailingPercent.IsZero() {
		return errors.New("trailing percent cannot be zero")
	}

	if ts.config.TrailingPercent.IsNegative() {
		return errors.New("trailing percent cannot be negative")
	}

	if ts.config.TrailingPercent.GreaterThan(decimal.NewFromInt(50)) {
		return fmt.Errorf("trailing percent too large: %s (max 50%%)", ts.config.TrailingPercent)
	}

	// Validate activation percent
	if ts.config.ActivationPercent.IsZero() {
		return errors.New("activation percent cannot be zero")
	}

	if ts.config.ActivationPercent.IsNegative() {
		return errors.New("activation percent cannot be negative")
	}

	if ts.config.ActivationPercent.GreaterThan(decimal.NewFromInt(100)) {
		return errors.New("activation percent cannot exceed 100%%")
	}

	// Validate initial percent
	if ts.config.InitialPercent.IsZero() {
		return nil // Default will be used
	}

	if ts.config.InitialPercent.LessThan(decimal.NewFromInt(-50)) {
		return fmt.Errorf("initial stop too low: %s (min -50%%)", ts.config.InitialPercent)
	}

	if ts.config.InitialPercent.GreaterThan(decimal.Zero) {
		return fmt.Errorf("initial stop should be negative or zero: %s", ts.config.InitialPercent)
	}

	return nil
}

// UpdateConfig updates the trailing stop configuration.
func (ts *TrailingStop) UpdateConfig(config *TrailingStopConfig) error {
	if err := ts.validateConfigInternal(config); err != nil {
		return err
	}

	ts.mu.Lock()
	ts.config = config
	ts.mu.Unlock()

	if ts.logger != nil {
		ts.logger.Info().
			Interface("config", config).
			Msg("Trailing stop config updated")
	}

	return nil
}

// validateConfigInternal validates a configuration.
func (ts *TrailingStop) validateConfigInternal(config *TrailingStopConfig) error {
	if config == nil || !config.Enabled {
		return nil
	}

	if config.TrailingPercent.IsZero() || config.TrailingPercent.IsNegative() {
		return errors.New("invalid trailing percent")
	}

	if config.TrailingPercent.GreaterThan(decimal.NewFromInt(50)) {
		return errors.New("trailing percent too large")
	}

	if config.ActivationPercent.IsZero() || config.ActivationPercent.IsNegative() {
		return errors.New("invalid activation percent")
	}

	if config.ActivationPercent.GreaterThan(decimal.NewFromInt(100)) {
		return errors.New("activation percent too large")
	}

	return nil
}

// CalculateActivationPrice calculates the price at which trailing activates.
func (ts *TrailingStop) CalculateActivationPrice(entryPrice decimal.Decimal) decimal.Decimal {
	if ts.config == nil {
		return decimal.Zero
	}

	multiplier := decimal.NewFromInt(1).Add(
		ts.config.ActivationPercent.Div(decimal.NewFromInt(100)),
	)
	return entryPrice.Mul(multiplier)
}

// CalculateTrailingStop calculates the trailing stop price given a highest price.
func (ts *TrailingStop) CalculateTrailingStop(highestPrice decimal.Decimal) decimal.Decimal {
	if ts.config == nil {
		return decimal.Zero
	}

	trailingDistance := decimal.NewFromInt(1).Sub(
		ts.config.TrailingPercent.Div(decimal.NewFromInt(100)),
	)
	return highestPrice.Mul(trailingDistance)
}

// EstimateProfitAtStop estimates the profit if stopped at current level.
func (ts *TrailingStop) EstimateProfitAtStop(positionID string) (decimal.Decimal, error) {
	state, err := ts.GetState(positionID)
	if err != nil {
		return decimal.Zero, err
	}

	if state.EntryPrice.IsZero() || state.CurrentStopPrice.IsZero() {
		return decimal.Zero, nil
	}

	profitPercent := state.CurrentStopPrice.Sub(state.EntryPrice).
		Div(state.EntryPrice).
		Mul(decimal.NewFromInt(100))

	return profitPercent, nil
}

// GetConfig returns the current configuration.
func (ts *TrailingStop) GetConfig() *TrailingStopConfig {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	return ts.config
}
