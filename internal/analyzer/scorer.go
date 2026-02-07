package analyzer

import (
	"fmt"
	"math"
	"strings"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// Scorer handles token scoring and recommendation generation.
type Scorer struct {
	logger *zerolog.Logger
	config AnalysisConfig
}

// NewScorer creates a new token scorer.
func NewScorer(config AnalysisConfig) *Scorer {
	logger := util.WithComponent("token_scorer")
	return &Scorer{
		logger: &logger,
		config: config,
	}
}

// CalculateScore calculates a comprehensive token score.
func (s *Scorer) CalculateScore(
	metadata TokenMetadata,
	liquidity LiquidityAnalysis,
	security SecurityAnalysis,
) TokenScore {
	score := TokenScore{}

	// Calculate individual scores
	score.MetadataScore = s.calculateMetadataScore(metadata)
	score.LiquidityScore = s.calculateLiquidityScore(liquidity)
	score.SecurityScore = s.calculateSecurityScore(security)
	score.SocialScore = s.calculateSocialScore(metadata, security)
	score.PotentialScore = s.calculatePotentialScore(metadata, liquidity, security)

	// Calculate overall score using weights
	weights := s.config.ScoreWeights
	score.OverallScore = (score.LiquidityScore*weights.Liquidity +
		score.SecurityScore*weights.Security +
		score.SocialScore*weights.Social +
		score.PotentialScore*weights.Potential)

	// Generate positive and negative factors
	score.PositiveFactors, score.NegativeFactors = s.generateFactors(
		metadata, liquidity, security, score)

	return score
}

// calculateMetadataScore calculates a score based on metadata completeness.
func (s *Scorer) calculateMetadataScore(metadata TokenMetadata) float64 {
	score := 0.0

	// Basic metadata (40 points)
	if metadata.Name != "" && len(metadata.Name) <= 50 {
		score += 15
	}
	if metadata.Symbol != "" && len(metadata.Symbol) <= 10 {
		score += 15
	}
	if metadata.Decimals > 0 && metadata.Decimals <= 18 {
		score += 10
	}

	// Supply information (20 points)
	if metadata.Supply != "" {
		supply, err := decimal.NewFromString(metadata.Supply)
		if err == nil && !supply.IsZero() && !supply.IsNegative() {
			score += 20
		}
	}

	// Metadata URI (10 points)
	if metadata.MetadataURI != "" {
		score += 10
	}

	// Contract verification for Base (10 points)
	if metadata.ContractVerified {
		score += 10
	}

	// Authority status for Solana (20 points)
	if metadata.MintAuthority != nil && *metadata.MintAuthority == "11111111111111111111111111111111" {
		score += 10
	}
	if metadata.FreezeAuthority != nil && *metadata.FreezeAuthority == "11111111111111111111111111111111" {
		score += 10
	}

	return math.Min(score, 100.0)
}

// calculateLiquidityScore calculates a score based on liquidity quality.
func (s *Scorer) calculateLiquidityScore(liquidity LiquidityAnalysis) float64 {
	return liquidity.DepthScore
}

// calculateSecurityScore calculates a score based on security analysis.
func (s *Scorer) calculateSecurityScore(security SecurityAnalysis) float64 {
	return security.SecurityScore
}

// calculateSocialScore calculates a score based on social media presence.
func (s *Scorer) calculateSocialScore(metadata TokenMetadata, security SecurityAnalysis) float64 {
	score := 0.0

	// Twitter (40 points)
	if metadata.Twitter != "" {
		score += 40
	}

	// Telegram (30 points)
	if metadata.Telegram != "" {
		score += 30
	}

	// Website (30 points)
	if metadata.Website != "" {
		score += 30
	}

	return math.Min(score, 100.0)
}

// calculatePotentialScore calculates a score based on potential for growth.
func (s *Scorer) calculatePotentialScore(
	metadata TokenMetadata,
	liquidity LiquidityAnalysis,
	security SecurityAnalysis,
) float64 {
	score := 50.0 // Base score

	// Bonus for high security
	switch security.RiskLevel {
	case "low":
		score += 20
	case "medium":
		score += 10
	}

	// Bonus for good liquidity
	if liquidity.DepthScore >= 70 {
		score += 15
	} else if liquidity.DepthScore >= 50 {
		score += 10
	}

	// Bonus for social presence
	if metadata.Twitter != "" && metadata.Telegram != "" {
		score += 10
	}

	// Bonus for complete metadata
	if metadata.Name != "" && metadata.Symbol != "" && metadata.Supply != "" {
		score += 5
	}

	return math.Min(score, 100.0)
}

// generateFactors generates positive and negative factors for the score.
func (s *Scorer) generateFactors(
	metadata TokenMetadata,
	liquidity LiquidityAnalysis,
	security SecurityAnalysis,
	score TokenScore,
) ([]string, []string) {
	positive := make([]string, 0)
	negative := make([]string, 0)

	// Metadata factors
	if metadata.Name != "" {
		positive = append(positive, "Has valid token name")
	}
	if metadata.Symbol != "" {
		positive = append(positive, "Has valid token symbol")
	}

	// Liquidity factors
	if liquidity.IsLocked {
		positive = append(positive, "Liquidity is locked")
	} else {
		negative = append(negative, "Liquidity is not locked")
	}
	if liquidity.BurnedLiquidity > 50 {
		positive = append(positive, "Majority of LP tokens burned")
	}

	// Security factors
	if security.MintAuthorityRevoked {
		positive = append(positive, "Mint authority revoked")
	} else if metadata.MintAuthority != nil {
		negative = append(negative, "Mint authority not revoked (risk of inflation)")
	}
	if security.FreezeAuthorityRevoked {
		positive = append(positive, "Freeze authority revoked")
	} else if metadata.FreezeAuthority != nil {
		negative = append(negative, "Freeze authority not revoked (risk of freeze)")
	}
	if security.Top10HolderConcentration < 0.3 {
		positive = append(positive, "Good holder distribution")
	} else {
		negative = append(negative, "High holder concentration")
	}

	// Social factors
	if security.SocialAccountsExist {
		positive = append(positive, "Has social media presence")
	} else {
		negative = append(negative, "No social media accounts")
	}

	// Honeypot check
	if !security.IsHoneypot {
		positive = append(positive, "Not a honeypot")
	} else {
		negative = append(negative, "Potential honeypot detected")
	}

	// Fee check
	if !security.HasHiddenFees {
		positive = append(positive, "No hidden transfer fees")
	} else {
		negative = append(negative, "Has hidden transfer fees")
	}

	return positive, negative
}

// GenerateRecommendation generates a trading recommendation.
func (s *Scorer) GenerateRecommendation(score TokenScore, security SecurityAnalysis) string {
	// Check for critical failures
	if security.IsHoneypot {
		return "warning"
	}
	if security.HasHiddenFees {
		return "warning"
	}
	if !security.LiquidityLocked && !security.LiquidityBurned {
		return "warning"
	}

	// Check score threshold
	if score.OverallScore >= 70 {
		return "buy"
	} else if score.OverallScore >= 50 {
		return "skip"
	}
	return "warning"
}

// ShouldAnalyze determines if a token should be analyzed based on pre-filter criteria.
func (s *Scorer) ShouldAnalyze(event *monitor.TokenEvent) bool {
	// Check if event has required fields
	if event.MintAddress == "" {
		return false
	}
	if event.LiquidityPoolAddress == "" {
		return false
	}

	// Check authority requirements for Solana
	if event.Chain == monitor.ChainTypeSolana {
		if s.config.RequireMintAuthorityRevoked && event.MintAuthority != nil &&
			*event.MintAuthority != "" && *event.MintAuthority != "11111111111111111111111111111111" {
			return false
		}
		if s.config.RequireFreezeAuthorityRevoked && event.FreezeAuthority != nil &&
			*event.FreezeAuthority != "" && *event.FreezeAuthority != "11111111111111111111111111111111" {
			return false
		}
	}

	return true
}

// CalculateScoreWithWeights calculates score with custom weights.
func (s *Scorer) CalculateScoreWithWeights(
	metadata TokenMetadata,
	liquidity LiquidityAnalysis,
	security SecurityAnalysis,
	weights ScoreWeights,
) TokenScore {
	score := s.CalculateScore(metadata, liquidity, security)

	// Recalculate overall score with custom weights
	score.OverallScore = (score.LiquidityScore*weights.Liquidity +
		score.SecurityScore*weights.Security +
		score.SocialScore*weights.Social +
		score.PotentialScore*weights.Potential)

	return score
}

// CompareTokens compares two tokens and returns the better one.
func (s *Scorer) CompareTokens(score1, score2 TokenScore) int {
	if score1.OverallScore > score2.OverallScore {
		return 1
	} else if score1.OverallScore < score2.OverallScore {
		return -1
	}
	return 0
}

// GetScoreExplanation returns a human-readable explanation of the score.
func (s *Scorer) GetScoreExplanation(score TokenScore) string {
	explanation := ""

	explanation += "Overall Score: " + formatFloat(score.OverallScore) + "/100\n"
	explanation += "  Metadata: " + formatFloat(score.MetadataScore) + "/100\n"
	explanation += "  Liquidity: " + formatFloat(score.LiquidityScore) + "/100\n"
	explanation += "  Security: " + formatFloat(score.SecurityScore) + "/100\n"
	explanation += "  Social: " + formatFloat(score.SocialScore) + "/100\n"
	explanation += "  Potential: " + formatFloat(score.PotentialScore) + "/100\n"

	if len(score.PositiveFactors) > 0 {
		explanation += "\nPositive Factors:\n"
		var explanationSb317 strings.Builder
		for _, factor := range score.PositiveFactors {
			explanationSb317.WriteString("  ✓ " + factor + "\n")
		}
		explanation += explanationSb317.String()
	}

	if len(score.NegativeFactors) > 0 {
		explanation += "\nNegative Factors:\n"
		var explanationSb324 strings.Builder
		for _, factor := range score.NegativeFactors {
			explanationSb324.WriteString("  ✗ " + factor + "\n")
		}
		explanation += explanationSb324.String()
	}

	return explanation
}

// IsScoreAboveThreshold checks if the score is above the minimum threshold.
func (s *Scorer) IsScoreAboveThreshold(score TokenScore) bool {
	// Calculate a threshold from config
	// Default threshold is 60 if not specified
	threshold := 60.0

	// If min score is specified in config, use it
	// This would be passed in the config
	return score.OverallScore >= threshold
}

// formatFloat formats a float to 2 decimal places.
func formatFloat(f float64) string {
	return fmt.Sprintf("%.2f", f)
}

// GetScoreRating returns a rating based on the score.
func (s *Scorer) GetScoreRating(score float64) string {
	if score >= 90 {
		return "Excellent"
	} else if score >= 80 {
		return "Very Good"
	} else if score >= 70 {
		return "Good"
	} else if score >= 60 {
		return "Fair"
	} else if score >= 50 {
		return "Poor"
	}
	return "Very Poor"
}

// EstimateFalsePositiveRate estimates the false positive rate for the scoring algorithm
// This would need to be calculated from historical data.
func (s *Scorer) EstimateFalsePositiveRate() float64 {
	// This would be calculated from actual trading results
	// For now, return a conservative estimate
	return 0.05 // 5% false positive rate
}

// OptimizeWeights optimizes the scoring weights based on historical performance.
func (s *Scorer) OptimizeWeights(historicalScores []TokenScore, actualOutcomes []bool) ScoreWeights {
	// This would use machine learning or statistical analysis
	// to optimize weights based on historical performance

	// For now, return the default weights
	return s.config.ScoreWeights
}
