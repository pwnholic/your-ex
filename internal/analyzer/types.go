package analyzer

import (
	"time"

	"github.com/lilwiggy/bot/internal/monitor"
)

// AnalysisResult contains the complete analysis of a token.
type AnalysisResult struct {
	// Token identification
	TokenAddress string             `json:"token_address"`
	Chain        monitor.ChainType  `json:"chain"`
	Source       monitor.SourceType `json:"source"`
	Timestamp    time.Time          `json:"timestamp"`

	// Metadata
	Metadata TokenMetadata `json:"metadata"`

	// Liquidity analysis
	Liquidity LiquidityAnalysis `json:"liquidity"`

	// Security analysis
	Security SecurityAnalysis `json:"security"`

	// Scoring
	Score TokenScore `json:"score"`

	// Recommendation
	Recommendation string `json:"recommendation"` // "buy", "skip", "warning"

	// Analysis metadata
	AnalysisDuration time.Duration `json:"analysis_duration"`
	Errors           []string      `json:"errors,omitempty"`
}

// TokenMetadata holds basic token information.
type TokenMetadata struct {
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Decimals    uint8  `json:"decimals"`
	Supply      string `json:"supply"`
	MetadataURI string `json:"metadata_uri,omitempty"`

	// Authority information (Solana)
	MintAuthority   *string `json:"mint_authority,omitempty"`
	FreezeAuthority *string `json:"freeze_authority,omitempty"`

	// Contract information (Base)
	ContractAddress  string `json:"contract_address,omitempty"`
	ContractVerified bool   `json:"contract_verified,omitempty"`

	// Social links
	Twitter  string `json:"twitter,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Website  string `json:"website,omitempty"`
}

// LiquidityAnalysis holds liquidity pool information.
type LiquidityAnalysis struct {
	PoolAddress      string         `json:"pool_address"`
	PoolType         string         `json:"pool_type"` // "raydium", "orca", "uniswap_v3", etc.
	TotalValueLocked string         `json:"total_value_locked"`
	TokenReserve     string         `json:"token_reserve"`
	BaseTokenReserve string         `json:"base_token_reserve"`
	InitialPrice     string         `json:"initial_price"`
	DepthScore       float64        `json:"depth_score"` // 0-100, higher is better
	IsLocked         bool           `json:"is_locked"`
	LockDuration     *time.Duration `json:"lock_duration,omitempty"`
	BurnedLiquidity  float64        `json:"burned_liquidity_percent"` // % of LP tokens burned
}

// SecurityAnalysis holds security check results.
type SecurityAnalysis struct {
	// Authority checks (Solana)
	MintAuthorityRevoked   bool `json:"mint_authority_revoked"`
	FreezeAuthorityRevoked bool `json:"freeze_authority_revoked"`

	// Ownership checks (Base)
	OwnershipRenounced bool   `json:"ownership_renounced,omitempty"`
	OwnerAddress       string `json:"owner_address,omitempty"`

	// Liquidity security
	LiquidityLocked bool `json:"liquidity_locked"`
	LiquidityBurned bool `json:"liquidity_burned"`
	LPHolderCount   int  `json:"lp_holder_count"`

	// Holder distribution
	HolderCount              int     `json:"holder_count"`
	Top10HolderConcentration float64 `json:"top10_holder_concentration"` // 0-1
	HolderDistributionScore  float64 `json:"holder_distribution_score"`  // 0-100

	// Transfer fees
	TransferFeeBuy  float64 `json:"transfer_fee_buy"`  // 0-1
	TransferFeeSell float64 `json:"transfer_fee_sell"` // 0-1
	HasHiddenFees   bool    `json:"has_hidden_fees"`

	// Honeypot detection
	IsHoneypot      bool    `json:"is_honeypot"`
	HoneypotReason  string  `json:"honeypot_reason,omitempty"`
	TestBuySuccess  bool    `json:"test_buy_success"`
	TestSellSuccess bool    `json:"test_sell_success"`
	ActualSlippage  float64 `json:"actual_slippage"` // 0-1

	// Social verification
	SocialAccountsExist bool `json:"social_accounts_exist"`
	TwitterVerified     bool `json:"twitter_verified"`
	TelegramVerified    bool `json:"telegram_verified"`

	// Overall security score
	SecurityScore float64 `json:"security_score"` // 0-100
	RiskLevel     string  `json:"risk_level"`     // "low", "medium", "high", "critical"
}

// TokenScore holds the scoring results.
type TokenScore struct {
	OverallScore   float64 `json:"overall_score"`   // 0-100
	MetadataScore  float64 `json:"metadata_score"`  // 0-100
	LiquidityScore float64 `json:"liquidity_score"` // 0-100
	SecurityScore  float64 `json:"security_score"`  // 0-100
	SocialScore    float64 `json:"social_score"`    // 0-100
	PotentialScore float64 `json:"potential_score"` // 0-100

	// Score breakdown
	PositiveFactors []string `json:"positive_factors"`
	NegativeFactors []string `json:"negative_factors"`
}

// AnalysisConfig holds configuration for token analysis.
type AnalysisConfig struct {
	// Minimum thresholds
	MinLiquidityUSD        float64 `json:"min_liquidity_usd"`
	MinHolderCount         int     `json:"min_holder_count"`
	MaxHolderConcentration float64 `json:"max_holder_concentration"` // 0-1

	// Security requirements
	RequireMintAuthorityRevoked   bool `json:"require_mint_authority_revoked"`
	RequireFreezeAuthorityRevoked bool `json:"require_freeze_authority_revoked"`
	RequireLiquidityLocked        bool `json:"require_liquidity_locked"`
	RequireSocialAccounts         bool `json:"require_social_accounts"`

	// Honeypot detection
	EnableHoneypotTest    bool    `json:"enable_honeypot_test"`
	TestTradeAmount       float64 `json:"test_trade_amount"`       // in base token (SOL/ETH)
	MaxAcceptableSlippage float64 `json:"max_acceptable_slippage"` // 0-1

	// Scoring weights
	ScoreWeights ScoreWeights `json:"score_weights"`

	// Timeout
	AnalysisTimeout time.Duration `json:"analysis_timeout"`
}

// ScoreWeights defines how different factors contribute to the final score.
type ScoreWeights struct {
	Liquidity float64 `json:"liquidity"` // 0-1
	Security  float64 `json:"security"`  // 0-1
	Social    float64 `json:"social"`    // 0-1
	Potential float64 `json:"potential"` // 0-1
}

// DefaultAnalysisConfig returns the default analysis configuration.
func DefaultAnalysisConfig() AnalysisConfig {
	return AnalysisConfig{
		MinLiquidityUSD:               1000,
		MinHolderCount:                50,
		MaxHolderConcentration:        0.3, // 30%
		RequireMintAuthorityRevoked:   true,
		RequireFreezeAuthorityRevoked: true,
		RequireLiquidityLocked:        true,
		RequireSocialAccounts:         false,
		EnableHoneypotTest:            false, // Disabled by default (requires actual trade)
		TestTradeAmount:               0.001, // Small test amount
		MaxAcceptableSlippage:         0.05,  // 5%
		ScoreWeights: ScoreWeights{
			Liquidity: 0.3,
			Security:  0.4,
			Social:    0.2,
			Potential: 0.1,
		},
		AnalysisTimeout: 30 * time.Second,
	}
}

// AnalysisStatus represents the status of an ongoing analysis.
type AnalysisStatus struct {
	TokenAddress string            `json:"token_address"`
	Chain        monitor.ChainType `json:"chain"`
	Status       string            `json:"status"`   // "pending", "analyzing", "completed", "failed"
	Progress     float64           `json:"progress"` // 0-1
	StartTime    time.Time         `json:"start_time"`
}

// CachedAnalysis represents a cached analysis result.
type CachedAnalysis struct {
	Result   AnalysisResult `json:"result"`
	CachedAt time.Time      `json:"cached_at"`
	TTL      time.Duration  `json:"ttl"`
	HitCount int64          `json:"hit_count"`
}
