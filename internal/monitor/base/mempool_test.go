package base

import (
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMempoolConfig tests mempool configuration.
func TestMempoolConfig(t *testing.T) {
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
		config      MempoolConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: MempoolConfig{
				RPCPool:             pool,
				FactoryAddress:      UniswapV3FactoryMainnet,
				ReconnectDelay:      5 * time.Second,
				SubscriptionTimeout: 30 * time.Second,
			},
			expectError: false,
		},
		{
			name: "missing RPC pool",
			config: MempoolConfig{
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: true,
			errorMsg:    "RPC pool is required",
		},
		{
			name: "default values",
			config: MempoolConfig{
				RPCPool:        pool,
				FactoryAddress: UniswapV3FactoryMainnet,
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor, err := NewMempoolMonitor(tt.config)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				assert.Nil(t, monitor)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, monitor)

				// Verify defaults are set
				if tt.config.ReconnectDelay == 0 {
					assert.Equal(t, 5*time.Second, monitor.config.ReconnectDelay)
				}
				if tt.config.SubscriptionTimeout == 0 {
					assert.Equal(t, 30*time.Second, monitor.config.SubscriptionTimeout)
				}
			}
		})
	}
}

// TestMempoolMonitorStatus tests the monitor status reporting.
func TestMempoolMonitorStatus(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	status := monitor.Status()

	assert.Equal(t, "mempool", status.Name)
	assert.Equal(t, "mempool", string(status.Source))
	assert.False(t, status.IsRunning)
	assert.Equal(t, int64(0), status.EventsDetected)
}

// TestPendingTransactionParsing tests pending transaction parsing.
func TestPendingTransactionParsing(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	// Create a mock transaction
	tx := types.NewTransaction(
		0,
		common.HexToAddress(UniswapV3FactoryMainnet),
		big.NewInt(0),
		100000,
		big.NewInt(1000000000),
		[]byte{},
	)

	// Sign the transaction
	signer := types.NewEIP155Signer(tx.ChainId())
	signedTx, err := types.SignTx(tx, signer, testKey)
	require.NoError(t, err)

	// Parse the transaction
	pendingTx, err := monitor.parsePendingTransaction(signedTx)
	require.NoError(t, err)

	assert.Equal(t, signedTx.Hash(), pendingTx.Hash)
	// Derive the actual address from the private key
	expectedFrom := crypto.PubkeyToAddress(testKey.PublicKey)
	assert.Equal(t, expectedFrom, pendingTx.From)
	assert.Equal(t, common.HexToAddress(UniswapV3FactoryMainnet), *pendingTx.To)
	assert.NotNil(t, pendingTx.Value)
	assert.NotNil(t, pendingTx.GasPrice)
	assert.Equal(t, uint64(100000), pendingTx.GasLimit)
}

// TestUniswapTransactionDetection tests Uniswap transaction detection.
func TestUniswapTransactionDetection(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	tests := []struct {
		name     string
		to       string
		data     []byte
		expected bool
	}{
		{
			name:     "Uniswap factory transaction",
			to:       UniswapV3FactoryMainnet,
			data:     []byte{},
			expected: true,
		},
		{
			name:     "CreatePool function selector",
			to:       "0x1234567890123456789012345678901234567890",
			data:     hexutil.MustDecode("0x5dee5714"),
			expected: true,
		},
		{
			name:     "CreatePair function selector (V2)",
			to:       "0x1234567890123456789012345678901234567890",
			data:     hexutil.MustDecode("0xc9c65396"),
			expected: true,
		},
		{
			name:     "Non-Uniswap transaction",
			to:       "0x1234567890123456789012345678901234567890",
			data:     []byte{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pendingTx := &PendingTransaction{
				To:   &common.Address{},
				Data: tt.data,
			}

			if tt.to != "" {
				addr := common.HexToAddress(tt.to)
				pendingTx.To = &addr
			}

			result := monitor.isUniswapTransaction(pendingTx)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestMempoolState tests mempool state reporting.
func TestMempoolState(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	// Add some pending transactions
	txHash := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	pendingTx := &PendingTransaction{
		Hash:               txHash,
		From:               common.HexToAddress(testAddress),
		IsPotentialUniswap: true,
		FirstSeen:          time.Now(),
	}

	monitor.pendingTxsMu.Lock()
	monitor.pendingTxs[txHash] = pendingTx
	monitor.pendingTxsMu.Unlock()

	// Get mempool state
	state := monitor.GetMempoolState()

	assert.Equal(t, 1, state["pending_tx_count"])
	assert.Contains(t, state, "recent_transactions")
	assert.Contains(t, state, "frontrun_detected")

	recentTxs := state["recent_transactions"].([]map[string]any)
	assert.Len(t, recentTxs, 1)
	assert.Equal(t, txHash.Hex(), recentTxs[0]["hash"])
}

// TestPendingTxCleanup tests pending transaction cleanup.
func TestPendingTxCleanup(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	// Add old transaction
	oldTxHash := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	oldTx := &PendingTransaction{
		Hash:      oldTxHash,
		FirstSeen: time.Now().Add(-10 * time.Minute), // 10 minutes ago
	}

	// Add recent transaction
	recentTxHash := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002")
	recentTx := &PendingTransaction{
		Hash:      recentTxHash,
		FirstSeen: time.Now(),
	}

	monitor.pendingTxsMu.Lock()
	monitor.pendingTxs[oldTxHash] = oldTx
	monitor.pendingTxs[recentTxHash] = recentTx
	monitor.pendingTxsMu.Unlock()

	// Run cleanup
	monitor.cleanupOldPendingTxs()

	// Verify old transaction was removed
	monitor.pendingTxsMu.RLock()
	_, exists := monitor.pendingTxs[oldTxHash]
	monitor.pendingTxsMu.RUnlock()

	assert.False(t, exists, "Old transaction should be removed")

	// Verify recent transaction still exists
	monitor.pendingTxsMu.RLock()
	_, exists = monitor.pendingTxs[recentTxHash]
	monitor.pendingTxsMu.RUnlock()

	assert.True(t, exists, "Recent transaction should still exist")
}

// TestMonitorLifecycle tests monitor lifecycle.
func TestMempoolMonitorLifecycle(t *testing.T) {
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

	config := MempoolConfig{
		RPCPool:        pool,
		FactoryAddress: UniswapV3FactoryMainnet,
		WSSEndpoint:    "wss://localhost:8545", // Invalid for testing
	}

	monitor, err := NewMempoolMonitor(config)
	require.NoError(t, err)

	// Test initial state
	status := monitor.Status()
	assert.False(t, status.IsRunning)
	assert.Equal(t, "mempool", status.Name)

	// Start (may fail due to invalid endpoint)
	_ = monitor.Start()

	// Stop should always work
	err = monitor.Stop()
	assert.NoError(t, err)

	// Verify stopped state
	status = monitor.Status()
	assert.False(t, status.IsRunning)
}

// Test constants.
func TestConstants(t *testing.T) {
	assert.Equal(t, "0x33128a8fC17869897dcE68Ed026d694621f6FDfD", UniswapV3FactoryMainnet)
	assert.Equal(t, "0x8909Dc15e40173Ff4699343b6eB8132c65e18eC6", UniswapV2FactoryMainnet)
	assert.Equal(t, "0x4200000000000000000000000000000000000006", WETHMainnet)
	assert.Equal(t, "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913", USDCMainnet)
}

// Helper variables for testing.
var (
	testKey, _  = crypto.HexToECDSA("b71c71a67e1177ad4e901d9594b39807972193156b84c7fd0ccc55d2e04d4b64")
	testAddress = "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb"
	testPrivKey *ecdsa.PrivateKey
)
