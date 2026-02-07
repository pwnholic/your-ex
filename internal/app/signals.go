// Package app provides signal handling for graceful shutdown
package app

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
)

// SignalHandler manages graceful shutdown on OS signals.
type SignalHandler struct {
	logger      *zerolog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	shutdownWg  sync.WaitGroup
	shutdownFns []func(context.Context) error
}

// NewSignalHandler creates a new signal handler.
func NewSignalHandler(parentCtx context.Context) *SignalHandler {
	ctx, cancel := context.WithCancel(parentCtx)
	logger := zerolog.New(zerolog.ConsoleWriter{Out: nil}).With().Str("component", "signals").Logger()

	return &SignalHandler{
		logger:      &logger,
		ctx:         ctx,
		cancel:      cancel,
		shutdownFns: make([]func(context.Context) error, 0),
	}
}

// RegisterShutdown registers a function to be called on shutdown.
func (s *SignalHandler) RegisterShutdown(fn func(context.Context) error) {
	s.shutdownFns = append(s.shutdownFns, fn)
}

// Start begins listening for shutdown signals.
func (s *SignalHandler) Start() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan,
		syscall.SIGINT,  // Ctrl+C
		syscall.SIGTERM, // kill
		syscall.SIGQUIT, // quit
		syscall.SIGHUP,  // hangup
	)

	go func() {
		sig := <-sigChan
		s.logger.Info().Str("signal", sig.String()).Msg("Received shutdown signal")

		// Cancel context
		s.cancel()

		// Run shutdown functions
		s.runShutdown()
	}()
}

// runShutdown executes all registered shutdown functions.
func (s *SignalHandler) runShutdown() {
	s.logger.Info().Int("functions", len(s.shutdownFns)).Msg("Running shutdown functions")

	for i, fn := range s.shutdownFns {
		s.logger.Info().Int("function", i).Msg("Running shutdown function")

		// Create timeout context for each shutdown function
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		s.shutdownWg.Add(1)
		go func(fn func(context.Context) error) {
			defer s.shutdownWg.Done()
			if err := fn(ctx); err != nil {
				s.logger.Error().Err(err).Int("function", i).Msg("Shutdown function error")
			}
		}(fn)
	}

	// Wait for all shutdown functions to complete
	done := make(chan struct{})
	go func() {
		s.shutdownWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info().Msg("All shutdown functions completed")
	case <-s.ctx.Done():
		s.logger.Warn().Msg("Shutdown timeout")
	}
}

// Stop stops the signal handler.
func (s *SignalHandler) Stop() {
	s.cancel()
}

// Canceled returns a channel that's closed when the handler is canceled.
func (s *SignalHandler) Canceled() <-chan struct{} {
	return s.ctx.Done()
}
