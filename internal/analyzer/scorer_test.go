package analyzer

import (
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestNewScorer(t *testing.T) {
	config := DefaultAnalysisConfig()
	scorer := NewScorer(config)

	assert.NotNil(t, scorer)
	assert.NotNil(t, scorer.logger)
	assert.Equal(t, config, scorer.config)
}

func TestScorer_CalculateScore(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		Twitter:         "@testtoken",
		Telegram:        "t.me/testtoken",
		Website:         "https://testtoken.com",
	}

	liquidity := LiquidityAnalysis{
		PoolAddress:      "pool_address",
		TotalValueLocked: "2.5",
		IsLocked:         true,
		LockDuration:     durationPtr(365 * 24 * time.Hour),
		BurnedLiquidity:  100.0,
		DepthScore:       85.0,
	}

	security := SecurityAnalysis{
		MintAuthorityRevoked:     true,
		FreezeAuthorityRevoked:   true,
		LiquidityLocked:          true,
		LiquidityBurned:          true,
		HolderCount:              500,
		Top10HolderConcentration: 0.15,
		HolderDistributionScore:  85.0,
		HasHiddenFees:            false,
		IsHoneypot:               false,
		SocialAccountsExist:      true,
		SecurityScore:            90.0,
		RiskLevel:                "low",
	}

	score := scorer.CalculateScore(metadata, liquidity, security)

	assert.Greater(t, score.OverallScore, 0.0)
	assert.LessOrEqual(t, score.OverallScore, 100.0)
	assert.Greater(t, score.MetadataScore, 0.0)
	assert.Greater(t, score.LiquidityScore, 0.0)
	assert.Greater(t, score.SecurityScore, 0.0)
	assert.Greater(t, score.SocialScore, 0.0)
	assert.Greater(t, score.PotentialScore, 0.0)
}

func TestScorer_CalculateMetadataScore(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	t.Run("perfect metadata", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:            "Test Token",
			Symbol:          "TEST",
			Decimals:        9,
			Supply:          "1000000000",
			MetadataURI:     "https://example.com/metadata.json",
			MintAuthority:   stringPtr("11111111111111111111111111111111"),
			FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		}

		score := scorer.calculateMetadataScore(metadata)
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

		score := scorer.calculateMetadataScore(metadata)
		assert.Greater(t, score, 40.0)
		assert.Less(t, score, 70.0)
	})

	t.Run("Base token with verified contract", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:             "Test Token",
			Symbol:           "TEST",
			Decimals:         18,
			Supply:           "1000000000000000000",
			MetadataURI:      "https://example.com/metadata.json",
			ContractVerified: true,
		}

		score := scorer.calculateMetadataScore(metadata)
		assert.Greater(t, score, 60.0)
	})
}

func TestScorer_CalculateSocialScore(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	t.Run("all social accounts", func(t *testing.T) {
		metadata := TokenMetadata{
			Twitter:  "@testtoken",
			Telegram: "t.me/testtoken",
			Website:  "https://testtoken.com",
		}
		security := SecurityAnalysis{
			SocialAccountsExist: true,
			TwitterVerified:     true,
			TelegramVerified:    true,
		}

		score := scorer.calculateSocialScore(metadata, security)
		assert.Equal(t, 100.0, score)
	})

	t.Run("no social accounts", func(t *testing.T) {
		metadata := TokenMetadata{}
		security := SecurityAnalysis{
			SocialAccountsExist: false,
		}

		score := scorer.calculateSocialScore(metadata, security)
		assert.Equal(t, 0.0, score)
	})

	t.Run("partial social accounts", func(t *testing.T) {
		metadata := TokenMetadata{
			Twitter: "@testtoken",
		}
		security := SecurityAnalysis{
			SocialAccountsExist: true,
			TwitterVerified:     true,
		}

		score := scorer.calculateSocialScore(metadata, security)
		assert.Equal(t, 40.0, score)
	})
}

func TestScorer_GenerateFactors(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		Twitter:         "@testtoken",
	}

	liquidity := LiquidityAnalysis{
		IsLocked:        true,
		BurnedLiquidity: 100.0,
	}

	security := SecurityAnalysis{
		MintAuthorityRevoked:     true,
		FreezeAuthorityRevoked:   true,
		LiquidityLocked:          true,
		Top10HolderConcentration: 0.2,
		SocialAccountsExist:      true,
		IsHoneypot:               false,
		HasHiddenFees:            false,
	}

	score := TokenScore{
		OverallScore: 75.0,
	}

	positive, negative := scorer.generateFactors(metadata, liquidity, security, score)

	assert.NotEmpty(t, positive)
	assert.Contains(t, positive, "Has valid token name")
	assert.Contains(t, positive, "Mint authority revoked")
	assert.Contains(t, positive, "Liquidity is locked")

	// Should have minimal negative factors
	assert.LessOrEqual(t, len(negative), 2)
}

func TestScorer_GenerateRecommendation(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	t.Run("buy recommendation", func(t *testing.T) {
		score := TokenScore{
			OverallScore: 75.0,
		}
		security := SecurityAnalysis{
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
			RiskLevel:       "low",
		}

		rec := scorer.GenerateRecommendation(score, security)
		assert.Equal(t, "buy", rec)
	})

	t.Run("skip recommendation", func(t *testing.T) {
		score := TokenScore{
			OverallScore: 55.0,
		}
		security := SecurityAnalysis{
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
			RiskLevel:       "medium",
		}

		rec := scorer.GenerateRecommendation(score, security)
		assert.Equal(t, "skip", rec)
	})

	t.Run("warning recommendation - low score", func(t *testing.T) {
		score := TokenScore{
			OverallScore: 40.0,
		}
		security := SecurityAnalysis{
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: true,
			RiskLevel:       "high",
		}

		rec := scorer.GenerateRecommendation(score, security)
		assert.Equal(t, "warning", rec)
	})

	t.Run("warning recommendation - honeypot", func(t *testing.T) {
		score := TokenScore{
			OverallScore: 80.0,
		}
		security := SecurityAnalysis{
			IsHoneypot:      true,
			HasHiddenFees:   false,
			LiquidityLocked: true,
		}

		rec := scorer.GenerateRecommendation(score, security)
		assert.Equal(t, "warning", rec)
	})

	t.Run("warning recommendation - no liquidity lock", func(t *testing.T) {
		score := TokenScore{
			OverallScore: 70.0,
		}
		security := SecurityAnalysis{
			IsHoneypot:      false,
			HasHiddenFees:   false,
			LiquidityLocked: false,
			LiquidityBurned: false,
		}

		rec := scorer.GenerateRecommendation(score, security)
		assert.Equal(t, "warning", rec)
	})
}

func TestScorer_ShouldAnalyze(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	t.Run("should analyze - valid Solana token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Chain:                monitor.ChainTypeSolana,
			MintAddress:          "test_address",
			LiquidityPoolAddress: "pool_address",
			MintAuthority:        stringPtr("11111111111111111111111111111111"),
			FreezeAuthority:      stringPtr("11111111111111111111111111111111"),
		}

		should := scorer.ShouldAnalyze(event)
		assert.True(t, should)
	})

	t.Run("should not analyze - no pool address", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Chain:       monitor.ChainTypeSolana,
			MintAddress: "test_address",
		}

		should := scorer.ShouldAnalyze(event)
		assert.False(t, should)
	})

	t.Run("should not analyze - mint authority not revoked", func(t *testing.T) {
		authority := "some_address"
		event := &monitor.TokenEvent{
			Chain:                monitor.ChainTypeSolana,
			MintAddress:          "test_address",
			LiquidityPoolAddress: "pool_address",
			MintAuthority:        &authority,
		}

		should := scorer.ShouldAnalyze(event)
		assert.False(t, should)
	})

	t.Run("should analyze - Base token", func(t *testing.T) {
		event := &monitor.TokenEvent{
			Chain:                monitor.ChainTypeBase,
			MintAddress:          "0x1234567890123456789012345678901234567890",
			LiquidityPoolAddress: "pool_address",
		}

		should := scorer.ShouldAnalyze(event)
		assert.True(t, should)
	})
}

func TestScorer_CompareTokens(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	score1 := TokenScore{OverallScore: 75.0}
	score2 := TokenScore{OverallScore: 85.0}

	result := scorer.CompareTokens(score1, score2)
	assert.Equal(t, -1, result)

	result = scorer.CompareTokens(score2, score1)
	assert.Equal(t, 1, result)

	result = scorer.CompareTokens(score1, TokenScore{OverallScore: 75.0})
	assert.Equal(t, 0, result)
}

func TestScorer_GetScoreExplanation(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	score := TokenScore{
		OverallScore:    75.5,
		MetadataScore:   80.0,
		LiquidityScore:  70.0,
		SecurityScore:   80.0,
		SocialScore:     60.0,
		PotentialScore:  85.0,
		PositiveFactors: []string{"Factor 1", "Factor 2"},
		NegativeFactors: []string{"Risk 1"},
	}

	explanation := scorer.GetScoreExplanation(score)

	assert.Contains(t, explanation, "75.50")
	assert.Contains(t, explanation, "80.00")
	assert.Contains(t, explanation, "Factor 1")
	assert.Contains(t, explanation, "Risk 1")
}

func TestScorer_GetScoreRating(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	testCases := []struct {
		score    float64
		expected string
	}{
		{95.0, "Excellent"},
		{85.0, "Very Good"},
		{75.0, "Good"},
		{65.0, "Fair"},
		{55.0, "Poor"},
		{35.0, "Very Poor"},
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			rating := scorer.GetScoreRating(tc.score)
			assert.Equal(t, tc.expected, rating)
		})
	}
}

func TestScorer_IsScoreAboveThreshold(t *testing.T) {
	scorer := NewScorer(DefaultAnalysisConfig())

	t.Run("above threshold", func(t *testing.T) {
		score := TokenScore{OverallScore: 75.0}
		assert.True(t, scorer.IsScoreAboveThreshold(score))
	})

	t.Run("below threshold", func(t *testing.T) {
		score := TokenScore{OverallScore: 55.0}
		assert.False(t, scorer.IsScoreAboveThreshold(score))
	})

	t.Run("at threshold", func(t *testing.T) {
		score := TokenScore{OverallScore: 60.0}
		assert.True(t, scorer.IsScoreAboveThreshold(score))
	})
}

func BenchmarkScorer_CalculateScore(b *testing.B) {
	scorer := NewScorer(DefaultAnalysisConfig())

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		Twitter:         "@testtoken",
	}

	liquidity := LiquidityAnalysis{
		TotalValueLocked: "2.5",
		IsLocked:         true,
		BurnedLiquidity:  100.0,
		DepthScore:       85.0,
	}

	security := SecurityAnalysis{
		MintAuthorityRevoked:    true,
		FreezeAuthorityRevoked:  true,
		LiquidityLocked:         true,
		LiquidityBurned:         true,
		HolderDistributionScore: 85.0,
		HasHiddenFees:           false,
		IsHoneypot:              false,
		SocialAccountsExist:     true,
		SecurityScore:           90.0,
	}

	for b.Loop() {
		scorer.CalculateScore(metadata, liquidity, security)
	}
}

func BenchmarkScorer_GenerateFactors(b *testing.B) {
	scorer := NewScorer(DefaultAnalysisConfig())

	metadata := TokenMetadata{
		Name:            "Test Token",
		Symbol:          "TEST",
		Decimals:        9,
		Supply:          "1000000000",
		MintAuthority:   stringPtr("11111111111111111111111111111111"),
		FreezeAuthority: stringPtr("11111111111111111111111111111111"),
		Twitter:         "@testtoken",
	}

	liquidity := LiquidityAnalysis{
		IsLocked:        true,
		BurnedLiquidity: 100.0,
	}

	security := SecurityAnalysis{
		MintAuthorityRevoked:     true,
		FreezeAuthorityRevoked:   true,
		LiquidityLocked:          true,
		Top10HolderConcentration: 0.2,
		SocialAccountsExist:      true,
		IsHoneypot:               false,
		HasHiddenFees:            false,
	}

	score := TokenScore{
		OverallScore: 75.0,
	}

	for b.Loop() {
		scorer.generateFactors(metadata, liquidity, security, score)
	}
}
