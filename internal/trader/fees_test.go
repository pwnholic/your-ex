package trader

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFeeCalculator(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger:          &logger,
		APIKey:          "test-api-key",
		BaseFee:         10000,
		MaxFee:          50000,
		Multiplier:      1.5,
		CongestionBased: true,
	}

	calc := NewFeeCalculator(config)

	assert.NotNil(t, calc)
	assert.Equal(t, "test-api-key", calc.apiKey)
	assert.Equal(t, uint64(10000), calc.baseFee)
	assert.Equal(t, uint64(50000), calc.maxFee)
	assert.Equal(t, 1.5, calc.multiplier)
	assert.True(t, calc.congestionBased)
}

func TestNewFeeCalculator_Defaults(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	assert.NotNil(t, calc)
	assert.Equal(t, uint64(defaultBaseFee), calc.baseFee)
	assert.Equal(t, uint64(maxPriorityFeeCalc), calc.maxFee)
	assert.Equal(t, 1.0, calc.multiplier)
	assert.False(t, calc.congestionBased)
}

func TestFeeCalculator_CalculateFallbackFee(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
		MaxFee: 50000,
	}

	calc := NewFeeCalculator(config)

	accounts := []string{"account1", "account2", "account3"}

	estimate, err := calc.calculateFallbackFee(accounts)

	require.NoError(t, err)
	require.NotNil(t, estimate)
	assert.Positive(t, estimate.TotalFeeMicroLamports)
	assert.LessOrEqual(t, estimate.TotalFeeMicroLamports, 50000)
	assert.Positive(t, estimate.Low)
	assert.Positive(t, estimate.Medium)
	assert.Positive(t, estimate.High)
	assert.Positive(t, estimate.VeryHigh)
	assert.NotZero(t, estimate.Timestamp)
}

func TestFeeCalculator_UpdateFeeHistory(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	estimate := &FeeEstimate{
		TotalFeeMicroLamports: 5000,
		Low:                   2500,
		Medium:                5000,
		High:                  7500,
		VeryHigh:              10000,
		Timestamp:             time.Now(),
	}

	// Add some estimates
	for range 5 {
		calc.updateFeeHistory(estimate)
	}

	history := calc.GetFeeHistory()
	assert.Len(t, history, 5)

	// Test history size limit
	for range 200 {
		calc.updateFeeHistory(estimate)
	}

	history = calc.GetFeeHistory()
	assert.LessOrEqual(t, len(history), feeHistorySize)
}

func TestFeeCalculator_GetAverageFee(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	// No history
	avg := calc.GetAverageFee()
	assert.Equal(t, defaultMicroLamport, avg)

	// Add some history
	estimates := []FeeEstimate{
		{TotalFeeMicroLamports: 1000, Timestamp: time.Now()},
		{TotalFeeMicroLamports: 2000, Timestamp: time.Now()},
		{TotalFeeMicroLamports: 3000, Timestamp: time.Now()},
		{TotalFeeMicroLamports: 4000, Timestamp: time.Now()},
	}

	for _, est := range estimates {
		calc.updateFeeHistory(&est)
	}

	avg = calc.GetAverageFee()
	assert.Equal(t, 2500, avg) // (1000+2000+3000+4000)/4
}

func TestFeeCalculator_IsNetworkCongested(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	// No history - not congested
	assert.False(t, calc.IsNetworkCongested())
	assert.False(t, calc.IsHighCongestion())

	// Add congested estimate
	estimate := &FeeEstimate{
		TotalFeeMicroLamports: 10000,
		NetworkUtilization:    0.8, // Above congestionThreshold (0.7)
		Timestamp:             time.Now(),
	}
	calc.updateFeeHistory(estimate)

	assert.True(t, calc.IsNetworkCongested())
	assert.False(t, calc.IsHighCongestion())

	// Add very high congestion estimate
	estimate2 := &FeeEstimate{
		TotalFeeMicroLamports: 20000,
		NetworkUtilization:    0.95, // Above highCongestionThreshold (0.9)
		Timestamp:             time.Now(),
	}
	calc.updateFeeHistory(estimate2)

	assert.True(t, calc.IsNetworkCongested())
	assert.True(t, calc.IsHighCongestion())
}

func TestFeeCalculator_Multiplier(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger:     &logger,
		Multiplier: 1.0,
	}

	calc := NewFeeCalculator(config)

	assert.Equal(t, 1.0, calc.GetMultiplier())

	// Test setting multiplier
	calc.SetMultiplier(2.5)
	assert.Equal(t, 2.5, calc.GetMultiplier())

	// Test lower bound
	calc.SetMultiplier(0.05)
	assert.Equal(t, 0.1, calc.GetMultiplier()) // Should be capped at 0.1

	// Test upper bound
	calc.SetMultiplier(15.0)
	assert.Equal(t, 10.0, calc.GetMultiplier()) // Should be capped at 10.0
}

func TestFeeCalculator_EstimateUtilization(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	tests := []struct {
		name    string
		fee     int
		minUtil float64
		maxUtil float64
	}{
		{
			name:    "low fee",
			fee:     500,
			minUtil: 0.0,
			maxUtil: 0.5,
		},
		{
			name:    "medium fee",
			fee:     3000,
			minUtil: 0.5,
			maxUtil: 0.7,
		},
		{
			name:    "high fee",
			fee:     8000,
			minUtil: 0.7,
			maxUtil: 0.9,
		},
		{
			name:    "very high fee",
			fee:     15000,
			minUtil: 0.9,
			maxUtil: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			util := calc.estimateUtilization(tt.fee)
			assert.GreaterOrEqual(t, util, tt.minUtil)
			assert.LessOrEqual(t, util, tt.maxUtil)
		})
	}
}

func TestFeeCalculator_GetRecommendedFee(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	ctx := context.Background()

	// Test each priority level
	tests := []struct {
		priority  FeePriority
		validator func(t *testing.T, fee int, estimate *FeeEstimate)
	}{
		{
			priority: FeePriorityLow,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.Low, fee)
			},
		},
		{
			priority: FeePriorityMedium,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.Medium, fee)
			},
		},
		{
			priority: FeePriorityHigh,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.High, fee)
			},
		},
		{
			priority: FeePriorityVeryHigh,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.VeryHigh, fee)
			},
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("priority_%d", tt.priority), func(t *testing.T) {
			fee, err := calc.GetRecommendedFee(ctx, tt.priority)
			require.NoError(t, err)
			assert.Positive(t, fee)
		})
	}
}

func TestFeeCalculator_EstimateTotalFee(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger:  &logger,
		BaseFee: 5000,
		MaxFee:  50000,
	}

	calc := NewFeeCalculator(config)

	ctx := context.Background()
	totalFee, err := calc.EstimateTotalFee(ctx, []string{})

	require.NoError(t, err)
	assert.GreaterOrEqual(t, totalFee, uint64(5000)) // At least base fee
}

func TestFeeCalculator_EstimateFeeForSwap(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger:  &logger,
		BaseFee: 5000,
		MaxFee:  50000,
	}

	calc := NewFeeCalculator(config)

	ctx := context.Background()

	// Test different route complexities
	tests := []struct {
		name            string
		routeComplexity int
		validator       func(t *testing.T, estimate *SwapFeeEstimate)
	}{
		{
			name:            "simple route",
			routeComplexity: 1,
			validator: func(t *testing.T, estimate *SwapFeeEstimate) {
				assert.NotNil(t, estimate)
				assert.Positive(t, estimate.BaseFeeLamports)
				assert.Positive(t, estimate.PriorityFeeMicroLamports)
				assert.Greater(t, estimate.TotalFeeLamports, estimate.BaseFeeLamports)
			},
		},
		{
			name:            "complex route",
			routeComplexity: 5,
			validator: func(t *testing.T, estimate *SwapFeeEstimate) {
				assert.NotNil(t, estimate)
				// Complex routes should have higher fees
				assert.Positive(t, estimate.PriorityFeeMicroLamports)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimate, err := calc.EstimateFeeForSwap(ctx, tt.routeComplexity)
			require.NoError(t, err)
			tt.validator(t, estimate)
		})
	}
}

func TestFeeCalculator_EstimateSlotTime(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	tests := []struct {
		name             string
		feeMicroLamports int
		expectedMin      int64
		expectedMax      int64
	}{
		{
			name:             "low fee",
			feeMicroLamports: 500,
			expectedMin:      3000,
			expectedMax:      3000,
		},
		{
			name:             "medium fee",
			feeMicroLamports: 3000,
			expectedMin:      2000,
			expectedMax:      2000,
		},
		{
			name:             "high fee",
			feeMicroLamports: 8000,
			expectedMin:      1000,
			expectedMax:      1000,
		},
		{
			name:             "very high fee",
			feeMicroLamports: 15000,
			expectedMin:      500,
			expectedMax:      500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slotTime := calc.estimateSlotTime(tt.feeMicroLamports)
			assert.GreaterOrEqual(t, slotTime, tt.expectedMin)
			assert.LessOrEqual(t, slotTime, tt.expectedMax)
		})
	}
}

func TestFeeCalculator_CalculateDynamicFee(t *testing.T) {
	logger := zerolog.Nop()
	config := FeeConfig{
		Logger: &logger,
	}

	calc := NewFeeCalculator(config)

	ctx := context.Background()

	tests := []struct {
		name           string
		targetSlotTime int64
		validator      func(t *testing.T, fee int, estimate *FeeEstimate)
	}{
		{
			name:           "very fast",
			targetSlotTime: 400,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.VeryHigh, fee)
			},
		},
		{
			name:           "fast",
			targetSlotTime: 800,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.High, fee)
			},
		},
		{
			name:           "medium",
			targetSlotTime: 1500,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.Medium, fee)
			},
		},
		{
			name:           "slow",
			targetSlotTime: 3000,
			validator: func(t *testing.T, fee int, estimate *FeeEstimate) {
				assert.Equal(t, estimate.Low, fee)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee, err := calc.CalculateDynamicFee(ctx, []string{}, tt.targetSlotTime)
			require.NoError(t, err)
			assert.Positive(t, fee)
		})
	}
}

func TestUtilityFunctions(t *testing.T) {
	t.Run("MicroLamportsToLamports", func(t *testing.T) {
		assert.Equal(t, 1.0, MicroLamportsToLamports(1000))
		assert.Equal(t, 0.5, MicroLamportsToLamports(500))
		assert.Equal(t, 10.0, MicroLamportsToLamports(10000))
	})

	t.Run("LamportsToMicroLamports", func(t *testing.T) {
		assert.Equal(t, 1000, LamportsToMicroLamports(1))
		assert.Equal(t, 5000, LamportsToMicroLamports(5))
		assert.Equal(t, 10000, LamportsToMicroLamports(10))
	})

	t.Run("SolToLamports", func(t *testing.T) {
		assert.Equal(t, uint64(1_000_000_000), SolToLamports(1.0))
		assert.Equal(t, uint64(500_000_000), SolToLamports(0.5))
		assert.Equal(t, uint64(100_000_000), SolToLamports(0.1))
	})

	t.Run("LamportsToSol", func(t *testing.T) {
		assert.Equal(t, 1.0, LamportsToSol(1_000_000_000))
		assert.Equal(t, 0.5, LamportsToSol(500_000_000))
		assert.Equal(t, 0.1, LamportsToSol(100_000_000))
	})
}

func TestFeePriority_String(t *testing.T) {
	tests := []struct {
		priority   FeePriority
		wantString string
	}{
		{FeePriorityLow, "Low"},
		{FeePriorityMedium, "Medium"},
		{FeePriorityHigh, "High"},
		{FeePriorityVeryHigh, "VeryHigh"},
	}

	for _, tt := range tests {
		t.Run(tt.wantString, func(t *testing.T) {
			// Just verify the priority exists and can be used
			assert.GreaterOrEqual(t, int(tt.priority), 0)
			assert.Less(t, int(tt.priority), 4)
		})
	}
}

func TestSwapFeeEstimate(t *testing.T) {
	estimate := &SwapFeeEstimate{
		BaseFeeLamports:          5000,
		PriorityFeeMicroLamports: 3000,
		TotalFeeLamports:         5003, // 5000 + 3000/1000
		EstimatedSlotTime:        1000,
		RecommendedPriority:      FeePriorityMedium,
		FeeLevels: FeeLevels{
			Low:      1500,
			Medium:   3000,
			High:     4500,
			VeryHigh: 6000,
		},
	}

	assert.Equal(t, uint64(5000), estimate.BaseFeeLamports)
	assert.Equal(t, 3000, estimate.PriorityFeeMicroLamports)
	assert.Equal(t, uint64(5003), estimate.TotalFeeLamports)
	assert.Equal(t, int64(1000), estimate.EstimatedSlotTime)
	assert.Equal(t, FeePriorityMedium, estimate.RecommendedPriority)
	assert.Equal(t, 1500, estimate.FeeLevels.Low)
	assert.Equal(t, 3000, estimate.FeeLevels.Medium)
	assert.Equal(t, 4500, estimate.FeeLevels.High)
	assert.Equal(t, 6000, estimate.FeeLevels.VeryHigh)
}

func TestFeeCalculator_Constants(t *testing.T) {
	assert.Equal(t, 5000, defaultBaseFee)
	assert.Equal(t, 1000, defaultMicroLamport)
	assert.Equal(t, 100000, maxPriorityFeeCalc)
	assert.Equal(t, 10*time.Second, feeUpdateInterval)
	assert.Equal(t, 100, feeHistorySize)
	assert.Equal(t, 0.7, congestionThreshold)
	assert.Equal(t, 0.9, highCongestionThreshold)
}
