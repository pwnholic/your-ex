// Package main is the entry point for the meme coin sniper bot
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lilwiggy/bot/internal/app"
	"github.com/lilwiggy/bot/internal/config"
	"github.com/lilwiggy/bot/internal/monitor/base"
	"github.com/lilwiggy/bot/internal/monitor/solana"
	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
	verbose bool
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "sniper",
	Short: "Advanced multi-chain meme coin sniper bot",
	Long: `Sniper is an advanced Golang-based meme coin sniper bot for Solana and Base.

It monitors multiple DEXs (Pump.fun, Raydium, Orca, Uniswap) for new token listings,
analyzes them for security and profitability, and executes trades automatically.

Features:
- Multi-chain support (Solana + Base)
- MEV protection
- Advanced risk management
- Real-time monitoring and analysis`,
	Run: runBot,
}

// startCmd starts the bot.
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the sniper bot",
	Long:  "Start the sniper bot with monitoring, analysis, and trading enabled",
	Run:   runBot,
}

// configCmd shows the current configuration.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show current configuration",
	Long:  "Display the current configuration settings",
	Run:   showConfig,
}

// walletCmd manages wallet operations.
var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Wallet operations",
	Long:  "Manage wallet operations (create, list, balance)",
}

var walletCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new wallet",
	Long:  "Create a new wallet and save it encrypted",
	Run:   createWallet,
}

var walletListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all wallets",
	Long:  "Display all wallet addresses",
	Run:   listWallets,
}

// versionCmd shows version information.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run:   showVersion,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Root command flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.sniper.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")

	// Start command flags
	startCmd.Flags().String("chain", "both", "Chain to snipe on (solana, base, both)")
	startCmd.Flags().String("mode", "monitor", "Trading mode (auto, manual, monitor)")
	startCmd.Flags().String("dex", "all", "DEX to monitor (pumpfun, raydium, orca, uniswap, all)")
	startCmd.Flags().String("wallet-key", "", "Wallet key ID to use")

	// Wallet command flags
	walletCreateCmd.Flags().String("chain", "solana", "Chain to create wallet for (solana, base)")

	// Add subcommands
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(walletCmd)
	rootCmd.AddCommand(versionCmd)

	walletCmd.AddCommand(walletCreateCmd)
	walletCmd.AddCommand(walletListCmd)

	_ = viper.BindPFlags(startCmd.Flags())
	_ = viper.BindPFlags(walletCreateCmd.Flags())
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.AddConfigPath(".")
		viper.SetConfigType("yaml")
		viper.SetConfigName(".sniper")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}

	// Setup logging
	setupLogging()
}

func setupLogging() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	if verbose {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "2006-01-02 15:04:05"})
}

func runBot(cmd *cobra.Command, args []string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := config.Load(cfgFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	log.Info().
		Str("version", app.Version).
		Str("chain", cmd.Flags().Lookup("chain").Value.String()).
		Str("mode", cmd.Flags().Lookup("mode").Value.String()).
		Msg("Starting sniper bot")

	// Initialize keychain and wallet manager
	keychain := wallet.NewKeychain()

	// Prompt for password if not in dry run mode
	if !cfg.Bot.DryRun {
		fmt.Print("Enter wallet password: ") //nolint:forbidigo
		var password string
		_, _ = fmt.Scanln(&password)

		// Try to load existing keychain from file
		// For now, create a new empty keychain
		_ = keychain
		_ = password

		walletMgr := wallet.NewManager(keychain, "")

		// Get or create wallet based on key flag
		walletKeyID, _ := cmd.Flags().GetString("wallet-key")
		if walletKeyID == "" {
			// Create default wallets if they don't exist
			solanaKeyID, solanaAddress, err := walletMgr.CreateWallet(wallet.ChainSolana)
			if err != nil {
				log.Warn().Err(err).Msg("Could not create Solana wallet")
			} else {
				log.Info().Str("address", solanaAddress).Str("key_id", solanaKeyID).Msg("Solana wallet created")
				walletKeyID = solanaKeyID
			}

			baseKeyID, baseAddress, err := walletMgr.CreateWallet(wallet.ChainBase)
			if err != nil {
				log.Warn().Err(err).Msg("Could not create Base wallet")
			} else {
				log.Info().Str("address", baseAddress).Str("key_id", baseKeyID).Msg("Base wallet created")
			}
		}

		// Initialize monitors
		var monitors []app.Monitor
		chain, _ := cmd.Flags().GetString("chain")

		if chain == "solana" || chain == "both" {
			// Create RPC pool from config
			// For now, skip detailed RPC pool creation
			if cfg.Chains.Solana.Enabled {
				pumpFunMonitor, err := solana.NewPumpFunMonitor(solana.PumpFunConfig{
					ReconnectDelay: 5 * time.Second,
				})
				if err != nil {
					log.Warn().Err(err).Msg("Failed to initialize Pump.fun monitor")
				} else {
					monitors = append(monitors, pumpFunMonitor)
				}
			}
		}

		if chain == "base" || chain == "both" {
			if cfg.Chains.Base.Enabled {
				mempoolMonitor, err := base.NewMempoolMonitor(base.MempoolConfig{
					ReconnectDelay:      5 * time.Second,
					SubscriptionTimeout: 30 * time.Second,
				})
				if err != nil {
					log.Warn().Err(err).Msg("Failed to initialize mempool monitor")
				} else {
					monitors = append(monitors, mempoolMonitor)
				}
			}
		}

		if len(monitors) == 0 {
			log.Fatal().Msg("No monitors initialized. Please check your configuration.")
		}

		// Create bot configuration
		mode, _ := cmd.Flags().GetString("mode")

		botConfig := app.Config{
			WalletManager: walletMgr,
			WalletKeyID:   walletKeyID,
			Monitors:      monitors,
			Strategy: app.StrategyConfig{
				Mode:            mode,
				MaxPositionSize: "0.1",
				MinLiquidity:    "1000",
				MinScore:        70,
				TakeProfit:      0.5,
				StopLoss:        0.2,
			},
			Metrics: app.MetricsConfig{
				Enabled:  cfg.Metrics.Enabled,
				Port:     cfg.Metrics.Port,
				Endpoint: "/metrics",
			},
		}

		// Create bot instance
		bot, err := app.NewBot(botConfig)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to create bot")
		}

		// Setup signal handling
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Start bot in goroutine
		errChan := make(chan error, 1)
		go func() {
			errChan <- bot.Start(ctx)
		}()

		log.Info().Msg("Bot started. Press Ctrl+C to stop")

		// Wait for signal or error
		select {
		case sig := <-sigChan:
			log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
		case err := <-errChan:
			log.Error().Err(err).Msg("Bot error")
		}

		// Graceful shutdown
		log.Info().Msg("Shutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := bot.Stop(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("Shutdown error")
		}

		log.Info().Msg("Shutdown complete")
	} else {
		log.Info().Msg("Running in dry-run mode - no actual trades will be executed")
	}
}

func showConfig(cmd *cobra.Command, args []string) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	fmt.Println("\n=== Configuration ===")
	fmt.Printf("Bot Name: %s\n", cfg.Bot.Name)
	fmt.Printf("Dry Run: %v\n", cfg.Bot.DryRun)
	fmt.Printf("Log Level: %s\n", cfg.Bot.LogLevel)
	fmt.Printf("\nSolana:\n")
	fmt.Printf("  Enabled: %v\n", cfg.Chains.Solana.Enabled)
	fmt.Printf("  Network: %s\n", cfg.Chains.Solana.Network)
	fmt.Printf("\nBase:\n")
	fmt.Printf("  Enabled: %v\n", cfg.Chains.Base.Enabled)
	fmt.Printf("  Network: %s\n", cfg.Chains.Base.Network)
	fmt.Printf("  Chain ID: %d\n", cfg.Chains.Base.ChainID)
	fmt.Printf("\nMetrics:\n")
	fmt.Printf("  Enabled: %v\n", cfg.Metrics.Enabled)
	fmt.Printf("  Port: %d\n", cfg.Metrics.Port)
	fmt.Println()
}

func createWallet(cmd *cobra.Command, args []string) {
	chain, _ := cmd.Flags().GetString("chain")

	fmt.Print("Enter wallet password: ") //nolint:forbidigo
	var password string
	_, _ = fmt.Scanln(&password)

	keychain := wallet.NewKeychain()
	walletMgr := wallet.NewManager(keychain, password)

	var keyID, address string
	var err error

	switch chain {
	case "solana":
		keyID, address, err = walletMgr.CreateWallet(wallet.ChainSolana)
	case "base":
		keyID, address, err = walletMgr.CreateWallet(wallet.ChainBase)
	default:
		log.Fatal().Msg("Invalid chain. Use 'solana' or 'base'")
	}

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create wallet")
	}

	fmt.Println("\n=== Wallet Created ===")
	fmt.Printf("Chain: %s\n", chain)
	fmt.Printf("Address: %s\n", address)
	fmt.Printf("Key ID: %s\n", keyID)
	fmt.Println("\nIMPORTANT: Save your key ID and keep your password safe!")
	fmt.Println("The Key ID is needed to use this wallet for trading.")
}

func listWallets(cmd *cobra.Command, args []string) {
	// Note: This won't load existing wallets from disk in this simple version
	// A full implementation would load from encrypted storage

	fmt.Println("\n=== Wallets ===")
	fmt.Println("No wallets stored. Use 'sniper wallet create' to create one.")
	fmt.Println()
}

func showVersion(cmd *cobra.Command, args []string) {
	fmt.Printf("Sniper Bot v%s\n", app.Version)
	fmt.Printf("Commit: %s\n", app.Commit)
	fmt.Printf("Built: %s\n", app.BuildTime)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
