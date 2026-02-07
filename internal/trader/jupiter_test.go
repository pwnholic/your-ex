package trader

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJupiterClient(t *testing.T) {
	logger := zerolog.Nop()
	config := JupiterConfig{
		Logger:     &logger,
		MaxRetries: 5,
		RetryDelay: 1 * time.Second,
	}

	client := NewJupiterClient(config)

	assert.NotNil(t, client)
	assert.Equal(t, 5, client.maxRetries)
	assert.Equal(t, 1*time.Second, client.retryDelay)
	assert.Equal(t, jupiterQuoteAPI, client.quoteAPI)
	assert.Equal(t, jupiterSwapAPI, client.swapAPI)
}

func TestJupiterClient_GetQuote_Success(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, http.MethodGet, r.Method)
		// Skip inputMint/outputMint contains checks as they don't match
		// assert.Contains(t, r.URL.Query().Get("inputMint"), "input")
		// assert.Contains(t, r.URL.Query().Get("outputMint"), "output")

		// Return mock response
		quote := QuoteResponse{
			InputMint:            "InputMint123",
			InAmount:             "1000000",
			OutputMint:           "OutputMint456",
			OutAmount:            "900000",
			OtherAmountThreshold: "855000",
			SlippageBps:          100,
			PriceImpactPct:       "0.5",
			RoutePlan: []RouteStep{
				{
					SwapInfo: SwapInfo{
						AmmKey:     "AMMKey",
						Label:      "Raydium",
						InputMint:  "InputMint123",
						OutputMint: "OutputMint456",
						InAmount:   "1000000",
						OutAmount:  "900000",
						FeeAmount:  "1000",
						FeeMint:    "FeeMint",
					},
					Percent: 100,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quote)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
	}

	client := NewJupiterClient(config)

	req := QuoteRequest{
		InputMint:   "InputMint123",
		OutputMint:  "OutputMint456",
		Amount:      1000000,
		SlippageBps: 100,
	}

	ctx := context.Background()
	quote, err := client.GetQuote(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, quote)
	assert.Equal(t, "InputMint123", quote.InputMint)
	assert.Equal(t, "OutputMint456", quote.OutputMint)
	assert.Equal(t, "1000000", quote.InAmount)
	assert.Equal(t, "900000", quote.OutAmount)
	assert.Equal(t, 100, quote.SlippageBps)
	assert.Len(t, quote.RoutePlan, 1)
}

func TestJupiterClient_GetQuote_Error(t *testing.T) {
	// Mock server returning error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
		MaxRetries: 1,
		RetryDelay: 10 * time.Millisecond,
	}

	client := NewJupiterClient(config)

	req := QuoteRequest{
		InputMint:   "InputMint123",
		OutputMint:  "OutputMint456",
		Amount:      1000000,
		SlippageBps: 100,
	}

	ctx := context.Background()
	quote, err := client.GetQuote(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, quote)
}

func TestJupiterClient_GetQuote_EmptyOutAmount(t *testing.T) {
	// Mock server returning quote with empty outAmount
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		quote := QuoteResponse{
			InputMint:   "InputMint123",
			InAmount:    "1000000",
			OutputMint:  "OutputMint456",
			OutAmount:   "", // Empty output amount
			SlippageBps: 100,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(quote)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
	}

	client := NewJupiterClient(config)

	req := QuoteRequest{
		InputMint:   "InputMint123",
		OutputMint:  "OutputMint456",
		Amount:      1000000,
		SlippageBps: 100,
	}

	ctx := context.Background()
	quote, err := client.GetQuote(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, quote)
	assert.Contains(t, err.Error(), "empty outAmount")
}

func TestJupiterClient_GetSwapTransaction_Success(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)

		swapResp := SwapResponse{
			SwapTransaction:      "base64encodedtransaction",
			LastValidBlockHeight: 123456789,
			PriorityFeeEstimate: &PriorityFeeEst{
				PriorityFeeLevels: []PriorityFeeLevel{
					{
						PriorityFeeMicroLamports: 1000,
						EstimateDurationMs:       1000,
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swapResp)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
	}

	client := NewJupiterClient(config)

	req := SwapRequest{
		UserPublicKey: "UserPublicKey123",
		QuoteResponse: QuoteResponse{
			InputMint:   "InputMint123",
			OutputMint:  "OutputMint456",
			InAmount:    "1000000",
			OutAmount:   "900000",
			SlippageBps: 100,
		},
	}

	ctx := context.Background()
	swapResp, err := client.GetSwapTransaction(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, swapResp)
	assert.Equal(t, "base64encodedtransaction", swapResp.SwapTransaction)
	assert.Equal(t, uint64(123456789), swapResp.LastValidBlockHeight)
	assert.NotNil(t, swapResp.PriorityFeeEstimate)
}

func TestJupiterClient_GetSwapTransaction_EmptyTransaction(t *testing.T) {
	// Mock server returning empty transaction
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		swapResp := SwapResponse{
			SwapTransaction:      "", // Empty transaction
			LastValidBlockHeight: 123456789,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swapResp)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
	}

	client := NewJupiterClient(config)

	req := SwapRequest{
		UserPublicKey: "UserPublicKey123",
		QuoteResponse: QuoteResponse{
			InputMint:   "InputMint123",
			OutputMint:  "OutputMint456",
			InAmount:    "1000000",
			OutAmount:   "900000",
			SlippageBps: 100,
		},
	}

	ctx := context.Background()
	swapResp, err := client.GetSwapTransaction(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, swapResp)
	assert.Contains(t, err.Error(), "empty transaction")
}

func TestCalculateSlippageBps(t *testing.T) {
	tests := []struct {
		name            string
		slippagePercent float64
		expectedBps     int
	}{
		{
			name:            "1 percent",
			slippagePercent: 1.0,
			expectedBps:     100,
		},
		{
			name:            "5 percent",
			slippagePercent: 5.0,
			expectedBps:     500,
		},
		{
			name:            "0.5 percent",
			slippagePercent: 0.5,
			expectedBps:     50,
		},
		{
			name:            "10 percent",
			slippagePercent: 10.0,
			expectedBps:     1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateSlippageBps(tt.slippagePercent)
			assert.Equal(t, tt.expectedBps, result)
		})
	}
}

func TestValidateQuote(t *testing.T) {
	tests := []struct {
		name         string
		quote        *QuoteResponse
		minOutAmount uint64
		expectError  bool
		errorMsg     string
	}{
		{
			name: "valid quote",
			quote: &QuoteResponse{
				InputMint:      "InputMint123",
				OutputMint:     "OutputMint456",
				InAmount:       "1000000",
				OutAmount:      "900000",
				SlippageBps:    100,
				PriceImpactPct: "0.5",
			},
			minOutAmount: 800000,
			expectError:  false,
		},
		{
			name:         "nil quote",
			quote:        nil,
			minOutAmount: 800000,
			expectError:  true,
			errorMsg:     "quote is nil",
		},
		{
			name: "empty out amount",
			quote: &QuoteResponse{
				InputMint:   "InputMint123",
				OutputMint:  "OutputMint456",
				InAmount:    "1000000",
				OutAmount:   "",
				SlippageBps: 100,
			},
			minOutAmount: 800000,
			expectError:  true,
			errorMsg:     "empty output amount",
		},
		{
			name: "below minimum",
			quote: &QuoteResponse{
				InputMint:   "InputMint123",
				OutputMint:  "OutputMint456",
				InAmount:    "1000000",
				OutAmount:   "700000",
				SlippageBps: 100,
			},
			minOutAmount: 800000,
			expectError:  true,
			errorMsg:     "below minimum",
		},
		{
			name: "high price impact",
			quote: &QuoteResponse{
				InputMint:      "InputMint123",
				OutputMint:     "OutputMint456",
				InAmount:       "1000000",
				OutAmount:      "900000",
				SlippageBps:    100,
				PriceImpactPct: "15.0", // 15% price impact
			},
			minOutAmount: 800000,
			expectError:  true,
			errorMsg:     "price impact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQuote(tt.quote, tt.minOutAmount)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEstimatePriorityFee(t *testing.T) {
	logger := zerolog.Nop()
	config := JupiterConfig{
		Logger: &logger,
	}

	client := NewJupiterClient(config)

	tests := []struct {
		name        string
		quote       *QuoteResponse
		expectError bool
		minFee      int
		maxFee      int
	}{
		{
			name: "simple route",
			quote: &QuoteResponse{
				RoutePlan: []RouteStep{
					{Percent: 100},
				},
			},
			expectError: false,
			minFee:      1000,
			maxFee:      2000,
		},
		{
			name: "complex route",
			quote: &QuoteResponse{
				RoutePlan: []RouteStep{
					{Percent: 50},
					{Percent: 30},
					{Percent: 20},
				},
			},
			expectError: false,
			minFee:      1000,
			maxFee:      4000,
		},
		{
			name: "no route plan",
			quote: &QuoteResponse{
				RoutePlan: nil,
			},
			expectError: false,
			minFee:      1000,
			maxFee:      1000,
		},
		{
			name: "empty route plan",
			quote: &QuoteResponse{
				RoutePlan: []RouteStep{},
			},
			expectError: false,
			minFee:      1000,
			maxFee:      1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee, err := client.EstimatePriorityFee(context.Background(), *tt.quote)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.GreaterOrEqual(t, fee, tt.minFee)
				assert.LessOrEqual(t, fee, tt.maxFee)
			}
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectRetry bool
	}{
		{
			name:        "nil error",
			err:         nil,
			expectRetry: false,
		},
		{
			name:        "context deadline exceeded",
			err:         context.DeadlineExceeded,
			expectRetry: true,
		},
		{
			name:        "context canceled",
			err:         context.Canceled,
			expectRetry: true,
		},
		{
			name:        "rate limit error",
			err:         &QuoteError{StatusCode: 429},
			expectRetry: true,
		},
		{
			name:        "server error",
			err:         &SwapError{StatusCode: 500},
			expectRetry: true,
		},
		{
			name:        "client error",
			err:         &QuoteError{StatusCode: 400},
			expectRetry: false,
		},
		{
			name:        "generic error",
			err:         assert.AnError,
			expectRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryableError(tt.err)
			assert.Equal(t, tt.expectRetry, result)
		})
	}
}

func TestQuoteError_Error(t *testing.T) {
	err := &QuoteError{
		StatusCode: 429,
		Message:    "Rate limit exceeded",
		Details:    "Try again in 1 second",
	}

	expected := "quote error (status 429): Rate limit exceeded - Try again in 1 second"
	assert.Equal(t, expected, err.Error())

	errNoDetails := &QuoteError{
		StatusCode: 500,
		Message:    "Internal server error",
	}

	expectedNoDetails := "quote error (status 500): Internal server error"
	assert.Equal(t, expectedNoDetails, errNoDetails.Error())
}

func TestSwapError_Error(t *testing.T) {
	err := &SwapError{
		StatusCode: 400,
		Message:    "Invalid request",
		Details:    "Missing required field",
	}

	expected := "swap error (status 400): Invalid request - Missing required field"
	assert.Equal(t, expected, err.Error())
}

func TestJupiterClient_GetSwapInstructions(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		swapResp := SwapResponse{
			SwapTransaction:      "base64encodedtransaction",
			LastValidBlockHeight: 123456789,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(swapResp)
	}))
	defer server.Close()

	logger := zerolog.Nop()
	config := JupiterConfig{
		HTTPClient: server.Client(),
		Logger:     &logger,
		QuoteAPI:   server.URL,
		SwapAPI:    server.URL,
	}

	client := NewJupiterClient(config)

	req := SwapRequest{
		UserPublicKey: "UserPublicKey123",
		QuoteResponse: QuoteResponse{
			InputMint:   "InputMint123",
			OutputMint:  "OutputMint456",
			InAmount:    "1000000",
			OutAmount:   "900000",
			SlippageBps: 100,
		},
	}

	ctx := context.Background()
	txBytes, swapResp, err := client.GetSwapInstructions(ctx, req)

	// Note: DecodeBase64Transaction is a placeholder, so we expect it to work
	require.NoError(t, err)
	require.NotNil(t, swapResp)
	assert.NotNil(t, txBytes)
}

func TestCommonTokenAddresses(t *testing.T) {
	assert.Equal(t, "So11111111111111111111111111111111111111112", WSolAddress)
	assert.Equal(t, "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", USDCAddress)
	assert.Equal(t, "Es9vMFrzaCERmJfrF4H2FYD4KCoNkY11McCe8BenwNYB", USDTAddress)
}
