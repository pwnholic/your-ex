package config

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/viper"
)

// Config represents the complete application configuration.
type Config struct {
	Bot        BotConfig        `mapstructure:"bot"        validate:"required"`
	Chains     ChainsConfig     `mapstructure:"chains"     validate:"required"`
	Wallets    WalletsConfig    `mapstructure:"wallets"    validate:"required"`
	Monitoring MonitoringConfig `mapstructure:"monitoring" validate:"required"`
	Analysis   AnalysisConfig   `mapstructure:"analysis"   validate:"required"`
	Strategies StrategiesConfig `mapstructure:"strategies" validate:"required"`
	Alerts     AlertsConfig     `mapstructure:"alerts"`
	Metrics    MetricsConfig    `mapstructure:"metrics"`
}

// BotConfig contains general bot settings.
type BotConfig struct {
	Name                string `mapstructure:"name"                  validate:"required"`
	DryRun              bool   `mapstructure:"dry_run"`
	LogLevel            string `mapstructure:"log_level"             validate:"required,oneof=debug info warn error fatal"`
	MaxConcurrentTrades int    `mapstructure:"max_concurrent_trades" validate:"min=1"`
}

// ChainsConfig contains blockchain-specific configurations.
type ChainsConfig struct {
	Solana SolanaConfig `mapstructure:"solana"`
	Base   BaseConfig   `mapstructure:"base"`
}

// SolanaConfig contains Solana-specific settings.
type SolanaConfig struct {
	Enabled      bool              `mapstructure:"enabled"`
	Network      string            `mapstructure:"network"       validate:"required,oneof=mainnet-beta testnet devnet"`
	RPCEndpoints []RPCEndpoint     `mapstructure:"rpc_endpoints" validate:"min=1,dive"`
	PriorityFee  PriorityFeeConfig `mapstructure:"priority_fee"`
	Commitment   string            `mapstructure:"commitment"    validate:"oneof=processed confirmed finalized"`
}

// RPCEndpoint represents a single RPC endpoint with load balancing weight.
type RPCEndpoint struct {
	URL    string  `mapstructure:"url"    validate:"required,url"`
	Name   string  `mapstructure:"name"   validate:"required"`
	Weight float64 `mapstructure:"weight" validate:"min=0,max=100"`
}

// PriorityFeeConfig contains Solana priority fee settings.
type PriorityFeeConfig struct {
	Base       int64   `mapstructure:"base"       validate:"min=0"`
	Max        int64   `mapstructure:"max"        validate:"min=0"`
	Multiplier float64 `mapstructure:"multiplier" validate:"min=1"`
}

// BaseConfig contains Base chain-specific settings.
type BaseConfig struct {
	Enabled       bool      `mapstructure:"enabled"`
	Network       string    `mapstructure:"network"        validate:"required,oneof=mainnet sepolia goerli"`
	RPCEndpoint   string    `mapstructure:"rpc_endpoint"   validate:"required,url"`
	WSEndpoint    string    `mapstructure:"ws_endpoint"    validate:"required,url"`
	Gas           GasConfig `mapstructure:"gas"`
	MEVProtection bool      `mapstructure:"mev_protection"`
	MEVProvider   string    `mapstructure:"mev_provider"   validate:"omitempty,oneof=flashbots merkle"`
	ChainID       int64     `mapstructure:"chain_id"       validate:"required,min=0"`
}

// GasConfig contains gas settings for Base chain.
type GasConfig struct {
	MaxPriorityFee     string `mapstructure:"max_priority_fee"    validate:"required"`
	MaxFee             string `mapstructure:"max_fee"             validate:"required"`
	ConfirmationBlocks int    `mapstructure:"confirmation_blocks" validate:"min=0,max=20"`
}

// WalletsConfig contains wallet settings.
type WalletsConfig struct {
	Solana WalletConfig `mapstructure:"solana" validate:"required"`
	Base   WalletConfig `mapstructure:"base"   validate:"required"`
}

// WalletConfig represents a single wallet configuration.
type WalletConfig struct {
	Path           string `mapstructure:"path"             validate:"required"`
	MaxTradeAmount string `mapstructure:"max_trade_amount" validate:"required"`
	ReserveAmount  string `mapstructure:"reserve_amount"   validate:"required"`
}

// MonitoringConfig contains monitoring settings for token launch detection.
type MonitoringConfig struct {
	Solana SolanaMonitoringConfig `mapstructure:"solana" validate:"required"`
	Base   BaseMonitoringConfig   `mapstructure:"base"   validate:"required"`
}

// SolanaMonitoringConfig contains Solana-specific monitoring settings.
type SolanaMonitoringConfig struct {
	PumpFun                 bool          `mapstructure:"pump_fun"`
	Raydium                 bool          `mapstructure:"raydium"`
	Orca                    bool          `mapstructure:"orca"`
	WebSocketReconnectDelay time.Duration `mapstructure:"websocket_reconnect_delay" validate:"min=1s"`
}

// BaseMonitoringConfig contains Base-specific monitoring settings.
type BaseMonitoringConfig struct {
	UniswapV3         bool `mapstructure:"uniswap_v3"`
	UniswapV4         bool `mapstructure:"uniswap_v4"`
	BlockConfirmation int  `mapstructure:"block_confirmation" validate:"min=0,max=20"`
}

// AnalysisConfig contains token analysis settings.
type AnalysisConfig struct {
	MinLiquidity           string  `mapstructure:"min_liquidity"            validate:"required"`
	MinScore               int     `mapstructure:"min_score"                validate:"min=0,max=100"`
	MaxHolderConcentration float64 `mapstructure:"max_holder_concentration" validate:"min=0,max=1"`
	RequireSocials         bool    `mapstructure:"require_socials"`
	HoneypotTestEnabled    bool    `mapstructure:"honeypot_test_enabled"`
}

// StrategiesConfig contains trading strategy settings.
type StrategiesConfig struct {
	Default StrategyConfig `mapstructure:"default" validate:"required"`
}

// StrategyConfig represents a trading strategy configuration.
type StrategyConfig struct {
	BuyAmount      map[string]string `mapstructure:"buy_amount"      validate:"required,min=1"`
	MaxSlippage    int               `mapstructure:"max_slippage"    validate:"min=0,max=100"`
	TakeProfit     []TakeProfitTier  `mapstructure:"take_profit"     validate:"dive"`
	StopLoss       StopLossConfig    `mapstructure:"stop_loss"`
	PositionLimits PositionLimits    `mapstructure:"position_limits"`
}

// TakeProfitTier represents a take profit tier.
type TakeProfitTier struct {
	Percent     float64 `mapstructure:"percent"      validate:"min=0"`
	SellPortion float64 `mapstructure:"sell_portion" validate:"min=0,max=1"`
}

// StopLossConfig contains stop loss settings.
type StopLossConfig struct {
	Enabled         bool    `mapstructure:"enabled"`
	Percent         float64 `mapstructure:"percent"          validate:"max=0"`
	Trailing        bool    `mapstructure:"trailing"`
	TrailingPercent float64 `mapstructure:"trailing_percent" validate:"min=0,max=100"`
}

// PositionLimits contains position management settings.
type PositionLimits struct {
	MaxPositions     int               `mapstructure:"max_positions"      validate:"min=1"`
	MaxPerToken      map[string]string `mapstructure:"max_per_token"`
	MaxTotalExposure map[string]string `mapstructure:"max_total_exposure"`
}

// AlertsConfig contains alerting configuration.
type AlertsConfig struct {
	Telegram TelegramConfig `mapstructure:"telegram"`
	Discord  DiscordConfig  `mapstructure:"discord"`
}

// TelegramConfig contains Telegram-specific alert settings.
type TelegramConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	BotToken string `mapstructure:"bot_token"`
	ChatID   string `mapstructure:"chat_id"`
}

// DiscordConfig contains Discord-specific alert settings.
type DiscordConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	WebhookURL string `mapstructure:"webhook_url"`
}

// MetricsConfig contains metrics configuration.
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Port    int    `mapstructure:"port"    validate:"min=1024,max=65535"`
	Path    string `mapstructure:"path"    validate:"required"`
}

// Load reads and parses the configuration file.
func Load(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// Enable environment variable substitution
	viper.AutomaticEnv()
	viper.SetEnvPrefix("BOT")

	// Read configuration file
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Expand environment variables in the config
	for _, key := range viper.AllKeys() {
		val := viper.Get(key)
		if strVal, ok := val.(string); ok {
			expanded := os.ExpandEnv(strVal)
			if expanded != strVal {
				viper.Set(key, expanded)
			}
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

// LoadWithDefaults loads configuration with default values.
func LoadWithDefaults(configPath string) (*Config, error) {
	config, err := Load(configPath)
	if err != nil {
		return nil, err
	}

	// Set default values if not specified
	if config.Bot.MaxConcurrentTrades == 0 {
		config.Bot.MaxConcurrentTrades = 3
	}
	if config.Chains.Solana.Commitment == "" {
		config.Chains.Solana.Commitment = "confirmed"
	}
	if config.Chains.Base.ChainID == 0 {
		config.Chains.Base.ChainID = 8453 // Base mainnet
	}

	return config, nil
}
