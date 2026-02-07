// Package strategy provides trading strategies for the meme sniper bot.
// This file defines common types and interfaces used across all strategy components.
package strategy

import (
	"time"

	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/shopspring/decimal"
)

// Chain represents the blockchain network.
type Chain string

const (
	ChainSolana Chain = "solana"
	ChainBase   Chain = "base"
	ChainETH    Chain = "ethereum"
)

// ChainFromString converts a string to Chain.
func ChainFromString(s string) Chain {
	switch s {
	case "solana", "SOL":
		return ChainSolana
	case "base", "BASE":
		return ChainBase
	case "ethereum", "ETH", "ETH_MAINNET":
		return ChainETH
	default:
		return Chain(s) // Return as-is for unknown chains
	}
}

// String returns the string representation of Chain.
func (c Chain) String() string {
	return string(c)
}

// StrategyConfig holds configuration for a trading strategy.
type StrategyConfig struct {
	Name               string            `json:"name"                     yaml:"name"                     validate:"required"`
	Chain              Chain             `json:"chain"                    yaml:"chain"                    validate:"required"`
	BuyAmount          decimal.Decimal   `json:"buyAmount"                yaml:"buyAmount"`
	MaxSlippage        int               `json:"maxSlippage"              yaml:"maxSlippage"` // basis points
	TakeProfit         *TakeProfitConfig `json:"takeProfit,omitempty"     yaml:"takeProfit,omitempty"`
	StopLoss           *StopLossConfig   `json:"stopLoss,omitempty"       yaml:"stopLoss,omitempty"`
	PositionLimits     *PositionLimits   `json:"positionLimits,omitempty" yaml:"positionLimits,omitempty"`
	EntryCriteria      *EntryCriteria    `json:"entryCriteria,omitempty"  yaml:"entryCriteria,omitempty"`
	RebalanceEnabled   bool              `json:"rebalanceEnabled"         yaml:"rebalanceEnabled"`
	RebalanceThreshold decimal.Decimal   `json:"rebalanceThreshold"       yaml:"rebalanceThreshold"`
}

// TakeProfitTier represents a single tier in a multi-tier take profit strategy.
type TakeProfitTier struct {
	Tier        int             `json:"tier"        yaml:"tier"`
	Percent     decimal.Decimal `json:"percent"     yaml:"percent"`     // Price increase percentage (e.g., 100 for 2x)
	SellPortion decimal.Decimal `json:"sellPortion" yaml:"sellPortion"` // Portion to sell (0-1)
	Enabled     bool            `json:"enabled"     yaml:"enabled"`
}

// TakeProfitConfig holds configuration for take profit strategy.
type TakeProfitConfig struct {
	Enabled bool             `json:"enabled" yaml:"enabled"`
	Tiers   []TakeProfitTier `json:"tiers"   yaml:"tiers"`
}

// StopLossConfig holds configuration for stop loss strategy.
type StopLossConfig struct {
	Enabled          bool            `json:"enabled"          yaml:"enabled"`
	Percent          decimal.Decimal `json:"percent"          yaml:"percent"` // Loss percentage (e.g., -50 for -50%)
	Trailing         bool            `json:"trailing"         yaml:"trailing"`
	TrailingPercent  decimal.Decimal `json:"trailingPercent"  yaml:"trailingPercent"`  // Trailing distance
	TrailingActivate decimal.Decimal `json:"trailingActivate" yaml:"trailingActivate"` // When to activate trailing
}

// PositionLimits defines limits on position sizes.
type PositionLimits struct {
	MaxPositions     int             `json:"maxPositions"     yaml:"maxPositions"`
	MaxPerToken      decimal.Decimal `json:"maxPerToken"      yaml:"maxPerToken"`
	MaxTotalValue    decimal.Decimal `json:"maxTotalValue"    yaml:"maxTotalValue"`
	MaxPerTrade      decimal.Decimal `json:"maxPerTrade"      yaml:"maxPerTrade"`
	ReserveAmount    decimal.Decimal `json:"reserveAmount"    yaml:"reserveAmount"`
	MaxPortfolioRisk decimal.Decimal `json:"maxPortfolioRisk" yaml:"maxPortfolioRisk"` // % of total portfolio
}

// EntryCriteria defines filters for entering positions.
type EntryCriteria struct {
	MinLiquidity           decimal.Decimal `json:"minLiquidity"           yaml:"minLiquidity"`
	MaxHolderConcentration decimal.Decimal `json:"maxHolderConcentration" yaml:"maxHolderConcentration"`
	MinScore               int             `json:"minScore"               yaml:"minScore"`
	RequireSocials         bool            `json:"requireSocials"         yaml:"requireSocials"`
	MaxAge                 time.Duration   `json:"maxAge"                 yaml:"maxAge"` // Max age after launch to consider
	MinMarketCap           decimal.Decimal `json:"minMarketCap"           yaml:"minMarketCap"`
	MaxMarketCap           decimal.Decimal `json:"maxMarketCap"           yaml:"maxMarketCap"`
	Blacklist              []string        `json:"blacklist"              yaml:"blacklist"`
	Whitelist              []string        `json:"whitelist"              yaml:"whitelist"`
}

// TradeSignal represents a signal to take a trading action.
type TradeSignal struct {
	Type      SignalType             `json:"type"`
	Position  *wallet.Position       `json:"position"`
	Tier      int                    `json:"tier,omitempty"` // Take profit tier
	Amount    decimal.Decimal        `json:"amount"`
	Reason    string                 `json:"reason"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// SignalType represents the type of trading signal.
type SignalType string

const (
	SignalTypeBuy        SignalType = "buy"
	SignalTypeSell       SignalType = "sell"
	SignalTypeTakeProfit SignalType = "take_profit"
	SignalTypeStopLoss   SignalType = "stop_loss"
	SignalTypeTrailing   SignalType = "trailing_stop"
	SignalTypeRebalance  SignalType = "rebalance"
)

// StrategyResult represents the result of a strategy evaluation.
type StrategyResult struct {
	Action      SignalType      `json:"action"`
	ShouldTrade bool            `json:"shouldTrade"`
	Amount      decimal.Decimal `json:"amount,omitempty"`
	Reason      string          `json:"reason"`
	Tier        int             `json:"tier,omitempty"`
	Price       decimal.Decimal `json:"price,omitempty"`
	Timestamp   time.Time       `json:"timestamp"`
}

// PositionSnapshot captures the state of a position at a point in time.
type PositionSnapshot struct {
	PositionID   string          `json:"positionId"`
	TokenAddress string          `json:"tokenAddress"`
	TokenSymbol  string          `json:"tokenSymbol"`
	Amount       decimal.Decimal `json:"amount"`
	EntryPrice   decimal.Decimal `json:"entryPrice"`
	CurrentPrice decimal.Decimal `json:"currentPrice"`
	PnL          decimal.Decimal `json:"pnl"`
	PnLPercent   decimal.Decimal `json:"pnlPercent"`
	Timestamp    time.Time       `json:"timestamp"`
	HighestPrice decimal.Decimal `json:"highestPrice,omitempty"` // For trailing stop
	LowestPrice  decimal.Decimal `json:"lowestPrice,omitempty"`  // For trailing stop
}

// TradeRequest represents a request to execute a trade.
type TradeRequest struct {
	Chain        Chain           `json:"chain"`
	TokenAddress string          `json:"tokenAddress"`
	TokenSymbol  string          `json:"tokenSymbol"`
	Amount       decimal.Decimal `json:"amount"`
	Price        decimal.Decimal `json:"price,omitempty"`
	Type         SignalType      `json:"type"`
	Reason       string          `json:"reason"`
	Tier         int             `json:"tier,omitempty"`
}

// RiskAssessment represents an assessment of trade risk.
type RiskAssessment struct {
	RiskLevel      RiskLevel       `json:"riskLevel"`
	RiskScore      decimal.Decimal `json:"riskScore"`
	MaxLoss        decimal.Decimal `json:"maxLoss"`
	ExpectedReturn decimal.Decimal `json:"expectedReturn"`
	RewardRatio    decimal.Decimal `json:"rewardRatio"` // Risk/reward ratio
	Factors        []RiskFactor    `json:"factors"`
	Approval       bool            `json:"approval"`
	Reason         string          `json:"reason,omitempty"`
}

// RiskLevel represents the risk level of a trade.
type RiskLevel string

const (
	RiskLevelLow     RiskLevel = "low"
	RiskLevelMedium  RiskLevel = "medium"
	RiskLevelHigh    RiskLevel = "high"
	RiskLevelExtreme RiskLevel = "extreme"
)

// RiskFactor represents a specific risk factor.
type RiskFactor struct {
	Name   string          `json:"name"`
	Impact RiskImpact      `json:"impact"`
	Score  decimal.Decimal `json:"score"`
	Desc   string          `json:"desc,omitempty"`
}

// RiskImpact represents the impact level of a risk factor.
type RiskImpact string

const (
	RiskImpactNone     RiskImpact = "none"
	RiskImpactLow      RiskImpact = "low"
	RiskImpactMedium   RiskImpact = "medium"
	RiskImpactHigh     RiskImpact = "high"
	RiskImpactCritical RiskImpact = "critical"
)

// StrategyEngine is the main interface for strategy execution.
type StrategyEngine interface {
	// EvaluateEntry evaluates whether to enter a position
	EvaluateEntry(ctx *StrategyContext) (*StrategyResult, error)

	// EvaluateExit evaluates whether to exit a position
	EvaluateExit(ctx *StrategyContext) (*StrategyResult, error)

	// CalculatePositionSize calculates the position size for a trade
	CalculatePositionSize(ctx *StrategyContext, amount decimal.Decimal) (decimal.Decimal, error)

	// AssessRisk assesses the risk of a potential trade
	AssessRisk(ctx *StrategyContext, trade *TradeRequest) (*RiskAssessment, error)
}

// StrategyContext provides context for strategy evaluation.
type StrategyContext struct {
	Strategy   *StrategyConfig        `json:"strategy"`
	Position   *wallet.Position       `json:"position,omitempty"`
	Snapshot   *PositionSnapshot      `json:"snapshot,omitempty"`
	Portfolio  []*wallet.Position     `json:"portfolio,omitempty"`
	TokenInfo  *TokenInfo             `json:"tokenInfo,omitempty"`
	MarketData *MarketData            `json:"marketData,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
}

// TokenInfo holds information about a token.
type TokenInfo struct {
	Address     string          `json:"address"`
	Symbol      string          `json:"symbol"`
	Name        string          `json:"name"`
	Decimals    uint8           `json:"decimals"`
	TotalSupply decimal.Decimal `json:"totalSupply"`
	MarketCap   decimal.Decimal `json:"marketCap"`
	Liquidity   decimal.Decimal `json:"liquidity"`
	Score       int             `json:"score"`
	Socials     *SocialInfo     `json:"socials,omitempty"`
	Holders     []HolderInfo    `json:"holders,omitempty"`
	LaunchTime  time.Time       `json:"launchTime,omitempty"`
}

// SocialInfo holds social media information.
type SocialInfo struct {
	Twitter  string `json:"twitter,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Discord  string `json:"discord,omitempty"`
	Website  string `json:"website,omitempty"`
}

// HolderInfo holds information about a token holder.
type HolderInfo struct {
	Address string          `json:"address"`
	Amount  decimal.Decimal `json:"amount"`
	Percent decimal.Decimal `json:"percent"`
}

// MarketData holds market data for analysis.
type MarketData struct {
	Price          decimal.Decimal `json:"price"`
	PriceChange24h decimal.Decimal `json:"priceChange24h"`
	Volume24h      decimal.Decimal `json:"volume24h"`
	High24h        decimal.Decimal `json:"high24h"`
	Low24h         decimal.Decimal `json:"low24h"`
	MarketCap      decimal.Decimal `json:"marketCap"`
	Timestamp      time.Time       `json:"timestamp"`
}
