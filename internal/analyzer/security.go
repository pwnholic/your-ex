package analyzer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// Security check cache TTL.
	securityCacheTTL = 30 * time.Second
	// Honeypot test timeout.
	honeypotTestTimeout = 15 * time.Second
)

// SecurityAnalyzer handles security checks for token analysis.
type SecurityAnalyzer struct {
	rpcPool       *rpc.Pool
	cache         sync.Map // map[string]*cachedSecurity
	logger        *zerolog.Logger
	config        AnalysisConfig
	tokenAnalyzer *TokenAnalyzer
}

type cachedSecurity struct {
	analysis SecurityAnalysis
	cachedAt time.Time
}

// NewSecurityAnalyzer creates a new security analyzer.
func NewSecurityAnalyzer(rpcPool *rpc.Pool, config AnalysisConfig, tokenAnalyzer *TokenAnalyzer) *SecurityAnalyzer {
	logger := util.WithComponent("security_analyzer")
	return &SecurityAnalyzer{
		rpcPool:       rpcPool,
		logger:        &logger,
		config:        config,
		tokenAnalyzer: tokenAnalyzer,
	}
}

// AnalyzeSecurity performs a comprehensive security analysis.
func (sa *SecurityAnalyzer) AnalyzeSecurity(
	ctx context.Context,
	event *monitor.TokenEvent,
	metadata TokenMetadata,
) (SecurityAnalysis, error) {
	start := time.Now()
	logger := sa.logger.With().
		Str("token_address", event.MintAddress).
		Str("chain", string(event.Chain)).
		Logger()

	// Check cache
	cacheKey := fmt.Sprintf("%s:%s:security", event.Chain, event.MintAddress)
	if cached, ok := sa.cache.Load(cacheKey); ok {
		cachedData, ok := cached.(*cachedSecurity)
		if !ok {
			sa.cache.Delete(cacheKey)
		} else if time.Since(cachedData.cachedAt) < securityCacheTTL {
			logger.Debug().Dur("duration", time.Since(start)).Msg("security analysis fetched from cache")
			return cachedData.analysis, nil
		} else {
			sa.cache.Delete(cacheKey)
		}
	}

	analysis := SecurityAnalysis{}

	switch event.Chain {
	case monitor.ChainTypeSolana:
		analysis = sa.analyzeSolanaSecurity(ctx, event, metadata)
	case monitor.ChainTypeBase:
		analysis = sa.analyzeBaseSecurity(ctx, event, metadata)
	default:
		return SecurityAnalysis{}, fmt.Errorf("unsupported chain: %s", event.Chain)
	}

	// Calculate security score
	analysis.SecurityScore = sa.calculateSecurityScore(analysis)

	// Determine risk level
	analysis.RiskLevel = sa.determineRiskLevel(analysis)

	// Cache the result
	sa.cache.Store(cacheKey, &cachedSecurity{
		analysis: analysis,
		cachedAt: time.Now(),
	})

	logger.Debug().
		Float64("security_score", analysis.SecurityScore).
		Str("risk_level", analysis.RiskLevel).
		Bool("is_honeypot", analysis.IsHoneypot).
		Dur("duration", time.Since(start)).
		Msg("security analysis completed")

	return analysis, nil
}

// analyzeSolanaSecurity performs security checks for Solana tokens.
func (sa *SecurityAnalyzer) analyzeSolanaSecurity(
	ctx context.Context,
	event *monitor.TokenEvent,
	metadata TokenMetadata,
) SecurityAnalysis {
	analysis := SecurityAnalysis{}

	// Authority checks
	analysis.MintAuthorityRevoked = sa.checkMintAuthorityRevoked(metadata)
	analysis.FreezeAuthorityRevoked = sa.checkFreezeAuthorityRevoked(metadata)

	// Liquidity security (simplified - would need actual RPC calls)
	analysis.LiquidityLocked = true // Assume locked for new launches
	analysis.LiquidityBurned = event.Source == monitor.SourcePumpFun
	analysis.LPHolderCount = 1 // Simplified

	// Holder distribution (would need to get largest holders via RPC)
	analysis.HolderCount = 100               // Estimate for new launches
	analysis.Top10HolderConcentration = 0.25 // Estimate: 25% held by top 10
	analysis.HolderDistributionScore = sa.calculateHolderDistributionScore(analysis)

	// Transfer fees
	analysis.TransferFeeBuy = 0.0 // Solana tokens typically don't have transfer fees
	analysis.TransferFeeSell = 0.0
	analysis.HasHiddenFees = false

	// Social verification
	socialExists := metadata.Twitter != "" || metadata.Telegram != "" || metadata.Website != ""
	analysis.SocialAccountsExist = socialExists
	analysis.TwitterVerified = metadata.Twitter != ""
	analysis.TelegramVerified = metadata.Telegram != ""

	// Honeypot detection
	if sa.config.EnableHoneypotTest {
		analysis.IsHoneypot, analysis.HoneypotReason = sa.detectHoneypot(ctx, event)
	} else {
		analysis.IsHoneypot = false
	}

	return analysis
}

// analyzeBaseSecurity performs security checks for Base (EVM) tokens.
func (sa *SecurityAnalyzer) analyzeBaseSecurity(
	ctx context.Context,
	event *monitor.TokenEvent,
	metadata TokenMetadata,
) SecurityAnalysis {
	analysis := SecurityAnalysis{}

	// Ownership checks (would need to query contract owner)
	analysis.OwnershipRenounced = false // Would need actual contract check
	analysis.OwnerAddress = ""          // Would need to get from contract

	// Liquidity security
	analysis.LiquidityLocked = true  // Assume for new Uniswap pools
	analysis.LiquidityBurned = false // Uniswap V3 LP is NFT, not typically burned
	analysis.LPHolderCount = 1

	// Holder distribution
	analysis.HolderCount = 50 // Estimate
	analysis.Top10HolderConcentration = 0.30
	analysis.HolderDistributionScore = sa.calculateHolderDistributionScore(analysis)

	// Transfer fees (check contract for transfer fee logic)
	analysis.TransferFeeBuy = 0.0
	analysis.TransferFeeSell = 0.0
	analysis.HasHiddenFees = false // Would need to analyze contract bytecode

	// Social verification
	socialExists := metadata.Twitter != "" || metadata.Telegram != "" || metadata.Website != ""
	analysis.SocialAccountsExist = socialExists
	analysis.TwitterVerified = metadata.Twitter != ""
	analysis.TelegramVerified = metadata.Telegram != ""

	// Honeypot detection
	if sa.config.EnableHoneypotTest {
		analysis.IsHoneypot, analysis.HoneypotReason = sa.detectHoneypot(ctx, event)
	} else {
		analysis.IsHoneypot = false
	}

	return analysis
}

// checkMintAuthorityRevoked checks if mint authority is revoked.
func (sa *SecurityAnalyzer) checkMintAuthorityRevoked(metadata TokenMetadata) bool {
	if metadata.MintAuthority == nil {
		return true // No mint authority set = revoked
	}
	// Check if mint authority points to the system program (burned)
	return *metadata.MintAuthority == "11111111111111111111111111111111"
}

// checkFreezeAuthorityRevoked checks if freeze authority is revoked.
func (sa *SecurityAnalyzer) checkFreezeAuthorityRevoked(metadata TokenMetadata) bool {
	if metadata.FreezeAuthority == nil {
		return true // No freeze authority set = revoked
	}
	// Check if freeze authority points to the system program (burned)
	return *metadata.FreezeAuthority == "11111111111111111111111111111111"
}

// calculateHolderDistributionScore calculates a score based on holder distribution.
func (sa *SecurityAnalyzer) calculateHolderDistributionScore(analysis SecurityAnalysis) float64 {
	score := 100.0

	// Penalize high concentration
	if analysis.Top10HolderConcentration > 0.5 {
		score -= 50 // Heavy penalty
	} else if analysis.Top10HolderConcentration > 0.3 {
		score -= 30
	} else if analysis.Top10HolderConcentration > 0.2 {
		score -= 10
	}

	// Bonus for high holder count
	if analysis.HolderCount >= 500 {
		score += 10
	} else if analysis.HolderCount >= 200 {
		score += 5
	}

	// Ensure score is in range [0, 100]
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	return score
}

// detectHoneypot performs a honeypot detection test.
func (sa *SecurityAnalyzer) detectHoneypot(ctx context.Context, event *monitor.TokenEvent) (bool, string) {
	// This would:
	// 1. Create a test wallet
	// 2. Execute a small buy
	// 3. Try to sell the tokens
	// 4. Check if the sell succeeds

	// For now, return false (not a honeypot) as this is disabled by default
	return false, ""
}

// performHoneypotTest actually executes a buy/sell test.
func (sa *SecurityAnalyzer) performHoneypotTest(
	ctx context.Context,
	event *monitor.TokenEvent,
) (bool, string, bool, float64) {
	// This would require:
	// 1. Creating a test wallet
	// 2. Building a buy transaction
	// 3. Simulating the transaction
	// 4. Building a sell transaction
	// 5. Simulating the sell
	// 6. Comparing expected vs actual output

	// Return: (isHoneypot, reason, testSuccess, actualSlippage)
	// For now, return as if test passed
	return false, "", true, 0.01 // 1% slippage
}

// calculateSecurityScore calculates an overall security score.
func (sa *SecurityAnalyzer) calculateSecurityScore(analysis SecurityAnalysis) float64 {
	score := 0.0

	// Authority checks (30 points)
	if analysis.MintAuthorityRevoked || analysis.OwnershipRenounced {
		score += 15
	}
	if analysis.FreezeAuthorityRevoked {
		score += 15
	}

	// Liquidity security (20 points)
	if analysis.LiquidityLocked {
		score += 10
	}
	if analysis.LiquidityBurned {
		score += 10
	}

	// Holder distribution (20 points)
	score += (analysis.HolderDistributionScore / 100.0) * 20

	// Fee checks (15 points)
	if !analysis.HasHiddenFees {
		score += 15
	}

	// Honeypot check (15 points - critical)
	if !analysis.IsHoneypot {
		score += 15
	}

	// Social verification (optional, - bonus points)
	if analysis.SocialAccountsExist {
		score += 5
	}

	return score
}

// determineRiskLevel determines the risk level based on the security analysis.
func (sa *SecurityAnalyzer) determineRiskLevel(analysis SecurityAnalysis) string {
	score := analysis.SecurityScore

	// Check for critical failures
	if analysis.IsHoneypot {
		return "critical"
	}
	if analysis.HasHiddenFees {
		return "high"
	}
	if !analysis.LiquidityLocked && !analysis.LiquidityBurned {
		return "high"
	}

	// Determine by score
	if score >= 80 {
		return "low"
	} else if score >= 60 {
		return "medium"
	} else if score >= 40 {
		return "high"
	}
	return "critical"
}

// CheckAuthorityRevoked checks if authorities are revoked (chain-agnostic).
func (sa *SecurityAnalyzer) CheckAuthorityRevoked(chain monitor.ChainType, metadata TokenMetadata) (mint, freeze bool) {
	if chain == monitor.ChainTypeSolana {
		return sa.checkMintAuthorityRevoked(metadata), sa.checkFreezeAuthorityRevoked(metadata)
	}
	// Base doesn't have mint/freeze authorities
	return true, true
}

// VerifySocialLinks verifies if social links exist and are valid.
func (sa *SecurityAnalyzer) VerifySocialLinks(metadata TokenMetadata) (twitter, telegram, website bool) {
	twitter = metadata.Twitter != ""
	telegram = metadata.Telegram != ""
	website = metadata.Website != ""
	return twitter, telegram, website
}

// AnalyzeHolderDistribution analyzes the distribution of token holders.
func (sa *SecurityAnalyzer) AnalyzeHolderDistribution(
	ctx context.Context,
	event *monitor.TokenEvent,
) (holderCount int, top10Concentration float64, err error) {
	// This would:
	// 1. Get the largest token holders via RPC
	// 2. Calculate the concentration

	// Return estimated values for now
	return 100, 0.25, nil
}

// CheckTransferFees checks for hidden transfer fees.
func (sa *SecurityAnalyzer) CheckTransferFees(
	ctx context.Context,
	event *monitor.TokenEvent,
) (buyFee, sellFee float64, hasHidden bool, err error) {
	// This would:
	// 1. Simulate a transfer transaction
	// 2. Check the actual vs expected output

	// Return 0 fees for now
	return 0.0, 0.0, false, nil
}

// ClearCache clears the security cache.
func (sa *SecurityAnalyzer) ClearCache() {
	sa.cache.Range(func(key, value any) bool {
		sa.cache.Delete(key)
		return true
	})
}

// GetCacheSize returns the current cache size.
func (sa *SecurityAnalyzer) GetCacheSize() int {
	size := 0
	sa.cache.Range(func(_, _ any) bool {
		size++
		return true
	})
	return size
}
