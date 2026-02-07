package analyzer

import (
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/stretchr/testify/assert"
)

func TestDefaultAnalysisConfig(t *testing.T) {
	config := DefaultAnalysisConfig()

	assert.Equal(t, 1000.0, config.MinLiquidityUSD)
	assert.Equal(t, 50, config.MinHolderCount)
	assert.Equal(t, 0.3, config.MaxHolderConcentration)
	assert.True(t, config.RequireMintAuthorityRevoked)
	assert.True(t, config.RequireFreezeAuthorityRevoked)
	assert.True(t, config.RequireLiquidityLocked)
	assert.False(t, config.RequireSocialAccounts)
	assert.False(t, config.EnableHoneypotTest)
	assert.Equal(t, 30*time.Second, config.AnalysisTimeout)
}

func TestScoreWeights(t *testing.T) {
	weights := ScoreWeights{
		Liquidity: 0.3,
		Security:  0.4,
		Social:    0.2,
		Potential: 0.1,
	}

	total := weights.Liquidity + weights.Security + weights.Social + weights.Potential
	assert.InDelta(t, 1.0, total, 0.01)
}

func TestAnalysisResult(t *testing.T) {
	result := AnalysisResult{
		TokenAddress: "test_address",
		Chain:        monitor.ChainTypeSolana,
		Source:       monitor.SourcePumpFun,
		Timestamp:    time.Now(),
		Metadata: TokenMetadata{
			Name:     "Test Token",
			Symbol:   "TEST",
			Decimals: 9,
			Supply:   "1000000000",
		},
		Liquidity: LiquidityAnalysis{
			PoolAddress:      "pool_address",
			PoolType:         "pump_fun",
			TotalValueLocked: "2.0",
			IsLocked:         true,
		},
		Security: SecurityAnalysis{
			MintAuthorityRevoked:   true,
			FreezeAuthorityRevoked: true,
			LiquidityLocked:        true,
			SecurityScore:          80.0,
			RiskLevel:              "low",
		},
		Score: TokenScore{
			OverallScore:   75.0,
			MetadataScore:  80.0,
			LiquidityScore: 70.0,
			SecurityScore:  80.0,
			SocialScore:    60.0,
			PotentialScore: 75.0,
		},
		Recommendation: "buy",
	}

	assert.Equal(t, "test_address", result.TokenAddress)
	assert.Equal(t, monitor.ChainTypeSolana, result.Chain)
	assert.Equal(t, "Test Token", result.Metadata.Name)
	assert.True(t, result.Security.MintAuthorityRevoked)
	assert.Equal(t, "buy", result.Recommendation)
}

func TestTokenMetadata(t *testing.T) {
	t.Run("Solana metadata", func(t *testing.T) {
		burned := "11111111111111111111111111111111"
		metadata := TokenMetadata{
			Name:            "Test Token",
			Symbol:          "TEST",
			Decimals:        9,
			Supply:          "1000000000",
			MintAuthority:   &burned,
			FreezeAuthority: &burned,
			Twitter:         "@testtoken",
			Telegram:        "t.me/testtoken",
		}

		assert.Equal(t, "Test Token", metadata.Name)
		assert.Equal(t, "TEST", metadata.Symbol)
		assert.NotNil(t, metadata.MintAuthority)
		assert.Equal(t, burned, *metadata.MintAuthority)
		assert.Equal(t, "@testtoken", metadata.Twitter)
	})

	t.Run("Base metadata", func(t *testing.T) {
		metadata := TokenMetadata{
			Name:             "Test Token",
			Symbol:           "TEST",
			Decimals:         18,
			Supply:           "1000000000000000000",
			ContractAddress:  "0x1234567890123456789012345678901234567890",
			ContractVerified: true,
		}

		assert.Equal(t, "Test Token", metadata.Name)
		assert.True(t, metadata.ContractVerified)
	})
}

func TestLiquidityAnalysis(t *testing.T) {
	lockDuration := 30 * 24 * time.Hour
	analysis := LiquidityAnalysis{
		PoolAddress:      "pool_address",
		PoolType:         "pump_fun",
		TotalValueLocked: "2.5",
		TokenReserve:     "1000000",
		BaseTokenReserve: "2.5",
		InitialPrice:     "0.0000025",
		DepthScore:       85.0,
		IsLocked:         true,
		LockDuration:     &lockDuration,
		BurnedLiquidity:  100.0,
	}

	assert.Equal(t, "pool_address", analysis.PoolAddress)
	assert.Equal(t, "pump_fun", analysis.PoolType)
	assert.True(t, analysis.IsLocked)
	assert.NotNil(t, analysis.LockDuration)
	assert.Equal(t, 30*24*time.Hour, *analysis.LockDuration)
	assert.Equal(t, 100.0, analysis.BurnedLiquidity)
}

func TestSecurityAnalysis(t *testing.T) {
	analysis := SecurityAnalysis{
		MintAuthorityRevoked:     true,
		FreezeAuthorityRevoked:   true,
		LiquidityLocked:          true,
		LiquidityBurned:          true,
		HolderCount:              500,
		Top10HolderConcentration: 0.15,
		HolderDistributionScore:  85.0,
		TransferFeeBuy:           0.0,
		TransferFeeSell:          0.0,
		HasHiddenFees:            false,
		IsHoneypot:               false,
		SocialAccountsExist:      true,
		TwitterVerified:          true,
		TelegramVerified:         true,
		SecurityScore:            90.0,
		RiskLevel:                "low",
	}

	assert.True(t, analysis.MintAuthorityRevoked)
	assert.True(t, analysis.LiquidityLocked)
	assert.Equal(t, 500, analysis.HolderCount)
	assert.Equal(t, 0.15, analysis.Top10HolderConcentration)
	assert.False(t, analysis.IsHoneypot)
	assert.Equal(t, "low", analysis.RiskLevel)
}

func TestTokenScore(t *testing.T) {
	score := TokenScore{
		OverallScore:   82.5,
		MetadataScore:  85.0,
		LiquidityScore: 80.0,
		SecurityScore:  85.0,
		SocialScore:    70.0,
		PotentialScore: 85.0,
		PositiveFactors: []string{
			"Has valid token name",
			"Mint authority revoked",
			"Liquidity is locked",
		},
		NegativeFactors: []string{
			"No social media accounts",
		},
	}

	assert.Equal(t, 82.5, score.OverallScore)
	assert.Len(t, score.PositiveFactors, 3)
	assert.Len(t, score.NegativeFactors, 1)
	assert.Contains(t, score.PositiveFactors, "Mint authority revoked")
}

func TestCachedAnalysis(t *testing.T) {
	result := AnalysisResult{
		TokenAddress:   "test_address",
		Recommendation: "buy",
	}

	cached := CachedAnalysis{
		Result:   result,
		CachedAt: time.Now(),
		TTL:      5 * time.Minute,
		HitCount: int64(10),
	}

	assert.Equal(t, "test_address", cached.Result.TokenAddress)
	assert.Equal(t, "buy", cached.Result.Recommendation)
	assert.Equal(t, int64(10), cached.HitCount)
	assert.Less(t, time.Since(cached.CachedAt), time.Minute)
}

func TestAnalysisStatus(t *testing.T) {
	status := AnalysisStatus{
		TokenAddress: "test_address",
		Chain:        monitor.ChainTypeBase,
		Status:       "analyzing",
		Progress:     0.5,
		StartTime:    time.Now(),
	}

	assert.Equal(t, "test_address", status.TokenAddress)
	assert.Equal(t, monitor.ChainTypeBase, status.Chain)
	assert.Equal(t, "analyzing", status.Status)
	assert.Equal(t, 0.5, status.Progress)
}

func TestRiskLevels(t *testing.T) {
	levels := []string{"low", "medium", "high", "critical"}

	for _, level := range levels {
		valid := false
		switch level {
		case "low", "medium", "high", "critical":
			valid = true
		}
		assert.True(t, valid, "Risk level %s should be valid", level)
	}
}

func TestRecommendations(t *testing.T) {
	recommendations := []string{"buy", "skip", "warning"}

	for _, rec := range recommendations {
		valid := false
		switch rec {
		case "buy", "skip", "warning":
			valid = true
		}
		assert.True(t, valid, "Recommendation %s should be valid", rec)
	}
}

func TestMetadataValidationCases(t *testing.T) {
	testCases := []struct {
		name     string
		metadata TokenMetadata
		want     bool
	}{
		{
			name: "valid metadata",
			metadata: TokenMetadata{
				Name:     "Valid Token",
				Symbol:   "VALID",
				Decimals: 9,
				Supply:   "1000000",
			},
			want: true,
		},
		{
			name: "missing name",
			metadata: TokenMetadata{
				Symbol:   "VALID",
				Decimals: 9,
				Supply:   "1000000",
			},
			want: false,
		},
		{
			name: "missing symbol",
			metadata: TokenMetadata{
				Name:     "Valid Token",
				Decimals: 9,
				Supply:   "1000000",
			},
			want: false,
		},
		{
			name: "suspiciously long name",
			metadata: TokenMetadata{
				Name:     string(make([]byte, 101)), // 101 characters
				Symbol:   "VALID",
				Decimals: 9,
				Supply:   "1000000",
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			analyzer := NewTokenAnalyzer(nil, DefaultAnalysisConfig())
			valid, issues := analyzer.ValidateMetadata(tc.metadata)

			assert.Equal(t, tc.want, valid)
			if !valid {
				assert.NotEmpty(t, issues)
			}
		})
	}
}

func TestAnalysisConfigValidation(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		config := DefaultAnalysisConfig()
		assert.Positive(t, config.MinLiquidityUSD)
		assert.Positive(t, config.MinHolderCount)
		assert.True(t, config.MaxHolderConcentration > 0 && config.MaxHolderConcentration <= 1)
		assert.True(t, config.MaxAcceptableSlippage >= 0 && config.MaxAcceptableSlippage <= 1)
	})

	t.Run("score weights sum to 1", func(t *testing.T) {
		config := DefaultAnalysisConfig()
		total := config.ScoreWeights.Liquidity +
			config.ScoreWeights.Security +
			config.ScoreWeights.Social +
			config.ScoreWeights.Potential

		assert.InDelta(t, 1.0, total, 0.01)
	})
}

func BenchmarkAnalysisResultCreation(b *testing.B) {
	event := &monitor.TokenEvent{
		MintAddress:          "test_address",
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		TokenName:            "Test Token",
		TokenSymbol:          "TEST",
		TokenDecimals:        9,
		LiquidityPoolAddress: "pool_address",
	}

	for b.Loop() {
		result := &AnalysisResult{
			TokenAddress: event.MintAddress,
			Chain:        event.Chain,
			Source:       event.Source,
			Timestamp:    time.Now(),
		}
		_ = result
	}
}
