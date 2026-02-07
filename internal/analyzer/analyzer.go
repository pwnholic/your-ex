package analyzer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

// Analyzer is the main coordinator for token analysis.
type Analyzer struct {
	tokenAnalyzer     *TokenAnalyzer
	liquidityAnalyzer *LiquidityAnalyzer
	securityAnalyzer  *SecurityAnalyzer
	scorer            *Scorer
	config            AnalysisConfig
	logger            *zerolog.Logger
	analysisCache     sync.Map // map[string]*CachedAnalysis
	mu                sync.RWMutex
	stats             AnalyzerStats
}

// AnalyzerStats holds statistics about the analyzer.
type AnalyzerStats struct {
	TotalAnalyzed   int64
	AnalyzedPassed  int64
	AnalyzedFailed  int64
	AverageDuration time.Duration
	LastAnalyzedAt  time.Time
}

// NewAnalyzer creates a new token analyzer.
func NewAnalyzer(rpcPool *rpc.Pool, config AnalysisConfig) *Analyzer {
	logger := util.WithComponent("analyzer")

	tokenAnalyzer := NewTokenAnalyzer(rpcPool, config)
	liquidityAnalyzer := NewLiquidityAnalyzer(rpcPool, config)
	securityAnalyzer := NewSecurityAnalyzer(rpcPool, config, tokenAnalyzer)
	scorer := NewScorer(config)

	return &Analyzer{
		tokenAnalyzer:     tokenAnalyzer,
		liquidityAnalyzer: liquidityAnalyzer,
		securityAnalyzer:  securityAnalyzer,
		scorer:            scorer,
		config:            config,
		logger:            &logger,
	}
}

// AnalyzeToken performs a complete analysis of a token event.
func (a *Analyzer) AnalyzeToken(ctx context.Context, event *monitor.TokenEvent) (*AnalysisResult, error) {
	start := time.Now()
	logger := a.logger.With().
		Str("token_address", event.MintAddress).
		Str("chain", string(event.Chain)).
		Str("source", string(event.Source)).
		Logger()

	logger.Info().Msg("starting token analysis")

	// Check if we should analyze this token
	if !a.scorer.ShouldAnalyze(event) {
		logger.Debug().Msg("token does not meet pre-filter criteria")
		a.updateStats(false, time.Since(start))
		return nil, errors.New("token does not meet pre-filter criteria")
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, a.config.AnalysisTimeout)
	defer cancel()

	result := &AnalysisResult{
		TokenAddress: event.MintAddress,
		Chain:        event.Chain,
		Source:       event.Source,
		Timestamp:    time.Now(),
		Errors:       make([]string, 0),
	}

	// Step 1: Fetch metadata
	logger.Debug().Msg("fetching token metadata")
	metadata, err := a.tokenAnalyzer.FetchMetadata(ctx, event)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("metadata fetch failed: %s", err))
		logger.Error().Err(err).Msg("failed to fetch metadata")
	}
	result.Metadata = metadata

	// Validate metadata
	if valid, issues := a.tokenAnalyzer.ValidateMetadata(metadata); !valid {
		result.Errors = append(result.Errors, issues...)
		logger.Warn().Strs("issues", issues).Msg("metadata validation failed")
	}

	// Step 2: Analyze liquidity
	logger.Debug().Msg("analyzing liquidity")
	liquidity, err := a.liquidityAnalyzer.AnalyzeLiquidity(ctx, event)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("liquidity analysis failed: %s", err))
		logger.Error().Err(err).Msg("failed to analyze liquidity")
	}
	result.Liquidity = liquidity

	// Step 3: Security analysis
	logger.Debug().Msg("performing security analysis")
	security, err := a.securityAnalyzer.AnalyzeSecurity(ctx, event, metadata)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("security analysis failed: %s", err))
		logger.Error().Err(err).Msg("failed to perform security analysis")
	}
	result.Security = security

	// Step 4: Calculate score
	logger.Debug().Msg("calculating token score")
	result.Score = a.scorer.CalculateScore(metadata, liquidity, security)

	// Step 5: Generate recommendation
	result.Recommendation = a.scorer.GenerateRecommendation(result.Score, security)

	// Record analysis duration
	result.AnalysisDuration = time.Since(start)

	// Update stats
	a.updateStats(true, result.AnalysisDuration)

	// Log result
	logger.Info().
		Float64("overall_score", result.Score.OverallScore).
		Str("recommendation", result.Recommendation).
		Str("risk_level", result.Security.RiskLevel).
		Dur("duration", result.AnalysisDuration).
		Msg("token analysis completed")

	// Log score explanation
	resultExplanation := a.scorer.GetScoreExplanation(result.Score)
	logger.Debug().Msg(resultExplanation)

	return result, nil
}

// AnalyzeTokenAsync analyzes a token asynchronously.
func (a *Analyzer) AnalyzeTokenAsync(
	ctx context.Context,
	event *monitor.TokenEvent,
) (<-chan *AnalysisResult, <-chan error) {
	resultChan := make(chan *AnalysisResult, 1)
	errorChan := make(chan error, 1)

	go func() {
		defer close(resultChan)
		defer close(errorChan)

		result, err := a.AnalyzeToken(ctx, event)
		if err != nil {
			errorChan <- err
			return
		}
		resultChan <- result
	}()

	return resultChan, errorChan
}

// AnalyzeTokenBatch analyzes multiple tokens in parallel.
func (a *Analyzer) AnalyzeTokenBatch(ctx context.Context, events []*monitor.TokenEvent) ([]*AnalysisResult, []error) {
	results := make([]*AnalysisResult, len(events))
	errors := make([]error, len(events))

	var wg sync.WaitGroup
	for i, event := range events {
		wg.Add(1)
		go func(idx int, evt *monitor.TokenEvent) {
			defer wg.Done()

			result, err := a.AnalyzeToken(ctx, evt)
			if err != nil {
				errors[idx] = err
				return
			}
			results[idx] = result
		}(i, event)
	}
	wg.Wait()

	return results, errors
}

// ShouldBuy determines if a token should be bought based on analysis.
func (a *Analyzer) ShouldBuy(result *AnalysisResult) bool {
	if result == nil {
		return false
	}

	// Check recommendation
	if result.Recommendation != "buy" {
		return false
	}

	// Check for critical issues
	if result.Security.IsHoneypot {
		return false
	}
	if result.Security.HasHiddenFees {
		return false
	}
	if !result.Security.LiquidityLocked && !result.Security.LiquidityBurned {
		return false
	}

	// Check score threshold
	if !a.scorer.IsScoreAboveThreshold(result.Score) {
		return false
	}

	return true
}

// GetStats returns the analyzer statistics.
func (a *Analyzer) GetStats() AnalyzerStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stats
}

// updateStats updates the analyzer statistics.
func (a *Analyzer) updateStats(analyzed bool, duration time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.stats.TotalAnalyzed++
	if analyzed {
		a.stats.AnalyzedPassed++
	} else {
		a.stats.AnalyzedFailed++
	}

	// Update average duration
	if a.stats.TotalAnalyzed == 1 {
		a.stats.AverageDuration = duration
	} else {
		a.stats.AverageDuration = (a.stats.AverageDuration*time.Duration(a.stats.TotalAnalyzed-1) + duration) / time.Duration(
			a.stats.TotalAnalyzed,
		)
	}

	a.stats.LastAnalyzedAt = time.Now()
}

// ClearCache clears all analyzer caches.
func (a *Analyzer) ClearCache() {
	a.tokenAnalyzer.ClearCache()
	a.liquidityAnalyzer.ClearCache()
	a.securityAnalyzer.ClearCache()
	a.analysisCache.Range(func(key, value any) bool {
		a.analysisCache.Delete(key)
		return true
	})

	a.logger.Info().Msg("all analyzer caches cleared")
}

// GetCacheSize returns the total cache size.
func (a *Analyzer) GetCacheSize() int {
	total := a.tokenAnalyzer.GetCacheSize()
	total += a.liquidityAnalyzer.GetCacheSize()
	total += a.securityAnalyzer.GetCacheSize()

	size := 0
	a.analysisCache.Range(func(_, _ any) bool {
		size++
		return true
	})
	total += size

	return total
}

// UpdateConfig updates the analyzer configuration.
func (a *Analyzer) UpdateConfig(config AnalysisConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.config = config
	a.logger.Info().Msg("analyzer configuration updated")
}

// GetConfig returns the current analyzer configuration.
func (a *Analyzer) GetConfig() AnalysisConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config
}

// Shutdown gracefully shuts down the analyzer.
func (a *Analyzer) Shutdown(ctx context.Context) error {
	a.logger.Info().Msg("shutting down analyzer")

	// Clear caches
	a.ClearCache()

	// Log final stats
	a.logger.Info().
		Int64("total_analyzed", a.stats.TotalAnalyzed).
		Int64("analyzed_passed", a.stats.AnalyzedPassed).
		Int64("analyzed_failed", a.stats.AnalyzedFailed).
		Dur("average_duration", a.stats.AverageDuration).
		Msg("analyzer shutdown complete")

	return nil
}

// GetTokenScoreExplanation returns a detailed explanation of a token's score.
func (a *Analyzer) GetTokenScoreExplanation(result *AnalysisResult) string {
	return a.scorer.GetScoreExplanation(result.Score)
}

// CompareTokenScores compares two analysis results.
func (a *Analyzer) CompareTokenScores(result1, result2 *AnalysisResult) int {
	return a.scorer.CompareTokens(result1.Score, result2.Score)
}

// Implement EventHandler interface to integrate with monitor.
func (a *Analyzer) HandleTokenEvent(event *monitor.TokenEvent) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.config.AnalysisTimeout)
	defer cancel()

	result, err := a.AnalyzeToken(ctx, event)
	if err != nil {
		a.logger.Error().Err(err).
			Str("token_address", event.MintAddress).
			Msg("failed to handle token event")
		return err
	}

	// Log the result
	if a.ShouldBuy(result) {
		a.logger.Info().
			Str("token_address", event.MintAddress).
			Str("token_symbol", result.Metadata.Symbol).
			Float64("score", result.Score.OverallScore).
			Msg("token passed analysis - BUY SIGNAL")
	} else {
		a.logger.Debug().
			Str("token_address", event.MintAddress).
			Str("token_symbol", result.Metadata.Symbol).
			Float64("score", result.Score.OverallScore).
			Str("recommendation", result.Recommendation).
			Msg("token did not pass analysis")
	}

	return nil
}

// OnError handles errors from the monitor.
func (a *Analyzer) OnError(err error) {
	a.logger.Error().Err(err).Msg("analyzer received error from monitor")
}

// GetScoreRating returns a human-readable rating for a score.
func (a *Analyzer) GetScoreRating(score float64) string {
	return a.scorer.GetScoreRating(score)
}

// EstimateFalsePositiveRate returns the estimated false positive rate.
func (a *Analyzer) EstimateFalsePositiveRate() float64 {
	return a.scorer.EstimateFalsePositiveRate()
}
