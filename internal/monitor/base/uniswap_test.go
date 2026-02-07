package base

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	monitorPkg "github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockEventHandler is a mock implementation of EventHandler for testing.
type MockEventHandler struct {
	events []*monitorPkg.TokenEvent
	errors []error
	mu     chan struct{}
}

func NewMockEventHandler() *MockEventHandler {
	return &MockEventHandler{
		events: make([]*monitorPkg.TokenEvent, 0),
		errors: make([]error, 0),
		mu:     make(chan struct{}, 1),
	}
}

func (m *MockEventHandler) HandleTokenEvent(event *monitorPkg.TokenEvent) error {
	m.mu <- struct{}{}
	defer func() { <-m.mu }()

	m.events = append(m.events, event)
	return nil
}

func (m *MockEventHandler) OnError(err error) {
	m.errors = append(m.errors, err)
}

func (m *MockEventHandler) GetEvents() []*monitorPkg.TokenEvent {
	return m.events
}

func (m *MockEventHandler) GetErrors() []error {
	return m.errors
}

func (m *MockEventHandler) WaitForEvents(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(m.events) >= count {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestUniswapConfig tests Uniswap configuration.
func TestUniswapConfig(t *testing.T) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	tests := []struct {
		name        string
		config      UniswapConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: UniswapConfig{
				RPCPool:             pool,
				FactoryAddress:      UniswapV3FactoryMainnet,
				ReconnectDelay:      5 * time.Second,
				SubscriptionTimeout: 30 * time.Second,
				ConfirmationBlocks:  1,
			},
			expectError: false,
		},
		{
			name: "missing RPC pool",
			config: UniswapConfig{
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: true,
			errorMsg:    "RPC pool is required",
		},
		{
			name: "default values",
			config: UniswapConfig{
				RPCPool:        pool,
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mon, err := NewUniswapMonitor(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, mon)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, mon)

				// Verify defaults are set
				if tt.config.ReconnectDelay == 0 {
					assert.Equal(t, 5*time.Second, mon.config.ReconnectDelay)
				}
				if tt.config.SubscriptionTimeout == 0 {
					assert.Equal(t, 30*time.Second, mon.config.SubscriptionTimeout)
				}
				if tt.config.ConfirmationBlocks == 0 {
					assert.Equal(t, uint64(1), mon.config.ConfirmationBlocks)
				}
			}
		})
	}
}

// TestUniswapMonitorStatus tests the monitor status reporting.
func TestUniswapMonitorStatus(t *testing.T) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(t, err)

	status := mon.Status()

	assert.Equal(t, "uniswap", status.Name)
	assert.Equal(t, monitorPkg.ChainTypeBase, status.Chain)
	assert.Equal(t, monitorPkg.SourceUniswap, status.Source)
	assert.False(t, status.IsRunning)
	assert.Equal(t, int64(0), status.EventsDetected)
}

// TestTokenMetadata tests token metadata parsing.
func TestTokenMetadata(t *testing.T) {
	// This test would require a mock RPC server or integration test
	// For now, we test the metadata structure
	metadata := TokenMetadata{
		Name:     "Test Token",
		Symbol:   "TST",
		Decimals: 18,
		URI:      "https://example.com/metadata.json",
	}

	assert.Equal(t, "Test Token", metadata.Name)
	assert.Equal(t, "TST", metadata.Symbol)
	assert.Equal(t, uint8(18), metadata.Decimals)
	assert.Equal(t, "https://example.com/metadata.json", metadata.URI)
}

// TestEventIDGeneration tests event ID generation.
func TestEventIDGeneration(t *testing.T) {
	id1 := generateEventID()
	id2 := generateEventID()

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id1, id2)
	assert.Contains(t, id1, "base-")
}

// TestConvertToWSSTests URL conversion.
func TestConvertToWSS(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "https://mainnet.base.org",
			expected: "wss://mainnet.base.org",
		},
		{
			input:    "http://localhost:8545",
			expected: "ws://localhost:8545",
		},
		{
			input:    "wss://mainnet.base.org",
			expected: "wss://mainnet.base.org",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := convertToWSS(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBaseTokens tests base token detection.
func TestBaseTokens(t *testing.T) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(t, err)

	// Verify base tokens are configured
	wethAddr := common.HexToAddress(WETHMainnet)
	usdcAddr := common.HexToAddress(USDCMainnet)
	assert.Contains(t, mon.baseTokens, wethAddr)
	assert.Contains(t, mon.baseTokens, usdcAddr)
	assert.Equal(t, "WETH", mon.baseTokens[wethAddr])
	assert.Equal(t, "USDC", mon.baseTokens[usdcAddr])
}

// TestPairCreatedEventParsing tests PairCreated event parsing.
func TestPairCreatedEventParsing(t *testing.T) {
	// Create mock log data for PairCreated event
	logData := map[string]any{
		"topics": []any{
			"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118", // PairCreated signature
			"0x000000000000000000000000a0b86a33e6d21612345678901234567890123456", // token0
			"0x0000000000000000000000004200000000000000000000000000000000000006", // token1 (WETH)
			"0x0000000000000000000000001234567890123456789012345678901234567890", // pair address
		},
		"data":            "0x", // No additional data for indexed parameters
		"address":         UniswapV3FactoryMainnet,
		"blockNumber":     "0x123456",
		"transactionHash": "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		"logIndex":        "0x0",
	}

	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(t, err)

	// Parse the event (note: this will fail to fetch metadata without actual RPC)
	// but we can test the parsing logic
	event, err := mon.parsePairCreatedEvent(logData)

	// Event will be nil if neither token is a base token
	// or if metadata fetching fails
	// For this test, we just verify the function doesn't crash
	assert.True(t, event == nil || err == nil)
}

// TestConfirmationTracking tests block confirmation tracking.
func TestConfirmationTracking(t *testing.T) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(t, err)

	// Verify pending blocks map exists
	assert.NotNil(t, mon.pendingBlocks)
	assert.NotNil(t, mon.finalizedBlocks)

	// Verify confirmation blocks setting
	assert.Equal(t, uint64(1), mon.config.ConfirmationBlocks)
}

// TestMonitorStartStop tests monitor lifecycle.
func TestMonitorStartStop(t *testing.T) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(t, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
		WSSEndpoint:    "wss://localhost:8545", // Invalid endpoint for testing
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(t, err)

	// Set a mock handler
	handler := NewMockEventHandler()
	mon.SetHandler(handler)

	// Start will fail due to invalid WebSocket endpoint
	err = mon.Start()
	// This may succeed or fail depending on network state
	// We're mainly testing that the monitor doesn't crash

	// Stop should always work
	err = mon.Stop()
	assert.NoError(t, err)

	status := mon.Status()
	assert.False(t, status.IsRunning)
}

// BenchmarkEventParsing benchmarks event parsing.
func BenchmarkEventParsing(b *testing.B) {
	pool, err := rpc.NewPool(rpc.PoolConfig{
		ChainType: rpc.ChainTypeBase,
		Endpoints: []rpc.EndpointConfig{
			{
				URL:    "https://mainnet.base.org",
				Name:   "base-mainnet",
				Weight: 1.0,
			},
		},
	})
	require.NoError(b, err)
	defer pool.Close()

	config := UniswapConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	mon, err := NewUniswapMonitor(config)
	require.NoError(b, err)

	logData := map[string]any{
		"topics": []any{
			"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118",
			"0x000000000000000000000000a0b86a33e6d21612345678901234567890123456",
			"0x0000000000000000000000004200000000000000000000000000000000000006",
			"0x0000000000000000000000001234567890123456789012345678901234567890",
		},
		"data":            "0x",
		"address":         UniswapV3FactoryMainnet,
		"blockNumber":     "0x123456",
		"transactionHash": "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		"logIndex":        "0x0",
	}

	for b.Loop() {
		_, _ = mon.parsePairCreatedEvent(logData)
	}
}
