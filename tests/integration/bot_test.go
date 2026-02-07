// Package integration provides end-to-end integration tests
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lilwiggy/bot/internal/app"
	"github.com/lilwiggy/bot/internal/config"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/internal/monitor/base"
	"github.com/lilwiggy/bot/internal/monitor/solana"
	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBotLifecycle tests the complete bot lifecycle.
func TestBotLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create test configuration
	keychain := wallet.NewKeychain()
	walletMgr := wallet.NewManager(keychain, "test-password")

	// Create monitors
	pumpFunMonitor, err := solana.NewPumpFunMonitor(solana.PumpFunConfig{
		ReconnectDelay: 1 * time.Second,
	})
	require.NoError(t, err)

	mempoolMonitor, err := base.NewMempoolMonitor(base.MempoolConfig{
		ReconnectDelay:      1 * time.Second,
		SubscriptionTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	// Create bot
	botConfig := app.Config{
		WalletManager: walletMgr,
		WalletKeyID:   "test-key",
		Monitors: []app.Monitor{
			pumpFunMonitor,
			mempoolMonitor,
		},
		Strategy: app.StrategyConfig{
			Mode:            "monitor",
			MaxPositionSize: "0.1",
			MinLiquidity:    "1000",
			MinScore:        70,
			TakeProfit:      0.5,
			StopLoss:        0.2,
		},
		Metrics: app.MetricsConfig{
			Enabled: false,
			Port:    9090,
		},
	}

	bot, err := app.NewBot(botConfig)
	require.NoError(t, err)
	require.NotNil(t, bot)

	// Test bot status
	status := bot.Status()
	assert.False(t, status.IsRunning)
	assert.Equal(t, 0, status.TradeCount)

	// Start bot (will fail to connect but tests lifecycle)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start in background
	errChan := make(chan error, 1)
	go func() {
		errChan <- bot.Start(ctx)
	}()

	// Wait a bit then check status
	time.Sleep(500 * ms)
	_ = bot.Status()
	// Status may be running or not depending on connection success

	// Stop bot
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	err = bot.Stop(shutdownCtx)
	assert.NoError(t, err)

	// Verify stopped status
	status = bot.Status()
	assert.False(t, status.IsRunning)
}

const ms = time.Millisecond

// TestEventHandling tests token event handling.
func TestEventHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create simple test configuration
	keychain := wallet.NewKeychain()
	walletMgr := wallet.NewManager(keychain, "test-password")

	pumpFunMonitor, err := solana.NewPumpFunMonitor(solana.PumpFunConfig{
		ReconnectDelay: 1 * time.Second,
	})
	require.NoError(t, err)

	botConfig := app.Config{
		WalletManager: walletMgr,
		WalletKeyID:   "test-key",
		Monitors: []app.Monitor{
			pumpFunMonitor,
		},
		Strategy: app.StrategyConfig{
			Mode: "manual", // Manual mode - no trades
		},
		Metrics: app.MetricsConfig{
			Enabled: false,
		},
	}

	bot, err := app.NewBot(botConfig)
	require.NoError(t, err)

	// Create test event
	testEvent := &monitor.TokenEvent{
		ID:            "test-1",
		Chain:         monitor.ChainTypeSolana,
		Source:        monitor.SourcePumpFun,
		MintAddress:   "TestToken123",
		TokenName:     "Test Token",
		TokenSymbol:   "TEST",
		TokenDecimals: 9,
		IsValid:       true,
		Timestamp:     time.Now(),
	}

	// Handle event
	err = bot.HandleTokenEvent(testEvent)
	assert.NoError(t, err)

	// Clean up
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = bot.Stop(ctx)
}

// TestConfigurationLoading tests configuration file loading.
func TestConfigurationLoading(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create test config file
	testConfig := `bot:
  name: test-bot
  dry_run: true
  log_level: debug
  max_concurrent_trades: 1

chains:
  solana:
    enabled: true
    network: devnet
    rpc_endpoints:
      - url: http://localhost:8899
        name: local-devnet
        weight: 100
  base:
    enabled: false
    network: sepolia
    chain_id: 11155111

wallets:
  data_dir: /tmp/sniper-test
  encryption:
    enabled: false

monitoring:
  poll_interval: 1s
  max_retries: 3

analysis:
  min_score: 70
  min_liquidity: 1000

strategies:
  take_profit_percent: 50
  stop_loss_percent: 20
  max_position_size_usd: 100

alerts:
  enabled: false

metrics:
  enabled: true
  port: 9090
`

	tmpFile, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(testConfig)
	require.NoError(t, err)
	tmpFile.Close()

	// Load configuration
	cfg, err := config.Load(tmpFile.Name())
	require.NoError(t, err)

	// Verify configuration
	assert.Equal(t, "test-bot", cfg.Bot.Name)
	assert.True(t, cfg.Bot.DryRun)
	assert.True(t, cfg.Chains.Solana.Enabled)
	assert.False(t, cfg.Chains.Base.Enabled)
	assert.True(t, cfg.Metrics.Enabled)
	assert.Equal(t, 9090, cfg.Metrics.Port)
}

// TestWalletCreation tests wallet creation and management.
func TestWalletCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	keychain := wallet.NewKeychain()
	password := "test-password-123"
	walletMgr := wallet.NewManager(keychain, password)

	// Create Solana wallet
	solanaKeyID, solanaAddress, err := walletMgr.CreateWallet(wallet.ChainSolana)
	require.NoError(t, err)
	assert.NotEmpty(t, solanaKeyID)
	assert.NotEmpty(t, solanaAddress)
	assert.Contains(t, solanaAddress, "x")

	// Create Base wallet
	baseKeyID, baseAddress, err := walletMgr.CreateWallet(wallet.ChainBase)
	require.NoError(t, err)
	assert.NotEmpty(t, baseKeyID)
	assert.NotEmpty(t, baseAddress)
	assert.Contains(t, baseAddress, "x")

	// Verify addresses are different
	assert.NotEqual(t, solanaAddress, baseAddress)
}

// TestMetricsRecording tests metrics recording.
func TestMetricsRecording(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Metrics are tested internally in the app package
	// This test serves as a placeholder for external metrics validation
	// In production, query the metrics endpoint to verify:
	// - Trade counts match expected values
	// - Event counts are incrementing
	// - Error rates are within acceptable bounds
}
