// Package strategy provides trading strategies for the meme sniper bot.
// This file implements position sizing logic.
package strategy

import (
	"errors"
	"fmt"
	"math"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

const (
	// Default position sizing constants.
	defaultMaxPositionRisk = 0.02  // 2% of portfolio per position
	defaultMaxTotalRisk    = 0.10  // 10% of portfolio total risk
	defaultKellyMultiplier = 0.25  // Conservative Kelly fraction
	defaultMinPositionSize = 0.001 // Minimum position size
)

// PositionSizer calculates position sizes based on risk parameters.
type PositionSizer struct {
	logger *zerolog.Logger
	config *PositionLimits
}

// PositionSizerConfig holds configuration for position sizing.
type PositionSizerConfig struct {
	Logger             *zerolog.Logger
	PositionLimits     *PositionLimits
	MaxPositionRisk    decimal.Decimal // Max risk per position (as % of portfolio)
	MaxTotalRisk       decimal.Decimal // Max total risk (as % of portfolio)
	KellyMultiplier    decimal.Decimal // Kelly criterion multiplier
	MinPositionSize    decimal.Decimal // Minimum position size
	UseKellyCriterion  bool            // Whether to use Kelly criterion
	UseFixedFractional bool            // Whether to use fixed fractional sizing
}

// NewPositionSizer creates a new position sizer.
func NewPositionSizer(config PositionSizerConfig) *PositionSizer {
	ps := &PositionSizer{
		logger: config.Logger,
		config: config.PositionLimits,
	}

	if ps.config == nil {
		ps.config = &PositionLimits{}
	}

	// Set defaults if not specified
	if ps.config.MaxPerToken.IsZero() {
		ps.config.MaxPerToken = decimal.NewFromInt(1)
	}
	if ps.config.MaxTotalValue.IsZero() {
		ps.config.MaxTotalValue = decimal.NewFromInt(10)
	}
	if ps.config.MaxPerTrade.IsZero() {
		ps.config.MaxPerTrade = decimal.NewFromInt(1)
	}

	return ps
}

// CalculatePositionSize calculates the appropriate position size.
func (ps *PositionSizer) CalculatePositionSize(
	ctx *StrategyContext,
	requestedAmount decimal.Decimal,
) (decimal.Decimal, error) {
	// Validate inputs
	if requestedAmount.IsZero() || requestedAmount.IsNegative() {
		return decimal.Zero, fmt.Errorf("invalid requested amount: %s", requestedAmount)
	}

	// Start with requested amount
	positionSize := requestedAmount

	// Apply various constraints
	positionSize = ps.applyMaxPerTrade(positionSize)
	positionSize = ps.applyMaxPerToken(ctx, positionSize)
	positionSize = ps.applyMaxTotalValue(ctx, positionSize)
	positionSize = ps.applyPortfolioRisk(ctx, positionSize)
	positionSize = ps.applyMaxPositionRisk(positionSize)
	positionSize = ps.applyMinPositionSize(positionSize)

	if ps.logger != nil {
		ps.logger.Debug().
			Str("requested", requestedAmount.String()).
			Str("calculated", positionSize.String()).
			Msg("Position size calculated")
	}

	return positionSize, nil
}

// applyMaxPerTrade applies the maximum per-trade limit.
func (ps *PositionSizer) applyMaxPerTrade(amount decimal.Decimal) decimal.Decimal {
	if ps.config.MaxPerTrade.IsZero() {
		return amount
	}
	if amount.GreaterThan(ps.config.MaxPerTrade) {
		return ps.config.MaxPerTrade
	}
	return amount
}

// applyMaxPerToken applies the maximum limit per token.
func (ps *PositionSizer) applyMaxPerToken(ctx *StrategyContext, amount decimal.Decimal) decimal.Decimal {
	if ps.config.MaxPerToken.IsZero() {
		return amount
	}

	// Check existing position for this token
	var existingPosition decimal.Decimal
	for _, pos := range ctx.Portfolio {
		if pos.Status == "open" && pos.TokenAddress == ctx.TokenInfo.Address {
			existingPosition = pos.TotalInvested.Amount
			break
		}
	}

	newTotal := existingPosition.Add(amount)
	if newTotal.GreaterThan(ps.config.MaxPerToken) {
		maximumAdditional := ps.config.MaxPerToken.Sub(existingPosition)
		if maximumAdditional.IsPositive() {
			return maximumAdditional
		}
		return decimal.Zero
	}

	return amount
}

// applyMaxTotalValue applies the maximum total portfolio value limit.
func (ps *PositionSizer) applyMaxTotalValue(ctx *StrategyContext, amount decimal.Decimal) decimal.Decimal {
	if ps.config.MaxTotalValue.IsZero() {
		return amount
	}

	// Calculate total current portfolio value
	var totalValue decimal.Decimal
	for _, pos := range ctx.Portfolio {
		if pos.Status == "open" {
			totalValue = totalValue.Add(pos.TotalInvested.Amount)
		}
	}

	newTotal := totalValue.Add(amount)
	if newTotal.GreaterThan(ps.config.MaxTotalValue) {
		maximumAdditional := ps.config.MaxTotalValue.Sub(totalValue)
		if maximumAdditional.IsPositive() {
			return maximumAdditional
		}
		return decimal.Zero
	}

	return amount
}

// applyPortfolioRisk applies portfolio-level risk limits.
func (ps *PositionSizer) applyPortfolioRisk(ctx *StrategyContext, amount decimal.Decimal) decimal.Decimal {
	if ps.config.MaxPortfolioRisk.IsZero() {
		return amount
	}

	// Calculate total portfolio value (simplified - assumes 1:1 with invested)
	var totalPortfolio decimal.Decimal
	for _, pos := range ctx.Portfolio {
		totalPortfolio = totalPortfolio.Add(pos.TotalInvested.Amount)
	}

	// Calculate maximum risk amount
	maxRisk := totalPortfolio.Mul(ps.config.MaxPortfolioRisk).Div(decimal.NewFromInt(100))

	// Estimate potential loss (simplified - assumes 100% loss on this position)
	potentialLoss := amount

	if potentialLoss.GreaterThan(maxRisk) {
		// Scale down to fit risk limit
		scaledAmount := amount.Mul(maxRisk).Div(potentialLoss)
		return scaledAmount
	}

	return amount
}

// applyMaxPositionRisk applies the maximum risk per position.
func (ps *PositionSizer) applyMaxPositionRisk(amount decimal.Decimal) decimal.Decimal {
	// This is a simplified implementation
	// In production, you'd want to consider stop-loss distance, volatility, etc.
	maxRiskPerTrade := decimal.NewFromFloat(defaultMaxPositionRisk)

	// Assume portfolio value (would be passed in context in production)
	portfolioValue := decimal.NewFromInt(100) // Placeholder
	maxRiskAmount := portfolioValue.Mul(maxRiskPerTrade)

	if amount.GreaterThan(maxRiskAmount) {
		return maxRiskAmount
	}

	return amount
}

// applyMinPositionSize ensures minimum position size.
func (ps *PositionSizer) applyMinPositionSize(amount decimal.Decimal) decimal.Decimal {
	minSize := decimal.NewFromFloat(defaultMinPositionSize)

	if amount.IsPositive() && amount.LessThan(minSize) {
		// If amount is positive but below minimum, round up to minimum
		return minSize
	}

	return amount
}

// CalculateOptimalSize calculates optimal position size using Kelly criterion.
func (ps *PositionSizer) CalculateOptimalSize(
	ctx *StrategyContext,
	winRate decimal.Decimal,
	avgWin decimal.Decimal,
	avgLoss decimal.Decimal,
) (decimal.Decimal, error) {
	// Kelly Criterion: f = (bp - q) / b
	// where:
	// f = fraction of portfolio to wager
	// b = avg win / avg loss (win/loss ratio)
	// p = probability of winning (win rate)
	// q = probability of losing (1 - p)

	if avgLoss.IsZero() {
		return decimal.Zero, errors.New("avg loss cannot be zero")
	}

	// Calculate win/loss ratio
	b := avgWin.Div(avgLoss)

	// Calculate Kelly fraction
	p := winRate
	q := decimal.NewFromInt(1).Sub(p)

	kellyFraction := b.Mul(p).Sub(q).Div(b)

	// Apply conservative multiplier
	kellyFraction = kellyFraction.Mul(decimal.NewFromFloat(defaultKellyMultiplier))

	// Ensure fraction is positive and reasonable
	if kellyFraction.IsNegative() {
		return decimal.Zero, nil // No edge
	}

	if kellyFraction.GreaterThan(decimal.NewFromFloat(0.25)) {
		kellyFraction = decimal.NewFromFloat(0.25) // Cap at 25%
	}

	// Calculate position size
	portfolioValue := ps.getPortfolioValue(ctx)
	positionSize := portfolioValue.Mul(kellyFraction)

	// Apply limits
	positionSize = ps.applyMaxPerTrade(positionSize)
	positionSize = ps.applyMaxPerToken(ctx, positionSize)
	positionSize = ps.applyMinPositionSize(positionSize)

	if ps.logger != nil {
		ps.logger.Debug().
			Str("kelly_fraction", kellyFraction.String()).
			Str("position_size", positionSize.String()).
			Msg("Kelly criterion calculated")
	}

	return positionSize, nil
}

// getPortfolioValue calculates total portfolio value.
func (ps *PositionSizer) getPortfolioValue(ctx *StrategyContext) decimal.Decimal {
	total := decimal.Zero

	for _, pos := range ctx.Portfolio {
		if pos.Status == "open" {
			total = total.Add(pos.TotalInvested.Amount)
		}
	}

	// Add available balance (would need wallet balance in context)
	// For now, just return invested amount
	if total.IsZero() {
		total = decimal.NewFromInt(100) // Default portfolio value
	}

	return total
}

// CalculateRiskAdjustedSize calculates position size based on risk/reward.
func (ps *PositionSizer) CalculateRiskAdjustedSize(
	ctx *StrategyContext,
	entryPrice decimal.Decimal,
	targetPrice decimal.Decimal,
	stopLossPrice decimal.Decimal,
) (decimal.Decimal, error) {
	// Risk/reward ratio
	risk := entryPrice.Sub(stopLossPrice)
	reward := targetPrice.Sub(entryPrice)

	if risk.IsZero() || risk.IsNegative() {
		return decimal.Zero, fmt.Errorf("invalid risk: %s", risk)
	}

	riskRewardRatio := reward.Div(risk)

	// Only take trades with favorable risk/reward
	minRiskReward := decimal.NewFromInt(2) // Minimum 2:1
	if riskRewardRatio.LessThan(minRiskReward) {
		return decimal.Zero, fmt.Errorf("risk/reward ratio %s below minimum %s",
			riskRewardRatio, minRiskReward)
	}

	// Calculate position size based on risk
	// Risk amount should be no more than 2% of portfolio
	portfolioValue := ps.getPortfolioValue(ctx)
	maxRiskAmount := portfolioValue.Mul(decimal.NewFromFloat(defaultMaxPositionRisk))

	// Position size = maxRiskAmount / riskPerUnit
	positionValue := maxRiskAmount.Div(risk).Mul(entryPrice)

	if ps.logger != nil {
		ps.logger.Debug().
			Str("risk", risk.String()).
			Str("reward", reward.String()).
			Str("risk_reward_ratio", riskRewardRatio.String()).
			Str("position_value", positionValue.String()).
			Msg("Risk-adjusted size calculated")
	}

	return positionValue, nil
}

// CalculatePyramidSize calculates size for pyramiding into a position.
func (ps *PositionSizer) CalculatePyramidSize(
	ctx *StrategyContext,
	currentPosition *PositionSnapshot,
) (decimal.Decimal, error) {
	if currentPosition == nil {
		return decimal.Zero, errors.New("no current position")
	}

	// Check if we're in profit
	if currentPosition.PnLPercent.IsNegative() {
		return decimal.Zero, errors.New("cannot pyramid losing position")
	}

	// Only pyramid if profit exceeds threshold
	minProfitForPyramid := decimal.NewFromInt(10) // 10% profit
	if currentPosition.PnLPercent.LessThan(minProfitForPyramid) {
		return decimal.Zero, nil
	}

	// Add smaller size as position becomes more profitable
	pyramidMultiplier := decimal.NewFromFloat(0.5) // Add 50% of original size
	baseSize := ctx.Strategy.BuyAmount

	pyramidSize := baseSize.Mul(pyramidMultiplier)

	// Apply limits
	pyramidSize = ps.applyMaxPerToken(ctx, pyramidSize)
	pyramidSize = ps.applyMaxTotalValue(ctx, pyramidSize)

	if ps.logger != nil {
		ps.logger.Debug().
			Str("pnl_percent", currentPosition.PnLPercent.String()).
			Str("pyramid_size", pyramidSize.String()).
			Msg("Pyramid size calculated")
	}

	return pyramidSize, nil
}

// CalculateScaleOutSize calculates size for scaling out of a position.
func (ps *PositionSizer) CalculateScaleOutSize(
	ctx *StrategyContext,
	snapshot *PositionSnapshot,
	tier TakeProfitTier,
) (decimal.Decimal, error) {
	if snapshot == nil {
		return decimal.Zero, errors.New("no position snapshot")
	}

	// Calculate portion to sell based on tier
	amountToSell := snapshot.Amount.Mul(tier.SellPortion)

	// Ensure we don't sell more than we have
	if amountToSell.GreaterThan(snapshot.Amount) {
		amountToSell = snapshot.Amount
	}

	if ps.logger != nil {
		ps.logger.Debug().
			Str("current_amount", snapshot.Amount.String()).
			Str("sell_portion", tier.SellPortion.String()).
			Str("amount_to_sell", amountToSell.String()).
			Msg("Scale-out size calculated")
	}

	return amountToSell, nil
}

// ValidatePositionSize validates if a position size meets all requirements.
func (ps *PositionSizer) ValidatePositionSize(
	ctx *StrategyContext,
	size decimal.Decimal,
) error {
	if size.IsZero() {
		return errors.New("position size cannot be zero")
	}

	if size.IsNegative() {
		return errors.New("position size cannot be negative")
	}

	// Check against max per trade
	if !ps.config.MaxPerTrade.IsZero() && size.GreaterThan(ps.config.MaxPerTrade) {
		return fmt.Errorf("position size %s exceeds max per trade %s",
			size, ps.config.MaxPerTrade)
	}

	// Check against max per token
	var existingPosition decimal.Decimal
	for _, pos := range ctx.Portfolio {
		if pos.Status == "open" && pos.TokenAddress == ctx.TokenInfo.Address {
			existingPosition = pos.TotalInvested.Amount
			break
		}
	}

	if !ps.config.MaxPerToken.IsZero() {
		newTotal := existingPosition.Add(size)
		if newTotal.GreaterThan(ps.config.MaxPerToken) {
			return fmt.Errorf("new position total %s would exceed max per token %s",
				newTotal, ps.config.MaxPerToken)
		}
	}

	return nil
}

// GetPositionLimits returns the current position limits.
func (ps *PositionSizer) GetPositionLimits() *PositionLimits {
	return ps.config
}

// UpdatePositionLimits updates the position limits.
func (ps *PositionSizer) UpdatePositionLimits(limits *PositionLimits) {
	ps.config = limits

	if ps.logger != nil {
		ps.logger.Info().
			Interface("limits", limits).
			Msg("Position limits updated")
	}
}

// CalculateUnitsToTrade calculates the number of units to trade
// Returns the integer number of token units based on amount and decimals.
func CalculateUnitsToTrade(amount decimal.Decimal, decimals uint8) (uint64, error) {
	if amount.IsZero() || amount.IsNegative() {
		return 0, fmt.Errorf("invalid amount: %s", amount)
	}

	// Convert to base units (smallest unit of the token)
	// For example, if decimals = 9 and amount = 1.5, result = 1500000000
	multiplier := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimals)))
	units := amount.Mul(multiplier)

	// Check for overflow
	if units.IsNegative() {
		return 0, errors.New("overflow calculating units")
	}

	// Convert to integer
	unitsInt, _ := units.Float64()
	if math.IsInf(unitsInt, 1) || math.IsNaN(unitsInt) {
		return 0, errors.New("invalid units value")
	}

	return uint64(unitsInt), nil
}

// CalculateAmountFromUnits converts token units to amount.
func CalculateAmountFromUnits(units uint64, decimals uint8) decimal.Decimal {
	divisor := decimal.NewFromInt(10).Pow(decimal.NewFromInt(int64(decimals)))
	return decimal.NewFromInt(int64(units)).Div(divisor)
}
