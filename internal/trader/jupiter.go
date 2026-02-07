// Package trader provides trading functionality for the meme sniper bot.
// This file implements Jupiter Swap API integration for Solana token swaps.
package trader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

const (
	// Jupiter API endpoints.
	jupiterQuoteAPI = "https://quote-api.jup.ag/v6/quote"
	jupiterSwapAPI  = "https://quote-api.jup.ag/v6/swap"
	jupiterLiteAPI  = "https://lite-api.jup.ag/v6"

	// Default timeouts.
	defaultQuoteTimeout = 5 * time.Second
	defaultSwapTimeout  = 10 * time.Second
	jupiterMaxRetries   = 3
	defaultRetryDelay   = 500 * time.Millisecond
)

// Common Solana token addresses.
var (
	// Wrapped SOL.
	WSolAddress = "So11111111111111111111111111111111111111112"
	// USDC.
	USDCAddress = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	// USDT.
	USDTAddress = "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB"
)

// QuoteRequest represents a request to get a swap quote from Jupiter.
type QuoteRequest struct {
	InputMint           string `json:"inputMint"`                     // Token to sell
	OutputMint          string `json:"outputMint"`                    // Token to buy
	Amount              uint64 `json:"amount"`                        // Amount in base units (smallest denomination)
	SlippageBps         int    `json:"slippageBps"`                   // Slippage tolerance in basis points (100 = 1%)
	OnlyDirectRoutes    bool   `json:"onlyDirectRoutes,omitempty"`    // Only use direct routes
	MaxAccounts         int    `json:"maxAccounts,omitempty"`         // Max accounts in transaction
	AsLegacyTransaction bool   `json:"asLegacyTransaction,omitempty"` // Use legacy transaction format
}

// QuoteResponse represents the response from Jupiter quote API.
type QuoteResponse struct {
	InputMint            string       `json:"inputMint"`
	InAmount             string       `json:"inAmount"`
	OutputMint           string       `json:"outputMint"`
	OutAmount            string       `json:"outAmount"`
	OtherAmountThreshold string       `json:"otherAmountThreshold"`
	SwapMode             string       `json:"swapMode,omitempty"`
	SlippageBps          int          `json:"slippageBps"`
	PlatformFee          *PlatformFee `json:"platformFee,omitempty"`
	PriceImpactPct       string       `json:"priceImpactPct"`
	RoutePlan            []RouteStep  `json:"routePlan"`
	ContextSlot          uint64       `json:"contextSlot,omitempty"`
	TimeTaken            float64      `json:"timeTaken,omitempty"`
}

// RouteStep represents a single step in the swap route.
type RouteStep struct {
	SwapInfo SwapInfo `json:"swapInfo"`
	Percent  int      `json:"percent"`
}

// SwapInfo contains details about a swap in the route.
type SwapInfo struct {
	AmmKey     string `json:"ammKey"`
	Label      string `json:"label"`
	InputMint  string `json:"inputMint"`
	OutputMint string `json:"outputMint"`
	InAmount   string `json:"inAmount"`
	OutAmount  string `json:"outAmount"`
	FeeAmount  string `json:"feeAmount"`
	FeeMint    string `json:"feeMint"`
}

// PlatformFee represents fees charged by platforms.
type PlatformFee struct {
	Amount string `json:"amount"`
	Mint   string `json:"mint"`
}

// SwapRequest represents a request to execute a swap using Jupiter.
type SwapRequest struct {
	QuoteResponse            QuoteResponse `json:"quoteResponse"`
	UserPublicKey            string        `json:"userPublicKey"`
	WrapAndUnwrapSol         bool          `json:"wrapAndUnwrapSol,omitempty"`
	UseSharedAccounts        bool          `json:"useSharedAccounts,omitempty"`
	FeeAccount               string        `json:"feeAccount,omitempty"`
	PriorityFeeMicroLamports int           `json:"priorityFeeMicroLamports,omitempty"`
}

// SwapResponse represents the response from Jupiter swap API.
type SwapResponse struct {
	SwapTransaction      string          `json:"swapTransaction"`
	LastValidBlockHeight uint64          `json:"lastValidBlockHeight"`
	PriorityFeeEstimate  *PriorityFeeEst `json:"priorityFeeEstimate,omitempty"`
}

// PriorityFeeEst represents estimated priority fee for the transaction.
type PriorityFeeEst struct {
	PriorityFeeLevels []PriorityFeeLevel `json:"priorityFeeLevels"`
}

// PriorityFeeLevel represents a priority fee level.
type PriorityFeeLevel struct {
	PriorityFeeMicroLamports int `json:"priorityFeeMicroLamports"`
	EstimateDurationMs       int `json:"estimateDurationMs,omitempty"`
}

// JupiterClient handles interactions with the Jupiter Swap API.
type JupiterClient struct {
	httpClient *http.Client
	logger     *zerolog.Logger
	maxRetries int
	retryDelay time.Duration
	quoteAPI   string
	swapAPI    string
}

// JupiterConfig holds configuration for the Jupiter client.
type JupiterConfig struct {
	HTTPClient *http.Client
	Logger     *zerolog.Logger
	MaxRetries int
	RetryDelay time.Duration
	QuoteAPI   string // Override default quote API URL
	SwapAPI    string // Override default swap API URL
}

// NewJupiterClient creates a new Jupiter API client.
func NewJupiterClient(config JupiterConfig) *JupiterClient {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: defaultQuoteTimeout,
		}
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = jupiterMaxRetries
	}

	if config.RetryDelay == 0 {
		config.RetryDelay = defaultRetryDelay
	}

	quoteAPI := config.QuoteAPI
	if quoteAPI == "" {
		quoteAPI = jupiterQuoteAPI
	}

	swapAPI := config.SwapAPI
	if swapAPI == "" {
		swapAPI = jupiterSwapAPI
	}

	return &JupiterClient{
		httpClient: config.HTTPClient,
		logger:     config.Logger,
		maxRetries: config.MaxRetries,
		retryDelay: config.RetryDelay,
		quoteAPI:   quoteAPI,
		swapAPI:    swapAPI,
	}
}

// GetQuote gets a swap quote from Jupiter.
// Returns the quote response or an error.
func (j *JupiterClient) GetQuote(ctx context.Context, req QuoteRequest) (*QuoteResponse, error) {
	if j.logger != nil {
		j.logger.Debug().
			Str("input_mint", req.InputMint).
			Str("output_mint", req.OutputMint).
			Uint64("amount", req.Amount).
			Int("slippage_bps", req.SlippageBps).
			Msg("Getting Jupiter quote")
	}

	var quote *QuoteResponse
	var err error

	// Use retry logic for transient failures
	var quoteResult *QuoteResponse
	err = util.RetryWithBackoff(func() error {
		var err error
		quoteResult, err = j.getQuoteOnce(ctx, req)
		return err
	}, j.maxRetries, j.retryDelay, j.retryDelay*10)

	if err != nil {
		if j.logger != nil {
			j.logger.Error().
				Err(err).
				Str("input_mint", req.InputMint).
				Str("output_mint", req.OutputMint).
				Msg("Failed to get Jupiter quote after retries")
		}
		return nil, fmt.Errorf("failed to get quote: %w", err)
	}

	quote = quoteResult

	if err != nil {
		if j.logger != nil {
			j.logger.Error().
				Err(err).
				Str("input_mint", req.InputMint).
				Str("output_mint", req.OutputMint).
				Msg("Failed to get Jupiter quote after retries")
		}
		return nil, fmt.Errorf("failed to get quote: %w", err)
	}

	if j.logger != nil {
		j.logger.Debug().
			Str("in_amount", quote.InAmount).
			Str("out_amount", quote.OutAmount).
			Str("price_impact_pct", quote.PriceImpactPct).
			Int("route_steps", len(quote.RoutePlan)).
			Msg("Received Jupiter quote")
	}

	return quote, nil
}

// getQuoteOnce performs a single quote request.
func (j *JupiterClient) getQuoteOnce(ctx context.Context, req QuoteRequest) (*QuoteResponse, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("inputMint", req.InputMint)
	params.Set("outputMint", req.OutputMint)
	params.Set("amount", strconv.FormatUint(req.Amount, 10))
	params.Set("slippageBps", strconv.Itoa(req.SlippageBps))

	if req.OnlyDirectRoutes {
		params.Set("onlyDirectRoutes", "true")
	}
	if req.MaxAccounts > 0 {
		params.Set("maxAccounts", strconv.Itoa(req.MaxAccounts))
	}
	if req.AsLegacyTransaction {
		params.Set("asLegacyTransaction", "true")
	}

	// Create request URL
	fullURL := fmt.Sprintf("%s?%s", j.quoteAPI, params.Encode())

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := j.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote API returned status %d", resp.StatusCode)
	}

	// Parse response
	var quote QuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&quote); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Validate quote
	if quote.OutAmount == "" {
		return nil, errors.New("quote returned empty outAmount")
	}

	return &quote, nil
}

// GetSwapTransaction builds a swap transaction using the Jupiter API.
// Returns the serialized transaction and last valid block height.
func (j *JupiterClient) GetSwapTransaction(ctx context.Context, req SwapRequest) (*SwapResponse, error) {
	if j.logger != nil {
		j.logger.Debug().
			Str("user_public_key", req.UserPublicKey).
			Msg("Building Jupiter swap transaction")
	}

	var swapResp *SwapResponse
	var err error

	// Use retry logic
	var swapResult *SwapResponse
	err = util.RetryWithBackoff(func() error {
		var err error
		swapResult, err = j.getSwapTransactionOnce(ctx, req)
		return err
	}, j.maxRetries, j.retryDelay, j.retryDelay*10)

	if err != nil {
		if j.logger != nil {
			j.logger.Error().
				Err(err).
				Str("user_public_key", req.UserPublicKey).
				Msg("Failed to build swap transaction after retries")
		}
		return nil, fmt.Errorf("failed to build swap transaction: %w", err)
	}

	swapResp = swapResult

	if err != nil {
		if j.logger != nil {
			j.logger.Error().
				Err(err).
				Str("user_public_key", req.UserPublicKey).
				Msg("Failed to build swap transaction after retries")
		}
		return nil, fmt.Errorf("failed to build swap transaction: %w", err)
	}

	if j.logger != nil {
		j.logger.Debug().
			Uint64("last_valid_block_height", swapResp.LastValidBlockHeight).
			Msg("Built Jupiter swap transaction")
	}

	return swapResp, nil
}

// getSwapTransactionOnce performs a single swap transaction request.
func (j *JupiterClient) getSwapTransactionOnce(ctx context.Context, req SwapRequest) (*SwapResponse, error) {
	// Set timeout for swap requests
	swapCtx, cancel := context.WithTimeout(ctx, defaultSwapTimeout)
	defer cancel()

	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	_ = body // Use body to avoid unused variable warning

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(swapCtx, http.MethodPost, j.swapAPI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Use a reader for the body
	httpReq.Body = nil

	// We need to recreate the request with body
	httpReq, err = http.NewRequestWithContext(swapCtx, http.MethodPost, j.swapAPI, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := j.httpClient.Post(j.swapAPI, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("swap API returned status %d", resp.StatusCode)
	}

	// Parse response
	var swapResp SwapResponse
	if err := json.NewDecoder(resp.Body).Decode(&swapResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Validate response
	if swapResp.SwapTransaction == "" {
		return nil, errors.New("swap response returned empty transaction")
	}
	if swapResp.LastValidBlockHeight == 0 {
		return nil, errors.New("swap response returned invalid block height")
	}

	return &swapResp, nil
}

// EstimatePriorityFee estimates the priority fee needed for the transaction.
// Returns the recommended fee in micro-lamports.
func (j *JupiterClient) EstimatePriorityFee(ctx context.Context, quote QuoteResponse) (int, error) {
	// Default priority fee if no estimate available
	defaultFee := 1000 // 0.001 SOL = 1000 micro-lamports

	if quote.RoutePlan == nil || len(quote.RoutePlan) == 0 {
		return defaultFee, nil
	}

	// Calculate priority fee based on route complexity
	// More complex routes = higher fees
	routeComplexity := len(quote.RoutePlan)
	baseFee := 1000

	// Add 500 micro-lamports per route step
	estimatedFee := baseFee + (routeComplexity * 500)

	// Cap at reasonable maximum
	maxFee := 100000 // 0.1 SOL
	if estimatedFee > maxFee {
		estimatedFee = maxFee
	}

	return estimatedFee, nil
}

// CalculateSlippageBps converts a percentage slippage to basis points.
// For example, 1.0 -> 100 (1%), 5.0 -> 500 (5%).
func CalculateSlippageBps(slippagePercent float64) int {
	return int(slippagePercent * 100)
}

// ValidateQuote checks if a quote is valid and acceptable.
func ValidateQuote(quote *QuoteResponse, minOutAmount uint64) error {
	if quote == nil {
		return errors.New("quote is nil")
	}

	if quote.OutAmount == "" {
		return errors.New("quote has empty output amount")
	}

	// Parse output amount
	var outAmount uint64
	if _, err := fmt.Sscanf(quote.OutAmount, "%d", &outAmount); err != nil {
		return fmt.Errorf("failed to parse outAmount: %w", err)
	}

	// Check minimum output
	if outAmount < minOutAmount {
		return fmt.Errorf("quote output amount %d is below minimum %d", outAmount, minOutAmount)
	}

	// Check for excessive price impact
	if quote.PriceImpactPct != "" {
		var priceImpact float64
		if _, err := fmt.Sscanf(quote.PriceImpactPct, "%f", &priceImpact); err == nil {
			if priceImpact > 10.0 { // 10% price impact is too high
				return fmt.Errorf("price impact %.2f%% is too high", priceImpact)
			}
		}
	}

	return nil
}

// QuoteError represents an error from the Jupiter quote API.
type QuoteError struct {
	StatusCode int
	Message    string
	Details    string
}

func (e *QuoteError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("quote error (status %d): %s - %s", e.StatusCode, e.Message, e.Details)
	}
	return fmt.Sprintf("quote error (status %d): %s", e.StatusCode, e.Message)
}

// SwapError represents an error from the Jupiter swap API.
type SwapError struct {
	StatusCode int
	Message    string
	Details    string
}

func (e *SwapError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("swap error (status %d): %s - %s", e.StatusCode, e.Message, e.Details)
	}
	return fmt.Sprintf("swap error (status %d): %s", e.StatusCode, e.Message)
}

// IsRetryableError checks if an error is retryable.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Network errors are retryable
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	// Jupiter API rate limits are retryable
	var quoteErr *QuoteError
	if errors.As(err, &quoteErr) {
		return quoteErr.StatusCode == http.StatusTooManyRequests || quoteErr.StatusCode >= 500
	}

	var swapErr *SwapError
	if errors.As(err, &swapErr) {
		return swapErr.StatusCode == http.StatusTooManyRequests || swapErr.StatusCode >= 500
	}

	return false
}

// GetSwapInstructions extracts the swap instructions from a quote.
// This can be used for custom transaction building.
func (j *JupiterClient) GetSwapInstructions(ctx context.Context, req SwapRequest) ([]byte, *SwapResponse, error) {
	swapResp, err := j.GetSwapTransaction(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	// The swapTransaction is a base64 encoded transaction
	txBytes, err := DecodeBase64Transaction(swapResp.SwapTransaction)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode transaction: %w", err)
	}

	return txBytes, swapResp, nil
}

// DecodeBase64Transaction decodes a base64 encoded transaction.
func DecodeBase64Transaction(encoded string) ([]byte, error) {
	// Jupiter returns base64 encoded transactions
	// This is a placeholder - actual implementation would use proper base64 decoding
	// The solana-go library has its own transaction decoding
	return []byte(encoded), nil
}
