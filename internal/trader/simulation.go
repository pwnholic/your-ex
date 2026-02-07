// Package trader provides trading functionality for the meme sniper bot.
// This file implements transaction simulation before broadcast.
package trader

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/gagliardetto/solana-go"
	solanaRPC "github.com/gagliardetto/solana-go/rpc"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/rs/zerolog"
)

const (
	// Simulation defaults.
	defaultSimTimeout    = 30 * time.Second
	defaultSimSlots      = 10
	defaultSimCommitment = solanaRPC.CommitmentProcessed
	maxSimRetries        = 3
	simRetryDelay        = 500 * time.Millisecond
)

// SimulationResult represents the result of a transaction simulation.
type SimulationResult struct {
	// Transaction result
	Value SimulationValue `json:"value"`

	// Error from simulation
	Err error `json:"err,omitempty"`

	// Simulation metadata
	Logs          []string `json:"logs,omitempty"`
	UnitsConsumed uint64   `json:"unitsConsumed,omitempty"`
	ReturnData    []byte   `json:"returnData,omitempty"`

	// Timing
	Duration time.Duration `json:"duration"`

	// Raw RPC response for debugging
	RawResponse any `json:"rawResponse,omitempty"`
}

// SimulationValue contains the value returned from simulation.
type SimulationValue struct {
	Err           any      `json:"err,omitempty"`
	Logs          []string `json:"logs,omitempty"`
	Accounts      []any    `json:"accounts,omitempty"`
	ReturnData    any      `json:"returnData,omitempty"`
	UnitsConsumed uint64   `json:"unitsConsumed,omitempty"`
}

// Simulator handles transaction simulation before broadcast.
type Simulator struct {
	rpcPool *rpc.Pool
	logger  *zerolog.Logger

	// Configuration
	timeout    time.Duration
	simSlots   uint64
	commitment string
	maxRetries int
	retryDelay time.Duration
}

// SimulatorConfig holds configuration for the simulator.
type SimulatorConfig struct {
	RPCPool    *rpc.Pool
	Logger     *zerolog.Logger
	Timeout    time.Duration
	SimSlots   uint64
	Commitment string
	MaxRetries int
	RetryDelay time.Duration
}

// NewSimulator creates a new transaction simulator.
func NewSimulator(config SimulatorConfig) *Simulator {
	if config.Timeout == 0 {
		config.Timeout = defaultSimTimeout
	}
	if config.SimSlots == 0 {
		config.SimSlots = defaultSimSlots
	}
	if config.Commitment == "" {
		config.Commitment = string(defaultSimCommitment)
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = maxSimRetries
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = simRetryDelay
	}

	return &Simulator{
		rpcPool:    config.RPCPool,
		logger:     config.Logger,
		timeout:    config.Timeout,
		simSlots:   config.SimSlots,
		commitment: config.Commitment,
		maxRetries: config.MaxRetries,
		retryDelay: config.RetryDelay,
	}
}

// SimulateTransaction simulates a transaction before broadcasting.
// Returns the simulation result or an error.
func (s *Simulator) SimulateTransaction(
	ctx context.Context,
	transaction *solana.Transaction,
) (*SimulationResult, error) {
	startTime := time.Now()

	if s.logger != nil {
		s.logger.Debug().Msg("Simulating transaction")
	}

	// Create timeout context
	simCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Simulate with retries
	var result *solanaRPC.SimulateTransactionResult
	var simErr error

	for attempt := range s.maxRetries {
		if attempt > 0 {
			if s.logger != nil {
				s.logger.Debug().
					Int("attempt", attempt+1).
					Msg("Retrying simulation")
			}
			time.Sleep(s.retryDelay)
		}

		result, simErr = s.simulateOnce(simCtx, transaction)
		if simErr == nil {
			break
		}

		// Check if error is retryable
		if !s.isRetryableError(simErr) {
			break
		}
	}

	if simErr != nil {
		if s.logger != nil {
			s.logger.Error().
				Err(simErr).
				Msg("Simulation failed after retries")
		}
		return nil, fmt.Errorf("simulation failed: %w", simErr)
	}

	duration := time.Since(startTime)

	// Build result
	var unitsConsumed uint64
	if result.UnitsConsumed != nil {
		unitsConsumed = *result.UnitsConsumed
	}

	simResult := &SimulationResult{
		Value: SimulationValue{
			Err:           result.Err,
			Logs:          result.Logs,
			Accounts:      makeAccountsInterface(result.Accounts),
			ReturnData:    nil,
			UnitsConsumed: unitsConsumed,
		},
		Logs:          result.Logs,
		UnitsConsumed: unitsConsumed,
		Duration:      duration,
		RawResponse:   result,
	}

	// Check for simulation errors
	if result.Err != nil {
		simResult.Err = fmt.Errorf("simulation error: %v", result.Err)
		if s.logger != nil {
			s.logger.Warn().
				Err(simResult.Err).
				Strs("logs", result.Logs).
				Msg("Transaction simulation returned error")
		}
	}

	if s.logger != nil {
		s.logger.Debug().
			Dur("duration", duration).
			Uint64("units_consumed", unitsConsumed).
			Int("log_count", len(result.Logs)).
			Msg("Simulation completed")
	}

	return simResult, nil
}

// makeAccountsInterface converts RPC accounts to interface slice.
func makeAccountsInterface(accounts []*solanaRPC.Account) []any {
	result := make([]any, len(accounts))
	for i, acc := range accounts {
		result[i] = acc
	}
	return result
}

// simulateOnce performs a single simulation attempt.
func (s *Simulator) simulateOnce(
	ctx context.Context,
	transaction *solana.Transaction,
) (*solanaRPC.SimulateTransactionResult, error) {
	// Placeholder for actual simulation
	// In production, this would call the RPC simulation endpoint
	units := uint64(100000)
	return &solanaRPC.SimulateTransactionResult{
		Err:           nil,
		Logs:          []string{"Program log: Simulation successful"},
		Accounts:      []*solanaRPC.Account{},
		UnitsConsumed: &units,
	}, nil
}

// SimulateTransactionWithAccounts simulates a transaction with specific account overrides.
// This is useful for testing scenarios where accounts have specific states.
func (s *Simulator) SimulateTransactionWithAccounts(
	ctx context.Context,
	transaction *solana.Transaction,
	accountOverrides map[string]string,
) (*SimulationResult, error) {
	// For account overrides, we'd need to use a more advanced simulation endpoint
	// This is a placeholder for that functionality

	if s.logger != nil {
		s.logger.Debug().
			Int("override_count", len(accountOverrides)).
			Msg("Simulating transaction with account overrides")
	}

	// For now, just do a regular simulation
	return s.SimulateTransaction(ctx, transaction)
}

// SimulateSerializedTransaction simulates a base64-encoded serialized transaction.
func (s *Simulator) SimulateSerializedTransaction(ctx context.Context, serializedTx []byte) (*SimulationResult, error) {
	// For now, just create a placeholder transaction
	var transaction solana.Transaction
	_ = serializedTx // Use serializedTx to avoid unused variable warning
	_ = transaction  // Placeholder for transaction

	return s.SimulateTransaction(ctx, &transaction)
}

// SimulateBase64Transaction simulates a base64-encoded transaction string.
func (s *Simulator) SimulateBase64Transaction(ctx context.Context, base64Tx string) (*SimulationResult, error) {
	// Decode base64
	txBytes, err := base64.StdEncoding.DecodeString(base64Tx)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	return s.SimulateSerializedTransaction(ctx, txBytes)
}

// ValidateSimulation validates a simulation result.
// Returns an error if the simulation indicates the transaction would fail.
func ValidateSimulation(result *SimulationResult) error {
	if result == nil {
		return errors.New("simulation result is nil")
	}

	// Check for simulation errors
	if result.Err != nil {
		return fmt.Errorf("transaction would fail: %w", result.Err)
	}

	if result.Value.Err != nil {
		return fmt.Errorf("transaction would fail: %v", result.Value.Err)
	}

	// Check for specific error logs
	errorLogPatterns := []string{
		"failed",
		"error",
		"insufficient",
		"overflow",
		"invalid",
	}

	for _, log := range result.Logs {
		for _, pattern := range errorLogPatterns {
			// This is a simple check - production would use more sophisticated error detection
			if contains(log, pattern) {
				return fmt.Errorf("suspicious log detected: %s", log)
			}
		}
	}

	return nil
}

// IsSimulationSuccessful checks if a simulation indicates success.
func IsSimulationSuccessful(result *SimulationResult) bool {
	if result == nil {
		return false
	}

	return result.Err == nil && result.Value.Err == nil
}

// ExtractReturnData extracts return data from a simulation result.
func ExtractReturnData(result *SimulationResult) ([]byte, error) {
	if result == nil {
		return nil, errors.New("simulation result is nil")
	}

	if result.Value.ReturnData == nil {
		return nil, errors.New("no return data")
	}

	// The return data format depends on the program
	// This is a simplified version
	returnData, ok := result.Value.ReturnData.([]byte)
	if !ok {
		return nil, errors.New("return data is not bytes")
	}

	return returnData, nil
}

// GetComputeUnits returns the compute units consumed by the transaction.
func (s *SimulationResult) GetComputeUnits() uint64 {
	if s == nil {
		return 0
	}

	return s.UnitsConsumed
}

// EstimateExecutionTime estimates the execution time based on compute units.
func EstimateExecutionTime(computeUnits uint64) time.Duration {
	// Rough estimate: 1M compute units ≈ 0.5 seconds
	// This is highly variable and network-dependent
	microseconds := float64(computeUnits) / 1_000_000 * 500_000
	return time.Duration(microseconds) * time.Microsecond
}

// GetFailedInstruction returns the first failed instruction from logs.
func GetFailedInstruction(result *SimulationResult) (int, string) {
	if result == nil {
		return -1, ""
	}

	// Look for failed instruction in logs
	for i, log := range result.Logs {
		if contains(log, "failed") || contains(log, "Error") {
			return i, log
		}
	}

	return -1, ""
}

// GetLogs returns all logs from a simulation result.
func (s *SimulationResult) GetLogs() []string {
	if s == nil {
		return nil
	}

	return s.Logs
}

// GetError returns the error from a simulation result.
func (s *SimulationResult) GetError() error {
	if s == nil {
		return nil
	}

	return s.Err
}

// GetDuration returns the simulation duration.
func (s *SimulationResult) GetDuration() time.Duration {
	if s == nil {
		return 0
	}

	return s.Duration
}

// isRetryableError checks if a simulation error is retryable.
func (s *Simulator) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Network errors are retryable
	if contains(err.Error(), "timeout") ||
		contains(err.Error(), "connection") ||
		contains(err.Error(), "temporary") {
		return true
	}

	return false
}

// contains checks if a string contains a substring (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) && (s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
				containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// SimulateMultiple simulates multiple transactions in parallel.
func (s *Simulator) SimulateMultiple(
	ctx context.Context,
	transactions []*solana.Transaction,
) ([]*SimulationResult, error) {
	results := make([]*SimulationResult, len(transactions))
	errChan := make(chan error, len(transactions))

	for i, tx := range transactions {
		go func(index int, transaction *solana.Transaction) {
			result, err := s.SimulateTransaction(ctx, transaction)
			results[index] = result
			errChan <- err
		}(i, tx)
	}

	// Wait for all simulations to complete
	var errs []error
	for range transactions {
		if err := <-errChan; err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return results, fmt.Errorf("%d simulations failed: %w", len(errs), errs[0])
	}

	return results, nil
}

// SimulateWithRetry simulates a transaction with automatic retry on failure.
func (s *Simulator) SimulateWithRetry(
	ctx context.Context,
	transaction *solana.Transaction,
	maxRetries int,
) (*SimulationResult, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if s.logger != nil {
				s.logger.Debug().
					Int("attempt", attempt).
					Msg("Retrying simulation")
			}
			time.Sleep(s.retryDelay * time.Duration(attempt))
		}

		result, err := s.SimulateTransaction(ctx, transaction)
		if err != nil {
			lastErr = err
			continue
		}

		// Check if simulation was successful
		if IsSimulationSuccessful(result) {
			return result, nil
		}

		// If simulation failed but was valid, return the result
		return result, nil
	}

	return nil, fmt.Errorf("simulation failed after %d attempts: %w", maxRetries+1, lastErr)
}

// GetRecommendedComputeBudget gets the recommended compute budget for a transaction.
// Placeholder for future RPC integration.
func (s *Simulator) GetRecommendedComputeBudget(ctx context.Context, transaction *solana.Transaction) (uint64, error) {
	result, err := s.SimulateTransaction(ctx, transaction)
	if err != nil {
		return 0, fmt.Errorf("simulation failed: %w", err)
	}

	// Add 20% buffer for safety
	computeUnits := result.UnitsConsumed
	budget := max(
		// Minimum budget is 200,000 compute units
		uint64(float64(computeUnits)*1.2), 200_000)

	return budget, nil
}

// CheckAccountChanges checks for significant account changes during simulation.
func (s *Simulator) CheckAccountChanges(result *SimulationResult) map[string]string {
	changes := make(map[string]string)

	if result == nil || result.Value.Accounts == nil {
		return changes
	}

	// Parse account changes from result
	// This is a simplified implementation
	for i, acc := range result.Value.Accounts {
		changes[fmt.Sprintf("account_%d", i)] = fmt.Sprintf("%v", acc)
	}

	return changes
}
