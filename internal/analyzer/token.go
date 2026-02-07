package analyzer

import (
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
	// Solana Metaplex Metadata program ID.
	metaplexMetadataProgram = "metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s"
	// Cache TTL for token metadata.
	metadataCacheTTL = 5 * time.Minute
)

// TokenAnalyzer handles token metadata analysis.
type TokenAnalyzer struct {
	rpcPool    *rpc.Pool
	httpClient *http.Client
	cache      sync.Map // map[string]*cachedMetadata
	logger     *zerolog.Logger
	config     AnalysisConfig
}

type cachedMetadata struct {
	metadata TokenMetadata
	cachedAt time.Time
}

// NewTokenAnalyzer creates a new token metadata analyzer.
func NewTokenAnalyzer(rpcPool *rpc.Pool, config AnalysisConfig) *TokenAnalyzer {
	logger := util.WithComponent("token_analyzer")
	return &TokenAnalyzer{
		rpcPool: rpcPool,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: &logger,
		config: config,
	}
}

// FetchMetadata fetches token metadata for the given token event.
func (ta *TokenAnalyzer) FetchMetadata(ctx context.Context, event *monitor.TokenEvent) (TokenMetadata, error) {
	start := time.Now()
	logger := ta.logger.With().
		Str("token_address", event.MintAddress).
		Str("chain", string(event.Chain)).
		Logger()

	// Check cache
	cacheKey := fmt.Sprintf("%s:%s", event.Chain, event.MintAddress)
	if cached, ok := ta.cache.Load(cacheKey); ok {
		cachedData, ok := cached.(*cachedMetadata)
		if !ok {
			ta.cache.Delete(cacheKey)
		} else if time.Since(cachedData.cachedAt) < metadataCacheTTL {
			logger.Debug().Dur("duration", time.Since(start)).Msg("metadata fetched from cache")
			return cachedData.metadata, nil
		} else {
			ta.cache.Delete(cacheKey)
		}
	}

	var metadata TokenMetadata
	var err error

	switch event.Chain {
	case monitor.ChainTypeSolana:
		metadata, err = ta.fetchSolanaMetadata(ctx, event)
	case monitor.ChainTypeBase:
		metadata, err = ta.fetchBaseMetadata(ctx, event)
	default:
		err = fmt.Errorf("unsupported chain: %s", event.Chain)
	}

	if err != nil {
		logger.Error().Err(err).Dur("duration", time.Since(start)).Msg("failed to fetch metadata")
		return TokenMetadata{}, err
	}

	// Cache the result
	ta.cache.Store(cacheKey, &cachedMetadata{
		metadata: metadata,
		cachedAt: time.Now(),
	})

	logger.Debug().
		Str("name", metadata.Name).
		Str("symbol", metadata.Symbol).
		Dur("duration", time.Since(start)).
		Msg("metadata fetched successfully")

	return metadata, nil
}

// fetchSolanaMetadata fetches metadata for a Solana token.
func (ta *TokenAnalyzer) fetchSolanaMetadata(ctx context.Context, event *monitor.TokenEvent) (TokenMetadata, error) {
	metadata := TokenMetadata{
		ContractAddress: event.MintAddress,
	}

	// Get mint account info
	if ta.rpcPool == nil {
		// Return basic metadata from event without RPC call
		metadata.Name = event.TokenName
		metadata.Symbol = event.TokenSymbol
		metadata.Decimals = event.TokenDecimals
		metadata.MetadataURI = event.TokenMetadataURI
		return metadata, nil
	}

	endpoint, err := ta.rpcPool.GetEndpoint()
	if err != nil {
		return metadata, fmt.Errorf("failed to get RPC endpoint: %w", err)
	}

	// Parse supply
	if event.Supply != "" {
		supply, err := decimal.NewFromString(event.Supply)
		if err == nil {
			// Adjust for decimals
			supplyAdjusted := supply.Shift(int32(-event.TokenDecimals))
			metadata.Supply = supplyAdjusted.String()
		}
	}

	// Store authority information from event
	if event.MintAuthority != nil {
		metadata.MintAuthority = event.MintAuthority
	}
	if event.FreezeAuthority != nil {
		metadata.FreezeAuthority = event.FreezeAuthority
	}

	// Set basic info from event
	metadata.Name = event.TokenName
	metadata.Symbol = event.TokenSymbol
	metadata.Decimals = event.TokenDecimals
	metadata.MetadataURI = event.TokenMetadataURI

	// Fetch Metaplex metadata if URI is available
	if event.TokenMetadataURI != "" {
		if metaplexData, err := ta.fetchMetaplexMetadata(ctx, event.TokenMetadataURI); err == nil {
			metadata.Name = metaplexData.Name
			metadata.Symbol = metaplexData.Symbol
			if metaplexData.Extensions != nil {
				metadata.Twitter = metaplexData.Extensions.Twitter
				metadata.Telegram = metaplexData.Extensions.Telegram
				metadata.Website = metaplexData.Extensions.Website
			}
		}
	}

	// Fallback: try to derive metadata PDA and fetch on-chain
	if metadata.Name == "" || metadata.Symbol == "" {
		if onChainMetadata, err := ta.fetchOnChainMetadata(ctx, endpoint, event.MintAddress); err == nil {
			if metadata.Name == "" {
				metadata.Name = onChainMetadata.Name
			}
			if metadata.Symbol == "" {
				metadata.Symbol = onChainMetadata.Symbol
			}
			if metadata.Twitter == "" && onChainMetadata.Extensions != nil {
				metadata.Twitter = onChainMetadata.Extensions.Twitter
				metadata.Telegram = onChainMetadata.Extensions.Telegram
				metadata.Website = onChainMetadata.Extensions.Website
			}
		}
	}

	return metadata, nil
}

// fetchBaseMetadata fetches metadata for a Base (EVM) token.
func (ta *TokenAnalyzer) fetchBaseMetadata(ctx context.Context, event *monitor.TokenEvent) (TokenMetadata, error) {
	metadata := TokenMetadata{
		ContractAddress:  event.MintAddress,
		ContractVerified: true, // Assume verified if detected on Uniswap
	}

	// Set basic info from event
	metadata.Name = event.TokenName
	metadata.Symbol = event.TokenSymbol
	metadata.Decimals = event.TokenDecimals

	// For Base tokens, we'd need to make an eth_call to get the token info
	// This requires a properly formatted RPC request with ABI encoding
	// For now, return the metadata from the event

	return metadata, nil
}

// fetchMetaplexMetadata fetches metadata from a Metaplex metadata URI.
func (ta *TokenAnalyzer) fetchMetaplexMetadata(ctx context.Context, uri string) (*MetaplexMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := ta.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var metadata MetaplexMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &metadata, nil
}

// fetchOnChainMetadata fetches on-chain Metaplex metadata by deriving the metadata PDA.
func (ta *TokenAnalyzer) fetchOnChainMetadata(
	ctx context.Context,
	endpoint *rpc.Endpoint,
	mintAddress string,
) (*MetaplexMetadata, error) {
	// Derive metadata PDA
	// This would require borsh deserialization and PDA derivation
	// For now, return empty metadata as this is a placeholder
	return &MetaplexMetadata{}, nil
}

// ValidateMetadata checks if token metadata meets minimum requirements.
func (ta *TokenAnalyzer) ValidateMetadata(metadata TokenMetadata) (bool, []string) {
	var issues []string

	// Check for name and symbol
	if metadata.Name == "" {
		issues = append(issues, "token name is empty")
	}
	if metadata.Symbol == "" {
		issues = append(issues, "token symbol is empty")
	}

	// Check for reasonable supply
	if metadata.Supply != "" {
		supply, err := decimal.NewFromString(metadata.Supply)
		if err != nil {
			issues = append(issues, "invalid token supply format")
		} else if supply.IsZero() || supply.IsNegative() {
			issues = append(issues, "token supply is zero or negative")
		}
	}

	// Check for suspicious names/symbols
	if len(metadata.Name) > 100 {
		issues = append(issues, "token name is suspiciously long")
	}
	if len(metadata.Symbol) > 20 {
		issues = append(issues, "token symbol is suspiciously long")
	}

	return len(issues) == 0, issues
}

// CalculateMetadataScore calculates a score based on metadata completeness.
func (ta *TokenAnalyzer) CalculateMetadataScore(metadata TokenMetadata) float64 {
	score := 0.0

	// Name and symbol (30 points)
	if metadata.Name != "" {
		score += 15
	}
	if metadata.Symbol != "" {
		score += 15
	}

	// Metadata URI (10 points)
	if metadata.MetadataURI != "" {
		score += 10
	}

	// Supply information (10 points)
	if metadata.Supply != "" {
		score += 10
	}

	// Social links (40 points)
	socialCount := 0
	if metadata.Twitter != "" {
		socialCount++
	}
	if metadata.Telegram != "" {
		socialCount++
	}
	if metadata.Website != "" {
		socialCount++
	}
	score += float64(socialCount) * (40.0 / 3.0)

	return score
}

// MetaplexMetadata represents the structure of Metaplex token metadata.
type MetaplexMetadata struct {
	Name        string              `json:"name"`
	Symbol      string              `json:"symbol"`
	Description string              `json:"description"`
	Image       string              `json:"image"`
	URI         string              `json:"uri,omitempty"`
	Extensions  *MetadataExtensions `json:"extensions,omitempty"`
}

// MetadataExtensions holds extended metadata fields.
type MetadataExtensions struct {
	Twitter  string `json:"twitter,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Website  string `json:"website,omitempty"`
}

// ClearCache clears the metadata cache.
func (ta *TokenAnalyzer) ClearCache() {
	ta.cache.Range(func(key, value any) bool {
		ta.cache.Delete(key)
		return true
	})
}

// GetCacheSize returns the current cache size.
func (ta *TokenAnalyzer) GetCacheSize() int {
	size := 0
	ta.cache.Range(func(_, _ any) bool {
		size++
		return true
	})
	return size
}
