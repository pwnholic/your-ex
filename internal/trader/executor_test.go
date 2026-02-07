package trader

import (
	"context"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestNewExecutor(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool:         &rpc.Pool{},
		Jupiter:         jupiterClient,
		Logger:          &logger,
		MaxRetries:      5,
		DefaultPriority: 2000,
		MaxPriorityFee:  50000,
		TxTimeout:       3 * time.Minute,
		ConfirmTimeout:  2 * time.Minute,
	}

	executor := NewExecutor(config)

	assert.NotNil(t, executor)
	assert.Equal(t, 5, executor.maxRetries)
	assert.Equal(t, uint64(2000), executor.defaultPriority)
	assert.Equal(t, uint64(50000), executor.maxPriorityFee)
	assert.Equal(t, 3*time.Minute, executor.txTimeout)
	assert.Equal(t, 2*time.Minute, executor.confirmTimeout)
}

func TestNewExecutor_Defaults(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	assert.NotNil(t, executor)
	assert.Equal(t, defaultMaxRetries, executor.maxRetries)
	assert.Equal(t, uint64(solanaPriorityFee), uint64(executor.defaultPriority))
	assert.Equal(t, uint64(maxPriorityFeeExecutor), uint64(executor.maxPriorityFee))
	assert.Equal(t, defaultTxTimeout, executor.txTimeout)
	assert.Equal(t, defaultConfirmTimeout, executor.confirmTimeout)
}

func TestExecutor_Stats(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	// Initial stats
	stats := executor.GetStats()
	assert.Equal(t, int64(0), stats.TotalExecutions)
	assert.Equal(t, int64(0), stats.SuccessfulSwaps)
	assert.Equal(t, int64(0), stats.FailedSwaps)

	// Record success
	executor.recordSuccess(5000)
	stats = executor.GetStats()
	assert.Equal(t, int64(1), stats.SuccessfulSwaps)
	assert.Equal(t, uint64(5000), stats.TotalFeePaid)
	assert.Equal(t, uint64(5000), stats.AverageFeePaid)

	// Record another success
	executor.recordSuccess(10000)
	stats = executor.GetStats()
	assert.Equal(t, int64(2), stats.SuccessfulSwaps)
	assert.Equal(t, uint64(15000), stats.TotalFeePaid)
	assert.Equal(t, uint64(7500), stats.AverageFeePaid)

	// Record failure
	executor.recordFailure()
	stats = executor.GetStats()
	assert.Equal(t, int64(1), stats.FailedSwaps)

	// Reset stats
	executor.ResetStats()
	stats = executor.GetStats()
	assert.Equal(t, int64(0), stats.TotalExecutions)
	assert.Equal(t, int64(0), stats.SuccessfulSwaps)
	assert.Equal(t, int64(0), stats.FailedSwaps)
	assert.Equal(t, uint64(0), stats.TotalFeePaid)
	assert.Equal(t, uint64(0), stats.AverageFeePaid)
}

func TestExecutor_EstimateSwapCost(t *testing.T) {
	// This test requires a mock Jupiter API, so we'll test the structure
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	params := SwapParams{
		InputMint:     solana.MustPublicKeyFromBase58(WSolAddress),
		OutputMint:    solana.MustPublicKeyFromBase58(USDCAddress),
		Amount:        1000000000, // 1 SOL
		SlippageBps:   300,        // 3%
		UserPublicKey: solana.MustPublicKeyFromBase58("11111111111111111111111111111111"),
	}

	// This will fail without real RPC/API, but we can test the call
	ctx := context.Background()
	estimate, err := executor.EstimateSwapCost(ctx, params)

	// Expected to fail without real connection
	assert.Error(t, err)
	assert.Nil(t, estimate)
}

func TestExecutor_RecordRetry(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	// Record retries
	executor.recordRetry()
	executor.recordRetry()
	executor.recordRetry()

	stats := executor.GetStats()
	assert.Equal(t, int64(3), stats.RetriedTx)
}

func TestExecutionResult(t *testing.T) {
	// Use a valid 64-character signature (proper base58 encoding)
	sig := solana.MustSignatureFromBase58(
		"3WyAg2hXuogheTyAoKHpE1BjKCuFfjRhYMvqfmkin8hKpGNWqhfS7xgGqWpEruJR9bmcpJ1pTUwvxVMnpxJdQ8fB",
	)

	result := &ExecutionResult{
		Signature:        sig,
		Slot:             123456,
		Confirmations:    1,
		FeePaid:          5000,
		PriorityFeePaid:  1000,
		Success:          true,
		Timestamp:        time.Now(),
		ConfirmationTime: time.Now().Add(2 * time.Second),
		Duration:         2 * time.Second,
	}

	assert.Equal(t, sig, result.Signature)
	assert.Equal(t, uint64(123456), result.Slot)
	assert.Equal(t, 1, result.Confirmations)
	assert.Equal(t, uint64(5000), result.FeePaid)
	assert.Equal(t, uint64(1000), result.PriorityFeePaid)
	assert.True(t, result.Success)
	assert.NotZero(t, result.Duration)
}

func TestSwapParams(t *testing.T) {
	params := SwapParams{
		InputMint:     solana.MustPublicKeyFromBase58(WSolAddress),
		OutputMint:    solana.MustPublicKeyFromBase58(USDCAddress),
		Amount:        1000000000,
		SlippageBps:   300,
		UserPublicKey: solana.MustPublicKeyFromBase58("11111111111111111111111111111111"),
		PriorityFee:   2000,
		WrapSol:       true,
	}

	assert.Equal(t, WSolAddress, params.InputMint.String())
	assert.Equal(t, USDCAddress, params.OutputMint.String())
	assert.Equal(t, uint64(1000000000), params.Amount)
	assert.Equal(t, 300, params.SlippageBps)
	assert.Equal(t, uint64(2000), params.PriorityFee)
	assert.True(t, params.WrapSol)
}

func TestSwapCostEstimate(t *testing.T) {
	estimate := &SwapCostEstimate{
		InputAmount:    1000000000,
		ExpectedOutput: "150000000",
		MinOutput:      "145500000",
		BaseFee:        5000,
		PriorityFee:    2000,
		TotalFee:       7000,
		PriceImpactPct: "0.5",
		RouteSteps:     2,
	}

	assert.Equal(t, uint64(1000000000), estimate.InputAmount)
	assert.Equal(t, "150000000", estimate.ExpectedOutput)
	assert.Equal(t, "145500000", estimate.MinOutput)
	assert.Equal(t, uint64(5000), estimate.BaseFee)
	assert.Equal(t, uint64(2000), estimate.PriorityFee)
	assert.Equal(t, uint64(7000), estimate.TotalFee)
	assert.Equal(t, "0.5", estimate.PriceImpactPct)
	assert.Equal(t, 2, estimate.RouteSteps)
}

func TestExecutor_Constants(t *testing.T) {
	assert.Equal(t, 2*time.Minute, defaultSolanaTxTimeout)
	assert.Equal(t, 60*time.Second, defaultConfirmTimeout)
	assert.Equal(t, 10, defaultSimulateSlots)
	assert.Equal(t, int(1000), solanaPriorityFee)
	assert.Equal(t, int(100000), maxPriorityFeeExecutor)
	assert.Equal(t, 3, defaultMaxRetries)
	assert.Equal(t, 300, defaultSlippageBps)
}

func TestExecutor_GetStats_ThreadSafe(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	// Concurrent access test
	done := make(chan bool)
	for range 10 {
		go func() {
			executor.recordSuccess(5000)
			executor.recordFailure()
			executor.recordRetry()
			executor.GetStats()
			done <- true
		}()
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}

	stats := executor.GetStats()
	assert.Equal(t, int64(10), stats.SuccessfulSwaps)
	assert.Equal(t, int64(10), stats.FailedSwaps)
	assert.Equal(t, int64(10), stats.RetriedTx)
}

func TestExecutor_BuildSwapTransaction(t *testing.T) {
	logger := zerolog.Nop()
	jupiterClient := NewJupiterClient(JupiterConfig{
		Logger: &logger,
	})

	config := ExecutorConfig{
		RPCPool: &rpc.Pool{},
		Jupiter: jupiterClient,
		Logger:  &logger,
	}

	executor := NewExecutor(config)

	params := SwapParams{
		InputMint:     solana.MustPublicKeyFromBase58(WSolAddress),
		OutputMint:    solana.MustPublicKeyFromBase58(USDCAddress),
		Amount:        1000000000,
		SlippageBps:   300,
		UserPublicKey: solana.MustPublicKeyFromBase58("11111111111111111111111111111111"),
	}

	ctx := context.Background()
	txBytes, swapResp, err := executor.BuildSwapTransaction(ctx, params)

	// Expected to fail without real connection
	assert.Error(t, err)
	assert.Nil(t, txBytes)
	assert.Nil(t, swapResp)
}
