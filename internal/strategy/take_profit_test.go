// Package strategy provides trading strategies for the meme sniper bot.
package strategy

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTakeProfitStrategy(t *testing.T) {
	logger := zerolog.Nop()
	config := &TakeProfitConfig{
		Enabled: true,
		Tiers: []TakeProfitTier{
			{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
			{Tier: 2, Percent: decimal.NewFromInt(400), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
			{Tier: 3, Percent: decimal.NewFromInt(900), SellPortion: decimal.NewFromFloat(0.2), Enabled: true},
		},
	}

	strategy := NewTakeProfitStrategy(TakeProfitStrategyConfig{
		Config: config,
		Logger: &logger,
	})

	assert.NotNil(t, strategy)
	assert.Equal(t, config, strategy.config)
}

func TestTakeProfitStrategy_Evaluate_NoProfit(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	snapshot := createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(90)) // -10% PnL

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
	assert.Equal(t, SignalTypeTakeProfit, result.Action)
	assert.Contains(t, result.Reason, "no profit")
}

func TestTakeProfitStrategy_Evaluate_Tier1Triggered(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	snapshot := createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(200)) // +100% PnL

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade)
	assert.Equal(t, SignalTypeTakeProfit, result.Action)
	assert.Equal(t, 1, result.Tier)
	assert.Equal(t, "5", result.Amount.String()) // 10 * 0.5
}

func TestTakeProfitStrategy_Evaluate_Tier2Triggered(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	snapshot := createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(500)) // +400% PnL

	// Mark tier 1 as executed
	strategy.MarkTierExecuted("test-position", 1)

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade)
	assert.Equal(t, 2, result.Tier)
	assert.Equal(t, "3", result.Amount.String()) // 10 * 0.3
}

func TestTakeProfitStrategy_Evaluate_AllTiersExecuted(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	snapshot := createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(1000)) // +900% PnL

	// Mark all tiers as executed
	strategy.MarkTierExecuted("test-position", 1)
	strategy.MarkTierExecuted("test-position", 2)
	strategy.MarkTierExecuted("test-position", 3)

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
}

func TestTakeProfitStrategy_MarkTierExecuted(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	strategy.MarkTierExecuted("test-position", 1)

	assert.True(t, strategy.isTierExecuted("test-position", 1))
	assert.False(t, strategy.isTierExecuted("test-position", 2))
}

func TestTakeProfitStrategy_GetExecutedTiers(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	strategy.MarkTierExecuted("test-position", 1)
	strategy.MarkTierExecuted("test-position", 3)

	tiers := strategy.GetExecutedTiers("test-position")

	assert.True(t, tiers[1])
	assert.False(t, tiers[2])
	assert.True(t, tiers[3])
	assert.Len(t, tiers, 2)
}

func TestTakeProfitStrategy_ResetExecutedTiers(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	strategy.MarkTierExecuted("test-position", 1)
	strategy.MarkTierExecuted("test-position", 2)

	strategy.ResetExecutedTiers("test-position")

	tiers := strategy.GetExecutedTiers("test-position")
	assert.Empty(t, tiers)
}

func TestTakeProfitStrategy_CalculateTargetPrice(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	entryPrice := decimal.NewFromFloat(100)
	tier := TakeProfitTier{
		Tier:        1,
		Percent:     decimal.NewFromInt(100), // 2x
		SellPortion: decimal.NewFromFloat(0.5),
		Enabled:     true,
	}

	targetPrice := strategy.CalculateTargetPrice(entryPrice, tier)
	assert.Equal(t, "200", targetPrice.String())
}

func TestTakeProfitStrategy_CalculateAllTargetPrices(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	entryPrice := decimal.NewFromFloat(100)

	targets := strategy.CalculateAllTargetPrices(entryPrice)

	assert.Len(t, targets, 3)
	assert.Equal(t, "200", targets[1].String())  // 2x
	assert.Equal(t, "500", targets[2].String())  // 5x
	assert.Equal(t, "1000", targets[3].String()) // 10x
}

func TestTakeProfitStrategy_ValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *TakeProfitConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: false,
		},
		{
			name: "disabled config",
			config: &TakeProfitConfig{
				Enabled: false,
			},
			wantErr: false,
		},
		{
			name: "valid config",
			config: &TakeProfitConfig{
				Enabled: true,
				Tiers: []TakeProfitTier{
					{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
				},
			},
			wantErr: false,
		},
		{
			name: "no tiers",
			config: &TakeProfitConfig{
				Enabled: true,
				Tiers:   []TakeProfitTier{},
			},
			wantErr: true,
		},
		{
			name: "duplicate tier",
			config: &TakeProfitConfig{
				Enabled: true,
				Tiers: []TakeProfitTier{
					{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
					{Tier: 1, Percent: decimal.NewFromInt(200), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid tier number",
			config: &TakeProfitConfig{
				Enabled: true,
				Tiers: []TakeProfitTier{
					{Tier: 0, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
				},
			},
			wantErr: true,
		},
		{
			name: "sell portion > 1",
			config: &TakeProfitConfig{
				Enabled: true,
				Tiers: []TakeProfitTier{
					{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(1.5), Enabled: true},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := NewTakeProfitStrategy(TakeProfitStrategyConfig{
				Config: tt.config,
			})
			err := strategy.ValidateConfig()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTakeProfitStrategy_GetNextTier(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	// No tiers executed, profit 100% (meets tier 1 threshold of 100%)
	tier := strategy.GetNextTier("test-position", decimal.NewFromInt(100))
	assert.NotNil(t, tier)
	assert.Equal(t, 1, tier.Tier)

	// Tier 1 executed, profit 100% doesn't meet tier 2 threshold (400%)
	strategy.MarkTierExecuted("test-position", 1)
	tier = strategy.GetNextTier("test-position", decimal.NewFromInt(100))
	assert.Nil(t, tier) // 100% doesn't meet tier 2's 400% threshold

	// Higher profit meets tier 2 threshold
	tier = strategy.GetNextTier("test-position", decimal.NewFromInt(400))
	assert.NotNil(t, tier)
	assert.Equal(t, 2, tier.Tier)

	// All tiers executed
	strategy.MarkTierExecuted("test-position", 2)
	strategy.MarkTierExecuted("test-position", 3)
	tier = strategy.GetNextTier("test-position", decimal.NewFromInt(900))
	assert.Nil(t, tier)
}

func TestTakeProfitStrategy_GetProgress(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	executed, total := strategy.GetProgress("test-position")
	assert.Equal(t, 0, executed)
	assert.Equal(t, 3, total)

	strategy.MarkTierExecuted("test-position", 1)
	strategy.MarkTierExecuted("test-position", 2)

	executed, total = strategy.GetProgress("test-position")
	assert.Equal(t, 2, executed)
	assert.Equal(t, 3, total)
}

func TestTakeProfitStrategy_ShouldExitAll(t *testing.T) {
	strategy := createTestProfitStrategy()

	assert.False(t, strategy.ShouldExitAll("test-position"))

	strategy.MarkTierExecuted("test-position", 1)
	assert.False(t, strategy.ShouldExitAll("test-position"))

	strategy.MarkTierExecuted("test-position", 2)
	assert.False(t, strategy.ShouldExitAll("test-position"))

	strategy.MarkTierExecuted("test-position", 3)
	assert.True(t, strategy.ShouldExitAll("test-position"))
}

func TestTakeProfitStrategy_EstimateExitValue(t *testing.T) {
	strategy := createTestTakeProfitStrategy()
	entryValue := decimal.NewFromFloat(100)

	targetPrices := map[int]decimal.Decimal{
		1: decimal.NewFromInt(200),  // 2x
		2: decimal.NewFromInt(500),  // 5x
		3: decimal.NewFromInt(1000), // 10x
	}

	exitValue := strategy.EstimateExitValue(entryValue, targetPrices)

	// Expected: 100 * (200/100) * 0.5 + 100 * (500/100) * 0.3 + 100 * (1000/100) * 0.2
	// = 100 * 2 * 0.5 + 100 * 5 * 0.3 + 100 * 10 * 0.2
	// = 100 + 150 + 200 = 450
	assert.Equal(t, "450", exitValue.String())
}

// Helper functions

func createTestTakeProfitStrategy() *TakeProfitStrategy {
	logger := zerolog.Nop()
	config := &TakeProfitConfig{
		Enabled: true,
		Tiers: []TakeProfitTier{
			{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
			{Tier: 2, Percent: decimal.NewFromInt(400), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
			{Tier: 3, Percent: decimal.NewFromInt(900), SellPortion: decimal.NewFromFloat(0.2), Enabled: true},
		},
	}

	return NewTakeProfitStrategy(TakeProfitStrategyConfig{
		Config: config,
		Logger: &logger,
	})
}

func createTestTPSnapshot(entryPrice, currentPrice decimal.Decimal) *PositionSnapshot {
	pnl := currentPrice.Sub(entryPrice)
	pnlPercent := pnl.Div(entryPrice).Mul(decimal.NewFromInt(100))

	return &PositionSnapshot{
		PositionID:   "test-position",
		TokenSymbol:  "TEST",
		Amount:       decimal.NewFromInt(10),
		EntryPrice:   entryPrice,
		CurrentPrice: currentPrice,
		PnL:          pnl,
		PnLPercent:   pnlPercent,
		Timestamp:    time.Now(),
	}
}

func createTestProfitStrategy() *TakeProfitStrategy {
	logger := zerolog.Nop()
	config := &TakeProfitConfig{
		Enabled: true,
		Tiers: []TakeProfitTier{
			{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
			{Tier: 2, Percent: decimal.NewFromInt(400), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
			{Tier: 3, Percent: decimal.NewFromInt(900), SellPortion: decimal.NewFromFloat(0.2), Enabled: true},
		},
	}

	return NewTakeProfitStrategy(TakeProfitStrategyConfig{
		Config: config,
		Logger: &logger,
	})
}

func TestTakeProfitStrategy_EvaluateAll(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	snapshots := []*PositionSnapshot{
		createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(200)), // 2x - should trigger
		createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(90)),  // -10% - no trigger
		createTestTPSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(600)), // 5x - should trigger
	}

	results, err := strategy.EvaluateAll(snapshots)
	require.NoError(t, err)
	assert.Len(t, results, 2) // Only returns results where ShouldTrade is true
	assert.True(t, results[0].ShouldTrade)
	assert.True(t, results[1].ShouldTrade)
}

func TestTakeProfitStrategy_Cleanup(t *testing.T) {
	strategy := createTestTakeProfitStrategy()

	strategy.MarkTierExecuted("test-position", 1)
	strategy.MarkTierExecuted("test-position", 2)

	strategy.Cleanup("test-position")

	tiers := strategy.GetExecutedTiers("test-position")
	assert.Empty(t, tiers)

	highestPrice := strategy.GetHighestPrice("test-position")
	assert.True(t, highestPrice.IsZero())
}
