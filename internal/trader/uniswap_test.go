// Package trader provides trading functionality for the meme sniper bot.
package trader

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUniswapClient(t *testing.T) {
	config := UniswapConfig{
		Logger: testLogger(),
	}

	client, err := NewUniswapClient(config)
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.Equal(t, big.NewInt(BaseChainID), client.chainID)
	assert.Equal(t, common.HexToAddress(UniswapV3RouterAddress), client.router)
	assert.Equal(t, common.HexToAddress(UniswapV3QuoterAddress), client.quoter)
	assert.Equal(t, defaultMaxRetries, client.maxRetries)
}

func TestNewUniswapClientDefaults(t *testing.T) {
	config := UniswapConfig{}

	client, err := NewUniswapClient(config)
	require.NoError(t, err)
	require.NotNil(t, client)

	// Check default values
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.logger)
	assert.Equal(t, defaultMaxRetries, client.maxRetries)
	assert.Equal(t, defaultRetryDelay, client.retryDelay)
	assert.Equal(t, big.NewInt(BaseChainID), client.chainID)
}

func TestNewUniswapClientCustomValues(t *testing.T) {
	customChainID := big.NewInt(1)
	customRouter := common.HexToAddress("0x1234567890123456789012345678901234567890")
	customQuoter := common.HexToAddress("0x0987654321098765432109876543210987654321")
	customRetries := 5
	customDelay := 2 * time.Second

	config := UniswapConfig{
		Logger:     testLogger(),
		ChainID:    customChainID,
		Router:     customRouter,
		Quoter:     customQuoter,
		MaxRetries: customRetries,
		RetryDelay: customDelay,
	}

	client, err := NewUniswapClient(config)
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.Equal(t, customChainID, client.chainID)
	assert.Equal(t, customRouter, client.router)
	assert.Equal(t, customQuoter, client.quoter)
	assert.Equal(t, customRetries, client.maxRetries)
	assert.Equal(t, customDelay, client.retryDelay)
}

func TestBuildSwapTransaction(t *testing.T) {
	client, err := NewUniswapClient(UniswapConfig{Logger: testLogger()})
	require.NoError(t, err)

	tokenIn := GetBaseWETHAddress()
	tokenOut := GetBaseUSDCAddress()
	amountIn := big.NewInt(1000000000000000000) // 1 ETH
	recipient := common.HexToAddress("0x1234567890123456789012345678901234567890")
	deadline := big.NewInt(time.Now().Add(20 * time.Minute).Unix())
	amountOutMin := big.NewInt(2000000) // 2 USDC minimum

	params := UniSwapParams{
		TokenIn:        tokenIn,
		TokenOut:       tokenOut,
		AmountIn:       amountIn,
		AmountOutMin:   amountOutMin,
		FeeTier:        FeeTier3000,
		Recipient:      recipient,
		Deadline:       deadline,
		SqrtPriceLimit: big.NewInt(0),
	}

	tx, err := client.BuildSwapTransaction(params)
	require.NoError(t, err)
	require.NotNil(t, tx)

	assert.Equal(t, uint64(0), tx.Nonce())
	assert.Equal(t, client.router, *tx.To())
	assert.Equal(t, amountIn, tx.Value())
	assert.NotNil(t, tx.Data())
	assert.NotEmpty(t, tx.Data())
}

func TestBuildSwapTransactionDefaults(t *testing.T) {
	client, err := NewUniswapClient(UniswapConfig{Logger: testLogger()})
	require.NoError(t, err)

	tokenIn := GetBaseWETHAddress()
	tokenOut := GetBaseUSDCAddress()
	amountIn := big.NewInt(1000000000000000000)
	recipient := common.HexToAddress("0x1234567890123456789012345678901234567890")

	params := UniSwapParams{
		TokenIn:   tokenIn,
		TokenOut:  tokenOut,
		AmountIn:  amountIn,
		Recipient: recipient,
		FeeTier:   FeeTier3000,
	}

	tx, err := client.BuildSwapTransaction(params)
	require.NoError(t, err)
	require.NotNil(t, tx)

	// Check that default values were applied
	assert.NotNil(t, tx)
	assert.Equal(t, client.router, *tx.To())
}

func TestBuildSwapTransactionValidation(t *testing.T) {
	client, err := NewUniswapClient(UniswapConfig{Logger: testLogger()})
	require.NoError(t, err)

	recipient := common.HexToAddress("0x1234567890123456789012345678901234567890")

	tests := []struct {
		name    string
		params  UniSwapParams
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing token in",
			params: UniSwapParams{
				TokenOut:  GetBaseUSDCAddress(),
				AmountIn:  big.NewInt(1000000000000000000),
				Recipient: recipient,
			},
			wantErr: true,
			errMsg:  "invalid token addresses",
		},
		{
			name: "missing token out",
			params: UniSwapParams{
				TokenIn:   GetBaseWETHAddress(),
				AmountIn:  big.NewInt(1000000000000000000),
				Recipient: recipient,
			},
			wantErr: true,
			errMsg:  "invalid token addresses",
		},
		{
			name: "invalid amount",
			params: UniSwapParams{
				TokenIn:   GetBaseWETHAddress(),
				TokenOut:  GetBaseUSDCAddress(),
				AmountIn:  big.NewInt(0),
				Recipient: recipient,
			},
			wantErr: true,
			errMsg:  "invalid amount in",
		},
		{
			name: "missing recipient",
			params: UniSwapParams{
				TokenIn:  GetBaseWETHAddress(),
				TokenOut: GetBaseUSDCAddress(),
				AmountIn: big.NewInt(1000000000000000000),
			},
			wantErr: true,
			errMsg:  "invalid recipient address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := client.BuildSwapTransaction(tt.params)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, tx)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tx)
			}
		})
	}
}

func TestBuildApprovalTransaction(t *testing.T) {
	client, err := NewUniswapClient(UniswapConfig{Logger: testLogger()})
	require.NoError(t, err)

	token := GetBaseUSDCAddress()
	spender := GetRouterAddress()
	amount := big.NewInt(1000000000) // 1000 USDC (6 decimals)

	tx, err := client.BuildApprovalTransaction(token, spender, amount)
	require.NoError(t, err)
	require.NotNil(t, tx)

	assert.Equal(t, uint64(0), tx.Nonce())
	assert.Equal(t, token, *tx.To())
	assert.Equal(t, big.NewInt(0), tx.Value()) // Approval sends no ETH
	assert.NotNil(t, tx.Data())
	assert.NotEmpty(t, tx.Data())
}

func TestEstimateGas(t *testing.T) {
	client, err := NewUniswapClient(UniswapConfig{Logger: testLogger()})
	require.NoError(t, err)

	tokenIn := GetBaseWETHAddress()
	tokenOut := GetBaseUSDCAddress()
	amountIn := big.NewInt(1000000000000000000)
	recipient := common.HexToAddress("0x1234567890123456789012345678901234567890")

	params := UniSwapParams{
		TokenIn:   tokenIn,
		TokenOut:  tokenOut,
		AmountIn:  amountIn,
		Recipient: recipient,
	}

	gas, err := client.EstimateGas(context.Background(), params)
	require.NoError(t, err)
	assert.Positive(t, gas)
	assert.LessOrEqual(t, gas, uint64(500000)) // Should be reasonable
}

func TestValidateUniswapQuote(t *testing.T) {
	tests := []struct {
		name        string
		quote       *UniQuoteResponse
		minAmount   *big.Int
		wantErr     bool
		errContains string
	}{
		{
			name: "valid quote",
			quote: &UniQuoteResponse{
				AmountOut:         big.NewInt(1000000),
				AmountOutMin:      big.NewInt(950000),
				SlippageTolerance: 50,
				PriceImpact:       decimalNewFromInt(1),
			},
			minAmount: big.NewInt(950000),
			wantErr:   false,
		},
		{
			name:        "nil quote",
			quote:       nil,
			wantErr:     true,
			errContains: "quote is nil",
		},
		{
			name: "nil amount out",
			quote: &UniQuoteResponse{
				AmountOut: nil,
			},
			wantErr:     true,
			errContains: "invalid amount out",
		},
		{
			name: "zero amount out",
			quote: &UniQuoteResponse{
				AmountOut: big.NewInt(0),
			},
			wantErr:     true,
			errContains: "invalid amount out",
		},
		{
			name: "amount below minimum",
			quote: &UniQuoteResponse{
				AmountOut:    big.NewInt(900000),
				AmountOutMin: big.NewInt(850000),
			},
			minAmount:   big.NewInt(950000),
			wantErr:     true,
			errContains: "below minimum",
		},
		{
			name: "high price impact",
			quote: &UniQuoteResponse{
				AmountOut:    big.NewInt(1000000),
				AmountOutMin: big.NewInt(950000),
				PriceImpact:  decimalNewFromInt(10), // 10%
			},
			wantErr:     true,
			errContains: "price impact too high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUniswapQuote(tt.quote, tt.minAmount)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCalculateMinAmountOut(t *testing.T) {
	tests := []struct {
		name        string
		amountOut   *big.Int
		slippageBps uint16
		expectedMin *big.Int
	}{
		{
			name:        "0% slippage",
			amountOut:   big.NewInt(1000000),
			slippageBps: 0,
			expectedMin: big.NewInt(1000000),
		},
		{
			name:        "1% slippage",
			amountOut:   big.NewInt(1000000),
			slippageBps: 100, // 1%
			expectedMin: big.NewInt(990000),
		},
		{
			name:        "5% slippage",
			amountOut:   big.NewInt(1000000),
			slippageBps: 500, // 5%
			expectedMin: big.NewInt(950000),
		},
		{
			name:        "10% slippage",
			amountOut:   big.NewInt(1000000),
			slippageBps: 1000, // 10%
			expectedMin: big.NewInt(900000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateMinAmountOut(tt.amountOut, tt.slippageBps)
			assert.Equal(t, tt.expectedMin, result)
		})
	}
}

func TestCalculatePriceImpact(t *testing.T) {
	tests := []struct {
		name         string
		amountIn     *big.Int
		amountOut    *big.Int
		reserveIn    *big.Int
		reserveOut   *big.Int
		expectImpact bool
	}{
		{
			name:         "normal trade",
			amountIn:     big.NewInt(1000000000000000000), // 1 ETH
			amountOut:    big.NewInt(2000000000),          // 2000 USDC
			reserveIn:    big.NewInt(100000000000000000),  // 10 ETH
			reserveOut:   big.NewInt(20000000000),         // 20,000 USDC
			expectImpact: true,
		},
		{
			name:         "zero reserves",
			amountIn:     big.NewInt(1000000000000000000),
			amountOut:    big.NewInt(2000000000),
			reserveIn:    big.NewInt(0),
			reserveOut:   big.NewInt(0),
			expectImpact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			impact := CalculatePriceImpact(tt.amountIn, tt.amountOut, tt.reserveIn, tt.reserveOut)
			if tt.expectImpact {
				assert.Positive(t, impact.Abs().IntPart())
			} else {
				assert.Equal(t, decimal.Zero, impact)
			}
		})
	}
}

func TestFeeTierString(t *testing.T) {
	tests := []struct {
		feeTier  uint24
		expected string
	}{
		{FeeTier100, "0.01%"},
		{FeeTier500, "0.05%"},
		{FeeTier2500, "0.25%"},
		{FeeTier3000, "0.3%"},
		{FeeTier10000, "1%"},
		{123, "123"}, // Unknown tier
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := FeeTierString(tt.feeTier)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsNativeToken(t *testing.T) {
	weth := GetBaseWETHAddress()
	usdc := GetBaseUSDCAddress()
	random := common.HexToAddress("0x1234567890123456789012345678901234567890")

	assert.True(t, IsNativeToken(weth))
	assert.False(t, IsNativeToken(usdc))
	assert.False(t, IsNativeToken(random))
}

func TestGetWETHAddress(t *testing.T) {
	weth := GetBaseWETHAddress()
	assert.Equal(t, common.HexToAddress(BaseWETHAddress), weth)
	assert.NotEqual(t, common.Address{}, weth)
}

func TestGetUSDCAddress(t *testing.T) {
	usdc := GetBaseUSDCAddress()
	assert.Equal(t, common.HexToAddress(BaseUSDCAddress), usdc)
	assert.NotEqual(t, common.Address{}, usdc)
}

func TestGetRouterAddress(t *testing.T) {
	router := GetRouterAddress()
	assert.Equal(t, common.HexToAddress(UniswapV3RouterAddress), router)
	assert.NotEqual(t, common.Address{}, router)
}

// Helper functions

func decimalNewFromInt(i int64) decimal.Decimal {
	return decimal.NewFromInt(i)
}
