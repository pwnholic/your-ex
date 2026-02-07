// Package strategy provides trading strategies for the meme sniper bot.
// This file implements the strategy manager that coordinates all strategy components.
package strategy

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// Manager coordinates all trading strategies.
type Manager struct {
	config *StrategyConfig
	logger *zerolog.Logger
	mu     sync.RWMutex

	// Strategy components
	positionSizer *PositionSizer
	takeProfit    *TakeProfitStrategy
	stopLoss      *StopLossStrategy
	trailingStop  *TrailingStop
	entryCriteria *EntryCriteriaFilter

	// State tracking
	activePositions   map[string]*PositionSnapshot
	evaluationHistory map[string][]*StrategyResult
}

// ManagerConfig holds configuration for the strategy manager.
type ManagerConfig struct {
	Strategy *StrategyConfig
	Logger   *zerolog.Logger
}

// NewManager creates a new strategy manager.
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Strategy == nil {
		return nil, errors.New("strategy config is required")
	}

	m := &Manager{
		config:            config.Strategy,
		logger:            config.Logger,
		activePositions:   make(map[string]*PositionSnapshot),
		evaluationHistory: make(map[string][]*StrategyResult),
	}

	// Initialize strategy components
	m.positionSizer = NewPositionSizer(PositionSizerConfig{
		Logger:         config.Logger,
		PositionLimits: config.Strategy.PositionLimits,
	})

	m.takeProfit = NewTakeProfitStrategy(TakeProfitStrategyConfig{
		Config: config.Strategy.TakeProfit,
		Logger: config.Logger,
	})

	m.stopLoss = NewStopLossStrategy(StopLossStrategyConfig{
		Config: config.Strategy.StopLoss,
		Logger: config.Logger,
	})

	if config.Strategy.StopLoss != nil && config.Strategy.StopLoss.Trailing {
		m.trailingStop = NewTrailingStop(&TrailingStopConfig{
			Enabled:           true,
			InitialPercent:    config.Strategy.StopLoss.Percent,
			TrailingPercent:   config.Strategy.StopLoss.TrailingPercent,
			ActivationPercent: config.Strategy.StopLoss.TrailingActivate,
		}, config.Logger)
	}

	m.entryCriteria = NewEntryCriteriaFilter(EntryCriteriaFilterConfig{
		Criteria: config.Strategy.EntryCriteria,
		Logger:   config.Logger,
	})

	// Validate all configurations
	if err := m.ValidateConfig(); err != nil {
		return nil, fmt.Errorf("invalid strategy configuration: %w", err)
	}

	return m, nil
}

// EvaluateEntry evaluates whether to enter a position.
func (m *Manager) EvaluateEntry(ctx *StrategyContext) (*StrategyResult, error) {
	if ctx == nil || ctx.TokenInfo == nil {
		return nil, errors.New("invalid context: missing token info")
	}

	if m.logger != nil {
		m.logger.Debug().
			Str("token", ctx.TokenInfo.Symbol).
			Str("address", ctx.TokenInfo.Address).
			Msg("Evaluating entry criteria")
	}

	// Check entry criteria
	entryResult, err := m.entryCriteria.Evaluate(ctx.TokenInfo)
	if err != nil {
		return nil, fmt.Errorf("error evaluating entry criteria: %w", err)
	}

	if !entryResult.Passed {
		return &StrategyResult{
			Action:      SignalTypeBuy,
			ShouldTrade: false,
			Reason:      entryResult.Reason,
			Timestamp:   time.Now(),
		}, nil
	}

	// Check position limits
	if m.config.PositionLimits != nil {
		if err := m.checkPositionLimits(ctx); err != nil {
			return &StrategyResult{
				Action:      SignalTypeBuy,
				ShouldTrade: false,
				Reason:      fmt.Sprintf("position limit: %s", err),
				Timestamp:   time.Now(),
			}, nil
		}
	}

	// Calculate position size
	positionSize, err := m.positionSizer.CalculatePositionSize(ctx, m.config.BuyAmount)
	if err != nil {
		return nil, fmt.Errorf("error calculating position size: %w", err)
	}

	if m.logger != nil {
		m.logger.Info().
			Str("token", ctx.TokenInfo.Symbol).
			Str("position_size", positionSize.String()).
			Msg("Entry criteria passed")
	}

	return &StrategyResult{
		Action:      SignalTypeBuy,
		ShouldTrade: true,
		Amount:      positionSize,
		Reason:      "all entry criteria passed",
		Timestamp:   time.Now(),
	}, nil
}

// EvaluateExit evaluates whether to exit a position.
func (m *Manager) EvaluateExit(snapshot *PositionSnapshot) ([]*StrategyResult, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot is nil")
	}

	var results []*StrategyResult

	// Update position snapshot
	m.updatePositionSnapshot(snapshot)

	// Evaluate take profit
	tpResult, err := m.takeProfit.Evaluate(snapshot)
	if err != nil {
		m.logError("error evaluating take profit", err, snapshot.PositionID)
	} else {
		results = append(results, tpResult)
	}

	// Evaluate stop loss
	slResult, err := m.stopLoss.Evaluate(snapshot)
	if err != nil {
		m.logError("error evaluating stop loss", err, snapshot.PositionID)
	} else {
		results = append(results, slResult)
	}

	// Record evaluation
	m.recordEvaluation(snapshot.PositionID, results...)

	// Log results
	for _, result := range results {
		if result.ShouldTrade {
			m.logSignal(result, snapshot)
		}
	}

	return results, nil
}

// EvaluateAll evaluates all active positions.
func (m *Manager) EvaluateAll() ([]*StrategyResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allResults []*StrategyResult

	for _, snapshot := range m.activePositions {
		results, err := m.EvaluateExit(snapshot)
		if err != nil {
			m.logError("error evaluating position", err, snapshot.PositionID)
			continue
		}
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// AddPosition adds a position to tracking.
func (m *Manager) AddPosition(snapshot *PositionSnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.activePositions[snapshot.PositionID] = snapshot

	// Initialize trailing stop if enabled
	if m.trailingStop != nil {
		_, err := m.trailingStop.Initialize(
			snapshot.PositionID,
			snapshot.TokenSymbol,
			snapshot.EntryPrice,
		)
		if err != nil && m.logger != nil {
			m.logger.Error().
				Err(err).
				Str("position_id", snapshot.PositionID).
				Msg("Failed to initialize trailing stop")
		}
	}

	if m.logger != nil {
		m.logger.Info().
			Str("position_id", snapshot.PositionID).
			Str("token", snapshot.TokenSymbol).
			Str("amount", snapshot.Amount.String()).
			Str("entry_price", snapshot.EntryPrice.String()).
			Msg("Position added to tracking")
	}

	return nil
}

// UpdatePosition updates a position's current state.
func (m *Manager) UpdatePosition(snapshot *PositionSnapshot) error {
	if snapshot == nil {
		return errors.New("snapshot is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Update snapshot
	m.activePositions[snapshot.PositionID] = snapshot

	// Update trailing stop
	if m.trailingStop != nil {
		_, err := m.trailingStop.Update(snapshot.PositionID, snapshot.CurrentPrice)
		if err != nil && m.logger != nil {
			m.logger.Debug().
				Err(err).
				Str("position_id", snapshot.PositionID).
				Msg("Failed to update trailing stop")
		}
	}

	return nil
}

// RemovePosition removes a position from tracking.
func (m *Manager) RemovePosition(positionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.activePositions, positionID)

	// Cleanup strategy components
	m.takeProfit.Cleanup(positionID)
	m.stopLoss.Cleanup(positionID)
	if m.trailingStop != nil {
		m.trailingStop.Remove(positionID)
	}

	if m.logger != nil {
		m.logger.Info().
			Str("position_id", positionID).
			Msg("Position removed from tracking")
	}
}

// GetPosition retrieves a position snapshot.
func (m *Manager) GetPosition(positionID string) (*PositionSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot, ok := m.activePositions[positionID]
	if !ok {
		return nil, fmt.Errorf("position not found: %s", positionID)
	}

	return snapshot, nil
}

// GetAllPositions returns all active positions.
func (m *Manager) GetAllPositions() []*PositionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	positions := make([]*PositionSnapshot, 0, len(m.activePositions))
	for _, snapshot := range m.activePositions {
		positions = append(positions, snapshot)
	}

	return positions
}

// GetPositionCount returns the number of active positions.
func (m *Manager) GetPositionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.activePositions)
}

// CalculatePositionSize calculates the position size for a trade.
func (m *Manager) CalculatePositionSize(
	ctx *StrategyContext,
	requestedAmount decimal.Decimal,
) (decimal.Decimal, error) {
	return m.positionSizer.CalculatePositionSize(ctx, requestedAmount)
}

// AssessRisk assesses the risk of a potential trade.
func (m *Manager) AssessRisk(ctx *StrategyContext, trade *TradeRequest) (*RiskAssessment, error) {
	assessment := &RiskAssessment{
		RiskLevel: RiskLevelMedium,
		Approval:  true,
		Factors:   []RiskFactor{},
	}

	// Calculate position size
	positionSize, err := m.positionSizer.CalculatePositionSize(ctx, trade.Amount)
	if err != nil {
		assessment.Approval = false
		assessment.Reason = fmt.Sprintf("position sizing failed: %s", err)
		return assessment, nil
	}

	// Calculate max loss
	maxLoss := decimal.Zero
	if m.config.StopLoss != nil && m.config.StopLoss.Enabled {
		maxLoss = m.stopLoss.EstimateLoss(positionSize)
	}
	assessment.MaxLoss = maxLoss

	// Calculate expected return (simplified - would need more data)
	assessment.ExpectedReturn = positionSize.Mul(decimal.NewFromFloat(1.5)) // Assumption

	// Calculate risk/reward ratio
	if !maxLoss.IsZero() {
		assessment.RewardRatio = assessment.ExpectedReturn.Div(maxLoss)
	}

	// Determine risk level
	riskScore := m.calculateRiskScore(ctx, trade)
	assessment.RiskScore = riskScore

	if riskScore.LessThan(decimal.NewFromInt(30)) {
		assessment.RiskLevel = RiskLevelLow
	} else if riskScore.LessThan(decimal.NewFromInt(60)) {
		assessment.RiskLevel = RiskLevelMedium
	} else if riskScore.LessThan(decimal.NewFromInt(80)) {
		assessment.RiskLevel = RiskLevelHigh
	} else {
		assessment.RiskLevel = RiskLevelExtreme
	}

	// Add risk factors
	assessment.Factors = append(assessment.Factors, RiskFactor{
		Name:   "position_size",
		Impact: m.assessPositionSizeImpact(positionSize),
		Score:  riskScore,
	})

	return assessment, nil
}

// calculateRiskScore calculates a risk score for a trade.
func (m *Manager) calculateRiskScore(ctx *StrategyContext, trade *TradeRequest) decimal.Decimal {
	score := decimal.NewFromInt(50) // Base score

	// Adjust based on token score
	if ctx.TokenInfo != nil {
		tokenScore := decimal.NewFromInt(int64(ctx.TokenInfo.Score))
		score = score.Sub(tokenScore.Div(decimal.NewFromInt(2)))
	}

	// Adjust based on position size
	if m.config.PositionLimits != nil {
		maxSize := m.config.PositionLimits.MaxPerTrade
		if !maxSize.IsZero() {
			sizeRatio := trade.Amount.Div(maxSize).Mul(decimal.NewFromInt(10))
			score = score.Add(sizeRatio)
		}
	}

	// Ensure score is in valid range
	if score.LessThan(decimal.Zero) {
		score = decimal.Zero
	}
	if score.GreaterThan(decimal.NewFromInt(100)) {
		score = decimal.NewFromInt(100)
	}

	return score
}

// assessPositionSizeImpact assesses the impact of position size on risk.
func (m *Manager) assessPositionSizeImpact(size decimal.Decimal) RiskImpact {
	if m.config.PositionLimits == nil || m.config.PositionLimits.MaxPerTrade.IsZero() {
		return RiskImpactMedium
	}

	ratio := size.Div(m.config.PositionLimits.MaxPerTrade)

	if ratio.LessThan(decimal.NewFromFloat(0.3)) {
		return RiskImpactLow
	} else if ratio.LessThan(decimal.NewFromFloat(0.7)) {
		return RiskImpactMedium
	} else {
		return RiskImpactHigh
	}
}

// checkPositionLimits checks if position limits allow a new position.
func (m *Manager) checkPositionLimits(ctx *StrategyContext) error {
	if m.config.PositionLimits == nil {
		return nil
	}

	// Check max positions
	if m.config.PositionLimits.MaxPositions > 0 {
		if len(ctx.Portfolio) >= m.config.PositionLimits.MaxPositions {
			return fmt.Errorf("max positions (%d) reached", m.config.PositionLimits.MaxPositions)
		}
	}

	// Check total exposure
	if !m.config.PositionLimits.MaxTotalValue.IsZero() {
		var totalExposure decimal.Decimal
		for _, pos := range ctx.Portfolio {
			if pos.Status == "open" {
				totalExposure = totalExposure.Add(pos.TotalInvested.Amount)
			}
		}

		if totalExposure.GreaterThanOrEqual(m.config.PositionLimits.MaxTotalValue) {
			return fmt.Errorf("max total exposure %s reached", m.config.PositionLimits.MaxTotalValue)
		}
	}

	return nil
}

// updatePositionSnapshot updates a position snapshot.
func (m *Manager) updatePositionSnapshot(snapshot *PositionSnapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.activePositions[snapshot.PositionID] = snapshot
}

// recordEvaluation records an evaluation result.
func (m *Manager) recordEvaluation(positionID string, results ...*StrategyResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.evaluationHistory[positionID] == nil {
		m.evaluationHistory[positionID] = make([]*StrategyResult, 0, 100)
	}

	m.evaluationHistory[positionID] = append(m.evaluationHistory[positionID], results...)

	// Keep only last 100 evaluations
	if len(m.evaluationHistory[positionID]) > 100 {
		m.evaluationHistory[positionID] = m.evaluationHistory[positionID][1:]
	}
}

// logSignal logs a trading signal.
func (m *Manager) logSignal(result *StrategyResult, snapshot *PositionSnapshot) {
	if m.logger == nil {
		return
	}

	m.logger.Info().
		Str("signal_type", string(result.Action)).
		Str("position_id", snapshot.PositionID).
		Str("token", snapshot.TokenSymbol).
		Str("amount", result.Amount.String()).
		Str("reason", result.Reason).
		Msg("Trading signal generated")
}

// logError logs an error.
func (m *Manager) logError(msg string, err error, positionID string) {
	if m.logger == nil {
		return
	}

	m.logger.Error().
		Err(err).
		Str("position_id", positionID).
		Msg(msg)
}

// ValidateConfig validates the strategy configuration.
func (m *Manager) ValidateConfig() error {
	if m.config == nil {
		return errors.New("strategy config is nil")
	}

	if err := m.takeProfit.ValidateConfig(); err != nil {
		return fmt.Errorf("take profit config invalid: %w", err)
	}

	if err := m.stopLoss.ValidateConfig(); err != nil {
		return fmt.Errorf("stop loss config invalid: %w", err)
	}

	if m.trailingStop != nil {
		if err := m.trailingStop.ValidateConfig(); err != nil {
			return fmt.Errorf("trailing stop config invalid: %w", err)
		}
	}

	if err := m.entryCriteria.ValidateConfig(); err != nil {
		return fmt.Errorf("entry criteria config invalid: %w", err)
	}

	return nil
}

// UpdateConfig updates the strategy configuration.
func (m *Manager) UpdateConfig(config *StrategyConfig) error {
	if config == nil {
		return errors.New("config is nil")
	}

	// Validate new config
	// Note: This would need a full validation in production
	m.mu.Lock()
	m.config = config
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info().
			Str("strategy", config.Name).
			Msg("Strategy config updated")
	}

	return nil
}

// GetConfig returns the current strategy configuration.
func (m *Manager) GetConfig() *StrategyConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.config
}

// GetStats returns statistics about the strategy manager.
func (m *Manager) GetStats() *ManagerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := &ManagerStats{
		ActivePositions:  len(m.activePositions),
		TotalEvaluations: 0,
		TakeProfitHits:   0,
		StopLossHits:     0,
		TrailingHits:     0,
	}

	for _, history := range m.evaluationHistory {
		stats.TotalEvaluations += len(history)

		for _, result := range history {
			if result.ShouldTrade {
				switch result.Action {
				case SignalTypeTakeProfit:
					stats.TakeProfitHits++
				case SignalTypeStopLoss:
					stats.StopLossHits++
				case SignalTypeTrailing:
					stats.TrailingHits++
				case SignalTypeBuy, SignalTypeSell, SignalTypeRebalance:
					// These signal types don't contribute to these stats
				}
			}
		}
	}

	if m.trailingStop != nil {
		trailingStats := m.trailingStop.GetStats()
		stats.TrailingActive = trailingStats.ActivatedCount
		stats.TrailingPending = trailingStats.PendingCount
	}

	return stats
}

// ManagerStats holds statistics about the strategy manager.
type ManagerStats struct {
	ActivePositions  int `json:"activePositions"`
	TotalEvaluations int `json:"totalEvaluations"`
	TakeProfitHits   int `json:"takeProfitHits"`
	StopLossHits     int `json:"stopLossHits"`
	TrailingHits     int `json:"trailingHits"`
	TrailingActive   int `json:"trailingActive"`
	TrailingPending  int `json:"trailingPending"`
}

// GetEvaluationHistory returns the evaluation history for a position.
func (m *Manager) GetEvaluationHistory(positionID string) []*StrategyResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	history, ok := m.evaluationHistory[positionID]
	if !ok {
		return nil
	}

	result := make([]*StrategyResult, len(history))
	copy(result, history)

	return result
}

// ShouldRebalance checks if portfolio rebalancing is needed.
func (m *Manager) ShouldRebalance(ctx *StrategyContext) (bool, string) {
	if !m.config.RebalanceEnabled {
		return false, "rebalancing disabled"
	}

	// Calculate portfolio deviation
	// This is a simplified implementation
	// In production, you'd check actual allocations vs target

	return false, "no rebalancing needed"
}

// GenerateRebalanceSignals generates rebalancing signals.
func (m *Manager) GenerateRebalanceSignals(ctx *StrategyContext) ([]*TradeRequest, error) {
	shouldRebalance, _ := m.ShouldRebalance(ctx)
	if !shouldRebalance {
		return nil, nil
	}

	// Generate rebalancing signals
	// This is a placeholder - would need actual rebalancing logic
	var signals []*TradeRequest

	return signals, nil
}
