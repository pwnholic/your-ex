package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestNewSecurityAnalyzer(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	assert.NotNil(t, analyzer)
	assert.NotNil(t, analyzer.logger)
	assert.NotNil(t, analyzer.tokenAnalyzer)
}

func TestSecurityAnalyzer_AnalyzeSecurity_Solana(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	event := &monitor.TokenEvent{
		Chain:           monitor.ChainTypeSolana,
		Source:          monitor.SourcePumpFun,
		MintAddress:     "test_mint_address",
		TokenName:       "Test Token",
		TokenSymbol:     "TEST",
		TokenDecimals:   9,
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
	}

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		Twitter:         "@testtoken",
		Telegram:        "t.me/testtoken",
	}

	ctx := context.Background()
	analysis, err := analyzer.AnalyzeSecurity(ctx, event, metadata)

	requireNoError(t, err)
	assert.True(t, analysis.MintAuthorityRevoked)
	assert.True(t, analysis.FreezeAuthorityRevoked)
	assert.True(t, analysis.SocialAccountsExist)
	assert.Greater(t, analysis.SecurityScore, 50.0)
}

func TestSecurityAnalyzer_AnalyzeSecurity_Base(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	event := &monitor.TokenEvent{
		Chain:                monitor.ChainTypeBase,
		Source:               monitor.SourceUniswap,
		MintAddress:          "0x1234567890123456789012345678901234567890",
		LiquidityPoolAddress: "pool_address",
	}

	metadata := TokenMetadata{
		Name:             "Test Token",
		Symbol:           "TEST",
		Decimals:         18,
		Supply:           "1000000000000000000",
		ContractAddress:  "0x1234567890123456789012345678901234567890",
		ContractVerified: true,
	}

	ctx := context.Background()
	analysis, err := analyzer.AnalyzeSecurity(ctx, event, metadata)

	requireNoError(t, err)
	assert.Greater(t, analysis.SecurityScore, 0.0)
}

func TestSecurityAnalyzer_CheckMintAuthorityRevoked(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("revoked (nil)", func(t *testing.T) {
		metadata := TokenMetadata{
			MintAuthority: nil,
		}

		revoked := analyzer.checkMintAuthorityRevoked(metadata)
		assert.True(t, revoked)
	})

	t.Run("revoked (system program)", func(t *testing.T) {
		systemProgram := "11111111111111111111111111111111"
		metadata := TokenMetadata{
			MintAuthority: &systemProgram,
		}

		revoked := analyzer.checkMintAuthorityRevoked(metadata)
		assert.True(t, revoked)
	})

	t.Run("not revoked", func(t *testing.T) {
		authority := "some_other_address"
		metadata := TokenMetadata{
			MintAuthority: &authority,
		}

		revoked := analyzer.checkMintAuthorityRevoked(metadata)
		assert.False(t, revoked)
	})
}

func TestSecurityAnalyzer_CheckFreezeAuthorityRevoked(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("revoked (nil)", func(t *testing.T) {
		metadata := TokenMetadata{
			FreezeAuthority: nil,
		}

		revoked := analyzer.checkFreezeAuthorityRevoked(metadata)
		assert.True(t, revoked)
	})

	t.Run("revoked (system program)", func(t *testing.T) {
		systemProgram := "11111111111111111111111111111111"
		metadata := TokenMetadata{
			FreezeAuthority: &systemProgram,
		}

		revoked := analyzer.checkFreezeAuthorityRevoked(metadata)
		assert.True(t, revoked)
	})

	t.Run("not revoked", func(t *testing.T) {
		authority := "some_other_address"
		metadata := TokenMetadata{
			FreezeAuthority: &authority,
		}

		revoked := analyzer.checkFreezeAuthorityRevoked(metadata)
		assert.False(t, revoked)
	})
}

func TestSecurityAnalyzer_CalculateHolderDistributionScore(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("good distribution", func(t *testing.T) {
		analysis := SecurityAnalysis{
			HolderCount:              500,
			Top10HolderConcentration: 0.15,
		}

		score := analyzer.calculateHolderDistributionScore(analysis)
		assert.Greater(t, score, 90.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("medium distribution", func(t *testing.T) {
		analysis := SecurityAnalysis{
			HolderCount:              200,
			Top10HolderConcentration: 0.25,
		}

		score := analyzer.calculateHolderDistributionScore(analysis)
		assert.GreaterOrEqual(t, score, 70.0)
		assert.LessOrEqual(t, score, 100.0)
	})

	t.Run("poor distribution", func(t *testing.T) {
		analysis := SecurityAnalysis{
			HolderCount:              50,
			Top10HolderConcentration: 0.4,
		}

		score := analyzer.calculateHolderDistributionScore(analysis)
		assert.LessOrEqual(t, score, 80.0)
	})

	t.Run("very poor distribution", func(t *testing.T) {
		analysis := SecurityAnalysis{
			HolderCount:              20,
			Top10HolderConcentration: 0.6,
		}

		score := analyzer.calculateHolderDistributionScore(analysis)
		assert.LessOrEqual(t, score, 60.0)
	})
}

func TestSecurityAnalyzer_CalculateSecurityScore(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("high security score", func(t *testing.T) {
		analysis := SecurityAnalysis{
			MintAuthorityRevoked:    true,
			FreezeAuthorityRevoked:  true,
			LiquidityLocked:         true,
			LiquidityBurned:         true,
			HolderDistributionScore: 85.0,
			HasHiddenFees:           false,
			IsHoneypot:              false,
			SocialAccountsExist:     true,
		}

		score := analyzer.calculateSecurityScore(analysis)
		assert.GreaterOrEqual(t, score, 80.0)
		assert.LessOrEqual(t, score, 105.0) // Allow small bonus
	})

	t.Run("medium security score", func(t *testing.T) {
		analysis := SecurityAnalysis{
			MintAuthorityRevoked:    true,
			FreezeAuthorityRevoked:  false,
			LiquidityLocked:         true,
			LiquidityBurned:         false,
			HolderDistributionScore: 70.0,
			HasHiddenFees:           false,
			IsHoneypot:              false,
			SocialAccountsExist:     false,
		}

		score := analyzer.calculateSecurityScore(analysis)
		assert.Greater(t, score, 50.0)
		assert.Less(t, score, 80.0)
	})

	t.Run("low security score", func(t *testing.T) {
		analysis := SecurityAnalysis{
			MintAuthorityRevoked:    false,
			FreezeAuthorityRevoked:  false,
			LiquidityLocked:         false,
			LiquidityBurned:         false,
			HolderDistributionScore: 40.0,
			HasHiddenFees:           true,
			IsHoneypot:              false,
			SocialAccountsExist:     false,
		}

		score := analyzer.calculateSecurityScore(analysis)
		assert.Less(t, score, 50.0)
	})
}

func TestSecurityAnalyzer_DetermineRiskLevel(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("low risk", func(t *testing.T) {
		analysis := SecurityAnalysis{
			SecurityScore:   85.0,
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
			LiquidityBurned: true,
		}

		risk := analyzer.determineRiskLevel(analysis)
		assert.Equal(t, "low", risk)
	})

	t.Run("medium risk", func(t *testing.T) {
		analysis := SecurityAnalysis{
			SecurityScore:   65.0,
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
			LiquidityBurned: false,
		}

		risk := analyzer.determineRiskLevel(analysis)
		assert.Equal(t, "medium", risk)
	})

	t.Run("high risk", func(t *testing.T) {
		analysis := SecurityAnalysis{
			SecurityScore:   45.0,
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: false,
			LiquidityBurned: false,
		}

		risk := analyzer.determineRiskLevel(analysis)
		assert.Equal(t, "high", risk)
	})

	t.Run("critical risk - honeypot", func(t *testing.T) {
		analysis := SecurityAnalysis{
			SecurityScore:   90.0,
			IsHoneypot:      true,
			HasHiddenFees:   false,
			LiquidityLocked: true,
		}

		risk := analyzer.determineRiskLevel(analysis)
		assert.Equal(t, "critical", risk)
	})

	t.Run("critical risk - hidden fees", func(t *testing.T) {
		analysis := SecurityAnalysis{
			SecurityScore:   70.0,
			IsHoneypot:      false,
			HasHiddenFees:   true,
			LiquidityLocked: true,
		}

		risk := analyzer.determineRiskLevel(analysis)
		assert.Equal(t, "high", risk)
	})
}

func TestSecurityAnalyzer_CheckAuthorityRevoked(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("Solana with revoked authorities", func(t *testing.T) {
		systemProgram := "11111111111111111111111111111111"
		metadata := TokenMetadata{
			MintAuthority:   &systemProgram,
			FreezeAuthority: &systemProgram,
		}

		mint, freeze := analyzer.CheckAuthorityRevoked(monitor.ChainTypeSolana, metadata)
		assert.True(t, mint)
		assert.True(t, freeze)
	})

	t.Run("Solana with active authorities", func(t *testing.T) {
		authority := "some_address"
		metadata := TokenMetadata{
			MintAuthority:   &authority,
			FreezeAuthority: &authority,
		}

		mint, freeze := analyzer.CheckAuthorityRevoked(monitor.ChainTypeSolana, metadata)
		assert.False(t, mint)
		assert.False(t, freeze)
	})

	t.Run("Base (no authorities)", func(t *testing.T) {
		metadata := TokenMetadata{}

		mint, freeze := analyzer.CheckAuthorityRevoked(monitor.ChainTypeBase, metadata)
		assert.True(t, mint)   // Base doesn't have mint authority
		assert.True(t, freeze) // Base doesn't have freeze authority
	})
}

func TestSecurityAnalyzer_VerifySocialLinks(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	t.Run("all social links", func(t *testing.T) {
		metadata := TokenMetadata{
			Twitter:  "@testtoken",
			Telegram: "t.me/testtoken",
			Website:  "https://testtoken.com",
		}

		twitter, telegram, website := analyzer.VerifySocialLinks(metadata)
		assert.True(t, twitter)
		assert.True(t, telegram)
		assert.True(t, website)
	})

	t.Run("no social links", func(t *testing.T) {
		metadata := TokenMetadata{}

		twitter, telegram, website := analyzer.VerifySocialLinks(metadata)
		assert.False(t, twitter)
		assert.False(t, telegram)
		assert.False(t, website)
	})

	t.Run("partial social links", func(t *testing.T) {
		metadata := TokenMetadata{
			Twitter: "@testtoken",
			Website: "https://testtoken.com",
		}

		twitter, telegram, website := analyzer.VerifySocialLinks(metadata)
		assert.True(t, twitter)
		assert.False(t, telegram)
		assert.True(t, website)
	})
}

func TestSecurityAnalyzer_ClearCache(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	// Add something to cache
	analyzer.cache.Store("test_key", &cachedSecurity{
		analysis: SecurityAnalysis{SecurityScore: 50},
		cachedAt: time.Now(),
	})

	assert.Equal(t, 1, analyzer.GetCacheSize())

	analyzer.ClearCache()

	assert.Equal(t, 0, analyzer.GetCacheSize())
}

func TestSecurityAnalyzer_GetCacheSize(t *testing.T) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	assert.Equal(t, 0, analyzer.GetCacheSize())

	// Add items to cache
	analyzer.cache.Store("key1", &cachedSecurity{})
	analyzer.cache.Store("key2", &cachedSecurity{})
	analyzer.cache.Store("key3", &cachedSecurity{})

	assert.Equal(t, 3, analyzer.GetCacheSize())
}

func BenchmarkSecurityAnalyzer_CalculateSecurityScore(b *testing.B) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	analysis := SecurityAnalysis{
		MintAuthorityRevoked:    true,
		FreezeAuthorityRevoked:  true,
		LiquidityLocked:         true,
		LiquidityBurned:         true,
		HolderDistributionScore: 80.0,
		HasHiddenFees:           false,
		IsHoneypot:              false,
		SocialAccountsExist:     true,
	}

	for b.Loop() {
		analyzer.calculateSecurityScore(analysis)
	}
}

func BenchmarkSecurityAnalyzer_AnalyzeSecurity(b *testing.B) {
	config := DefaultAnalysisConfig()
	tokenAnalyzer := NewTokenAnalyzer(nil, config)
	analyzer := NewSecurityAnalyzer(nil, config, tokenAnalyzer)

	event := &monitor.TokenEvent{
		Chain:           monitor.ChainTypeSolana,
		Source:          monitor.SourcePumpFun,
		MintAddress:     "test_mint_address",
		TokenName:       "Test Token",
		TokenSymbol:     "TEST",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
	}

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
	}

	ctx := context.Background()

	for b.Loop() {
		_, _ = analyzer.AnalyzeSecurity(ctx, event, metadata)
	}
}
