// Package app provides the main bot orchestration logic
package app

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gagliardetto/solana-go"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/internal/trader"
	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/rs/zerolog"
)

const (
	// Version is the bot version.
	Version = "1.0.0"
	// Commit is the git commit hash.
	Commit = "dev"
	// BuildTime is the build timestamp.
	BuildTime = "unknown"
)

// Monitor is the interface that all monitors must implement.
type Monitor interface {
	SetHandler(handler monitor.EventHandler)
	Start() error
	Stop() error
	Status() monitor.MonitorStatus
}

// Config holds the bot configuration.
type Config struct {
	WalletManager *wallet.Manager
	WalletKeyID   string
	Monitors      []Monitor
	SolanaTrader  *trader.Executor
	BaseTrader    *trader.UniswapClient
	Strategy      StrategyConfig
	Metrics       MetricsConfig
}

// StrategyConfig holds strategy configuration.
type StrategyConfig struct {
	Mode            string  // auto, manual, monitor
	MaxPositionSize string  // Maximum position size (e.g., "0.1 SOL")
	MinLiquidity    string  // Minimum liquidity requirement
	MinScore        float64 // Minimum analysis score (0-100)
	TakeProfit      float64 // Take profit percentage
	StopLoss        float64 // Stop loss percentage
}

// MetricsConfig holds metrics configuration.
type MetricsConfig struct {
	Enabled  bool
	Port     int
	Endpoint string
}

// Bot is the main bot instance.
type Bot struct {
	config Config
	logger *zerolog.Logger
	ctx    context.Context
	cancel context.CancelFunc

	// State
	isRunning    atomic.Bool
	startTime    time.Time
	tradeCount   atomic.Int64
	successCount atomic.Int64
	failureCount atomic.Int64

	// Monitors
	monitors   []Monitor
	monitorsMu sync.RWMutex

	// Traders
	solanaTrader *trader.Executor
	baseTrader   *trader.UniswapClient

	// Event handling
	eventChan chan *monitor.TokenEvent
	errorChan chan error

	// Metrics server
	metricsServer *MetricsServer
}

// NewBot creates a new bot instance.
func NewBot(config Config) (*Bot, error) {
	if config.WalletManager == nil {
		return nil, errors.New("wallet manager is required")
	}
	if len(config.Monitors) == 0 {
		return nil, errors.New("at least one monitor is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	logger := zerolog.New(zerolog.ConsoleWriter{Out: nil}).With().Str("component", "bot").Logger()

	bot := &Bot{
		config:       config,
		logger:       &logger,
		ctx:          ctx,
		cancel:       cancel,
		monitors:     config.Monitors,
		solanaTrader: config.SolanaTrader,
		baseTrader:   config.BaseTrader,
		eventChan:    make(chan *monitor.TokenEvent, 100),
		errorChan:    make(chan error, 10),
	}

	// Initialize metrics server if enabled
	if config.Metrics.Enabled {
		metricsServer, err := NewMetricsServer(config.Metrics.Port, config.Metrics.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to create metrics server: %w", err)
		}
		bot.metricsServer = metricsServer
	}

	return bot, nil
}

// Start begins bot operation.
func (b *Bot) Start(ctx context.Context) error {
	if b.isRunning.Load() {
		return errors.New("bot is already running")
	}

	b.logger.Info().
		Str("version", Version).
		Msg("Starting bot")

	b.isRunning.Store(true)
	b.startTime = time.Now()

	// Setup event handlers for all monitors
	for _, m := range b.monitors {
		m.SetHandler(b)
	}

	// Start metrics server if enabled
	if b.metricsServer != nil {
		go func() {
			if err := b.metricsServer.Start(); err != nil {
				b.logger.Error().Err(err).Msg("Metrics server error")
			}
		}()
		b.logger.Info().Int("port", b.config.Metrics.Port).Msg("Metrics server started")
	}

	// Start all monitors
	var wg sync.WaitGroup
	for _, m := range b.monitors {
		wg.Add(1)
		go func(mon Monitor) {
			defer wg.Done()
			if err := mon.Start(); err != nil {
				b.logger.Error().
					Str("monitor", mon.Status().Name).
					Err(err).
					Msg("Monitor error")
				b.errorChan <- err
			}
		}(m)
		b.logger.Info().Str("monitor", m.Status().Name).Msg("Started monitor")
	}

	// Start event processing loop
	go b.eventProcessingLoop()

	// Start health check loop
	go b.healthCheckLoop()

	b.logger.Info().
		Int("monitors", len(b.monitors)).
		Str("mode", b.config.Strategy.Mode).
		Msg("Bot started successfully")

	// Wait for context cancellation
	<-ctx.Done()

	return nil
}

// Stop gracefully stops the bot.
func (b *Bot) Stop(ctx context.Context) error {
	b.logger.Info().Msg("Stopping bot")

	b.isRunning.Store(false)
	b.cancel()

	// Stop all monitors
	var wg sync.WaitGroup
	for _, m := range b.monitors {
		wg.Add(1)
		go func(mon Monitor) {
			defer wg.Done()
			if err := mon.Stop(); err != nil {
				b.logger.Error().
					Str("monitor", mon.Status().Name).
					Err(err).
					Msg("Error stopping monitor")
			}
		}(m)
	}

	// Wait for monitors or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.logger.Info().Msg("All monitors stopped")
	case <-time.After(10 * time.Second):
		b.logger.Warn().Msg("Timeout waiting for monitors to stop")
	}

	// Stop metrics server
	if b.metricsServer != nil {
		if err := b.metricsServer.Stop(); err != nil {
			b.logger.Error().Err(err).Msg("Error stopping metrics server")
		}
	}

	// Close channels
	close(b.eventChan)
	close(b.errorChan)

	b.logger.Info().
		Int64("total_trades", b.tradeCount.Load()).
		Int64("successful", b.successCount.Load()).
		Int64("failed", b.failureCount.Load()).
		Dur("uptime", time.Since(b.startTime)).
		Msg("Bot stopped")

	return nil
}

// HandleTokenEvent implements monitor.EventHandler.
func (b *Bot) HandleTokenEvent(event *monitor.TokenEvent) error {
	// Send to event channel for async processing
	select {
	case b.eventChan <- event:
		return nil
	case <-b.ctx.Done():
		return errors.New("bot is shutting down")
	default:
		return errors.New("event channel is full")
	}
}

// OnError implements monitor.EventHandler.
func (b *Bot) OnError(err error) {
	b.logger.Error().Err(err).Msg("Monitor error")
	b.errorChan <- err
}

// eventProcessingLoop processes token events.
func (b *Bot) eventProcessingLoop() {
	for {
		select {
		case <-b.ctx.Done():
			return
		case event, ok := <-b.eventChan:
			if !ok {
				return
			}
			b.processEvent(event)
		case err := <-b.errorChan:
			b.logger.Error().Err(err).Msg("Error from component")
		}
	}
}

// processEvent processes a single token event.
func (b *Bot) processEvent(event *monitor.TokenEvent) {
	b.logger.Debug().
		Str("token", event.MintAddress).
		Str("chain", string(event.Chain)).
		Str("source", string(event.Source)).
		Msg("Processing token event")

	// Record metrics
	RecordEvent(string(event.Chain), string(event.Source), false)

	// Skip if in monitor-only mode
	if b.config.Strategy.Mode == "monitor" {
		b.logger.Info().
			Str("token", event.MintAddress).
			Msg("Monitor mode: skipping trade")
		return
	}

	// In manual mode, just log the event
	if b.config.Strategy.Mode == "manual" {
		b.logger.Info().
			Str("token", event.MintAddress).
			Str("mint", event.MintAddress).
			Str("liquidity", event.InitialPrice).
			Msg("Manual mode: token detected")
		return
	}

	// Auto mode: execute trade
	// Note: Score checking would be done by analysis layer before event reaches here
	if b.config.Strategy.Mode == "auto" {
		b.executeTrade(event)
	}

	RecordEvent(string(event.Chain), string(event.Source), true)
}

// executeTrade executes a trade for a token event.
func (b *Bot) executeTrade(event *monitor.TokenEvent) {
	b.tradeCount.Add(1)

	startTime := time.Now()

	switch event.Chain {
	case monitor.ChainTypeSolana:
		if b.solanaTrader == nil {
			b.logger.Warn().Msg("Solana trader not initialized")
			return
		}
		b.executeSolanaTrade(event, startTime)
	case monitor.ChainTypeBase:
		if b.baseTrader == nil {
			b.logger.Warn().Msg("Base trader not initialized")
			return
		}
		b.executeBaseTrade(event, startTime)
	default:
		b.logger.Warn().Str("chain", string(event.Chain)).Msg("Unknown chain type")
	}
}

// executeSolanaTrade executes a trade on Solana.
func (b *Bot) executeSolanaTrade(event *monitor.TokenEvent, startTime time.Time) {
	// Parse mint addresses
	inputMint := solana.MustPublicKeyFromBase58(event.BaseTokenMint)
	outputMint := solana.MustPublicKeyFromBase58(event.MintAddress)

	// Parse amount (simplified - in production would parse string properly)
	amount := uint64(100000000) // 0.1 SOL in lamports

	params := trader.SwapParams{
		InputMint:   inputMint,
		OutputMint:  outputMint,
		Amount:      amount,
		SlippageBps: 300, // 3%
		PriorityFee: 10000,
		WrapSol:     true,
	}

	result, err := b.solanaTrader.ExecuteSwap(b.ctx, params)
	duration := time.Since(startTime)

	if err != nil {
		b.failureCount.Add(1)
		RecordTrade(string(event.Chain), string(event.Source), 0, duration, false, "execution_error")
		b.logger.Error().
			Str("token", event.MintAddress).
			Err(err).
			Msg("Trade execution failed")
		return
	}

	b.successCount.Add(1)
	RecordTrade(string(event.Chain), string(event.Source), float64(amount)/1e9, duration, true, "")
	b.logger.Info().
		Str("token", event.MintAddress).
		Str("signature", result.Signature.String()).
		Msg("Trade executed successfully")
}

// executeBaseTrade executes a trade on Base.
func (b *Bot) executeBaseTrade(event *monitor.TokenEvent, startTime time.Time) {
	// Parse token addresses
	tokenIn := common.HexToAddress("0x4200000000000000000000000000000000000006") // WETH on Base
	tokenOut := common.HexToAddress(event.MintAddress)

	// Parse amount
	amountIn := big.NewInt(100000000000000000) // 0.1 WETH (18 decimals)

	// Calculate minimum output with slippage
	amountOutMin := new(big.Int).Mul(amountIn, big.NewInt(970)) // 3% slippage
	amountOutMin.Div(amountOutMin, big.NewInt(1000))

	params := trader.UniSwapParams{
		TokenIn:        tokenIn,
		TokenOut:       tokenOut,
		AmountIn:       amountIn,
		AmountOutMin:   amountOutMin,
		FeeTier:        3000,             // 0.3% pool
		Recipient:      common.Address{}, // Would be set to wallet address
		Deadline:       big.NewInt(time.Now().Add(5 * time.Minute).Unix()),
		SqrtPriceLimit: big.NewInt(0),
	}

	tx, err := b.baseTrader.BuildSwapTransaction(params)
	duration := time.Since(startTime)

	if err != nil {
		b.failureCount.Add(1)
		RecordTrade(string(event.Chain), string(event.Source), 0, duration, false, "tx_build_error")
		b.logger.Error().
			Str("token", event.MintAddress).
			Err(err).
			Msg("Failed to build swap transaction")
		return
	}

	b.successCount.Add(1)
	RecordTrade(string(event.Chain), string(event.Source), 0, duration, true, "")
	b.logger.Info().
		Str("token", event.MintAddress).
		Str("tx_hash", tx.Hash().Hex()).
		Msg("Swap transaction built (signing and broadcasting required)")
}

// healthCheckLoop runs periodic health checks.
func (b *Bot) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.runHealthCheck()
		}
	}
}

// runHealthCheck checks the health of all components.
func (b *Bot) runHealthCheck() {
	healthyMonitors := 0
	for _, m := range b.monitors {
		status := m.Status()
		if status.IsRunning {
			healthyMonitors++
			UpdateMonitorStatus(status.Name, string(status.Chain), true)
		} else {
			UpdateMonitorStatus(status.Name, string(status.Chain), false)
			b.logger.Warn().
				Str("monitor", status.Name).
				Msg("Monitor is not running")
		}
	}

	b.logger.Debug().
		Int("healthy_monitors", healthyMonitors).
		Int("total_monitors", len(b.monitors)).
		Msg("Health check")
}

// Status returns the current bot status.
func (b *Bot) Status() BotStatus {
	uptime := time.Since(b.startTime)

	monitors := make([]monitor.MonitorStatus, len(b.monitors))
	for i, m := range b.monitors {
		monitors[i] = m.Status()
	}

	return BotStatus{
		IsRunning:    b.isRunning.Load(),
		Uptime:       uptime,
		TradeCount:   b.tradeCount.Load(),
		SuccessCount: b.successCount.Load(),
		FailureCount: b.failureCount.Load(),
		Monitors:     monitors,
		StartTime:    b.startTime,
	}
}

// BotStatus represents the bot's current status.
type BotStatus struct {
	IsRunning    bool
	Uptime       time.Duration
	TradeCount   int64
	SuccessCount int64
	FailureCount int64
	Monitors     []monitor.MonitorStatus
	StartTime    time.Time
}
