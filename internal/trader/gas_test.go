// Package trader provides trading functionality for the meme sniper bot.
package trader

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGasPriceClient is a mock implementation of GasPriceClient for testing.
type mockGasPriceClient struct {
	baseFee *big.Int
	headers []*types.Header
}

func (m *mockGasPriceClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	if m.baseFee != nil {
		return m.baseFee, nil
	}
	return big.NewInt(defaultBaseFee), nil
}

func (m *mockGasPriceClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if len(m.headers) > 0 {
		if number == nil {
			return m.headers[len(m.headers)-1], nil
		}
		idx := number.Int64()
		if idx >= 0 && idx < int64(len(m.headers)) {
			return m.headers[idx], nil
		}
	}
	return &types.Header{
		BaseFee: m.baseFee,
		Number:  big.NewInt(100),
	}, nil
}

func TestNewGasPriceTracker(t *testing.T) {
	tracker := NewGasPriceTracker(nil)

	assert.NotNil(t, tracker)
	assert.NotNil(t, tracker.logger)
	assert.NotNil(t, tracker.baseFeeHistory)
	assert.NotNil(t, tracker.tipHistory)
	assert.Equal(t, gasPriceHistoryLength, tracker.maxHistory)
	assert.Equal(t, defaultGasBaseFee, tracker.currentBaseFee.Int64())
}

func TestNewGasEstimator(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	config := EstimatorConfig{
		Logger:           testLogger(),
		RPCClient:        client,
		MaxPriorityFee:   big.NewInt(50000000000),
		MaxFeePerGas:     big.NewInt(100000000000),
		MinPriorityFee:   big.NewInt(100000000),
		EstimationBuffer: 1.3,
	}

	estimator, err := NewGasEstimator(config)
	require.NoError(t, err)
	require.NotNil(t, estimator)

	assert.NotNil(t, estimator.tracker)
	assert.NotNil(t, estimator.client)
	assert.NotNil(t, estimator.logger)
	assert.Equal(t, big.NewInt(50000000000), estimator.maxPriorityFee)
	assert.Equal(t, big.NewInt(100000000000), estimator.maxFeePerGas)
	assert.Equal(t, 1.3, estimator.estimationBuffer)
}

func TestNewGasEstimatorDefaults(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	config := EstimatorConfig{
		RPCClient: client,
	}

	estimator, err := NewGasEstimator(config)
	require.NoError(t, err)
	require.NotNil(t, estimator)

	// Check defaults
	assert.NotNil(t, estimator.maxPriorityFee)
	assert.NotNil(t, estimator.maxFeePerGas)
	assert.NotNil(t, estimator.minPriorityFee)
	assert.Equal(t, defaultGasEstimationBuffer, estimator.estimationBuffer)
}

func TestUpdatePrices(t *testing.T) {
	baseFee := big.NewInt(30000000000) // 30 Gwei
	client := &mockGasPriceClient{baseFee: baseFee}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	// Verify prices were updated
	tracker := estimator.tracker
	assert.Equal(t, baseFee.Int64(), tracker.currentBaseFee.Int64())
	assert.NotNil(t, tracker.currentTip)
	assert.NotNil(t, tracker.currentMaxFee)
	assert.NotZero(t, tracker.lastUpdate)
}

func TestUpdatePricesHistory(t *testing.T) {
	baseFee := big.NewInt(30000000000)
	client := &mockGasPriceClient{baseFee: baseFee}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Update multiple times
	for range 5 {
		err = estimator.UpdatePrices(context.Background())
		require.NoError(t, err)
	}

	// Check history
	tracker := estimator.tracker
	assert.Len(t, tracker.baseFeeHistory, 5)
	assert.Len(t, tracker.tipHistory, 5)
}

func TestGasEstimateGas(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	tests := []struct {
		name          string
		callMsg       ethereum.CallMsg
		expectedRange [2]uint64 // min, max
	}{
		{
			name: "simple transfer",
			callMsg: ethereum.CallMsg{
				To:   addrPtr(common.HexToAddress("0x1234567890123456789012345678901234567890")),
				Data: []byte{},
			},
			expectedRange: [2]uint64{21000, 30000},
		},
		{
			name: "contract call",
			callMsg: ethereum.CallMsg{
				To:   addrPtr(common.HexToAddress("0x1234567890123456789012345678901234567890")),
				Data: make([]byte, 500),
			},
			expectedRange: [2]uint64{50000, 200000},
		},
		{
			name: "complex call",
			callMsg: ethereum.CallMsg{
				To:   addrPtr(common.HexToAddress("0x1234567890123456789012345678901234567890")),
				Data: make([]byte, 2000),
			},
			expectedRange: [2]uint64{200000, 400000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gas, err := estimator.EstimateGas(context.Background(), tt.callMsg)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, gas, tt.expectedRange[0])
			assert.LessOrEqual(t, gas, tt.expectedRange[1])
		})
	}
}

func TestGetSuggestedGasPrice(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	baseFee, priorityFee, maxFee := estimator.GetSuggestedGasPrice()

	assert.NotNil(t, baseFee)
	assert.NotNil(t, priorityFee)
	assert.NotNil(t, maxFee)
	assert.Positive(t, baseFee.Int64())
	assert.Positive(t, priorityFee.Int64())
	assert.GreaterOrEqual(t, maxFee.Int64(), baseFee.Int64())
}

func TestGetSuggestedGasPriceForCongestion(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:         testLogger(),
		RPCClient:      client,
		MaxPriorityFee: big.NewInt(50000000000),
		MaxFeePerGas:   big.NewInt(100000000000),
	})
	require.NoError(t, err)

	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	urgencies := []GasUrgency{
		UrgencyLow,
		UrgencyMedium,
		UrgencyHigh,
		UrgencyCritical,
	}

	for _, urgency := range urgencies {
		t.Run(urgency.String(), func(t *testing.T) {
			baseFee, priorityFee, maxFee := estimator.GetSuggestedGasPriceForCongestion(urgency)

			assert.NotNil(t, baseFee)
			assert.NotNil(t, priorityFee)
			assert.NotNil(t, maxFee)

			// Verify fees are within bounds
			assert.Positive(t, priorityFee.Int64())
			assert.LessOrEqual(t, priorityFee.Int64(), estimator.maxPriorityFee.Int64())
			assert.LessOrEqual(t, maxFee.Int64(), estimator.maxFeePerGas.Int64())
		})
	}
}

func TestEstimateTransactionFee(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	tests := []struct {
		name         string
		gasLimit     uint64
		maxFeePerGas *big.Int
	}{
		{
			name:         "standard transaction",
			gasLimit:     21000,
			maxFeePerGas: big.NewInt(50000000000), // 50 Gwei
		},
		{
			name:         "large transaction",
			gasLimit:     300000,
			maxFeePerGas: big.NewInt(100000000000), // 100 Gwei
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee := estimator.EstimateTransactionFee(tt.gasLimit, tt.maxFeePerGas)
			assert.NotNil(t, fee)
			assert.Positive(t, fee.Int64())

			// Verify calculation: gasLimit * maxFeePerGas
			expected := new(big.Int).Mul(big.NewInt(int64(tt.gasLimit)), tt.maxFeePerGas)
			assert.Equal(t, expected, fee)
		})
	}
}

func TestOptimizeGasForSpeedup(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:         testLogger(),
		RPCClient:      client,
		MaxPriorityFee: big.NewInt(50000000000),
		MaxFeePerGas:   big.NewInt(100000000000),
	})
	require.NoError(t, err)

	originalMaxFee := big.NewInt(50000000000) // 50 Gwei
	originalTip := big.NewInt(2000000000)     // 2 Gwei

	newMaxFee, newTip := estimator.OptimizeGasForSpeedup(originalMaxFee, originalTip)

	// Verify fees increased
	assert.Greater(t, newMaxFee.Int64(), originalMaxFee.Int64())
	assert.Greater(t, newTip.Int64(), originalTip.Int64())

	// Verify within bounds
	assert.LessOrEqual(t, newTip.Int64(), estimator.maxPriorityFee.Int64())
	assert.LessOrEqual(t, newMaxFee.Int64(), estimator.maxFeePerGas.Int64())
}

func TestParseGasUrgency(t *testing.T) {
	tests := []struct {
		input    string
		expected GasUrgency
	}{
		{"low", UrgencyLow},
		{"medium", UrgencyMedium},
		{"high", UrgencyHigh},
		{"critical", UrgencyCritical},
		{"unknown", UrgencyMedium}, // defaults to medium
		{"", UrgencyMedium},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseGasUrgency(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGasUrgencyString(t *testing.T) {
	tests := []struct {
		urgency  GasUrgency
		expected string
	}{
		{UrgencyLow, "low"},
		{UrgencyMedium, "medium"},
		{UrgencyHigh, "high"},
		{UrgencyCritical, "critical"},
		{GasUrgency(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.urgency.String())
		})
	}
}

func TestGetAverageGasPrice(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Before any updates, should return default
	avg := estimator.GetAverageGasPrice()
	assert.Equal(t, defaultBaseFee, avg.Int64())

	// Update prices a few times
	for range 5 {
		err = estimator.UpdatePrices(context.Background())
		require.NoError(t, err)
	}

	avg = estimator.GetAverageGasPrice()
	assert.Positive(t, avg.Int64())
}

func TestGetGasPriceTrend(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Before any updates, trend should be 0
	trend := estimator.GetGasPriceTrend()
	assert.Equal(t, float64(0), trend)

	// Update prices
	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	trend = estimator.GetGasPriceTrend()
	// With stable prices, trend should be near 0
	assert.GreaterOrEqual(t, trend, -1.0)
	assert.LessOrEqual(t, trend, 1.0)
}

func TestShouldWaitForLowerGas(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	tests := []struct {
		name         string
		threshold    *big.Int
		maxWait      time.Duration
		expectResult bool
	}{
		{
			name:         "current below threshold",
			threshold:    big.NewInt(100000000000), // 100 Gwei
			maxWait:      time.Minute,
			expectResult: false,
		},
		{
			name:         "very short max wait",
			threshold:    big.NewInt(10000000000), // 10 Gwei
			maxWait:      time.Second,
			expectResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := estimator.ShouldWaitForLowerGas(tt.threshold, tt.maxWait)
			assert.Equal(t, tt.expectResult, result)
		})
	}
}

func TestGetCongestionLevel(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Before update, should be default
	congestion := estimator.GetCongestionLevel()
	assert.GreaterOrEqual(t, congestion, 0.0)
	assert.LessOrEqual(t, congestion, 1.0)

	// Update prices
	err = estimator.UpdatePrices(context.Background())
	require.NoError(t, err)

	congestion = estimator.GetCongestionLevel()
	assert.GreaterOrEqual(t, congestion, 0.0)
	assert.LessOrEqual(t, congestion, 1.0)
}

func TestGasEstimateGasForSwap(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:           testLogger(),
		RPCClient:        client,
		EstimationBuffer: 1.2,
	})
	require.NoError(t, err)

	// Simple swap
	gas := estimator.EstimateGasForSwap(false)
	assert.GreaterOrEqual(t, gas, uint64(180000))
	assert.LessOrEqual(t, gas, uint64(300000))

	// Multi-hop swap
	gas = estimator.EstimateGasForSwap(true)
	assert.GreaterOrEqual(t, gas, uint64(230000))
	assert.LessOrEqual(t, gas, uint64(400000))
}

func TestGasEstimateGasForApproval(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:           testLogger(),
		RPCClient:        client,
		EstimationBuffer: 1.2,
	})
	require.NoError(t, err)

	gas := estimator.EstimateGasForApproval()
	assert.GreaterOrEqual(t, gas, uint64(50000))
	assert.LessOrEqual(t, gas, uint64(80000))
}

func TestGasEstimateGasForTransfer(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	gas := estimator.EstimateGasForTransfer()
	assert.Equal(t, uint64(21000), gas) // Standard ETH transfer
}

func TestGasEstimateGasForTokenTransfer(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:           testLogger(),
		RPCClient:        client,
		EstimationBuffer: 1.2,
	})
	require.NoError(t, err)

	gas := estimator.EstimateGasForTokenTransfer()
	assert.GreaterOrEqual(t, gas, uint64(60000))
	assert.LessOrEqual(t, gas, uint64(100000))
}

func TestGetPriceHistory(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Initially empty
	baseFees, tips := estimator.GetPriceHistory()
	assert.Empty(t, baseFees)
	assert.Empty(t, tips)

	// Add some history
	for range 5 {
		err = estimator.UpdatePrices(context.Background())
		require.NoError(t, err)
	}

	baseFees, tips = estimator.GetPriceHistory()
	assert.Len(t, baseFees, 5)
	assert.Len(t, tips, 5)
}

func TestRecordActualGas(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	// Record some estimates
	estimator.RecordActualGas(100000, 95000)  // Underestimate
	estimator.RecordActualGas(100000, 110000) // Overestimate
	estimator.RecordActualGas(100000, 100000) // Exact

	stats := estimator.GetStats()
	assert.Equal(t, int64(3), stats.TotalEstimations)
	assert.Equal(t, int64(1), stats.TotalUnderEstimates)
	assert.Equal(t, int64(1), stats.TotalOverEstimates)
}

func TestWeiToGwei(t *testing.T) {
	tests := []struct {
		name     string
		wei      *big.Int
		expected float64
	}{
		{
			name:     "1 Gwei",
			wei:      big.NewInt(1000000000),
			expected: 1.0,
		},
		{
			name:     "50 Gwei",
			wei:      big.NewInt(50000000000),
			expected: 50.0,
		},
		{
			name:     "0.5 Gwei",
			wei:      big.NewInt(500000000),
			expected: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gwei := WeiToGwei(tt.wei)
			gweiFloat, _ := gwei.Float64()
			assert.InDelta(t, tt.expected, gweiFloat, 0.001)
		})
	}
}

func TestGweiToWei(t *testing.T) {
	tests := []struct {
		name     string
		gwei     float64
		expected *big.Int
	}{
		{
			name:     "1 Gwei",
			gwei:     1.0,
			expected: big.NewInt(1000000000),
		},
		{
			name:     "50 Gwei",
			gwei:     50.0,
			expected: big.NewInt(50000000000),
		},
		{
			name:     "0.5 Gwei",
			gwei:     0.5,
			expected: big.NewInt(500000000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wei := GweiToWei(tt.gwei)
			assert.Equal(t, tt.expected, wei)
		})
	}
}

func TestGetStats(t *testing.T) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, err := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})
	require.NoError(t, err)

	stats := estimator.GetStats()
	assert.Equal(t, int64(0), stats.TotalEstimations)
	assert.Equal(t, uint64(0), stats.AverageGasUsed)
}

// Helper functions

func addrPtr(a common.Address) *common.Address {
	return &a
}

// Benchmark tests

func BenchmarkEstimateGas(b *testing.B) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, _ := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})

	callMsg := ethereum.CallMsg{
		To:   addrPtr(common.HexToAddress("0x1234567890123456789012345678901234567890")),
		Data: make([]byte, 200),
	}

	for b.Loop() {
		_, _ = estimator.EstimateGas(context.Background(), callMsg)
	}
}

func BenchmarkGetSuggestedGasPrice(b *testing.B) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, _ := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})

	_ = estimator.UpdatePrices(context.Background())

	for b.Loop() {
		_, _, _ = estimator.GetSuggestedGasPrice()
	}
}

func BenchmarkUpdatePrices(b *testing.B) {
	client := &mockGasPriceClient{baseFee: big.NewInt(30000000000)}

	estimator, _ := NewGasEstimator(EstimatorConfig{
		Logger:    testLogger(),
		RPCClient: client,
	})

	for b.Loop() {
		_ = estimator.UpdatePrices(context.Background())
	}
}
