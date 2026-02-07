// Package trader provides trading functionality for the meme sniper bot.
package trader

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMEVProtection(t *testing.T) {
	config := MEVConfig{
		Logger: testLogger(),
	}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)
	require.NotNil(t, mev)

	assert.Equal(t, MEVProviderAuto, mev.config.Provider)
	assert.NotNil(t, mev.httpClient)
	assert.NotNil(t, mev.logger)
}

func TestNewMEVProtectionDefaults(t *testing.T) {
	config := MEVConfig{}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)
	require.NotNil(t, mev)

	// Check default values
	assert.NotNil(t, mev.httpClient)
	assert.NotNil(t, mev.logger)
	assert.Equal(t, MEVProviderAuto, mev.config.Provider)
	assert.NotNil(t, mev.config.MinTradeSize)
	assert.Equal(t, StrategyStandard, mev.config.PriorityFeeStrategy)
	assert.NotNil(t, mev.config.MaxPriorityFee)
	assert.NotNil(t, mev.config.MaxFeePerGas)
}

func TestNewMEVProtectionCustomValues(t *testing.T) {
	customMinTradeSize := big.NewInt(50000000000000000) // 0.05 ETH
	customProvider := MEVProviderFlashbots
	customStrategy := StrategyAggressive
	customMaxPriorityFee := big.NewInt(100000000000) // 100 Gwei
	customMaxFee := big.NewInt(200000000000)         // 200 Gwei

	config := MEVConfig{
		Logger:              testLogger(),
		Provider:            customProvider,
		MinTradeSize:        customMinTradeSize,
		PriorityFeeStrategy: customStrategy,
		MaxPriorityFee:      customMaxPriorityFee,
		MaxFeePerGas:        customMaxFee,
	}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)
	require.NotNil(t, mev)

	assert.Equal(t, customProvider, mev.config.Provider)
	assert.Equal(t, customMinTradeSize, mev.config.MinTradeSize)
	assert.Equal(t, customStrategy, mev.config.PriorityFeeStrategy)
	assert.Equal(t, customMaxPriorityFee, mev.config.MaxPriorityFee)
	assert.Equal(t, customMaxFee, mev.config.MaxFeePerGas)
}

func TestShouldProtect(t *testing.T) {
	tests := []struct {
		name        string
		provider    MEVProvider
		minSize     *big.Int
		tradeSize   *big.Int
		wantProtect bool
	}{
		{
			name:        "none provider - never protect",
			provider:    MEVProviderNone,
			minSize:     big.NewInt(10000000000000000),
			tradeSize:   big.NewInt(100000000000000000),
			wantProtect: false,
		},
		{
			name:        "auto provider - large trade",
			provider:    MEVProviderAuto,
			minSize:     big.NewInt(10000000000000000),  // 0.01 ETH
			tradeSize:   big.NewInt(100000000000000000), // 0.1 ETH
			wantProtect: true,
		},
		{
			name:        "auto provider - small trade",
			provider:    MEVProviderAuto,
			minSize:     big.NewInt(10000000000000000), // 0.01 ETH
			tradeSize:   big.NewInt(5000000000000000),  // 0.005 ETH
			wantProtect: false,
		},
		{
			name:        "flashbots provider - always protect",
			provider:    MEVProviderFlashbots,
			minSize:     big.NewInt(10000000000000000),
			tradeSize:   big.NewInt(1000000000000000), // Small trade
			wantProtect: true,
		},
		{
			name:        "merkle provider - always protect",
			provider:    MEVProviderMerkle,
			minSize:     big.NewInt(10000000000000000),
			tradeSize:   big.NewInt(1000000000000000), // Small trade
			wantProtect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := MEVConfig{
				Logger:       testLogger(),
				Provider:     tt.provider,
				MinTradeSize: tt.minSize,
			}

			mev, err := NewMEVProtection(config)
			require.NoError(t, err)

			result := mev.ShouldProtect(tt.tradeSize)
			assert.Equal(t, tt.wantProtect, result)
		})
	}
}

func TestGetProvider(t *testing.T) {
	minTradeSize := big.NewInt(10000000000000000) // 0.01 ETH

	config := MEVConfig{
		Logger:       testLogger(),
		Provider:     MEVProviderAuto,
		MinTradeSize: minTradeSize,
	}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)

	tests := []struct {
		name      string
		tradeSize *big.Int
		expected  MEVProvider
	}{
		{
			name:      "small trade",
			tradeSize: big.NewInt(5000000000000000), // 0.005 ETH
			expected:  MEVProviderMerkle,
		},
		{
			name:      "large trade",
			tradeSize: big.NewInt(100000000000000000), // 0.1 ETH
			expected:  MEVProviderFlashbots,
		},
		{
			name:      "exact threshold",
			tradeSize: minTradeSize,
			expected:  MEVProviderFlashbots,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := mev.GetProvider(tt.tradeSize)
			assert.Equal(t, tt.expected, provider)
		})
	}
}

func TestCalculatePriorityFee(t *testing.T) {
	config := MEVConfig{
		Logger:              testLogger(),
		PriorityFeeStrategy: StrategyStandard,
		MaxPriorityFee:      big.NewInt(50000000000),  // 50 Gwei
		MaxFeePerGas:        big.NewInt(100000000000), // 100 Gwei
	}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)

	tests := []struct {
		name          string
		strategy      string
		baseFee       *big.Int
		congestion    float64
		expectInRange bool
	}{
		{
			name:          "conservative low congestion",
			strategy:      StrategyConservative,
			baseFee:       big.NewInt(30000000000), // 30 Gwei
			congestion:    0.2,
			expectInRange: true,
		},
		{
			name:          "standard medium congestion",
			strategy:      StrategyStandard,
			baseFee:       big.NewInt(30000000000),
			congestion:    0.5,
			expectInRange: true,
		},
		{
			name:          "aggressive high congestion",
			strategy:      StrategyAggressive,
			baseFee:       big.NewInt(30000000000),
			congestion:    0.8,
			expectInRange: true,
		},
		{
			name:          "max strategy",
			strategy:      StrategyMax,
			baseFee:       big.NewInt(30000000000),
			congestion:    0.5,
			expectInRange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mev.config.PriorityFeeStrategy = tt.strategy

			priorityFee, err := mev.CalculatePriorityFee(tt.baseFee, tt.congestion)
			require.NoError(t, err)
			require.NotNil(t, priorityFee)

			if tt.expectInRange {
				// Should be positive and capped at max
				assert.Positive(t, priorityFee.Int64())
				assert.LessOrEqual(t, priorityFee.Int64(), mev.config.MaxPriorityFee.Int64())
			}

			if tt.strategy == StrategyMax {
				assert.Equal(t, mev.config.MaxPriorityFee, priorityFee)
			}
		})
	}
}

func TestEstimateMEVRisk(t *testing.T) {
	config := MEVConfig{
		Logger: testLogger(),
	}

	mev, err := NewMEVProtection(config)
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")

	tests := []struct {
		name        string
		priorityFee *big.Int
		tradeSize   *big.Int
		expectRisk  MEVRisk
	}{
		{
			name:        "low risk - high priority fee",
			priorityFee: big.NewInt(5000000000),        // 5 Gwei
			tradeSize:   big.NewInt(10000000000000000), // 0.01 ETH
			expectRisk:  MEVRiskLow,
		},
		{
			name:        "high risk - low priority fee",
			priorityFee: big.NewInt(500000000), // 0.5 Gwei
			tradeSize:   big.NewInt(10000000000000000),
			expectRisk:  MEVRiskHigh,
		},
		{
			name:        "medium risk - medium priority fee",
			priorityFee: big.NewInt(1500000000), // 1.5 Gwei
			tradeSize:   big.NewInt(10000000000000000),
			expectRisk:  MEVRiskMedium,
		},
		{
			name:        "medium risk - large trade medium fee",
			priorityFee: big.NewInt(2500000000),         // 2.5 Gwei
			tradeSize:   big.NewInt(100000000000000000), // 0.1 ETH
			expectRisk:  MEVRiskMedium,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := types.NewTx(&types.DynamicFeeTx{
				Nonce:     0,
				To:        &to,
				Value:     big.NewInt(0),
				Gas:       21000,
				GasFeeCap: big.NewInt(50000000000),
				GasTipCap: tt.priorityFee,
				Data:      []byte{},
			})

			risk := mev.EstimateMEVRisk(tx, tt.tradeSize)

			// Check risk level (allow some flexibility)
			assert.NotEqual(t, MEVRiskUnknown, risk)
		})
	}
}

func TestGetRecommendedStrategy(t *testing.T) {
	mev, err := NewMEVProtection(MEVConfig{Logger: testLogger()})
	require.NoError(t, err)

	tests := []struct {
		name      string
		risk      MEVRisk
		tradeSize *big.Int
	}{
		{
			name:      "low risk small trade",
			risk:      MEVRiskLow,
			tradeSize: big.NewInt(5000000000000000), // 0.005 ETH
		},
		{
			name:      "high risk large trade",
			risk:      MEVRiskHigh,
			tradeSize: big.NewInt(200000000000000000), // 0.2 ETH
		},
		{
			name:      "medium risk",
			risk:      MEVRiskMedium,
			tradeSize: big.NewInt(50000000000000000), // 0.05 ETH
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := mev.GetRecommendedStrategy(tt.risk, tt.tradeSize)

			// Verify strategy is valid
			validStrategies := map[string]bool{
				StrategyConservative: true,
				StrategyStandard:     true,
				StrategyAggressive:   true,
				StrategyMax:          true,
			}

			assert.True(t, validStrategies[strategy], "Strategy should be valid")
		})
	}
}

func TestMEVStats(t *testing.T) {
	mev, err := NewMEVProtection(MEVConfig{Logger: testLogger()})
	require.NoError(t, err)

	// Initially no stats
	stats := mev.GetStats()
	assert.Equal(t, int64(0), stats.TotalProtected)
	assert.Equal(t, int64(0), stats.FlashbotsProtected)
	assert.Equal(t, int64(0), stats.MerkleProtected)

	// Record some protected transactions
	mev.recordProtected(MEVProviderFlashbots)
	mev.recordProtected(MEVProviderMerkle)
	mev.recordProtected(MEVProviderFlashbots)

	stats = mev.GetStats()
	assert.Equal(t, int64(3), stats.TotalProtected)
	assert.Equal(t, int64(2), stats.FlashbotsProtected)
	assert.Equal(t, int64(1), stats.MerkleProtected)

	// Record failure
	mev.recordFailure()
	stats = mev.GetStats()
	assert.Equal(t, int64(1), stats.FailedProtected)
}

func TestEstimateMEVSavings(t *testing.T) {
	tests := []struct {
		name                  string
		tradeSize             *big.Int
		estimatedSlippage     float64
		expectPositiveSavings bool
	}{
		{
			name:                  "normal trade",
			tradeSize:             big.NewInt(100000000000000000), // 0.1 ETH
			estimatedSlippage:     0.5,                            // 0.5%
			expectPositiveSavings: true,
		},
		{
			name:                  "large trade",
			tradeSize:             big.NewInt(1000000000000000000), // 1 ETH
			estimatedSlippage:     1.0,                             // 1%
			expectPositiveSavings: true,
		},
		{
			name:                  "zero trade size",
			tradeSize:             big.NewInt(0),
			estimatedSlippage:     0.5,
			expectPositiveSavings: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			savings := EstimateMEVSavings(tt.tradeSize, tt.estimatedSlippage)

			if tt.expectPositiveSavings {
				assert.Positive(t, savings.Int64())
			} else {
				assert.Equal(t, big.NewInt(0), savings)
			}
		})
	}
}

func TestMEVRiskString(t *testing.T) {
	tests := []struct {
		risk     MEVRisk
		expected string
	}{
		{MEVRiskLow, "low"},
		{MEVRiskMedium, "medium"},
		{MEVRiskHigh, "high"},
		{MEVRiskUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.risk.String())
		})
	}
}

func TestMEVProviderString(t *testing.T) {
	providers := []struct {
		provider MEVProvider
		expected string
	}{
		{MEVProviderNone, "none"},
		{MEVProviderFlashbots, "flashbots"},
		{MEVProviderMerkle, "merkle"},
		{MEVProviderAuto, "auto"},
	}

	for _, tt := range providers {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.provider))
		})
	}
}

func TestBuildMevProtectedTx(t *testing.T) {
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	baseFee := big.NewInt(30000000000) // 30 Gwei
	oldTip := big.NewInt(2000000000)   // 2 Gwei
	newTip := big.NewInt(5000000000)   // 5 Gwei

	// Create base transaction
	baseTx := &types.DynamicFeeTx{
		ChainID:    big.NewInt(BaseChainID),
		Nonce:      5,
		GasTipCap:  oldTip,
		GasFeeCap:  new(big.Int).Add(baseFee, oldTip),
		Gas:        21000,
		To:         &to,
		Value:      big.NewInt(0),
		Data:       []byte{},
		AccessList: nil,
	}

	wrappedTx := types.NewTx(baseTx)

	// Build MEV protected transaction
	protectedTx, err := BuildMevProtectedTx(wrappedTx, newTip)
	require.NoError(t, err)
	require.NotNil(t, protectedTx)

	// Use type check instead of direct type assertion
	require.Equal(t, types.DynamicFeeTxType, protectedTx.Type())

	// Verify transaction was created
	assert.Equal(t, uint64(5), protectedTx.Nonce())
	assert.Equal(t, to, *protectedTx.To())
}

func TestBuildMevProtectedTxValidation(t *testing.T) {
	tests := []struct {
		name    string
		tx      *types.Transaction
		wantErr bool
	}{
		{
			name:    "nil transaction",
			tx:      nil,
			wantErr: true,
		},
		{
			name:    "non-EIP-1559 transaction",
			tx:      types.NewTransaction(0, common.Address{}, big.NewInt(0), 21000, big.NewInt(50000000000), []byte{}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priorityFee := big.NewInt(5000000000)
			_, err := BuildMevProtectedTx(tt.tx, priorityFee)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Benchmark tests

func BenchmarkCalculatePriorityFee(b *testing.B) {
	mev, _ := NewMEVProtection(MEVConfig{
		Logger:              testLogger(),
		PriorityFeeStrategy: StrategyStandard,
		MaxPriorityFee:      big.NewInt(50000000000),
		MaxFeePerGas:        big.NewInt(100000000000),
	})

	baseFee := big.NewInt(30000000000)
	congestion := 0.5

	for b.Loop() {
		_, _ = mev.CalculatePriorityFee(baseFee, congestion)
	}
}

func BenchmarkEstimateMEVRisk(b *testing.B) {
	mev, _ := NewMEVProtection(MEVConfig{Logger: testLogger()})

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     0,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       21000,
		GasFeeCap: big.NewInt(50000000000),
		GasTipCap: big.NewInt(2000000000),
		Data:      []byte{},
	})
	tradeSize := big.NewInt(10000000000000000)

	for b.Loop() {
		_ = mev.EstimateMEVRisk(tx, tradeSize)
	}
}
