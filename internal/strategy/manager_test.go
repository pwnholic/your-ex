// Package strategy provides trading strategies for the meme sniper bot.
package strategy

import (
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	logger := zerolog.Nop()
	config := createTestStrategyConfig()

	manager, err := NewManager(ManagerConfig{
		Strategy: config,
		Logger:   &logger,
	})

	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.Equal(t, config, manager.config)
}

func TestNewManager_InvalidConfig(t *testing.T) {
	logger := zerolog.Nop()

	// Invalid take profit config (duplicate tiers)
	config := &StrategyConfig{
		Name:      "test",
		Chain:     ChainSolana,
		BuyAmount: decimal.NewFromFloat(0.1),
		TakeProfit: &TakeProfitConfig{
			Enabled: true,
			Tiers: []TakeProfitTier{
				{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
				{Tier: 1, Percent: decimal.NewFromInt(200), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
			},
		},
	}

	_, err := NewManager(ManagerConfig{
		Strategy: config,
		Logger:   &logger,
	})

	assert.Error(t, err)
}

func TestManager_EvaluateEntry_Passes(t *testing.T) {
	manager := createTestManager()
	ctx := createPassingEntryContext()

	result, err := manager.EvaluateEntry(ctx)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade)
	assert.Equal(t, SignalTypeBuy, result.Action)
}

func TestManager_EvaluateEntry_FailsCriteria(t *testing.T) {
	manager := createTestManager()
	ctx := createFailingEntryContext()

	result, err := manager.EvaluateEntry(ctx)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
}

func TestManager_EvaluateEntry_PositionLimitReached(t *testing.T) {
	manager := createTestManager()

	// Create a context with max positions already reached
	ctx := &StrategyContext{
		Strategy: manager.config,
		TokenInfo: &TokenInfo{
			Address:   "test-address",
			Symbol:    "TEST",
			Name:      "Test Token",
			Decimals:  9,
			Liquidity: decimal.NewFromInt(10000),
			Score:     80,
			MarketCap: decimal.NewFromInt(100000),
		},
		Portfolio: make([]*wallet.Position, 10), // Max positions is 10
	}

	result, err := manager.EvaluateEntry(ctx)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
	assert.Contains(t, result.Reason, "position limit")
}

func TestManager_AddPosition(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()

	err := manager.AddPosition(snapshot)
	require.NoError(t, err)

	retrieved, err := manager.GetPosition(snapshot.PositionID)
	require.NoError(t, err)
	assert.Equal(t, snapshot.PositionID, retrieved.PositionID)
}

func TestManager_UpdatePosition(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()

	manager.AddPosition(snapshot)

	// Update with new price
	snapshot.CurrentPrice = decimal.NewFromFloat(200)
	snapshot.PnL = decimal.NewFromInt(100)
	snapshot.PnLPercent = decimal.NewFromInt(100)

	err := manager.UpdatePosition(snapshot)
	require.NoError(t, err)

	retrieved, _ := manager.GetPosition(snapshot.PositionID)
	assert.Equal(t, "200", retrieved.CurrentPrice.String())
}

func TestManager_RemovePosition(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()

	manager.AddPosition(snapshot)
	manager.RemovePosition(snapshot.PositionID)

	_, err := manager.GetPosition(snapshot.PositionID)
	assert.Error(t, err)
}

func TestManager_GetAllPositions(t *testing.T) {
	manager := createTestManager()

	snapshot1 := createTestPositionSnapshot()
	snapshot1.PositionID = "pos-1"
	snapshot2 := createTestPositionSnapshot()
	snapshot2.PositionID = "pos-2"

	manager.AddPosition(snapshot1)
	manager.AddPosition(snapshot2)

	positions := manager.GetAllPositions()
	assert.Len(t, positions, 2)
}

func TestManager_GetPositionCount(t *testing.T) {
	manager := createTestManager()

	assert.Equal(t, 0, manager.GetPositionCount())

	snapshot := createTestPositionSnapshot()
	manager.AddPosition(snapshot)

	assert.Equal(t, 1, manager.GetPositionCount())
}

func TestManager_EvaluateExit_TakeProfit(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()
	snapshot.CurrentPrice = decimal.NewFromFloat(200) // 2x - triggers tier 1
	snapshot.PnL = decimal.NewFromInt(100)
	snapshot.PnLPercent = decimal.NewFromInt(100)

	manager.AddPosition(snapshot)

	results, err := manager.EvaluateExit(snapshot)
	require.NoError(t, err)

	// Should have take profit signal
	found := false
	for _, result := range results {
		if result.Action == SignalTypeTakeProfit && result.ShouldTrade {
			found = true
			assert.Equal(t, 1, result.Tier)
		}
	}
	assert.True(t, found)
}

func TestManager_EvaluateExit_StopLoss(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()
	snapshot.CurrentPrice = decimal.NewFromFloat(40) // -60% - triggers stop loss
	snapshot.PnL = decimal.NewFromInt(-60)
	snapshot.PnLPercent = decimal.NewFromInt(-60)

	manager.AddPosition(snapshot)

	results, err := manager.EvaluateExit(snapshot)
	require.NoError(t, err)

	// Should have stop loss signal
	found := false
	for _, result := range results {
		if result.Action == SignalTypeStopLoss && result.ShouldTrade {
			found = true
		}
	}
	assert.True(t, found)
}

func TestManager_CalculatePositionSize(t *testing.T) {
	manager := createTestManager()
	ctx := &StrategyContext{
		Strategy: manager.config,
	}

	size, err := manager.CalculatePositionSize(ctx, decimal.NewFromFloat(1))
	require.NoError(t, err)
	assert.True(t, size.IsPositive())
}

func TestManager_AssessRisk(t *testing.T) {
	manager := createTestManager()
	ctx := &StrategyContext{
		Strategy: manager.config,
		TokenInfo: &TokenInfo{
			Score: 75,
		},
	}
	trade := &TradeRequest{
		Chain:        ChainSolana,
		TokenAddress: "test-address",
		TokenSymbol:  "TEST",
		Amount:       decimal.NewFromFloat(0.1),
		Type:         SignalTypeBuy,
	}

	assessment, err := manager.AssessRisk(ctx, trade)
	require.NoError(t, err)
	assert.NotNil(t, assessment)
	assert.NotEmpty(t, assessment.Factors)
}

func TestManager_ValidateConfig(t *testing.T) {
	manager := createTestManager()
	err := manager.ValidateConfig()
	assert.NoError(t, err)
}

func TestManager_UpdateConfig(t *testing.T) {
	manager := createTestManager()
	newConfig := createTestStrategyConfig()
	newConfig.Name = "updated"

	err := manager.UpdateConfig(newConfig)
	require.NoError(t, err)
	assert.Equal(t, "updated", manager.config.Name)
}

func TestManager_GetStats(t *testing.T) {
	manager := createTestManager()

	snapshot := createTestPositionSnapshot()
	manager.AddPosition(snapshot)

	stats := manager.GetStats()
	assert.NotNil(t, stats)
	assert.Equal(t, 1, stats.ActivePositions)
}

func TestManager_GetEvaluationHistory(t *testing.T) {
	manager := createTestManager()
	snapshot := createTestPositionSnapshot()
	manager.AddPosition(snapshot)

	// Evaluate to create history
	_, _ = manager.EvaluateExit(snapshot)

	history := manager.GetEvaluationHistory(snapshot.PositionID)
	assert.NotNil(t, history)
	assert.NotEmpty(t, history)
}

// Helper functions

func createTestManager() *Manager {
	logger := zerolog.Nop()
	config := createTestStrategyConfig()

	manager, _ := NewManager(ManagerConfig{
		Strategy: config,
		Logger:   &logger,
	})

	return manager
}

func createTestStrategyConfig() *StrategyConfig {
	return &StrategyConfig{
		Name:        "test-strategy",
		Chain:       ChainSolana,
		BuyAmount:   decimal.NewFromFloat(0.1),
		MaxSlippage: 300, // 3%
		TakeProfit: &TakeProfitConfig{
			Enabled: true,
			Tiers: []TakeProfitTier{
				{Tier: 1, Percent: decimal.NewFromInt(100), SellPortion: decimal.NewFromFloat(0.5), Enabled: true},
				{Tier: 2, Percent: decimal.NewFromInt(400), SellPortion: decimal.NewFromFloat(0.3), Enabled: true},
				{Tier: 3, Percent: decimal.NewFromInt(900), SellPortion: decimal.NewFromFloat(0.2), Enabled: true},
			},
		},
		StopLoss: &StopLossConfig{
			Enabled: true,
			Percent: decimal.NewFromInt(-50),
		},
		PositionLimits: &PositionLimits{
			MaxPositions:  10,
			MaxPerToken:   decimal.NewFromInt(1),
			MaxTotalValue: decimal.NewFromInt(10),
			MaxPerTrade:   decimal.NewFromFloat(0.5),
		},
		EntryCriteria: &EntryCriteria{
			MinLiquidity:           decimal.NewFromInt(1000),
			MaxHolderConcentration: decimal.NewFromFloat(0.3),
			MinScore:               60,
			RequireSocials:         false,
		},
		RebalanceEnabled: false,
	}
}

func createPassingEntryContext() *StrategyContext {
	return &StrategyContext{
		Strategy: createTestStrategyConfig(),
		TokenInfo: &TokenInfo{
			Address:   "test-address",
			Symbol:    "TEST",
			Name:      "Test Token",
			Decimals:  9,
			Liquidity: decimal.NewFromInt(10000),
			Score:     80,
			MarketCap: decimal.NewFromInt(100000),
			Socials:   &SocialInfo{Twitter: "@test"},
			Holders: []HolderInfo{
				{Address: "holder1", Amount: decimal.NewFromInt(1000), Percent: decimal.NewFromFloat(0.1)},
				{Address: "holder2", Amount: decimal.NewFromInt(1000), Percent: decimal.NewFromFloat(0.1)},
			},
		},
		Portfolio: []*wallet.Position{},
	}
}

func createFailingEntryContext() *StrategyContext {
	return &StrategyContext{
		Strategy: createTestStrategyConfig(),
		TokenInfo: &TokenInfo{
			Address:   "test-address",
			Symbol:    "TEST",
			Name:      "Test Token",
			Decimals:  9,
			Liquidity: decimal.NewFromInt(100), // Below minimum
			Score:     40,                      // Below minimum
		},
		Portfolio: []*wallet.Position{},
	}
}

func createTestPositionSnapshot() *PositionSnapshot {
	return &PositionSnapshot{
		PositionID:   "test-position",
		TokenSymbol:  "TEST",
		Amount:       decimal.NewFromInt(10),
		EntryPrice:   decimal.NewFromFloat(100),
		CurrentPrice: decimal.NewFromFloat(100),
		PnL:          decimal.Zero,
		PnLPercent:   decimal.Zero,
		Timestamp:    time.Now(),
		HighestPrice: decimal.NewFromFloat(100),
	}
}
