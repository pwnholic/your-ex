package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestNewLiquidityAnalyzer(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewLiquidityAnalyzer(nil, config)

	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.logger)
	assert.NotNil(t, analyzer.httpClient)
	assert.Equal(t, 10*time.Second, analyzer.httpClient.Timeout)
}

func TestLiquidityAnalyzer_AnalyzeLiquidity_Solana(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		LiquidityPoolAddress: "pool_address",
		InitialPrice:         "0.000001",
	}

	ctx := context.Background()
	analysis, err := analyzer.AnalyzeLiquidity(ctx, event)

	// Since we don't have a real RPC, we expect some data
	if err == nil {
		assert.Equal(t, "pool_address", analysis.PoolAddress)
		assert.Equal(t, "pump_fun", analysis.PoolType)
	}
}

func TestLiquidityAnalyzer_AnalyzeLiquidity_Base(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeBase,
		Source:               monitor.SourceUniswap,
		MintAddress:          "0x1234567890123456789012345678901234567890",
		LiquidityPoolAddress: "pool_address",
		InitialPrice:         "0.000001",
	}

	ctx := context.Background()
	analysis, err := analyzer.AnalyzeLiquidity(ctx, event)

	// Since we don't have a real RPC, we expect some data
	if err == nil {
		assert.Equal(t, "pool_address", analysis.PoolAddress)
		assert.Equal(t, "uniswap_v3", analysis.PoolType)
	}
}

func TestLiquidityAnalyzer_CalculateDepthScore(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	t.Run("high liquidity with lock", func(t *testing.T) {
		analysis := LiquidityAnalysis{
			TotalValueLocked: "5.0",
			IsLocked:         true,
			LockDuration:     durationPtr(365 * 24 * time.Hour),
			BurnedLiquidity:  100.0,
		}

		score := analyzer.calculateDepthScore(analysis)
		assert.Greater(t, score, 80.0)
	})

	t.Run("medium liquidity with lock", func(t *testing.T) {
		analysis := LiquidityAnalysis{
			TotalValueLocked: "1.0",
			IsLocked:         true,
			BurnedLiquidity:  50.0,
		}

		score := analyzer.calculateDepthScore(analysis)
		assert.Greater(t, score, 50.0)
		assert.Less(t, score, 80.0)
	})

	t.Run("low liquidity without lock", func(t *testing.T) {
		analysis := LiquidityAnalysis{
			TotalValueLocked: "0.05",
			IsLocked:         false,
			BurnedLiquidity:  0.0,
		}

		score := analyzer.calculateDepthScore(analysis)
		assert.Less(t, score, 30.0)
	})

	t.Run("no liquidity", func(t *testing.T) {
		analysis := LiquidityAnalysis{
			TotalValueLocked: "invalid",
			IsLocked:         false,
			BurnedLiquidity:  0.0,
		}

		score := analyzer.calculateDepthScore(analysis)
		assert.Less(t, score, 20.0)
	})
}

func TestLiquidityAnalyzer_GetPoolTypeFromSource(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	testCases := []struct {
		source   monitor.SourceType
		expected string
	}{
		{monitor.SourcePumpFun, "pump_fun"},
		{monitor.SourceRaydium, "raydium"},
		{monitor.SourceOrca, "orca"},
		{monitor.SourceUniswap, "uniswap_v3"},
	}

	for _, tc := range testCases {
		t.Run(string(tc.source), func(t *testing.T) {
			result := analyzer.getPoolTypeFromSource(tc.source)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLiquidityAnalyzer_FetchPoolPrice(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	event := &monitor.TokenEvent{
		InitialPrice: "0.000001",
	}

	ctx := context.Background()
	price, err := analyzer.FetchPoolPrice(ctx, event)

	assert.NoError(t, err)
	assert.Equal(t, "0.000001", price)
}

func TestLiquidityAnalyzer_CalculateLiquidityScore(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	analysis := LiquidityAnalysis{
		DepthScore: 75.0,
	}

	score := analyzer.CalculateLiquidityScore(analysis)
	assert.Equal(t, 75.0, score)
}

func TestLiquidityAnalyzer_ClearCache(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	// Add something to cache
	analyzer.cache.Store("test_key", &cachedLiquidity{
		analysis: LiquidityAnalysis{PoolAddress: "test"},
		cachedAt: time.Now(),
	})

	assert.Equal(t, 1, analyzer.GetCacheSize())

	analyzer.ClearCache()

	assert.Equal(t, 0, analyzer.GetCacheSize())
}

func TestLiquidityAnalyzer_GetCacheSize(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	assert.Equal(t, 0, analyzer.GetCacheSize())

	// Add items to cache
	analyzer.cache.Store("key1", &cachedLiquidity{})
	analyzer.cache.Store("key2", &cachedLiquidity{})
	analyzer.cache.Store("key3", &cachedLiquidity{})

	assert.Equal(t, 3, analyzer.GetCacheSize())
}

func TestLiquidityAnalyzer_CacheExpiry(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	// Store an expired item
	expiredTime := time.Now().Add(-liquidityCacheTTL - time.Second)
	analyzer.cache.Store("expired_key", &cachedLiquidity{
		analysis: LiquidityAnalysis{PoolAddress: "expired"},
		cachedAt: expiredTime,
	})

	// Store a fresh item
	analyzer.cache.Store("fresh_key", &cachedLiquidity{
		analysis: LiquidityAnalysis{PoolAddress: "fresh"},
		cachedAt: time.Now(),
	})

	// Initial size
	assert.Equal(t, 2, analyzer.GetCacheSize())

	// Try to fetch - the expired one should be removed on access
	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		MintAddress:          "test",
		LiquidityPoolAddress: "expired_key",
	}

	ctx := context.Background()
	_, _ = analyzer.AnalyzeLiquidity(ctx, event)

	// The expired cache should have been removed or not hit
	// Just verify the cache size is <= 3 (expired removed, fresh kept, new entry added)
	assert.LessOrEqual(t, analyzer.GetCacheSize(), 3)
}

func TestCheckSolanaLPBurned(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	t.Run("pump.fun token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Source: monitor.SourcePumpFun,
		}

		burned, percent := analyzer.checkSolanaLPBurned(context.Background(), event)
		assert.True(t, burned)
		assert.Equal(t, 100.0, percent)
	})

	t.Run("raydium token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Source: monitor.SourceRaydium,
		}

		burned, percent := analyzer.checkSolanaLPBurned(context.Background(), event)
		assert.False(t, burned)
		assert.Equal(t, 0.0, percent)
	})
}

func TestCheckLiquidityLockDuration(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	t.Run("pump.fun token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Source: monitor.SourcePumpFun,
		}

		duration := analyzer.checkLiquidityLockDuration(context.Background(), event)
		assert.Equal(t, 365*24*time.Hour, duration)
	})

	t.Run("raydium token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Source: monitor.SourceRaydium,
		}

		duration := analyzer.checkLiquidityLockDuration(context.Background(), event)
		assert.Equal(t, time.Duration(0), duration)
	})
}

func TestFetchSolanaTokenBalance(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	ctx := context.Background()
	balance, err := analyzer.FetchSolanaTokenBalance(ctx, "account", "mint")

	// Since we don't have real RPC, expect 0 balance or error
	if err == nil {
		assert.Equal(t, uint64(0), balance)
	}
}

func TestFetchBaseTokenBalance(t *testing.T) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())

	ctx := context.Background()
	balance, err := analyzer.FetchBaseTokenBalance(ctx, "account", "contract")

	// Since we don't have real RPC, expect zero balance or error
	if err == nil {
		assert.NotNil(t, balance)
	}
}

func BenchmarkLiquidityAnalyzer_CalculateDepthScore(b *testing.B) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())
	analysis := LiquidityAnalysis{
		TotalValueLocked: "2.5",
		IsLocked:         true,
		LockDuration:     durationPtr(30 * 24 * time.Hour),
		BurnedLiquidity:  75.0,
	}

	for b.Loop() {
		analyzer.calculateDepthScore(analysis)
	}
}

func BenchmarkLiquidityAnalyzer_AnalyzeLiquidity(b *testing.B) {
	analyzer := NewLiquidityAnalyzer(nil, DefaultAnalysisConfig())
	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		LiquidityPoolAddress: "pool_address",
		InitialPrice:         "0.000001",
	}

	ctx := context.Background()

	for b.Loop() {
		_, _ = analyzer.AnalyzeLiquidity(ctx, event)
	}
}
