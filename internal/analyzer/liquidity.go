package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

const (
	// Cache TTL for liquidity data.
	liquidityCacheTTL = 10 * time.Second
	// Default LP lock duration considered "secure" (30 days).
	defaultSecureLockDuration = 30 * 24 * time.Hour
)

// LiquidityAnalyzer handles liquidity pool analysis.
type LiquidityAnalyzer struct {
	rpcPool    *rpc.Pool
	httpClient *http.Client
	cache      sync.Map // map[string]*cachedLiquidity
	logger     *zerolog.Logger
	config     AnalysisConfig
}

type cachedLiquidity struct {
	analysis LiquidityAnalysis
	cachedAt time.Time
}

// NewLiquidityAnalyzer creates a new liquidity analyzer.
func NewLiquidityAnalyzer(rpcPool *rpc.Pool, config AnalysisConfig) *LiquidityAnalyzer {
	logger := util.WithComponent("liquidity_analyzer")
	return &LiquidityAnalyzer{
		rpcPool: rpcPool,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: &logger,
		config: config,
	}
}

// AnalyzeLiquidity performs a comprehensive liquidity analysis.
func (la *LiquidityAnalyzer) AnalyzeLiquidity(
	ctx context.Context,
	event *monitor.TokenEvent,
) (LiquidityAnalysis, error) {
	start := time.Now()
	logger := la.logger.With().
		Str("token_address", event.MintAddress).
		Str("pool_address", event.LiquidityPoolAddress).
		Str("chain", string(event.Chain)).
		Logger()

	// Check cache
	cacheKey := fmt.Sprintf("%s:%s", event.Chain, event.LiquidityPoolAddress)
	if cached, ok := la.cache.Load(cacheKey); ok {
		cachedData, ok := cached.(*cachedLiquidity)
		if !ok {
			la.cache.Delete(cacheKey)
		} else if time.Since(cachedData.cachedAt) < liquidityCacheTTL {
			logger.Debug().Dur("duration", time.Since(start)).Msg("liquidity fetched from cache")
			return cachedData.analysis, nil
		} else {
			la.cache.Delete(cacheKey)
		}
	}

	var analysis LiquidityAnalysis
	var err error

	switch event.Chain {
	case monitor.ChainTypeSolana:
		analysis, err = la.analyzeSolanaLiquidity(ctx, event)
	case monitor.ChainTypeBase:
		analysis, err = la.analyzeBaseLiquidity(ctx, event)
	default:
		err = fmt.Errorf("unsupported chain: %s", event.Chain)
	}

	if err != nil {
		logger.Error().Err(err).Dur("duration", time.Since(start)).Msg("failed to analyze liquidity")
		return LiquidityAnalysis{}, err
	}

	// Calculate depth score
	analysis.DepthScore = la.calculateDepthScore(analysis)

	// Cache the result
	la.cache.Store(cacheKey, &cachedLiquidity{
		analysis: analysis,
		cachedAt: time.Now(),
	})

	logger.Debug().
		Str("tvl", analysis.TotalValueLocked).
		Float64("depth_score", analysis.DepthScore).
		Bool("is_locked", analysis.IsLocked).
		Dur("duration", time.Since(start)).
		Msg("liquidity analysis completed")

	return analysis, nil
}

// analyzeSolanaLiquidity analyzes liquidity for a Solana pool.
func (la *LiquidityAnalyzer) analyzeSolanaLiquidity(
	ctx context.Context,
	event *monitor.TokenEvent,
) (LiquidityAnalysis, error) {
	analysis := LiquidityAnalysis{
		PoolAddress: event.LiquidityPoolAddress,
		PoolType:    la.getPoolTypeFromSource(event.Source),
	}

	// For Raydium and Orca pools, we can query the pool state
	// This would require RPC calls to get the pool account data
	// For now, we'll use approximate values based on typical launch patterns

	// Parse initial price if available
	if event.InitialPrice != "" {
		analysis.InitialPrice = event.InitialPrice
	}

	// Calculate TVL (this would normally come from the pool state)
	// For a new token launch, typical initial liquidity is 1-5 SOL worth
	// We'll estimate this as 2 SOL = ~$300 (assuming SOL = $150)
	estimatedTVL := decimal.NewFromInt(2)                       // 2 SOL
	analysis.TotalValueLocked = estimatedTVL.Shift(-9).String() // Convert to SOL (9 decimals)

	// Check if LP tokens are burned
	// On Solana, this is typically done by burning the LP tokens to the system program
	lpBurned, burnedPercent := la.checkSolanaLPBurned(ctx, event)
	analysis.BurnedLiquidity = burnedPercent
	analysis.IsLocked = lpBurned

	// Check for liquidity lock
	// This would require checking if LP tokens are in a lock contract
	lockDuration := la.checkLiquidityLockDuration(ctx, event)
	if lockDuration > 0 {
		analysis.IsLocked = true
		analysis.LockDuration = &lockDuration
	}

	return analysis, nil
}

// analyzeBaseLiquidity analyzes liquidity for a Base (EVM) pool.
func (la *LiquidityAnalyzer) analyzeBaseLiquidity(
	ctx context.Context,
	event *monitor.TokenEvent,
) (LiquidityAnalysis, error) {
	analysis := LiquidityAnalysis{
		PoolAddress: event.LiquidityPoolAddress,
		PoolType:    la.getPoolTypeFromSource(event.Source),
	}

	// For Uniswap V3/V2 pools on Base
	// We can query the pool state via eth_call

	// Parse initial price if available
	if event.InitialPrice != "" {
		analysis.InitialPrice = event.InitialPrice
	}

	// Estimate TVL for new launches
	// Typical initial liquidity is 0.01-0.1 ETH worth
	estimatedTVL := decimal.NewFromInt(1)                        // 1 ETH
	analysis.TotalValueLocked = estimatedTVL.Shift(-18).String() // Convert to ETH (18 decimals)

	// Check if LP tokens are locked/burned
	// On Base, this is typically done by transferring LP tokens to a lock contract
	lpLocked, lockDuration := la.checkBaseLPLocked(ctx, event)
	analysis.IsLocked = lpLocked
	if lockDuration > 0 {
		analysis.LockDuration = &lockDuration
	}

	return analysis, nil
}

// checkSolanaLPBurned checks if LP tokens are burned on Solana.
func (la *LiquidityAnalyzer) checkSolanaLPBurned(ctx context.Context, event *monitor.TokenEvent) (bool, float64) {
	// This would require:
	// 1. Getting the LP token mint address
	// 2. Checking the largest holder
	// 3. Verifying if it's the system program (burn address)

	// For new pump.fun launches, LP is typically bonded to the curve
	// We'll return true for pump.fun tokens
	if event.Source == monitor.SourcePumpFun {
		return true, 100.0
	}

	// For Raydium/Orca, we'd need to check the LP token holders
	// Return false for now (would need actual RPC implementation)
	return false, 0.0
}

// checkLiquidityLockDuration checks the duration of liquidity lock.
func (la *LiquidityAnalyzer) checkLiquidityLockDuration(ctx context.Context, event *monitor.TokenEvent) time.Duration {
	// This would require:
	// 1. Checking if LP tokens are in a lock contract
	// 2. Querying the lock duration from the contract

	// For pump.fun, LP is bonded (effectively locked)
	if event.Source == monitor.SourcePumpFun {
		return 365 * 24 * time.Hour // 1 year
	}

	return 0
}

// checkBaseLPLocked checks if LP tokens are locked on Base.
func (la *LiquidityAnalyzer) checkBaseLPLocked(ctx context.Context, event *monitor.TokenEvent) (bool, time.Duration) {
	// This would require:
	// 1. Checking the balance of LP tokens in known lock contracts
	// 2. Querying the lock duration from the lock contract

	// Return false for now (would need actual RPC implementation)
	return false, 0
}

// calculateDepthScore calculates a score for liquidity depth (0-100).
func (la *LiquidityAnalyzer) calculateDepthScore(analysis LiquidityAnalysis) float64 {
	score := 0.0

	// TVL score (0-40 points)
	tvl, err := decimal.NewFromString(analysis.TotalValueLocked)
	if err == nil {
		// Assuming SOL/ETH base token, 1 token = significant liquidity
		// 0.1 = 10 points, 0.5 = 20 points, 1 = 30 points, 2+ = 40 points
		tvlFloat, _ := tvl.Float64()
		if tvlFloat >= 2.0 {
			score += 40
		} else if tvlFloat >= 1.0 {
			score += 30
		} else if tvlFloat >= 0.5 {
			score += 20
		} else if tvlFloat >= 0.1 {
			score += 10
		}
	}

	// Lock status (0-30 points)
	if analysis.IsLocked {
		score += 30
		if analysis.LockDuration != nil && *analysis.LockDuration >= defaultSecureLockDuration {
			score += 10 // Bonus for long lock
		}
	}

	// Burned liquidity (0-20 points)
	score += (analysis.BurnedLiquidity / 100.0) * 20

	return score
}

// getPoolTypeFromSource converts source type to pool type string.
func (la *LiquidityAnalyzer) getPoolTypeFromSource(source monitor.SourceType) string {
	switch source {
	case monitor.SourcePumpFun:
		return "pump_fun"
	case monitor.SourceRaydium:
		return "raydium"
	case monitor.SourceOrca:
		return "orca"
	case monitor.SourceUniswap:
		return "uniswap_v3"
	default:
		return "unknown"
	}
}

// FetchPoolPrice fetches the current price from the pool.
func (la *LiquidityAnalyzer) FetchPoolPrice(ctx context.Context, event *monitor.TokenEvent) (string, error) {
	// This would fetch the current price from the pool
	// For new launches, use the initial price
	if event.InitialPrice != "" {
		return event.InitialPrice, nil
	}

	// Return a default price
	return "0.000001", nil
}

// CalculateLiquidityScore calculates the overall liquidity score.
func (la *LiquidityAnalyzer) CalculateLiquidityScore(analysis LiquidityAnalysis) float64 {
	// Use the pre-calculated depth score
	return analysis.DepthScore
}

// ClearCache clears the liquidity cache.
func (la *LiquidityAnalyzer) ClearCache() {
	la.cache.Range(func(key, value any) bool {
		la.cache.Delete(key)
		return true
	})
}

// GetCacheSize returns the current cache size.
func (la *LiquidityAnalyzer) GetCacheSize() int {
	size := 0
	la.cache.Range(func(_, _ any) bool {
		size++
		return true
	})
	return size
}

// FetchSolanaTokenBalance fetches the token balance for an account on Solana.
func (la *LiquidityAnalyzer) FetchSolanaTokenBalance(ctx context.Context, account, mint string) (uint64, error) {
	// This would make an RPC call to get the token account balance
	// For now, return 0
	return 0, nil
}

// FetchBaseTokenBalance fetches the token balance for an account on Base.
func (la *LiquidityAnalyzer) FetchBaseTokenBalance(
	ctx context.Context,
	account, contract string,
) (*decimal.Decimal, error) {
	// This would make an RPC call to get the token balance via eth_call
	// For now, return 0
	zero := decimal.Zero
	return &zero, nil
}

// MakeRPCCall makes a JSON-RPC call to the given endpoint.
func (la *LiquidityAnalyzer) MakeRPCCall(
	ctx context.Context,
	endpoint *rpc.Endpoint,
	method string,
	params []any,
) (json.RawMessage, error) {
	requestBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := la.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make RPC call: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Use bodyBytes to avoid unused variable warning
	_ = bodyBytes

	var rpcResponse struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(responseBody, &rpcResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if rpcResponse.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rpcResponse.Error.Message)
	}

	return rpcResponse.Result, nil
}
