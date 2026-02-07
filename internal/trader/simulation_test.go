package trader

import (
	"context"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	solanaRPC "github.com/gagliardetto/solana-go/rpc"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSimulator(t *testing.T) {
	logger := zerolog.Nop()
	config := SimulatorConfig{
		RPCPool:    &rpc.Pool{},
		Logger:     &logger,
		Timeout:    20 * time.Second,
		SimSlots:   20,
		Commitment: "confirmed",
		MaxRetries: 5,
		RetryDelay: 1 * time.Second,
	}

	simulator := NewSimulator(config)

	assert.NotNil(t, simulator)
	assert.Equal(t, 20*time.Second, simulator.timeout)
	assert.Equal(t, uint64(20), simulator.simSlots)
	assert.Equal(t, "confirmed", simulator.commitment)
	assert.Equal(t, 5, simulator.maxRetries)
	assert.Equal(t, 1*time.Second, simulator.retryDelay)
}

func TestNewSimulator_Defaults(t *testing.T) {
	logger := zerolog.Nop()
	config := SimulatorConfig{
		RPCPool: &rpc.Pool{},
		Logger:  &logger,
	}

	simulator := NewSimulator(config)

	assert.NotNil(t, simulator)
	assert.Equal(t, defaultSimTimeout, simulator.timeout)
	assert.Equal(t, uint64(defaultSimSlots), simulator.simSlots)
	assert.Equal(t, string(defaultSimCommitment), simulator.commitment)
	assert.Equal(t, maxSimRetries, simulator.maxRetries)
	assert.Equal(t, simRetryDelay, simulator.retryDelay)
}

func TestSimulationResult(t *testing.T) {
	result := &SimulationResult{
		Value: SimulationValue{
			Logs:          []string{"Program log: Test"},
			UnitsConsumed: 100000,
		},
		Logs:          []string{"Program log: Test"},
		UnitsConsumed: 100000,
		Duration:      100 * time.Millisecond,
	}

	assert.Equal(t, []string{"Program log: Test"}, result.GetLogs())
	assert.NoError(t, result.GetError())
	assert.Equal(t, 100*time.Millisecond, result.GetDuration())
	assert.Equal(t, uint64(100000), result.GetComputeUnits())
}

func TestValidateSimulation(t *testing.T) {
	tests := []struct {
		name        string
		result      *SimulationResult
		expectError bool
		errorMsg    string
	}{
		{
			name: "successful simulation",
			result: &SimulationResult{
				Value: SimulationValue{
					Logs:          []string{"Program log: Success"},
					UnitsConsumed: 100000,
				},
				Logs:          []string{"Program log: Success"},
				UnitsConsumed: 100000,
			},
			expectError: false,
		},
		{
			name:        "nil result",
			result:      nil,
			expectError: true,
			errorMsg:    "nil",
		},
		{
			name: "simulation error",
			result: &SimulationResult{
				Value: SimulationValue{
					Err:           "Custom error",
					UnitsConsumed: 100000,
				},
				Err: assert.AnError,
			},
			expectError: true,
			errorMsg:    "would fail",
		},
		{
			name: "error in logs",
			result: &SimulationResult{
				Value: SimulationValue{
					Logs:          []string{"Program log: failed to execute"},
					UnitsConsumed: 100000,
				},
				Logs:          []string{"Program log: failed to execute"},
				UnitsConsumed: 100000,
			},
			expectError: true,
			errorMsg:    "suspicious",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSimulation(tt.result)

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

func TestIsSimulationSuccessful(t *testing.T) {
	tests := []struct {
		name     string
		result   *SimulationResult
		expected bool
	}{
		{
			name: "successful",
			result: &SimulationResult{
				Value: SimulationValue{
					Logs:          []string{"Program log: Success"},
					UnitsConsumed: 100000,
				},
				Logs:          []string{"Program log: Success"},
				UnitsConsumed: 100000,
			},
			expected: true,
		},
		{
			name:     "nil result",
			result:   nil,
			expected: false,
		},
		{
			name: "with error",
			result: &SimulationResult{
				Value: SimulationValue{
					Err:           "Error",
					UnitsConsumed: 100000,
				},
				Err: assert.AnError,
			},
			expected: false,
		},
		{
			name: "with value error",
			result: &SimulationResult{
				Value: SimulationValue{
					Err:           "Error",
					UnitsConsumed: 100000,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsSimulationSuccessful(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEstimateExecutionTime(t *testing.T) {
	tests := []struct {
		name         string
		computeUnits uint64
		minDuration  time.Duration
		maxDuration  time.Duration
	}{
		{
			name:         "small transaction",
			computeUnits: 100_000,
			minDuration:  50 * time.Millisecond,
			maxDuration:  50 * time.Millisecond,
		},
		{
			name:         "medium transaction",
			computeUnits: 500_000,
			minDuration:  250 * time.Millisecond,
			maxDuration:  250 * time.Millisecond,
		},
		{
			name:         "large transaction",
			computeUnits: 1_000_000,
			minDuration:  500 * time.Millisecond,
			maxDuration:  500 * time.Millisecond,
		},
		{
			name:         "very large transaction",
			computeUnits: 2_000_000,
			minDuration:  1000 * time.Millisecond,
			maxDuration:  1000 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := EstimateExecutionTime(tt.computeUnits)
			assert.GreaterOrEqual(t, duration, tt.minDuration)
			assert.LessOrEqual(t, duration, tt.maxDuration)
		})
	}
}

func TestGetFailedInstruction(t *testing.T) {
	tests := []struct {
		name          string
		result        *SimulationResult
		expectedIndex int
		expectedLog   string
	}{
		{
			name: "failed instruction",
			result: &SimulationResult{
				Logs: []string{
					"Program log: Processing",
					"Program log: failed at instruction 2",
					"Program log: Done",
				},
			},
			expectedIndex: 1,
			expectedLog:   "Program log: failed at instruction 2",
		},
		{
			name: "no failure",
			result: &SimulationResult{
				Logs: []string{
					"Program log: Processing",
					"Program log: Done",
				},
			},
			expectedIndex: -1,
			expectedLog:   "",
		},
		{
			name:          "nil result",
			result:        nil,
			expectedIndex: -1,
			expectedLog:   "",
		},
		{
			name: "error in logs",
			result: &SimulationResult{
				Logs: []string{
					"Program log: Processing",
					"Program Error: Invalid instruction",
					"Program log: Done",
				},
			},
			expectedIndex: 1,
			expectedLog:   "Program Error: Invalid instruction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index, log := GetFailedInstruction(tt.result)
			assert.Equal(t, tt.expectedIndex, index)
			assert.Equal(t, tt.expectedLog, log)
		})
	}
}

func TestExtractReturnData(t *testing.T) {
	tests := []struct {
		name        string
		result      *SimulationResult
		expectError bool
		dataLength  int
	}{
		{
			name: "valid return data",
			result: &SimulationResult{
				Value: SimulationValue{
					ReturnData: []byte{1, 2, 3, 4, 5},
				},
			},
			expectError: false,
			dataLength:  5,
		},
		{
			name:        "nil result",
			result:      nil,
			expectError: true,
		},
		{
			name: "no return data",
			result: &SimulationResult{
				Value: SimulationValue{},
			},
			expectError: true,
		},
		{
			name: "invalid return data type",
			result: &SimulationResult{
				Value: SimulationValue{
					ReturnData: "string data",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := ExtractReturnData(tt.result)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, data)
			} else {
				require.NoError(t, err)
				assert.Len(t, data, tt.dataLength)
			}
		})
	}
}

func TestSimulator_Constants(t *testing.T) {
	assert.Equal(t, 30*time.Second, defaultSimTimeout)
	assert.Equal(t, 10, defaultSimSlots)
	assert.Equal(t, solanaRPC.CommitmentProcessed, defaultSimCommitment)
	assert.Equal(t, 3, maxSimRetries)
	assert.Equal(t, 500*time.Millisecond, simRetryDelay)
}

func TestSimulator_CheckAccountChanges(t *testing.T) {
	simulator := NewSimulator(SimulatorConfig{
		RPCPool: &rpc.Pool{},
	})

	result := &SimulationResult{
		Value: SimulationValue{
			Accounts: []any{
				"account1_data",
				"account2_data",
				"account3_data",
			},
		},
	}

	changes := simulator.CheckAccountChanges(result)

	assert.Len(t, changes, 3)
	assert.Contains(t, changes, "account_0")
	assert.Contains(t, changes, "account_1")
	assert.Contains(t, changes, "account_2")
}

func TestSimulator_SimulateBase64Transaction(t *testing.T) {
	logger := zerolog.Nop()
	simulator := NewSimulator(SimulatorConfig{
		RPCPool: &rpc.Pool{},
		Logger:  &logger,
	})

	// Valid base64 transaction (this is a simplified test)
	validBase64 := "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABAAEDxzb2wWWrHHGEJvtSA7bAeR1mWf4/O7/dFqMJQWpWzW7vAAUdReVEcSNEh3R7B6wDRCFPLmJwOLXYaryvTQfPd39gXwYoC0KAAAAAFdYMEBROulM+yMxLCR/E/jPH8UqxVhtECVCzhOjREwUOAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB"

	ctx := context.Background()
	result, err := simulator.SimulateBase64Transaction(ctx, validBase64)

	// Expected to fail without real RPC connection
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestSimulator_SimulateSerializedTransaction(t *testing.T) {
	logger := zerolog.Nop()
	simulator := NewSimulator(SimulatorConfig{
		RPCPool: &rpc.Pool{},
		Logger:  &logger,
	})

	// Create a simple transaction
	tx := &solana.Transaction{}

	serialized, err := tx.MarshalBinary()
	require.NoError(t, err)

	ctx := context.Background()
	result, err := simulator.SimulateSerializedTransaction(ctx, serialized)

	// Should succeed with our simulation placeholder
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, IsSimulationSuccessful(result))
}

func TestSimulator_SimulateMultiple(t *testing.T) {
	logger := zerolog.Nop()
	simulator := NewSimulator(SimulatorConfig{
		RPCPool: &rpc.Pool{},
		Logger:  &logger,
	})

	// Create multiple transactions
	transactions := []*solana.Transaction{
		{},
		{},
		{},
	}

	ctx := context.Background()
	results, err := simulator.SimulateMultiple(ctx, transactions)

	// Should succeed with our simulation placeholder
	assert.NoError(t, err)
	assert.NotNil(t, results)
	assert.Len(t, results, 3)
}

func TestContains(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		substr string
		result bool
	}{
		{
			name:   "exact match",
			s:      "hello world",
			substr: "hello world",
			result: true,
		},
		{
			name:   "prefix match",
			s:      "hello world",
			substr: "hello",
			result: true,
		},
		{
			name:   "suffix match",
			s:      "hello world",
			substr: "world",
			result: true,
		},
		{
			name:   "middle match",
			s:      "hello world",
			substr: "lo wo",
			result: true,
		},
		{
			name:   "no match",
			s:      "hello world",
			substr: "foo",
			result: false,
		},
		{
			name:   "empty substring",
			s:      "hello world",
			substr: "",
			result: true,
		},
		{
			name:   "substring longer than string",
			s:      "hi",
			substr: "hello",
			result: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			assert.Equal(t, tt.result, result)
		})
	}
}

func TestSimulator_GetRecommendedComputeBudget(t *testing.T) {
	logger := zerolog.Nop()
	simulator := NewSimulator(SimulatorConfig{
		RPCPool: &rpc.Pool{},
		Logger:  &logger,
	})

	tx := &solana.Transaction{}

	ctx := context.Background()
	budget, err := simulator.GetRecommendedComputeBudget(ctx, tx)

	// Should succeed with our simulation placeholder
	assert.NoError(t, err)
	assert.Positive(t, budget)
}
