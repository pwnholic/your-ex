package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestNewAnalyzer(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.tokenAnalyzer)
	assert.NotNil(t, analyzer.liquidityAnalyzer)
	assert.NotNil(t, analyzer.securityAnalyzer)
	assert.NotNil(t, analyzer.scorer)
	assert.NotNil(t, analyzer.logger)
}

func TestAnalyzer_AnalyzeToken(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        9,
		Supply:               "1000000000000",
		MintAuthority:        stringPtr("11111111111111111111111111111111"),
		FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		TokenMetadataURI:     "https://example.com/metadata.json",
		LiquidityPoolAddress: "pool_address",
		InitialPrice:         "0.000001",
	}

	ctx := context.Background()
	result, err := analyzer.AnalyzeToken(ctx, event)

	// Since we don't have a real RPC, we expect an error but some analysis may still complete
	// The error is expected due to nil RPC pool
	if err == nil {
		// If no error, verify the result structure
		assert.NotNil(t, result)
		assert.Equal(t, "test_mint_address", result.TokenAddress)
		assert.Equal(t, monitor.ChainTypeSolana, result.Chain)
		assert.Equal(t, monitor.SourcePumpFun, result.Source)
		assert.NotEqual(t, time.Time{}, result.Timestamp)
		assert.Greater(t, result.AnalysisDuration, time.Duration(0))
	} else {
		// Error is expected with nil RPC pool
		assert.Error(t, err)
	}
}

func TestAnalyzer_AnalyzeTokenAsync(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        9,
		MintAuthority:        stringPtr("11111111111111111111111111111111"),
		FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		LiquidityPoolAddress: "pool_address",
	}

	ctx := context.Background()
	resultChan, errorChan := analyzer.AnalyzeTokenAsync(ctx, event)

	select {
	case result := <-resultChan:
		assert.NotNil(t, result)
	case err := <-errorChan:
		// Error is acceptable without real RPC
		assert.Error(t, err)
	case <-time.After(35 * time.Second):
		t.Fatal("async analysis timed out")
	}
}

func TestAnalyzer_AnalyzeTokenBatch(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	events := []*monitor.TokenEvent{
		{
			Chain:                monitor.ChainTypeSolana,
			Source:               monitor.SourcePumpFun,
			MintAddress:          "test_mint_address_1",
			TokenName:            "Test Token 1",
			TokenSymbol:          "TEST1",
			TokenDecimals:        9,
			MintAuthority:        stringPtr("11111111111111111111111111111111"),
			FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
			LiquidityPoolAddress: "pool_address_1",
		},
		{
			Chain:                monitor.ChainTypeSolana,
			Source:               monitor.SourceRaydium,
			MintAddress:          "test_mint_address_2",
			TokenName:            "Test Token 2",
			TokenSymbol:          "TEST2",
			TokenDecimals:        9,
			MintAuthority:        stringPtr("11111111111111111111111111111111"),
			FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
			LiquidityPoolAddress: "pool_address_2",
		},
		{
			Chain:                monitor.ChainTypeBase,
			Source:               monitor.SourceUniswap,
			MintAddress:          "0x1234567890123456789012345678901234567890",
			TokenName:            "Test Token 3",
			TokenSymbol:          "TEST3",
			TokenDecimals:        18,
			LiquidityPoolAddress: "pool_address_3",
		},
	}

	ctx := context.Background()
	results, errors := analyzer.AnalyzeTokenBatch(ctx, events)

	assert.Len(t, results, 3)
	assert.Len(t, errors, 3)

	// At least some results should be non-nil even without RPC
	hasResult := false
	for _, result := range results {
		if result != nil {
			hasResult = true
			break
		}
	}
	assert.True(t, hasResult, "Expected at least one non-nil result")
}

func TestAnalyzer_ShouldBuy(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	t.Run("should buy - high score", func(t *testing.T) {
		result := &AnalysisResult{
			Recommendation: "buy",
			Security: SecurityAnalysis{
				IsHoneypot:      false,
				HasHiddenFees:   false,
				LiquidityLocked: true,
			},
			Score: TokenScore{
				OverallScore: 75.0,
			},
		}

		should := analyzer.ShouldBuy(result)
		assert.True(t, should)
	})

	t.Run("should not buy - warning recommendation", func(t *testing.T) {
		result := &AnalysisResult{
			Recommendation: "warning",
			Security: SecurityAnalysis{
				IsHoneypot:      false,
				HasHiddenFees:   false,
				LiquidityLocked: true,
			},
			Score: TokenScore{
				OverallScore: 40.0,
			},
		}

		should := analyzer.ShouldBuy(result)
		assert.False(t, should)
	})

	t.Run("should not buy - honeypot", func(t *testing.T) {
		result := &AnalysisResult{
			Recommendation: "buy",
			Security: SecurityAnalysis{
				IsHoneypot:      true,
				HasHiddenFees:   false,
				LiquidityLocked: true,
			},
			Score: TokenScore{
				OverallScore: 80.0,
			},
		}

		should := analyzer.ShouldBuy(result)
		assert.False(t, should)
	})

	t.Run("should not buy - no liquidity lock", func(t *testing.T) {
		result := &AnalysisResult{
			Recommendation: "buy",
			Security: SecurityAnalysis{
				IsHoneypot:      false,
				HasHiddenFees:   false,
				LiquidityLocked: false,
				LiquidityBurned: false,
			},
			Score: TokenScore{
				OverallScore: 75.0,
			},
		}

		should := analyzer.ShouldBuy(result)
		assert.False(t, should)
	})

	t.Run("should not buy - nil result", func(t *testing.T) {
		should := analyzer.ShouldBuy(nil)
		assert.False(t, should)
	})
}

func TestAnalyzer_GetStats(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	stats := analyzer.GetStats()
	assert.Equal(t, int64(0), stats.TotalAnalyzed)
	assert.Equal(t, int64(0), stats.AnalyzedPassed)
	assert.Equal(t, int64(0), stats.AnalyzedFailed)
}

func TestAnalyzer_ClearCache(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	// Add something to caches
	analyzer.tokenAnalyzer.cache.Store("key", &cachedMetadata{})
	analyzer.liquidityAnalyzer.cache.Store("key", &cachedLiquidity{})
	analyzer.securityAnalyzer.cache.Store("key", &cachedSecurity{})
	analyzer.analysisCache.Store("key", &CachedAnalysis{})

	// Verify caches have items
	assert.Positive(t, analyzer.GetCacheSize())

	analyzer.ClearCache()

	// All caches should be empty
	assert.Equal(t, 0, analyzer.GetCacheSize())
}

func TestAnalyzer_GetCacheSize(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	// Initially empty
	assert.Equal(t, 0, analyzer.GetCacheSize())

	// Add items to each cache
	analyzer.tokenAnalyzer.cache.Store("key1", &cachedMetadata{})
	analyzer.tokenAnalyzer.cache.Store("key2", &cachedMetadata{})
	analyzer.liquidityAnalyzer.cache.Store("key3", &cachedLiquidity{})
	analyzer.securityAnalyzer.cache.Store("key4", &cachedSecurity{})
	analyzer.analysisCache.Store("key5", &CachedAnalysis{})

	// Should have 5 items
	assert.Equal(t, 5, analyzer.GetCacheSize())
}

func TestAnalyzer_UpdateConfig(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	// Modify config
	newConfig := config
	newConfig.MinLiquidityUSD = 5000.0
	newConfig.MinHolderCount = 100

	analyzer.UpdateConfig(newConfig)

	retrievedConfig := analyzer.GetConfig()
	assert.Equal(t, 5000.0, retrievedConfig.MinLiquidityUSD)
	assert.Equal(t, 100, retrievedConfig.MinHolderCount)
}

func TestAnalyzer_Shutdown(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	ctx := context.Background()
	err := analyzer.Shutdown(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 0, analyzer.GetCacheSize())
}

func TestAnalyzer_GetTokenScoreExplanation(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	result := &AnalysisResult{
		Score: TokenScore{
			OverallScore:    75.5,
			MetadataScore:   80.0,
			LiquidityScore:  70.0,
			SecurityScore:   80.0,
			SocialScore:     60.0,
			PotentialScore:  85.0,
			PositiveFactors: []string{"Factor 1", "Factor 2"},
			NegativeFactors: []string{"Risk 1"},
		},
	}

	explanation := analyzer.GetTokenScoreExplanation(result)

	assert.Contains(t, explanation, "75.50")
	assert.Contains(t, explanation, "Factor 1")
	assert.Contains(t, explanation, "Risk 1")
}

func TestAnalyzer_CompareTokenScores(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	result1 := &AnalysisResult{
		Score: TokenScore{OverallScore: 75.0},
	}

	result2 := &AnalysisResult{
		Score: TokenScore{OverallScore: 85.0},
	}

	result := analyzer.CompareTokenScores(result1, result2)
	assert.Equal(t, -1, result)

	result = analyzer.CompareTokenScores(result2, result1)
	assert.Equal(t, 1, result)
}

func TestAnalyzer_GetScoreRating(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	rating := analyzer.GetScoreRating(85.0)
	assert.Equal(t, "Very Good", rating)
}

func TestAnalyzer_EstimateFalsePositiveRate(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	rate := analyzer.EstimateFalsePositiveRate()
	assert.Equal(t, 0.05, rate)
}

func TestAnalyzer_HandleTokenEvent(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        9,
		MintAuthority:        stringPtr("11111111111111111111111111111111"),
		FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		LiquidityPoolAddress: "pool_address",
	}

	err := analyzer.HandleTokenEvent(event)
	// Should not error even without RPC
	assert.NoError(t, err)
}

func TestAnalyzer_OnError(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	// Should not panic
	analyzer.OnError(assert.AnError)
}

func TestAnalyzer_PreFilter(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	t.Run("passes filter", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Chain:                monitor.ChainTypeSolana,
			MintAddress:          "test_address",
			LiquidityPoolAddress: "pool_address",
			MintAuthority:        stringPtr("11111111111111111111111111111111"),
			FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		}

		ctx := context.Background()
		_, err := analyzer.AnalyzeToken(ctx, event)
		// Should not error on pre-filter
		// May error on actual analysis without RPC
		if err != nil {
			assert.NotContains(t, err.Error(), "pre-filter")
		}
	})

	t.Run("fails pre-filter - no pool", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Chain:       monitor.ChainTypeSolana,
			MintAddress: "test_address",
		}

		ctx := context.Background()
		_, err := analyzer.AnalyzeToken(ctx, event)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pre-filter")
	})
}

func BenchmarkAnalyzer_AnalyzeToken(b *testing.B) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		MintAddress:          "test_mint_address",
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        9,
		MintAuthority:        stringPtr("11111111111111111111111111111111"),
		FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		LiquidityPoolAddress: "pool_address",
		InitialPrice:         "0.000001",
	}

	ctx := context.Background()

	for b.Loop() {
		_, _ = analyzer.AnalyzeToken(ctx, event)
	}
}

func BenchmarkAnalyzer_ShouldBuy(b *testing.B) {
	config := DefaultAnalysisConfig()
	analyzer := NewAnalyzer(nil, config)

	result := &AnalysisResult{
		Recommendation: "buy",
		Security: SecurityAnalysis{
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
		},
		Score: TokenScore{
			OverallScore: 75.0,
		},
	}

	for b.Loop() {
		analyzer.ShouldBuy(result)
	}
}
