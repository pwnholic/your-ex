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

func TestNewStopLossStrategy(t *testing.T) {
	logger := zerolog.Nop()
	config := &StopLossConfig{
		Enabled:          true,
		Percent:          decimal.NewFromInt(-50),
		Trailing:         true,
		TrailingPercent:  decimal.NewFromInt(10),
		TrailingActivate: decimal.NewFromInt(10),
	}

	strategy := NewStopLossStrategy(StopLossStrategyConfig{
		Config: config,
		Logger: &logger,
	})

	assert.NotNil(t, strategy)
	assert.Equal(t, config, strategy.config)
}

func TestStopLossStrategy_Evaluate_Disabled(t *testing.T) {
	logger := zerolog.Nop()
	config := &StopLossConfig{Enabled: false}

	strategy := NewStopLossStrategy(StopLossStrategyConfig{
		Config: config,
		Logger: &logger,
	})

	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(40))

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
	assert.Contains(t, result.Reason, "disabled")
}

func TestStopLossStrategy_Evaluate_Triggered(t *testing.T) {
	strategy := createTestStopLossStrategy()
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(40)) // -60%, below -50% stop

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade)
	assert.Equal(t, SignalTypeStopLoss, result.Action)
	assert.Contains(t, result.Reason, "stop loss")
}

func TestStopLossStrategy_Evaluate_NotTriggered(t *testing.T) {
	strategy := createTestStopLossStrategy()
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(80)) // -20%, above -50% stop

	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.False(t, result.ShouldTrade)
	assert.Contains(t, result.Reason, "above stop")
}

func TestStopLossStrategy_Evaluate_Trailing_Activated(t *testing.T) {
	strategy := createTrailingStopLossStrategy()

	// Price goes up to 120 (+20%), then back to 108
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(120))
	snapshot.HighestPrice = decimal.NewFromFloat(120)

	result1, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.False(t, result1.ShouldTrade)

	// Trailing should be activated at 110 (10% above entry)
	// With 10% trailing, stop should be at 108 (120 * 0.9)
	snapshot.CurrentPrice = decimal.NewFromFloat(108)

	result2, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	// Should trigger at 108 (trailing stop)
	assert.True(t, result2.ShouldTrade)
}

func TestStopLossStrategy_calculateEntryStopPrice(t *testing.T) {
	strategy := createTestStopLossStrategy()

	entryPrice := decimal.NewFromFloat(100)
	stopPrice := strategy.calculateEntryStopPrice(entryPrice)

	assert.Equal(t, "50", stopPrice.String()) // 100 * (1 - 0.5) = 50
}

func TestStopLossStrategy_calculateProfitPercent(t *testing.T) {
	strategy := createTestStopLossStrategy()

	tests := []struct {
		name         string
		entryPrice   decimal.Decimal
		currentPrice decimal.Decimal
		expected     decimal.Decimal
	}{
		{
			name:         "10% profit",
			entryPrice:   decimal.NewFromFloat(100),
			currentPrice: decimal.NewFromFloat(110),
			expected:     decimal.NewFromInt(10),
		},
		{
			name:         "50% loss",
			entryPrice:   decimal.NewFromFloat(100),
			currentPrice: decimal.NewFromFloat(50),
			expected:     decimal.NewFromInt(-50),
		},
		{
			name:         "break even",
			entryPrice:   decimal.NewFromFloat(100),
			currentPrice: decimal.NewFromFloat(100),
			expected:     decimal.Zero,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profitPercent := strategy.calculateProfitPercent(tt.entryPrice, tt.currentPrice)
			assert.True(t, profitPercent.Equal(tt.expected))
		})
	}
}

func TestStopLossStrategy_GetTrailingState(t *testing.T) {
	strategy := createTrailingStopLossStrategy()
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(105)) // 5% - below activation

	// First evaluation creates state
	_, _ = strategy.Evaluate(snapshot)

	state := strategy.GetTrailingState("test-position")
	assert.NotNil(t, state)
	assert.False(t, state.Activated) // Not yet activated since below 10% threshold
}

func TestStopLossStrategy_Reset(t *testing.T) {
	strategy := createTestStopLossStrategy()

	// Mark as triggered
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(40))
	_, _ = strategy.Evaluate(snapshot)

	// Reset
	strategy.Reset("test-position")

	// Should evaluate again
	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade) // Still should trigger, but state was reset
}

func TestStopLossStrategy_ValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *StopLossConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: false,
		},
		{
			name: "disabled",
			config: &StopLossConfig{
				Enabled: false,
			},
			wantErr: false,
		},
		{
			name: "valid config",
			config: &StopLossConfig{
				Enabled: true,
				Percent: decimal.NewFromInt(-50),
			},
			wantErr: false,
		},
		{
			name: "positive percent",
			config: &StopLossConfig{
				Enabled: true,
				Percent: decimal.NewFromInt(50),
			},
			wantErr: true,
		},
		{
			name: "less than -100%",
			config: &StopLossConfig{
				Enabled: true,
				Percent: decimal.NewFromInt(-150),
			},
			wantErr: true,
		},
		{
			name: "valid trailing",
			config: &StopLossConfig{
				Enabled:          true,
				Percent:          decimal.NewFromInt(-50),
				Trailing:         true,
				TrailingPercent:  decimal.NewFromInt(10),
				TrailingActivate: decimal.NewFromInt(10),
			},
			wantErr: false,
		},
		{
			name: "trailing with negative percent",
			config: &StopLossConfig{
				Enabled:          true,
				Percent:          decimal.NewFromInt(-50),
				Trailing:         true,
				TrailingPercent:  decimal.NewFromInt(-10), // Negative should error
				TrailingActivate: decimal.NewFromInt(10),
			},
			wantErr: true,
		},
		{
			name: "trailing percent too large",
			config: &StopLossConfig{
				Enabled:          true,
				Percent:          decimal.NewFromInt(-50),
				Trailing:         true,
				TrailingPercent:  decimal.NewFromInt(60),
				TrailingActivate: decimal.NewFromInt(10),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := NewStopLossStrategy(StopLossStrategyConfig{
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

func TestStopLossStrategy_EstimateLoss(t *testing.T) {
	strategy := createTestStopLossStrategy()

	entryValue := decimal.NewFromFloat(1000)
	loss := strategy.EstimateLoss(entryValue)

	// -50% of 1000 = 500
	assert.Equal(t, "500", loss.String())
}

func TestStopLossStrategy_IsTrailingActive(t *testing.T) {
	strategy := createTrailingStopLossStrategy()

	assert.False(t, strategy.IsTrailingActive("test-position"))

	// Activate trailing by getting price above activation threshold
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(120))
	_, _ = strategy.Evaluate(snapshot)

	// Check if activated
	state := strategy.GetTrailingState("test-position")
	if state != nil && state.Activated {
		assert.True(t, strategy.IsTrailingActive("test-position"))
	}
}

func TestStopLossStrategy_GetDistanceToStop(t *testing.T) {
	strategy := createTestStopLossStrategy()

	currentPrice := decimal.NewFromFloat(75)
	entryPrice := decimal.NewFromFloat(100)

	distance := strategy.GetDistanceToStop("test-position", currentPrice, entryPrice)

	// Stop is at 50, current is 75
	// Distance = (75 - 50) / 75 * 100 = 33.33%
	assert.True(t, distance.GreaterThan(decimal.NewFromInt(30)))
	assert.True(t, distance.LessThan(decimal.NewFromInt(40)))
}

func TestStopLossStrategy_Cleanup(t *testing.T) {
	strategy := createTestStopLossStrategy()

	// Trigger stop loss
	snapshot := createTestSnapshot(decimal.NewFromFloat(100), decimal.NewFromFloat(40))
	_, _ = strategy.Evaluate(snapshot)

	// Cleanup
	strategy.Cleanup("test-position")

	// Should no longer be triggered
	result, err := strategy.Evaluate(snapshot)
	require.NoError(t, err)
	assert.True(t, result.ShouldTrade) // Can trigger again
}

func TestStopLossStrategy_GetStopPrice(t *testing.T) {
	strategy := createTestStopLossStrategy()

	entryPrice := decimal.NewFromFloat(100)
	stopPrice := strategy.GetStopPrice("test-position", entryPrice)

	assert.Equal(t, "50", stopPrice.String()) // 50% of entry
}

func TestStopLossStrategy_GetConfig(t *testing.T) {
	config := &StopLossConfig{
		Enabled: true,
		Percent: decimal.NewFromInt(-40),
	}

	strategy := NewStopLossStrategy(StopLossStrategyConfig{
		Config: config,
	})

	assert.Equal(t, config, strategy.GetConfig())
}

// Helper functions

func createTestStopLossStrategy() *StopLossStrategy {
	logger := zerolog.Nop()
	config := &StopLossConfig{
		Enabled:  true,
		Percent:  decimal.NewFromInt(-50), // -50% stop loss
		Trailing: false,
	}

	return NewStopLossStrategy(StopLossStrategyConfig{
		Config: config,
		Logger: &logger,
	})
}

func createTrailingStopLossStrategy() *StopLossStrategy {
	logger := zerolog.Nop()
	config := &StopLossConfig{
		Enabled:          true,
		Percent:          decimal.NewFromInt(-50),
		Trailing:         true,
		TrailingPercent:  decimal.NewFromInt(10), // 10% trailing
		TrailingActivate: decimal.NewFromInt(10), // Activate at +10%
	}

	return NewStopLossStrategy(StopLossStrategyConfig{
		Config: config,
		Logger: &logger,
	})
}

func createTestSnapshot(entryPrice, currentPrice decimal.Decimal) *PositionSnapshot {
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
		HighestPrice: currentPrice,
	}
}
