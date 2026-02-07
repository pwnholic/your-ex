package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()

	// Register custom tag validators
	_ = validate.RegisterValidation("valid_url", isValidURL)
	_ = validate.RegisterValidation("valid_rpc_url", isValidRPCURL)
	_ = validate.RegisterValidation("valid_chain_id", isValidChainID)
}

// Validate performs comprehensive validation on the configuration.
func (c *Config) Validate() error {
	if err := validate.Struct(c); err != nil {
		return fmt.Errorf("configuration validation failed: %w", err)
	}

	// Perform cross-field validation
	if err := c.validateChains(); err != nil {
		return err
	}

	if err := c.validateWallets(); err != nil {
		return err
	}

	if err := c.validateMonitoring(); err != nil {
		return err
	}

	if err := c.validateStrategies(); err != nil {
		return err
	}

	if err := c.validateAlerts(); err != nil {
		return err
	}

	return nil
}

// validateChains validates chain-specific configuration.
func (c *Config) validateChains() error {
	// At least one chain must be enabled
	if !c.Chains.Solana.Enabled && !c.Chains.Base.Enabled {
		return errors.New("at least one chain must be enabled")
	}

	// Validate Solana configuration
	if c.Chains.Solana.Enabled {
		if err := c.validateSolanaConfig(); err != nil {
			return fmt.Errorf("solana config invalid: %w", err)
		}
	}

	// Validate Base configuration
	if c.Chains.Base.Enabled {
		if err := c.validateBaseConfig(); err != nil {
			return fmt.Errorf("base config invalid: %w", err)
		}
	}

	return nil
}

// validateSolanaConfig validates Solana-specific configuration.
func (c *Config) validateSolanaConfig() error {
	// Validate RPC endpoints
	totalWeight := 0.0
	for i, endpoint := range c.Chains.Solana.RPCEndpoints {
		if endpoint.URL == "" {
			return fmt.Errorf("solana rpc endpoint %d: url is required", i)
		}
		if endpoint.Weight <= 0 {
			return fmt.Errorf("solana rpc endpoint %d: weight must be positive", i)
		}
		totalWeight += endpoint.Weight
	}

	if totalWeight <= 0 {
		return errors.New("solana: total rpc endpoint weight must be positive")
	}

	// Validate priority fee settings
	if c.Chains.Solana.PriorityFee.Max < c.Chains.Solana.PriorityFee.Base {
		return errors.New("solana: priority_fee max must be >= base")
	}

	if c.Chains.Solana.PriorityFee.Multiplier < 1 {
		return errors.New("solana: priority_fee multiplier must be >= 1")
	}

	return nil
}

// validateBaseConfig validates Base-specific configuration.
func (c *Config) validateBaseConfig() error {
	// Validate RPC endpoint
	if _, err := url.Parse(c.Chains.Base.RPCEndpoint); err != nil {
		return fmt.Errorf("base rpc endpoint invalid: %w", err)
	}

	// Validate WebSocket endpoint
	if _, err := url.Parse(c.Chains.Base.WSEndpoint); err != nil {
		return fmt.Errorf("base ws endpoint invalid: %w", err)
	}

	// Validate gas settings
	if c.Chains.Base.Gas.MaxFee == "" {
		return errors.New("base: gas.max_fee is required")
	}

	if c.Chains.Base.Gas.MaxPriorityFee == "" {
		return errors.New("base: gas.max_priority_fee is required")
	}

	// Validate chain ID for known networks
	switch c.Chains.Base.Network {
	case "mainnet":
		if c.Chains.Base.ChainID != 8453 {
			return fmt.Errorf("base mainnet must have chain_id 8453, got %d", c.Chains.Base.ChainID)
		}
	case "sepolia":
		if c.Chains.Base.ChainID != 84532 {
			return fmt.Errorf("base sepolia must have chain_id 84532, got %d", c.Chains.Base.ChainID)
		}
	case "goerli":
		if c.Chains.Base.ChainID != 84531 {
			return fmt.Errorf("base goerli must have chain_id 84531, got %d", c.Chains.Base.ChainID)
		}
	}

	// Validate MEV provider if protection is enabled
	if c.Chains.Base.MEVProtection {
		provider := strings.ToLower(c.Chains.Base.MEVProvider)
		if provider != "" && provider != "flashbots" && provider != "merkle" {
			return fmt.Errorf(
				"base: invalid mev_provider '%s', must be 'flashbots' or 'merkle'",
				c.Chains.Base.MEVProvider,
			)
		}
	}

	return nil
}

// validateWallets validates wallet configuration.
func (c *Config) validateWallets() error {
	// Validate Solana wallet if Solana is enabled
	if c.Chains.Solana.Enabled {
		if c.Wallets.Solana.Path == "" {
			return errors.New("solana wallet path is required when solana is enabled")
		}
		if err := validateAmountFormat(c.Wallets.Solana.MaxTradeAmount); err != nil {
			return fmt.Errorf("solana max_trade_amount invalid: %w", err)
		}
		if err := validateAmountFormat(c.Wallets.Solana.ReserveAmount); err != nil {
			return fmt.Errorf("solana reserve_amount invalid: %w", err)
		}
	}

	// Validate Base wallet if Base is enabled
	if c.Chains.Base.Enabled {
		if c.Wallets.Base.Path == "" {
			return errors.New("base wallet path is required when base is enabled")
		}
		if err := validateAmountFormat(c.Wallets.Base.MaxTradeAmount); err != nil {
			return fmt.Errorf("base max_trade_amount invalid: %w", err)
		}
		if err := validateAmountFormat(c.Wallets.Base.ReserveAmount); err != nil {
			return fmt.Errorf("base reserve_amount invalid: %w", err)
		}
	}

	return nil
}

// validateMonitoring validates monitoring configuration.
func (c *Config) validateMonitoring() error {
	// At least one monitoring source must be enabled
	solanaSources := 0
	if c.Chains.Solana.Enabled {
		if c.Monitoring.Solana.PumpFun {
			solanaSources++
		}
		if c.Monitoring.Solana.Raydium {
			solanaSources++
		}
		if c.Monitoring.Solana.Orca {
			solanaSources++
		}
		if solanaSources == 0 {
			return errors.New("solana: at least one monitoring source must be enabled")
		}
	}

	baseSources := 0
	if c.Chains.Base.Enabled {
		if c.Monitoring.Base.UniswapV3 {
			baseSources++
		}
		if c.Monitoring.Base.UniswapV4 {
			baseSources++
		}
		if baseSources == 0 {
			return errors.New("base: at least one monitoring source must be enabled")
		}
	}

	return nil
}

// validateStrategies validates strategy configuration.
func (c *Config) validateStrategies() error {
	strategy := c.Strategies.Default

	// Validate buy amounts
	if len(strategy.BuyAmount) == 0 {
		return errors.New("strategies: buy_amount must be specified")
	}

	if c.Chains.Solana.Enabled {
		solAmount, ok := strategy.BuyAmount["solana"]
		if !ok || solAmount == "" {
			return errors.New("strategies: buy_amount for solana is required")
		}
		if err := validateAmountFormat(solAmount); err != nil {
			return fmt.Errorf("strategies: solana buy_amount invalid: %w", err)
		}
	}

	if c.Chains.Base.Enabled {
		baseAmount, ok := strategy.BuyAmount["base"]
		if !ok || baseAmount == "" {
			return errors.New("strategies: buy_amount for base is required")
		}
		if err := validateAmountFormat(baseAmount); err != nil {
			return fmt.Errorf("strategies: base buy_amount invalid: %w", err)
		}
	}

	// Validate take profit tiers
	totalSellPortion := 0.0
	for i, tier := range strategy.TakeProfit {
		if tier.Percent < 0 {
			return fmt.Errorf("strategies: take_profit tier %d percent must be non-negative", i)
		}
		if tier.SellPortion <= 0 || tier.SellPortion > 1 {
			return fmt.Errorf("strategies: take_profit tier %d sell_portion must be between 0 and 1", i)
		}
		totalSellPortion += tier.SellPortion
	}

	if len(strategy.TakeProfit) > 0 && totalSellPortion > 1.01 { // Allow small rounding error
		return errors.New("strategies: total take_profit sell_portion exceeds 1.0")
	}

	// Validate stop loss
	if strategy.StopLoss.Enabled {
		if strategy.StopLoss.Percent >= 0 {
			return errors.New("strategies: stop_loss percent must be negative")
		}
		if strategy.StopLoss.Trailing {
			if strategy.StopLoss.TrailingPercent <= 0 || strategy.StopLoss.TrailingPercent > 100 {
				return errors.New("strategies: stop_loss trailing_percent must be between 0 and 100")
			}
		}
	}

	// Validate position limits
	if strategy.PositionLimits.MaxPositions < 1 {
		return errors.New("strategies: max_positions must be at least 1")
	}

	return nil
}

// validateAlerts validates alerting configuration.
func (c *Config) validateAlerts() error {
	if c.Alerts.Telegram.Enabled {
		if c.Alerts.Telegram.BotToken == "" {
			return errors.New("alerts: telegram bot_token is required when telegram is enabled")
		}
		if c.Alerts.Telegram.ChatID == "" {
			return errors.New("alerts: telegram chat_id is required when telegram is enabled")
		}
	}

	if c.Alerts.Discord.Enabled {
		if _, err := url.Parse(c.Alerts.Discord.WebhookURL); err != nil {
			return fmt.Errorf("alerts: discord webhook_url is invalid: %w", err)
		}
	}

	return nil
}

// validateAmountFormat validates cryptocurrency amount format (e.g., "1.5 SOL", "0.01 ETH").
func validateAmountFormat(amount string) error {
	parts := strings.Fields(amount)
	if len(parts) != 2 {
		return errors.New("invalid format, expected '<amount> <currency>'")
	}

	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return fmt.Errorf("invalid amount value: %w", err)
	}

	if value <= 0 {
		return errors.New("amount must be positive")
	}

	return nil
}

// Custom validators

func isValidURL(fl validator.FieldLevel) bool {
	u := fl.Field().String()
	if u == "" {
		return false
	}
	_, err := url.Parse(u)
	return err == nil
}

func isValidRPCURL(fl validator.FieldLevel) bool {
	u := fl.Field().String()
	if u == "" {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https" || scheme == "ws" || scheme == "wss"
}

func isValidChainID(fl validator.FieldLevel) bool {
	id := fl.Field().Int()
	return id >= 0
}

// Validate performs validation on the config and returns detailed error information.
func Validate(configPath string) (*Config, []string, error) {
	config, err := LoadWithDefaults(configPath)
	if err != nil {
		return nil, nil, err
	}

	if err := config.Validate(); err != nil {
		return config, []string{err.Error()}, err
	}

	return config, nil, nil
}
