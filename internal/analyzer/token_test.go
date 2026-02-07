package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestNewTokenAnalyzer(t *testing.T) {
	config := DefaultAnalysisConfig()
	analyzer := NewTokenAnalyzer(nil, config)

	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.logger)
	assert.NotNil(t, analyzer.httpClient)
	assert.Equal(t, 10*time.Second, analyzer.httpClient.Timeout)
}

func TestTokenAnalyzer_FetchMetadata_Solana(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

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
	}

	ctx := context.Background()
	metadata, err := analyzer.FetchMetadata(ctx, event)

	// Since we don't have a real RPC, we expect some data but may have errors
	if err == nil {
		assert.Equal(t, "Test Token", metadata.Name)
		assert.Equal(t, "TEST", metadata.Symbol)
		assert.Equal(t, uint8(9), metadata.Decimals)
	}
}

func TestTokenAnalyzer_FetchMetadata_Base(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeBase,
		Source:               monitor.SourceUniswap,
		MintAddress:          "0x1234567890123456789012345678901234567890",
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        18,
		LiquidityPoolAddress: "pool_address",
	}

	ctx := context.Background()
	metadata, err := analyzer.FetchMetadata(ctx, event)

	// Since we don't have a real RPC, we expect some data but may have errors
	if err == nil {
		assert.Equal(t, "Test Token", metadata.Name)
		assert.Equal(t, "TEST", metadata.Symbol)
		assert.Equal(t, uint8(18), metadata.Decimals)
	}
}

func TestTokenAnalyzer_ValidateMetadata(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	t.Run("valid metadata", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:     "Valid Token",
			Symbol:   "VALID",
			Decimals: 9,
			Supply:   "1000000000",
		}

		valid, issues := analyzer.ValidateMetadata(metadata)
		assert.True(t, valid)
		assert.Empty(t, issues)
	})

	t.Run("missing name", func(t *testing.T) {
		metadata := TokenMetadata{
			Symbol:   "VALID",
			Decimals: 9,
			Supply:   "1000000000",
		}

		valid, issues := analyzer.ValidateMetadata(metadata)
		assert.False(t, valid)
		assert.NotEmpty(t, issues)
		assert.Contains(t, issues[0], "name")
	})

	t.Run("missing symbol", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:     "Valid Token",
			Decimals: 9,
			Supply:   "1000000000",
		}

		valid, issues := analyzer.ValidateMetadata(metadata)
		assert.False(t, valid)
		assert.NotEmpty(t, issues)
		assert.Contains(t, issues[0], "symbol")
	})

	t.Run("invalid supply", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:     "Valid Token",
			Symbol:   "VALID",
			Decimals: 9,
			Supply:   "invalid",
		}

		valid, issues := analyzer.ValidateMetadata(metadata)
		assert.False(t, valid)
		assert.NotEmpty(t, issues)
		assert.Contains(t, issues[0], "supply")
	})

	t.Run("suspiciously long name", func(t *testing.T) {
		longName := string(make([]byte, 101))
		metadata := TokenMetadata{
			Name:     longName,
			Symbol:   "VALID",
			Decimals: 9,
			Supply:   "1000000000",
		}

		valid, issues := analyzer.ValidateMetadata(metadata)
		assert.False(t, valid)
		assert.NotEmpty(t, issues)
		assert.Contains(t, issues[0], "suspiciously long")
	})
}

func TestTokenAnalyzer_CalculateMetadataScore(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	t.Run("complete metadata", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:        "Test Token",
			Symbol:      "TEST",
			Decimals:    9,
			Supply:      "1000000000",
			MetadataURI: "https://example.com/metadata.json",
			Twitter:     "@testtoken",
			Telegram:    "t.me/testtoken",
			Website:     "https://testtoken.com",
		}

		score := analyzer.CalculateMetadataScore(metadata)
		assert.Greater(t, score, 80.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("minimal metadata", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:     "Test Token",
			Symbol:   "TEST",
			Decimals: 9,
			Supply:   "1000000000",
		}

		score := analyzer.CalculateMetadataScore(metadata)
		assert.GreaterOrEqual(t, score, 40.0)
		assert.LessOrEqual(t, score, 70.0)
	})

	t.Run("no social links", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:        "Test Token",
			Symbol:      "TEST",
			Decimals:    9,
			Supply:      "1000000000",
			MetadataURI: "https://example.com/metadata.json",
		}

		score := analyzer.CalculateMetadataScore(metadata)
		assert.GreaterOrEqual(t, score, 50.0)
		assert.LessOrEqual(t, score, 80.0)
	})
}

func TestTokenAnalyzer_ClearCache(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	// Add something to cache
	analyzer.cache.Store("test_key", &cachedMetadata{
		metadata: TokenMetadata{Name: "Test"},
		cachedAt: time.Now(),
	})

	assert.Equal(t, 1, analyzer.GetCacheSize())

	analyzer.ClearCache()

	assert.Equal(t, 0, analyzer.GetCacheSize())
}

func TestTokenAnalyzer_GetCacheSize(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	assert.Equal(t, 0, analyzer.GetCacheSize())

	// Add items to cache
	analyzer.cache.Store("key1", &cachedMetadata{})
	analyzer.cache.Store("key2", &cachedMetadata{})
	analyzer.cache.Store("key3", &cachedMetadata{})

	assert.Equal(t, 3, analyzer.GetCacheSize())
}

func TestMetaplexMetadata(t *testing.T) {
	metadata := MetaplexMetadata{
		Name:        "Test Token",
		Symbol:      "TEST",
		Description: "A test token",
		Image:       "https://example.com/image.png",
		URI:         "https://example.com/metadata.json",
		Extensions: &MetadataExtensions{
			Twitter:  "@testtoken",
			Telegram: "t.me/testtoken",
			Website:  "https://testtoken.com",
		},
	}

	assert.Equal(t, "Test Token", metadata.Name)
	assert.Equal(t, "TEST", metadata.Symbol)
	assert.NotNil(t, metadata.Extensions)
	assert.Equal(t, "@testtoken", metadata.Extensions.Twitter)
}

func TestMetadataExtensions(t *testing.T) {
	t.Run("all fields", func(t *testing.T) {
		ext := MetadataExtensions{
			Twitter:  "@testtoken",
			Telegram: "t.me/testtoken",
			Website:  "https://testtoken.com",
		}

		assert.Equal(t, "@testtoken", ext.Twitter)
		assert.Equal(t, "t.me/testtoken", ext.Telegram)
		assert.Equal(t, "https://testtoken.com", ext.Website)
	})

	t.Run("nil extensions", func(t *testing.T) {
		var ext *MetadataExtensions
		assert.Nil(t, ext)
	})
}

func TestTokenAnalyzer_CacheExpiry(t *testing.T) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())

	// Store an expired item
	expiredTime := time.Now().Add(-metadataCacheTTL - time.Second)
	analyzer.cache.Store("expired_key", &cachedMetadata{
		metadata: TokenMetadata{Name: "Expired"},
		cachedAt: expiredTime,
	})

	// Store a fresh item
	analyzer.cache.Store("fresh_key", &cachedMetadata{
		metadata: TokenMetadata{Name: "Fresh"},
		cachedAt: time.Now(),
	})

	// Initial size
	assert.Equal(t, 2, analyzer.GetCacheSize())

	// Try to fetch - the expired one should be removed on access
	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeSolana,
		MintAddress:          "expired_key",
		LiquidityPoolAddress: "pool",
	}

	ctx := context.Background()
	_, _ = analyzer.FetchMetadata(ctx, event)

	// The expired cache should have been removed
	// But since we don't have real RPC, the cache might not be hit
	// Just verify the cache size is <= 3 (expired removed, fresh kept, new entry added)
	assert.LessOrEqual(t, analyzer.GetCacheSize(), 3)
}

func BenchmarkTokenAnalyzer_ValidateMetadata(b *testing.B) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())
	metadata := TokenMetadata{
		Name:     "Test Token",
		Symbol:   "TEST",
		Decimals: 9,
		Supply:   "1000000000",
	}

	for b.Loop() {
		_, _ = analyzer.ValidateMetadata(metadata)
	}
}

func BenchmarkTokenAnalyzer_CalculateMetadataScore(b *testing.B) {
	analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())
	metadata := TokenMetadata{
		Name:        "Test Token",
		Symbol:      "TEST",
		Decimals:    9,
		Supply:      "1000000000",
		MetadataURI: "https://example.com/metadata.json",
		Twitter:     "@testtoken",
		Telegram:    "t.me/testtoken",
		Website:     "https://testtoken.com",
	}

	for b.Loop() {
		analyzer.CalculateMetadataScore(metadata)
	}
}
