package monitor

import (
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/lilwiggy/bot/internal/wallet"
)

// ChainType represents the blockchain network.
type ChainType string

const (
	ChainTypeSolana ChainType = "solana"
	ChainTypeBase   ChainType = "base"
)

// SourceType represents where the token was detected.
type SourceType string

const (
	SourcePumpFun SourceType = "pump_fun"
	SourceRaydium SourceType = "raydium"
	SourceOrca    SourceType = "orca"
	SourceUniswap SourceType = "uniswap"
)

// TokenEvent represents a new token detection event.
type TokenEvent struct {
	// Identification
	ID        string     `json:"id"`
	Chain     ChainType  `json:"chain"`
	Source    SourceType `json:"source"`
	Timestamp time.Time  `json:"timestamp"`

	// Token details
	MintAddress      string `json:"mint_address"`
	TokenName        string `json:"name"`
	TokenSymbol      string `json:"symbol"`
	TokenDecimals    uint8  `json:"decimals"`
	TokenMetadataURI string `json:"metadata_uri,omitempty"`

	// Liquidity pool details
	LiquidityPoolAddress string `json:"liquidity_pool_address"`
	DEX                  string `json:"dex"`
	BaseTokenMint        string `json:"base_token_mint"` // Usually SOL or USDC
	InitialPrice         string `json:"initial_price,omitempty"`

	// On-chain data
	Signature solana.Signature `json:"signature,omitempty"`
	Slot      uint64           `json:"slot,omitempty"`

	// Metadata detected from on-chain data
	MintAuthority   *string `json:"mint_authority,omitempty"`
	FreezeAuthority *string `json:"freeze_authority,omitempty"`
	Supply          string  `json:"supply,omitempty"`

	// Social links (if available)
	Twitter  string `json:"twitter,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Website  string `json:"website,omitempty"`

	// Validation flags
	IsValid bool `json:"is_valid"`
}

// TokenFilter defines criteria for filtering token events.
type TokenFilter struct {
	MinLiquidity           float64 `json:"min_liquidity"`
	RequireMintDisabled    bool    `json:"require_mint_disabled"`
	RequireFreezeDisabled  bool    `json:"require_freeze_disabled"`
	MaxHolderConcentration float64 `json:"max_holder_concentration"`
}

// EventHandler handles token events.
type EventHandler interface {
	// HandleTokenEvent processes a new token event
	HandleTokenEvent(event *TokenEvent) error

	// OnError handles errors from the monitor
	OnError(err error)
}

// MonitorStatus represents the current status of a monitor.
type MonitorStatus struct {
	Name           string     `json:"name"`
	Chain          ChainType  `json:"chain"`
	Source         SourceType `json:"source"`
	IsRunning      bool       `json:"is_running"`
	EventsDetected int64      `json:"events_detected"`
	LastEventTime  time.Time  `json:"last_event_time"`
	LastError      string     `json:"last_error,omitempty"`
	ConnectedSince time.Time  `json:"connected_since,omitempty"`
}

// MonitorStats provides statistics for monitoring.
type MonitorStats struct {
	TotalEvents     int64         `json:"total_events"`
	FilteredEvents  int64         `json:"filtered_events"`
	ProcessedEvents int64         `json:"processed_events"`
	AverageLatency  time.Duration `json:"average_latency"`
	PeopleReady     int64         `json:"people_ready"` // Events ready for analysis
	Uptime          time.Duration `json:"uptime"`
}

// PositionUpdate represents an update to a trading position.
type PositionUpdate struct {
	KeyID        string            `json:"key_id"`
	Chain        wallet.Chain      `json:"chain"`
	TokenAddress string            `json:"token_address"`
	TokenSymbol  string            `json:"token_symbol"`
	Amount       string            `json:"amount"`
	PriceUSD     string            `json:"price_usd,omitempty"`
	ValueUSD     string            `json:"value_usd,omitempty"`
	PnL          string            `json:"pnl,omitempty"`
	PnLPercent   float64           `json:"pnl_percent,omitempty"`
	Timestamp    time.Time         `json:"timestamp"`
	Reason       string            `json:"reason"` // "buy", "sell", "update", "stop_loss", "take_profit"
	Metadata     map[string]string `json:"metadata,omitempty"`
}
