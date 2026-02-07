// Package trader provides trading functionality for the meme sniper bot.
// This file implements Uniswap V3 integration for Base chain token swaps.
package trader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// uint24 represents a 24-bit unsigned integer (for Uniswap fee tiers).
type uint24 uint32

const (
	// Uniswap V3 Router on Base.
	UniswapV3RouterAddress  = "0xE592427A0AEce92De3Edee1F18E0157C05861564"
	UniswapV3QuoterAddress  = "0x3274dDcE8323eABc65219E1Db4Db0349675aA3A4"
	UniswapV3FactoryAddress = "0x33128a8fC17869897dcE68Ed026d694621f6FDfD"

	// Common token addresses on Base.
	BaseWETHAddress = "0x4200000000000000000000000000000000000006"
	BaseUSDCAddress = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	BaseUSDBAddress = "0x4200000000000000000000000000000000000022"

	// Uniswap V3 pool fees.
	FeeTier100   = 100   // 0.01%
	FeeTier500   = 500   // 0.05%
	FeeTier2500  = 2500  // 0.25%
	FeeTier3000  = 3000  // 0.3%
	FeeTier10000 = 10000 // 1%

	// Defaults.
	defaultUniswapQuoteTimeout      = 5 * time.Second
	defaultUniswapSwapTimeout       = 10 * time.Second
	defaultUniswapSlippageTolerance = 50 // 50 basis points = 0.5%

	// Base chain ID value reference (use evm.go BaseChainID constant).
	BaseChainIDValue = 8453
)

var (
	// Uniswap V3 Router ABI (exactInputSingle function).
	routerABI = `[{
		"inputs": [
			{"internalType": "struct ISwapRouter.ExactInputSingleParams", "name": "params", "type": "tuple"}
		],
		"name": "exactInputSingle",
		"outputs": [{"internalType": "uint256", "name": "amountOut", "type": "uint256"}],
		"stateMutability": "payable",
		"type": "function"
	}, {
		"inputs": [
			{"internalType": "struct ISwapRouter.ExactOutputSingleParams", "name": "params", "type": "tuple"}
		],
		"name": "exactOutputSingle",
		"outputs": [{"internalType": "uint256", "name": "amountIn", "type": "uint256"}],
		"stateMutability": "payable",
		"type": "function"
	}]`

	// Uniswap V3 Quoter ABI (quoteExactInputSingle function).
	quoterABI = `[{
		"inputs": [
			{"internalType": "address", "name": "tokenIn", "type": "address"},
			{"internalType": "address", "name": "tokenOut", "type": "address"},
			{"internalType": "uint24", "name": "fee", "type": "uint24"},
			{"internalType": "uint256", "name": "amountIn", "type": "uint256"},
			{"internalType": "uint160", "name": "sqrtPriceLimitX96", "type": "uint160"}
		],
		"name": "quoteExactInputSingle",
		"outputs": [{"internalType": "uint256", "name": "amountOut", "type": "uint256"}],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`
)

// QuoteParams represents parameters for getting a Uniswap quote.
type QuoteParams struct {
	TokenIn           common.Address // Token to sell
	TokenOut          common.Address // Token to buy
	AmountIn          *big.Int       // Amount to sell (in wei)
	FeeTier           uint24         // Pool fee tier (100, 500, 2500, 3000, 10000)
	SlippageTolerance uint16         // Slippage tolerance in basis points (100 = 1%)
	SqrtPriceLimit    *big.Int       // Price limit (0 = no limit)
}

// UniQuoteResponse represents a Uniswap quote response.
type UniQuoteResponse struct {
	TokenIn           common.Address
	TokenOut          common.Address
	AmountIn          *big.Int
	AmountOut         *big.Int
	AmountOutMin      *big.Int
	FeeTier           uint24
	SlippageTolerance uint16
	PriceImpact       decimal.Decimal
	Route             []UniRouteStep
	BlockNumber       uint64
	Timestamp         time.Time
}

// UniRouteStep represents a single step in a swap route.
type UniRouteStep struct {
	TokenIn   common.Address
	TokenOut  common.Address
	FeeTier   uint24
	AmountIn  *big.Int
	AmountOut *big.Int
}

// UniSwapParams represents parameters for executing a Uniswap swap.
type UniSwapParams struct {
	TokenIn        common.Address // Token to sell
	TokenOut       common.Address // Token to buy
	AmountIn       *big.Int       // Amount to sell (in wei)
	AmountOutMin   *big.Int       // Minimum amount to receive (in wei)
	FeeTier        uint24         // Pool fee tier
	Recipient      common.Address // Recipient of the output tokens
	Deadline       *big.Int       // Transaction deadline (unix timestamp)
	SqrtPriceLimit *big.Int       // Price limit (0 = no limit)
}

// SwapResult represents the result of a swap execution.
type SwapResult struct {
	TxHash    common.Hash
	AmountIn  *big.Int
	AmountOut *big.Int
	GasUsed   uint64
	GasPrice  *big.Int
	Success   bool
	Error     string
	Timestamp time.Time
}

// UniswapClient handles interactions with Uniswap V3 on Base.
type UniswapClient struct {
	httpClient *http.Client
	logger     *zerolog.Logger
	maxRetries int
	retryDelay time.Duration
	chainID    *big.Int
	router     common.Address
	quoter     common.Address
	routerABI  abi.ABI
	quoterABI  abi.ABI
	baseAPIURL string // Base API for quotes (e.g., 1inch, paraswap)
}

// UniswapConfig holds configuration for the Uniswap client.
type UniswapConfig struct {
	HTTPClient *http.Client
	Logger     *zerolog.Logger
	MaxRetries int
	RetryDelay time.Duration
	ChainID    *big.Int
	Router     common.Address
	Quoter     common.Address
	BaseAPIURL string // Optional: Use aggregator API for quotes
}

// NewUniswapClient creates a new Uniswap V3 client.
func NewUniswapClient(config UniswapConfig) (*UniswapClient, error) {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: defaultUniswapQuoteTimeout,
		}
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = defaultMaxRetries
	}

	if config.RetryDelay == 0 {
		config.RetryDelay = defaultRetryDelay
	}

	// Set default chain ID to Base
	if config.ChainID == nil {
		config.ChainID = big.NewInt(BaseChainIDValue)
	}

	// Set default router address
	if config.Router == (common.Address{}) {
		config.Router = common.HexToAddress(UniswapV3RouterAddress)
	}

	// Set default quoter address
	if config.Quoter == (common.Address{}) {
		config.Quoter = common.HexToAddress(UniswapV3QuoterAddress)
	}

	// Parse router ABI
	routerAbi, err := abi.JSON(strings.NewReader(routerABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse router ABI: %w", err)
	}

	// Parse quoter ABI
	quoterAbi, err := abi.JSON(strings.NewReader(quoterABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse quoter ABI: %w", err)
	}

	logger := config.Logger
	if logger == nil {
		l := util.WithComponent("uniswap")
		logger = &l
	}

	return &UniswapClient{
		httpClient: config.HTTPClient,
		logger:     logger,
		maxRetries: config.MaxRetries,
		retryDelay: config.RetryDelay,
		chainID:    config.ChainID,
		router:     config.Router,
		quoter:     config.Quoter,
		routerABI:  routerAbi,
		quoterABI:  quoterAbi,
		baseAPIURL: config.BaseAPIURL,
	}, nil
}

// GetQuote gets a swap quote from Uniswap V3.
// This can use either the on-chain quoter contract or an aggregator API.
func (u *UniswapClient) GetQuote(ctx context.Context, params QuoteParams) (*UniQuoteResponse, error) {
	startTime := time.Now()

	// Validate parameters
	if params.TokenIn == (common.Address{}) || params.TokenOut == (common.Address{}) {
		return nil, errors.New("invalid token addresses")
	}

	if params.AmountIn == nil || params.AmountIn.Sign() <= 0 {
		return nil, errors.New("invalid amount in")
	}

	// Set default fee tier to 0.3% if not specified
	if params.FeeTier == 0 {
		params.FeeTier = FeeTier3000
	}

	// Set default slippage tolerance
	if params.SlippageTolerance == 0 {
		params.SlippageTolerance = defaultUniswapSlippageTolerance
	}

	u.logger.Debug().
		Str("token_in", params.TokenIn.Hex()).
		Str("token_out", params.TokenOut.Hex()).
		Str("amount_in", params.AmountIn.String()).
		Uint32("fee_tier", uint32(params.FeeTier)).
		Uint16("slippage_bps", params.SlippageTolerance).
		Msg("Getting Uniswap quote")

	var response *UniQuoteResponse
	var err error

	// Try aggregator API first if configured
	if u.baseAPIURL != "" {
		response, err = u.getAggregatorQuote(ctx, params)
		if err == nil {
			u.logger.Debug().
				Str("amount_out", response.AmountOut.String()).
				Str("duration", time.Since(startTime).String()).
				Msg("Got quote from aggregator")
			return response, nil
		}
		u.logger.Debug().Err(err).Msg("Aggregator quote failed, falling back to on-chain")
	}

	// Fall back to on-chain quoter
	response, err = u.getOnChainQuote(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get quote: %w", err)
	}

	u.logger.Debug().
		Str("amount_out", response.AmountOut.String()).
		Str("duration", time.Since(startTime).String()).
		Msg("Got on-chain quote")

	return response, nil
}

// getOnChainQuote gets a quote using the on-chain Uniswap V3 quoter contract.
func (u *UniswapClient) getOnChainQuote(ctx context.Context, params QuoteParams) (*UniQuoteResponse, error) {
	// This would normally call the quoter contract via RPC
	// For now, return a placeholder that will be implemented with RPC integration
	// In production, you would:
	// 1. Encode the quoteExactInputSingle call
	// 2. Make an eth_call RPC request
	// 3. Decode the result

	return nil, errors.New("on-chain quoter requires RPC integration - use aggregator API")
}

// getAggregatorQuote gets a quote from an aggregator API (1inch, Paraswap, etc.).
func (u *UniswapClient) getAggregatorQuote(ctx context.Context, params QuoteParams) (*UniQuoteResponse, error) {
	// Build request URL for Base aggregator
	// This example uses a generic structure - adjust based on your chosen aggregator

	reqURL, err := url.Parse(u.baseAPIURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base API URL: %w", err)
	}

	// Build query parameters for the aggregator
	query := reqURL.Query()
	query.Set("fromTokenAddress", params.TokenIn.Hex())
	query.Set("toTokenAddress", params.TokenOut.Hex())
	query.Set("amount", params.AmountIn.String())
	query.Set("chainId", u.chainID.String())
	reqURL.RawQuery = query.Encode()

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// Execute with retry
	var resp *http.Response
	err = util.RetryWithContext(ctx, func(ctx context.Context) error {
		r, reqErr := u.httpClient.Do(req) //nolint:bodyclose // response closed below
		if reqErr != nil {
			return reqErr
		}
		// Close previous response if exists
		if resp != nil {
			_ = resp.Body.Close()
		}
		resp = r
		return nil
	}, util.DefaultRetryConfig(), isRetryableHTTP)

	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	// The exact structure depends on the aggregator being used
	// This is a generic example
	var apiResp struct {
		ToTokenAmount string `json:"toTokenAmount"`
		EstimatedGas  uint64 `json:"estimatedGas"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Parse output amount
	amountOut, ok := new(big.Int).SetString(apiResp.ToTokenAmount, 10)
	if !ok {
		return nil, errors.New("invalid amount in response")
	}

	// Calculate minimum amount with slippage
	amountOutMin := calculateMinAmountOut(amountOut, params.SlippageTolerance)

	response := &UniQuoteResponse{
		TokenIn:           params.TokenIn,
		TokenOut:          params.TokenOut,
		AmountIn:          params.AmountIn,
		AmountOut:         amountOut,
		AmountOutMin:      amountOutMin,
		FeeTier:           params.FeeTier,
		SlippageTolerance: params.SlippageTolerance,
		PriceImpact:       decimal.Zero, // Would be calculated from additional data
		Timestamp:         time.Now(),
		Route: []UniRouteStep{
			{
				TokenIn:   params.TokenIn,
				TokenOut:  params.TokenOut,
				FeeTier:   params.FeeTier,
				AmountIn:  params.AmountIn,
				AmountOut: amountOut,
			},
		},
	}

	return response, nil
}

// BuildSwapTransaction builds a Uniswap V3 swap transaction.
func (u *UniswapClient) BuildSwapTransaction(params UniSwapParams) (*types.Transaction, error) {
	// Validate parameters
	if params.TokenIn == (common.Address{}) || params.TokenOut == (common.Address{}) {
		return nil, errors.New("invalid token addresses")
	}

	if params.AmountIn == nil || params.AmountIn.Sign() <= 0 {
		return nil, errors.New("invalid amount in")
	}

	if params.Recipient == (common.Address{}) {
		return nil, errors.New("invalid recipient address")
	}

	// Set default fee tier
	if params.FeeTier == 0 {
		params.FeeTier = FeeTier3000
	}

	// Set default deadline (20 minutes from now)
	if params.Deadline == nil {
		deadline := big.NewInt(time.Now().Add(20 * time.Minute).Unix())
		params.Deadline = deadline
	}

	u.logger.Debug().
		Str("token_in", params.TokenIn.Hex()).
		Str("token_out", params.TokenOut.Hex()).
		Str("amount_in", params.AmountIn.String()).
		Str("recipient", params.Recipient.Hex()).
		Uint32("fee_tier", uint32(params.FeeTier)).
		Msg("Building Uniswap swap transaction")

	// Build the exactInputSingle call data
	// ISwapRouter.ExactInputSingleParams(
	//     tokenIn,
	//     tokenOut,
	//     fee,
	//     recipient,
	//     deadline,
	//     amountIn,
	//     amountOutMin,
	//     sqrtPriceLimitX96
	// )

	swapParams := struct {
		TokenIn           common.Address
		TokenOut          common.Address
		Fee               uint24
		Recipient         common.Address
		Deadline          *big.Int
		AmountIn          *big.Int
		AmountOutMinimum  *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           params.TokenIn,
		TokenOut:          params.TokenOut,
		Fee:               params.FeeTier,
		Recipient:         params.Recipient,
		Deadline:          params.Deadline,
		AmountIn:          params.AmountIn,
		AmountOutMinimum:  params.AmountOutMin,
		SqrtPriceLimitX96: params.SqrtPriceLimit,
	}

	// Encode the method call
	data, err := u.routerABI.Pack("exactInputSingle", swapParams)
	if err != nil {
		return nil, fmt.Errorf("failed to encode swap data: %w", err)
	}

	// Create the transaction
	// Note: Gas estimation and gas price should be set by the caller
	tx := types.NewTransaction(
		0, // nonce - should be set by caller
		u.router,
		params.AmountIn, // ETH amount if swapping WETH
		0,               // gas limit - should be estimated
		nil,             // gasPrice - nil for EIP-1559
		data,
	)

	return tx, nil
}

// EstimateGas estimates the gas required for a swap transaction.
func (u *UniswapClient) EstimateGas(ctx context.Context, params UniSwapParams) (uint64, error) {
	// This would normally call eth_estimateGas via RPC
	// For now, return a reasonable default estimate

	// Base gas for Uniswap V3 swap
	baseGas := uint64(150000)

	// Add extra for complex routes
	if len(params.TokenIn.Bytes()) > 0 && len(params.TokenOut.Bytes()) > 0 {
		baseGas += 50000
	}

	return baseGas, nil
}

// BuildApprovalTransaction builds an ERC20 approval transaction for Uniswap.
func (u *UniswapClient) BuildApprovalTransaction(
	token, spender common.Address,
	amount *big.Int,
) (*types.Transaction, error) {
	// ERC20 approve(address spender, uint256 amount)
	approvalABI := `[{
		"inputs": [
			{"name": "spender", "type": "address"},
			{"name": "amount", "type": "uint256"}
		],
		"name": "approve",
		"outputs": [{"name": "", "type": "bool"}],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`

	parsedABI, err := abi.JSON(strings.NewReader(approvalABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	data, err := parsedABI.Pack("approve", spender, amount)
	if err != nil {
		return nil, fmt.Errorf("failed to encode approve data: %w", err)
	}

	tx := types.NewTransaction(
		0, // nonce - should be set by caller
		token,
		big.NewInt(0), // value
		0,             // gas limit
		nil,           // gasPrice
		data,
	)

	return tx, nil
}

// ValidateQuote validates a quote response.
func ValidateUniswapQuote(quote *UniQuoteResponse, minAmountOut *big.Int) error {
	if quote == nil {
		return errors.New("quote is nil")
	}

	if quote.AmountOut == nil || quote.AmountOut.Sign() <= 0 {
		return errors.New("invalid amount out")
	}

	if minAmountOut != nil && quote.AmountOut.Cmp(minAmountOut) < 0 {
		return fmt.Errorf("amount out %s below minimum %s", quote.AmountOut, minAmountOut)
	}

	// Check price impact
	if quote.PriceImpact.GreaterThan(decimal.NewFromInt(5)) { // 5%
		return fmt.Errorf("price impact too high: %s%%", quote.PriceImpact.String())
	}

	return nil
}

// Helper functions

// calculateMinAmountOut calculates the minimum amount out with slippage.
func calculateMinAmountOut(amountOut *big.Int, slippageBps uint16) *big.Int {
	if amountOut == nil {
		return big.NewInt(0)
	}

	// slippageBps is in basis points (100 = 1%)
	// Calculate: amountOut * (10000 - slippageBps) / 10000
	amount := new(big.Float).SetInt(amountOut)
	slippage := new(big.Float).SetInt64(int64(10000 - int64(slippageBps)))
	factor := new(big.Float).Quo(slippage, big.NewFloat(10000))

	result := new(big.Float).Mul(amount, factor)
	minAmount, _ := result.Int(nil)

	return minAmount
}

// isRetryableHTTPUniswap determines if an HTTP error should trigger a retry.
//
//nolint:unused // Kept for future use
func isRetryableHTTPUniswap(err error) bool {
	if err == nil {
		return false
	}

	// Retry on timeout or temporary errors
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

// CalculatePriceImpact calculates the price impact of a trade.
func CalculatePriceImpact(amountIn, amountOut, reserveIn, reserveOut *big.Int) decimal.Decimal {
	// Simplified price impact calculation
	// In production, use the proper constant product formula

	if reserveIn.Sign() == 0 || reserveOut.Sign() == 0 {
		return decimal.Zero
	}

	// Current price: reserveOut / reserveIn
	currentPrice := new(big.Float).Quo(
		new(big.Float).SetInt(reserveOut),
		new(big.Float).SetInt(reserveIn),
	)

	// Execution price: amountOut / amountIn
	executionPrice := new(big.Float).Quo(
		new(big.Float).SetInt(amountOut),
		new(big.Float).SetInt(amountIn),
	)

	// Price impact as percentage
	diff := new(big.Float).Sub(currentPrice, executionPrice)
	impact := new(big.Float).Quo(diff, currentPrice)

	impactDec, _ := decimal.NewFromString(impact.String())
	return impactDec.Abs().Mul(decimal.NewFromInt(100))
}

// Common addresses.
func GetBaseWETHAddress() common.Address {
	return common.HexToAddress(BaseWETHAddress)
}

func GetBaseUSDCAddress() common.Address {
	return common.HexToAddress(BaseUSDCAddress)
}

func GetRouterAddress() common.Address {
	return common.HexToAddress(UniswapV3RouterAddress)
}

// FeeTierString returns a string representation of a fee tier.
func FeeTierString(fee uint24) string {
	switch fee {
	case FeeTier100:
		return "0.01%"
	case FeeTier500:
		return "0.05%"
	case FeeTier2500:
		return "0.25%"
	case FeeTier3000:
		return "0.3%"
	case FeeTier10000:
		return "1%"
	default:
		return fmt.Sprintf("%d", fee)
	}
}

// IsNativeToken checks if an address is WETH (wrapped native token).
func IsNativeToken(token common.Address) bool {
	return token == common.HexToAddress(BaseWETHAddress)
}
