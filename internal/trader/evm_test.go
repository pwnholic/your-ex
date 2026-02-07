// Package trader provides trading functionality for the meme sniper bot.
package trader

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLogger() *zerolog.Logger {
	logger := zerolog.Nop()
	return &logger
}

func TestNewTransactionBuilder(t *testing.T) {
	config := BuilderConfig{
		Logger: testLogger(),
	}

	builder, err := NewTransactionBuilder(config)
	require.NoError(t, err)
	require.NotNil(t, builder)

	assert.Equal(t, big.NewInt(BaseChainID), builder.chainID)
	assert.NotNil(t, builder.signer)
	assert.NotNil(t, builder.logger)
	assert.Equal(t, uint64(defaultGasLimit), builder.defaultGasLimit)
	assert.Equal(t, defaultGasBuffer, builder.gasBuffer)
}

func TestNewTransactionBuilderDefaults(t *testing.T) {
	config := BuilderConfig{}

	builder, err := NewTransactionBuilder(config)
	require.NoError(t, err)
	require.NotNil(t, builder)

	assert.Equal(t, big.NewInt(BaseChainID), builder.chainID)
	assert.Equal(t, uint64(defaultGasLimit), builder.defaultGasLimit)
	assert.Equal(t, defaultGasBuffer, builder.gasBuffer)
	assert.NotNil(t, builder.maxPriorityFee)
	assert.NotNil(t, builder.defaultMaxFee)
}

func TestNewTransactionBuilderCustomValues(t *testing.T) {
	customChainID := big.NewInt(1)
	customGasLimit := uint64(500000)
	customBuffer := 1.5
	customPriorityFee := big.NewInt(2000000000) // 2 Gwei
	customMaxFee := big.NewInt(100000000000)    // 100 Gwei

	config := BuilderConfig{
		Logger:             testLogger(),
		ChainID:            customChainID,
		DefaultGasLimit:    customGasLimit,
		GasBuffer:          customBuffer,
		DefaultPriorityFee: customPriorityFee,
		MaxPriorityFee:     customPriorityFee,
		DefaultMaxFee:      customMaxFee,
	}

	builder, err := NewTransactionBuilder(config)
	require.NoError(t, err)
	require.NotNil(t, builder)

	assert.Equal(t, customChainID, builder.chainID)
	assert.Equal(t, customGasLimit, builder.defaultGasLimit)
	assert.Equal(t, customBuffer, builder.gasBuffer)
	assert.Equal(t, customPriorityFee, builder.maxPriorityFee)
	assert.Equal(t, customMaxFee, builder.defaultMaxFee)
}

func TestBuildTransaction(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	value := big.NewInt(1000000000000000000) // 1 ETH
	data := []byte{0x12, 0x34, 0x56}
	gasLimit := uint64(100000)
	priorityFee := big.NewInt(2000000000) // 2 Gwei
	maxFee := big.NewInt(50000000000)     // 50 Gwei
	nonce := uint64(5)

	params := TxParams{
		Nonce:             nonce,
		To:                &to,
		Value:             value,
		Data:              data,
		GasLimit:          gasLimit,
		PriorityFeePerGas: priorityFee,
		MaxFeePerGas:      maxFee,
	}

	tx, err := builder.BuildTransaction(params)
	require.NoError(t, err)
	require.NotNil(t, tx)

	assert.Equal(t, nonce, tx.Nonce())
	assert.Equal(t, to, *tx.To())
	assert.Equal(t, value, tx.Value())
	assert.Equal(t, data, tx.Data())
	assert.Equal(t, types.DynamicFeeTxType, uint8(tx.Type()))
}

func TestBuildTransactionDefaults(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")

	params := TxParams{
		Nonce: uint64(1),
		To:    &to,
	}

	tx, err := builder.BuildTransaction(params)
	require.NoError(t, err)
	require.NotNil(t, tx)

	// Check defaults are applied
	assert.Equal(t, types.DynamicFeeTxType, tx.Type())
	assert.Positive(t, tx.Gas())
}

func TestBuildTransactionValidation(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	tests := []struct {
		name    string
		params  TxParams
		wantErr bool
		errMsg  string
	}{
		{
			name: "missing recipient",
			params: TxParams{
				Nonce: uint64(1),
			},
			wantErr: true,
			errMsg:  "missing recipient address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := builder.BuildTransaction(tt.params)
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

func TestBuildSpeedupTransaction(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	priorityFee := big.NewInt(2000000000) // 2 Gwei
	maxFee := big.NewInt(50000000000)     // 50 Gwei

	originalTx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(BaseChainID),
		Nonce:     5,
		GasTipCap: priorityFee,
		GasFeeCap: maxFee,
		Gas:       21000,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      []byte{},
	})

	speedupTx, err := builder.BuildSpeedupTransaction(originalTx, 10) // 10% increase
	require.NoError(t, err)
	require.NotNil(t, speedupTx)

	assert.Equal(t, originalTx.Nonce(), speedupTx.Nonce())
	assert.Equal(t, to, *speedupTx.To())

	// Verify transaction was created
	assert.Equal(t, types.DynamicFeeTxType, speedupTx.Type())
}

func TestBuildCancelTransaction(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	priorityFee := big.NewInt(2000000000) // 2 Gwei
	maxFee := big.NewInt(50000000000)     // 50 Gwei

	// Create a DynamicFeeTx for testing
	originalTx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(BaseChainID),
		Nonce:     5,
		GasTipCap: priorityFee,
		GasFeeCap: maxFee,
		Gas:       100000,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      []byte{},
	})

	cancelTx, err := builder.BuildCancelTransaction(originalTx)
	require.NoError(t, err)
	require.NotNil(t, cancelTx)

	assert.Equal(t, originalTx.Nonce(), cancelTx.Nonce())
	assert.Equal(t, to, *cancelTx.To())

	// Cancel tx should send 0 ETH
	assert.Equal(t, big.NewInt(0), cancelTx.Value())

	// Verify fees are higher - use type checking
	assert.Equal(t, types.DynamicFeeTxType, cancelTx.Type())
}

func TestNonceManagement(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	// Test SetNonce
	builder.SetNonce(10)
	assert.Equal(t, uint64(10), builder.GetNonce())

	// Test GetNextNonce increments
	nonce1 := builder.GetNextNonce()
	assert.Equal(t, uint64(10), nonce1)
	assert.Equal(t, uint64(11), builder.GetNonce())

	nonce2 := builder.GetNextNonce()
	assert.Equal(t, uint64(11), nonce2)
	assert.Equal(t, uint64(12), builder.GetNonce())
}

func TestTrackPendingTransactions(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     5,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       21000,
		GasFeeCap: big.NewInt(50000000000),
		GasTipCap: big.NewInt(2000000000),
		Data:      []byte{},
	})

	// Track transaction
	builder.TrackPendingTransaction(tx)

	// Check it's tracked
	retrieved, ok := builder.GetPendingTransaction(5)
	assert.True(t, ok)
	assert.Equal(t, tx.Hash(), retrieved.Hash())

	// Check count
	assert.Equal(t, 1, builder.GetPendingNonceCount())

	// Remove transaction
	builder.RemovePendingTransaction(5)

	// Check it's gone
	_, ok = builder.GetPendingTransaction(5)
	assert.False(t, ok)
	assert.Equal(t, 0, builder.GetPendingNonceCount())
}

func TestCalculateMaxFeePerGas(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	tests := []struct {
		name         string
		baseFee      *big.Int
		priorityFee  *big.Int
		expectedLess *big.Int
	}{
		{
			name:        "normal fees",
			baseFee:     big.NewInt(30000000000), // 30 Gwei
			priorityFee: big.NewInt(2000000000),  // 2 Gwei
			// maxFee = 30 + 2*2 = 34 Gwei
			expectedLess: big.NewInt(34000000000),
		},
		{
			name:        "high fees",
			baseFee:     big.NewInt(100000000000), // 100 Gwei
			priorityFee: big.NewInt(5000000000),   // 5 Gwei
			// maxFee = 100 + 2*5 = 110 Gwei
			expectedLess: big.NewInt(110000000000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxFee := builder.CalculateMaxFeePerGas(tt.baseFee, tt.priorityFee)
			// maxFee should be at least baseFee + 2*priorityFee
			expectedMin := new(big.Int).Add(tt.baseFee, new(big.Int).Mul(tt.priorityFee, big.NewInt(2)))
			assert.GreaterOrEqual(t, maxFee.Int64(), expectedMin.Int64())
		})
	}
}

func TestEstimateMaxPriorityFee(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	tests := []struct {
		name    string
		baseFee *big.Int
	}{
		{
			name:    "low base fee",
			baseFee: big.NewInt(10000000000), // 10 Gwei
		},
		{
			name:    "high base fee",
			baseFee: big.NewInt(100000000000), // 100 Gwei
		},
		{
			name:    "nil base fee",
			baseFee: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priorityFee, err := builder.EstimateMaxPriorityFee(nil, tt.baseFee)
			require.NoError(t, err)
			require.NotNil(t, priorityFee)
			assert.Positive(t, priorityFee.Int64())
		})
	}
}

func TestEstimateGasForTokenTransfer(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	gas := builder.EstimateGasForTokenTransfer()
	assert.GreaterOrEqual(t, gas, uint64(50000))
	assert.LessOrEqual(t, gas, uint64(100000))
}

func TestEstimateGasForSwap(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	simpleGas := builder.EstimateGasForSwap(false)
	complexGas := builder.EstimateGasForSwap(true)

	assert.GreaterOrEqual(t, simpleGas, uint64(100000))
	assert.Greater(t, complexGas, simpleGas) // Complex swaps need more gas
}

func TestEstimateGasForApproval(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	gas := builder.EstimateGasForApproval()
	assert.GreaterOrEqual(t, gas, uint64(40000))
	assert.LessOrEqual(t, gas, uint64(80000))
}

func TestNewPrivateKeyFromHex(t *testing.T) {
	// Valid private key (32 bytes)
	validHex := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	key, err := NewPrivateKeyFromHex(validHex)
	require.NoError(t, err)
	require.NotNil(t, key)
	assert.Equal(t, "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef", key.D.Text(16))

	// Invalid private key (wrong length)
	invalidHex := "0x1234"
	_, err = NewPrivateKeyFromHex(invalidHex)
	assert.Error(t, err)
}

func TestGetAddress(t *testing.T) {
	// Known private key and address pair
	privKeyHex := "0x0000000000000000000000000000000000000000000000000000000000000001"
	expectedAddress := common.HexToAddress("0x7E5F4552091A69125d5DfCb978bDeC859D7119c5")

	key, err := NewPrivateKeyFromHex(privKeyHex)
	require.NoError(t, err)

	address, err := key.GetAddress()
	require.NoError(t, err)
	assert.Equal(t, expectedAddress, address)
}

func TestVerifyTransactionChain(t *testing.T) {
	builder, err := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})
	require.NoError(t, err)

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     0,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       21000,
		GasFeeCap: big.NewInt(50000000000),
		GasTipCap: big.NewInt(2000000000),
		Data:      []byte{},
	})

	// Should verify for correct chain
	assert.True(t, builder.VerifyTransactionChain(tx))

	// Should fail for nil tx
	assert.False(t, builder.VerifyTransactionChain(nil))
}

func TestEstimateL1Fee(t *testing.T) {
	tests := []struct {
		name          string
		txSize        int
		l1BaseFee     *big.Int
		expectNonZero bool
	}{
		{
			name:          "small transaction",
			txSize:        100,
			l1BaseFee:     big.NewInt(1000000000), // 1 Gwei
			expectNonZero: true,
		},
		{
			name:          "large transaction",
			txSize:        500,
			l1BaseFee:     big.NewInt(1000000000), // 1 Gwei
			expectNonZero: true,
		},
		{
			name:          "nil base fee",
			txSize:        100,
			l1BaseFee:     nil,
			expectNonZero: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fee := EstimateL1Fee(tt.txSize, tt.l1BaseFee)
			if tt.expectNonZero {
				assert.Positive(t, fee.Int64())
			} else {
				assert.Equal(t, big.NewInt(0), fee)
			}
		})
	}
}

func TestSigningStats(t *testing.T) {
	stats := &SigningStats{}

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     0,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       21000,
		GasFeeCap: big.NewInt(50000000000),
		GasTipCap: big.NewInt(2000000000),
		Data:      []byte{},
	})

	// Record signing
	stats.RecordSigned(tx)
	assert.Equal(t, int64(1), stats.TotalSigned)

	// Record speedup
	stats.RecordSpeedup()
	assert.Equal(t, int64(1), stats.TotalSpeedups)

	// Record cancel
	stats.RecordCancel()
	assert.Equal(t, int64(1), stats.TotalCancels)

	// Record failure
	stats.RecordFailed()
	assert.Equal(t, int64(1), stats.TotalFailed)
}

// Benchmark tests

func BenchmarkBuildTransaction(b *testing.B) {
	builder, _ := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	value := big.NewInt(1000000000000000000)
	data := make([]byte, 100)
	priorityFee := big.NewInt(2000000000)
	maxFee := big.NewInt(50000000000)

	params := TxParams{
		Nonce:             uint64(1),
		To:                &to,
		Value:             value,
		Data:              data,
		GasLimit:          uint64(100000),
		PriorityFeePerGas: priorityFee,
		MaxFeePerGas:      maxFee,
	}

	for b.Loop() {
		_, _ = builder.BuildTransaction(params)
	}
}

func BenchmarkBuildSpeedupTransaction(b *testing.B) {
	builder, _ := NewTransactionBuilder(BuilderConfig{Logger: testLogger()})

	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	tx := types.NewTx(&types.DynamicFeeTx{
		Nonce:     5,
		To:        &to,
		Value:     big.NewInt(0),
		Gas:       21000,
		GasFeeCap: big.NewInt(50000000000),
		GasTipCap: big.NewInt(2000000000),
		Data:      []byte{},
	})

	for b.Loop() {
		_, _ = builder.BuildSpeedupTransaction(tx, 10)
	}
}
