package util

import (
	"context"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"time"
)

// RetryConfig holds configuration for retry behavior.
type RetryConfig struct {
	// MaxAttempts is the maximum number of retry attempts
	MaxAttempts int
	// InitialDelay is the initial delay before the first retry
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration
	// Multiplier is the factor by which delay increases each retry
	Multiplier float64
	// Jitter adds randomness to delays to prevent thundering herd
	Jitter bool
	// JitterRange is the range for jitter (0.0-1.0)
	JitterRange float64
}

// DefaultRetryConfig returns a standard retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
		Jitter:       true,
		JitterRange:  0.1,
	}
}

// RetryableFunc is a function that can be retried.
type RetryableFunc func() error

// RetryableWithContextFunc is a function that can be retried with context.
type RetryableWithContextFunc func(ctx context.Context) error

// IsRetryable determines if an error should trigger a retry.
type IsRetryable func(error) bool

// Retry executes a function with exponential backoff retry.
func Retry(fn RetryableFunc, config RetryConfig) error {
	return RetryWithContext(context.Background(), func(ctx context.Context) error {
		return fn()
	}, config, nil)
}

// RetryWithContext executes a function with context and exponential backoff retry.
func RetryWithContext(
	ctx context.Context,
	fn RetryableWithContextFunc,
	config RetryConfig,
	isRetryable IsRetryable,
) error {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 3
	}
	if config.InitialDelay <= 0 {
		config.InitialDelay = 100 * time.Millisecond
	}
	if config.MaxDelay <= 0 {
		config.MaxDelay = 5 * time.Second
	}
	if config.Multiplier <= 1 {
		config.Multiplier = 2.0
	}

	var lastErr error
	delay := config.InitialDelay

	for attempt := range config.MaxAttempts {
		if attempt > 0 {
			// Log retry attempt
			Debug("retry attempt",
				map[string]any{
					"attempt":      attempt + 1,
					"max_attempts": config.MaxAttempts,
					"delay":        delay.String(),
					"error":        lastErr.Error(),
				})

			// Wait before retry
			select {
			case <-time.After(calculateDelay(delay, config)):
			case <-ctx.Done():
				return fmt.Errorf("retry canceled: %w", ctx.Err())
			}
		}

		// Execute the function
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if isRetryable != nil && !isRetryable(err) {
			return fmt.Errorf("non-retryable error: %w", err)
		}

		// Calculate next delay
		delay = min(time.Duration(float64(delay)*config.Multiplier), config.MaxDelay)
	}

	return fmt.Errorf("max retry attempts (%d) reached: %w", config.MaxAttempts, lastErr)
}

// calculateDelay applies jitter to the delay if configured.
func calculateDelay(delay time.Duration, config RetryConfig) time.Duration {
	if !config.Jitter || config.JitterRange <= 0 {
		return delay
	}

	// Add random jitter within +/- JitterRange%
	jitter := (mrand.Float64()*2 - 1) * config.JitterRange
	delayWithJitter := float64(delay) * (1 + jitter)

	return time.Duration(delayWithJitter)
}

// RetryWithBackoff executes a function with a simple exponential backoff.
func RetryWithBackoff(fn RetryableFunc, maxAttempts int, initialDelay, maxDelay time.Duration) error {
	config := RetryConfig{
		MaxAttempts:  maxAttempts,
		InitialDelay: initialDelay,
		MaxDelay:     maxDelay,
		Multiplier:   2.0,
		Jitter:       true,
		JitterRange:  0.1,
	}
	return Retry(fn, config)
}

// Common retryable error checkers

// IsTemporaryError checks if an error is temporary (retryable).
func IsTemporaryError(err error) bool {
	if err == nil {
		return false
	}
	// Add more specific error type checks as needed
	return true
}

// IsNetworkError checks if an error is network-related.
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// Add more specific network error checks
	return true
}

// IsTimeoutError checks if an error is timeout-related.
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// Check for timeout errors
	return false
}

// CalculateBackoff calculates exponential backoff with jitter.
func CalculateBackoff(attempt int, baseDelay, maxDelay time.Duration, multiplier float64, jitter bool) time.Duration {
	if attempt <= 0 {
		return baseDelay
	}

	// Calculate exponential backoff
	backoff := float64(baseDelay) * math.Pow(multiplier, float64(attempt-1))

	// Apply jitter if enabled
	if jitter {
		// Add +/- 10% jitter
		jitterFactor := 0.9 + mrand.Float64()*0.2
		backoff *= jitterFactor
	}

	// Cap at max delay
	if backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}

	return time.Duration(backoff)
}

// RetryUntilSuccess retries until successful or context is canceled.
func RetryUntilSuccess(ctx context.Context, fn RetryableWithContextFunc, initialDelay, maxDelay time.Duration) error {
	delay := initialDelay
	attempt := 0

	for {
		attempt++
		err := fn(ctx)
		if err == nil {
			return nil
		}

		// Log retry attempt
		Debug("retry until success attempt",
			map[string]any{
				"attempt": attempt,
				"delay":   delay.String(),
				"error":   err.Error(),
			})

		// Wait before next attempt
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return fmt.Errorf("canceled after %d attempts: %w", attempt, ctx.Err())
		}

		// Increase delay with backoff
		delay = min(time.Duration(float64(delay)*1.5), maxDelay)
	}
}
