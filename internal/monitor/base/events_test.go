package base

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventsConfig tests event subscription configuration.
func TestEventsConfig(t *testing.T) {
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
		config      EventsConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: EventsConfig{
				RPCPool:             pool,
				FactoryAddress:      UniswapV3FactoryMainnet,
				ReconnectDelay:      5 * time.Second,
				SubscriptionTimeout: 30 * time.Second,
				ConfirmationBlocks:  2,
				BatchSize:           100,
			},
			expectError: false,
		},
		{
			name: "missing RPC pool",
			config: EventsConfig{
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: true,
			errorMsg:    "RPC pool is required",
		},
		{
			name: "default values",
			config: EventsConfig{
				RPCPool:        pool,
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subscription, err := NewEventSubscription(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, subscription)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, subscription)

				// Verify defaults are set
				if tt.config.ReconnectDelay == 0 {
					assert.Equal(t, 5*time.Second, subscription.config.ReconnectDelay)
				}
				if tt.config.SubscriptionTimeout == 0 {
					assert.Equal(t, 30*time.Second, subscription.config.SubscriptionTimeout)
				}
				if tt.config.ConfirmationBlocks == 0 {
					assert.Equal(t, uint64(2), subscription.config.ConfirmationBlocks)
				}
				if tt.config.BatchSize == 0 {
					assert.Equal(t, uint64(100), subscription.config.BatchSize)
				}
			}
		})
	}
}

// TestEventSubscriptionStatus tests the subscription status reporting.
func TestEventSubscriptionStatus(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	status := subscription.Status()

	assert.Equal(t, "events", status.Name)
	assert.Equal(t, monitor.ChainTypeBase, status.Chain)
	assert.Equal(t, "events", string(status.Source))
	assert.False(t, status.IsRunning)
	assert.Equal(t, int64(0), status.EventsDetected)
}

// TestBlockTracker tests block tracking for reorg detection.
func TestBlockTracker(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Verify block tracker is initialized
	assert.NotNil(t, subscription.blockTracker)
	assert.NotNil(t, subscription.blockTracker.blocks)
	assert.NotNil(t, subscription.blockTracker.canonical)
	assert.Equal(t, uint64(0), subscription.blockTracker.lastBlock)
}

// TestBlockTrackerStatus tests block tracker status reporting.
func TestBlockTrackerStatus(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Add a mock block
	blockNumber := uint64(12345)
	header := &types.Header{
		Number: big.NewInt(int64(blockNumber)),
	}
	subscription.blockTracker.blocks[blockNumber] = header
	subscription.blockTracker.canonical[blockNumber] = true
	subscription.blockTracker.lastBlock = blockNumber

	// Get status
	status := subscription.GetBlockTrackerStatus()

	assert.Equal(t, uint64(12345), status["last_block"])
	assert.Equal(t, 1, status["tracked_blocks"])
	assert.Equal(t, 1, status["canonical_blocks"])
	assert.Contains(t, status, "reorg_depth")
}

// TestReorgHandler tests reorg handler functionality.
func TestReorgHandler(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Set reorg handler
	reorgCalled := false
	reorgStart := uint64(0)
	reorgEnd := uint64(0)

	subscription.SetReorgHandler(func(start, end uint64) {
		reorgCalled = true
		reorgStart = start
		reorgEnd = end
	})

	// Simulate reorg detection
	if subscription.reorgHandler != nil {
		subscription.reorgHandler(100, 105)
	}

	assert.True(t, reorgCalled)
	assert.Equal(t, uint64(100), reorgStart)
	assert.Equal(t, uint64(105), reorgEnd)
}

// TestProcessNewBlock tests new block processing.
func TestProcessNewBlock(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	tests := []struct {
		name        string
		headerData  map[string]any
		expectError bool
		checkReorg  bool
	}{
		{
			name: "valid new block",
			headerData: map[string]any{
				"number":     "0x123456",
				"hash":       "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
			},
			expectError: false,
			checkReorg:  false,
		},
		{
			name: "block with reorg",
			headerData: map[string]any{
				"number":     "0x123456",
				"hash":       "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000001",
			},
			expectError: false,
			checkReorg:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up previous block if testing reorg
			if tt.checkReorg {
				prevHeader := &types.Header{
					Number: big.NewInt(0x123455),
				}
				// Set parent hash to different value
				subscription.blockTracker.blocks[0x123455] = prevHeader
			}

			err := subscription.processNewBlock(tt.headerData)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCleanupOldBlocks tests cleanup of old blocks.
func TestCleanupOldBlocks(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Add blocks
	for i := range uint64(20) {
		header := &types.Header{
			Number: big.NewInt(int64(i)),
		}
		subscription.blockTracker.blocks[i] = header
		subscription.blockTracker.canonical[i] = true
	}
	subscription.blockTracker.lastBlock = 19

	// Run cleanup (should remove blocks < 15, keeping last 5 + reorg depth buffer)
	subscription.cleanupOldBlocks()

	// Verify old blocks are removed
	_, exists := subscription.blockTracker.blocks[5]
	assert.False(t, exists, "Old block should be removed")

	// Verify recent blocks still exist
	_, exists = subscription.blockTracker.blocks[18]
	assert.True(t, exists, "Recent block should still exist")
}

// TestEventSubscriptionLifecycle tests subscription lifecycle.
func TestEventSubscriptionLifecycle(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
		WSSEndpoint:    "wss://localhost:8545", // Invalid for testing
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Test initial state
	status := subscription.Status()
	assert.False(t, status.IsRunning)
	assert.Equal(t, "events", status.Name)

	// Start (may fail due to invalid endpoint or lack of RPC connection)
	// We're mainly testing that it doesn't crash
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start in goroutine to avoid blocking
	go func() {
		_ = subscription.Start()
	}()

	// Wait a bit then stop
	<-ctx.Done()
	err = subscription.Stop()
	assert.NoError(t, err)

	// Verify stopped state
	status = subscription.Status()
	assert.False(t, status.IsRunning)
}

// TestCanonicalChainTracking tests canonical chain tracking.
func TestCanonicalChainTracking(t *testing.T) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(t, err)

	// Mark some blocks as canonical/non-canonical
	subscription.blockTracker.canonicalMu.Lock()
	subscription.blockTracker.canonical[100] = true
	subscription.blockTracker.canonical[101] = true
	subscription.blockTracker.canonical[102] = false // Reorged
	subscription.blockTracker.canonicalMu.Unlock()

	// Verify state
	subscription.blockTracker.canonicalMu.RLock()
	is100Canonical := subscription.blockTracker.canonical[100]
	is102Canonical := subscription.blockTracker.canonical[102]
	subscription.blockTracker.canonicalMu.RUnlock()

	assert.True(t, is100Canonical)
	assert.False(t, is102Canonical)
}

// BenchmarkBlockProcessing benchmarks block processing.
func BenchmarkBlockProcessing(b *testing.B) {
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

	config := EventsConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	subscription, err := NewEventSubscription(config)
	require.NoError(b, err)

	headerData := map[string]any{
		"number":     "0x123456",
		"hash":       "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		"parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
	}

	for b.Loop() {
		_ = subscription.processNewBlock(headerData)
	}
}
