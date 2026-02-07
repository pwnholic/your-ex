package wallet

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	// ErrPositionNotFound is returned when a position is not found.
	ErrPositionNotFound = errors.New("position not found")
	// ErrInsufficientBalance is returned when there's not enough balance to sell.
	ErrInsufficientBalance = errors.New("insufficient balance")
	// ErrInvalidAmount is returned when the amount is invalid.
	ErrInvalidAmount = errors.New("invalid amount")
)

// PositionType represents whether this is a long or short position.
type PositionType string

const (
	PositionTypeLong  PositionType = "long"
	PositionTypeShort PositionType = "short"
)

// PositionStatus represents the current status of a position.
type PositionStatus string

const (
	PositionStatusOpen   PositionStatus = "open"
	PositionStatusClosed PositionStatus = "closed"
)

// TokenAmount represents an amount of tokens with precision.
type TokenAmount struct {
	Amount   decimal.Decimal `json:"amount"`
	Decimals uint8           `json:"decimals"`
	Symbol   string          `json:"symbol"`
}

// NewTokenAmount creates a new TokenAmount from a float value
// Note: For new code, prefer NewTokenAmountFromDecimal or NewTokenAmountFromString
// to avoid floating-point precision issues.
func NewTokenAmount(value float64, decimals uint8, symbol string) *TokenAmount {
	return &TokenAmount{
		Amount:   decimal.NewFromFloat(value).Round(int32(decimals)),
		Decimals: decimals,
		Symbol:   symbol,
	}
}

// NewTokenAmountFromString creates a new TokenAmount from a string (recommended).
func NewTokenAmountFromString(value string, decimals uint8, symbol string) (*TokenAmount, error) {
	amount, err := decimal.NewFromString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal value: %w", err)
	}
	return &TokenAmount{
		Amount:   amount.Round(int32(decimals)),
		Decimals: decimals,
		Symbol:   symbol,
	}, nil
}

// NewTokenAmountFromDecimal creates a new TokenAmount from a decimal.Decimal.
func NewTokenAmountFromDecimal(amount decimal.Decimal, decimals uint8, symbol string) *TokenAmount {
	return &TokenAmount{
		Amount:   amount.Round(int32(decimals)),
		Decimals: decimals,
		Symbol:   symbol,
	}
}

// ToFloat converts TokenAmount to float64 (use sparingly - prefer Decimal operations).
func (ta *TokenAmount) ToFloat() float64 {
	return ta.Amount.InexactFloat64()
}

// ToDecimal returns the underlying decimal.Decimal.
func (ta *TokenAmount) ToDecimal() decimal.Decimal {
	return ta.Amount
}

// String returns a string representation of the amount.
func (ta *TokenAmount) String() string {
	return fmt.Sprintf("%s %s", ta.ToFormattedString(), ta.Symbol)
}

// ToFormattedString returns a formatted string with proper decimals.
func (ta *TokenAmount) ToFormattedString() string {
	return ta.Amount.StringFixed(int32(ta.Decimals))
}

// Add adds another TokenAmount to this one.
func (ta *TokenAmount) Add(other *TokenAmount) (*TokenAmount, error) {
	if ta.Decimals != other.Decimals {
		return nil, errors.New("cannot add amounts with different decimals")
	}

	return &TokenAmount{
		Amount:   ta.Amount.Add(other.Amount),
		Decimals: ta.Decimals,
		Symbol:   ta.Symbol,
	}, nil
}

// Sub subtracts another TokenAmount from this one.
func (ta *TokenAmount) Sub(other *TokenAmount) (*TokenAmount, error) {
	if ta.Decimals != other.Decimals {
		return nil, errors.New("cannot subtract amounts with different decimals")
	}

	result := ta.Amount.Sub(other.Amount)
	if result.IsNegative() {
		return nil, ErrInsufficientBalance
	}

	return &TokenAmount{
		Amount:   result,
		Decimals: ta.Decimals,
		Symbol:   ta.Symbol,
	}, nil
}

// Mul multiplies the TokenAmount by a decimal value.
func (ta *TokenAmount) Mul(value decimal.Decimal) *TokenAmount {
	return &TokenAmount{
		Amount:   ta.Amount.Mul(value).Round(int32(ta.Decimals)),
		Decimals: ta.Decimals,
		Symbol:   ta.Symbol,
	}
}

// Div divides the TokenAmount by a decimal value.
func (ta *TokenAmount) Div(value decimal.Decimal) (*TokenAmount, error) {
	if value.IsZero() {
		return nil, errors.New("division by zero")
	}
	return &TokenAmount{
		Amount:   ta.Amount.Div(value).Round(int32(ta.Decimals)),
		Decimals: ta.Decimals,
		Symbol:   ta.Symbol,
	}, nil
}

// Cmp compares two TokenAmounts
// Returns: -1 if ta < other, 0 if ta == other, 1 if ta > other.
func (ta *TokenAmount) Cmp(other *TokenAmount) (int, error) {
	if ta.Decimals != other.Decimals {
		return 0, errors.New("cannot compare amounts with different decimals")
	}
	return ta.Amount.Cmp(other.Amount), nil
}

// IsZero returns true if the amount is zero.
func (ta *TokenAmount) IsZero() bool {
	return ta.Amount.IsZero()
}

// IsNegative returns true if the amount is negative.
func (ta *TokenAmount) IsNegative() bool {
	return ta.Amount.IsNegative()
}

// IsPositive returns true if the amount is positive.
func (ta *TokenAmount) IsPositive() bool {
	return ta.Amount.IsPositive()
}

// Position represents a trading position.
type Position struct {
	ID            string         `json:"id"`
	KeyID         string         `json:"keyId"` // Reference to wallet key
	Chain         Chain          `json:"chain"`
	TokenAddress  string         `json:"tokenAddress"` // Token mint or contract address
	TokenSymbol   string         `json:"tokenSymbol"`
	TokenDecimals uint8          `json:"tokenDecimals"`
	Amount        *TokenAmount   `json:"amount"`       // Current token amount
	EntryPrice    *TokenAmount   `json:"entryPrice"`   // Price per token at entry (in base currency)
	CurrentPrice  *TokenAmount   `json:"currentPrice"` // Current price per token (in base currency)
	Type          PositionType   `json:"type"`
	Status        PositionStatus `json:"status"`
	OpenedAt      time.Time      `json:"openedAt"`
	ClosedAt      *time.Time     `json:"closedAt,omitempty"`
	TotalInvested *TokenAmount   `json:"totalInvested"` // Total invested in base currency
	TotalReturn   *TokenAmount   `json:"totalReturn"`   // Total returned on close (in base currency)
	Notes         string         `json:"notes,omitempty"`
	TakeProfitSet *TokenAmount   `json:"takeProfitSet,omitempty"`
	StopLossSet   *TokenAmount   `json:"stopLossSet,omitempty"`
	TxnHashBuy    string         `json:"txnHashBuy,omitempty"`
	TxnHashSell   string         `json:"txnHashSell,omitempty"`
}

// PositionUpdate represents an update to a position.
type PositionUpdate struct {
	PositionID    string         `json:"positionId"`
	Amount        *TokenAmount   `json:"amount,omitempty"`
	CurrentPrice  *TokenAmount   `json:"currentPrice,omitempty"`
	Status        PositionStatus `json:"status,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	TakeProfitSet *TokenAmount   `json:"takeProfitSet,omitempty"`
	StopLossSet   *TokenAmount   `json:"stopLossSet,omitempty"`
}

// PnL represents profit and loss calculation.
type PnL struct {
	UnrealizedPnL *TokenAmount `json:"unrealizedPnL"`
	RealizedPnL   *TokenAmount `json:"realizedPnL"`
	ROI           float64      `json:"roi"` // Return on investment as percentage
	ROIFormatted  string       `json:"roiFormatted"`
}

// CalculatePnL calculates the profit and loss for a position.
func (p *Position) CalculatePnL() *PnL {
	pnl := &PnL{}

	if p.Amount == nil || p.EntryPrice == nil || p.CurrentPrice == nil || p.TotalInvested == nil {
		return pnl
	}

	// Calculate current value using decimal arithmetic
	currentValue := p.Amount.Amount.Mul(p.CurrentPrice.Amount)

	// Calculate unrealized PnL
	if p.Status == PositionStatusOpen {
		pnlValue := currentValue.Sub(p.TotalInvested.Amount)
		pnl.UnrealizedPnL = NewTokenAmountFromDecimal(pnlValue, p.EntryPrice.Decimals, p.EntryPrice.Symbol)
	}

	// Calculate realized PnL
	if p.Status == PositionStatusClosed && p.TotalReturn != nil {
		// For realized PnL, handle negative values (losses)
		pnlValue := p.TotalReturn.Amount.Sub(p.TotalInvested.Amount)
		pnl.RealizedPnL = NewTokenAmountFromDecimal(pnlValue, p.TotalReturn.Decimals, p.TotalReturn.Symbol)

		// Calculate ROI using decimal
		if !p.TotalInvested.Amount.IsZero() {
			roi := p.TotalReturn.Amount.Sub(p.TotalInvested.Amount).
				Div(p.TotalInvested.Amount).
				Mul(decimal.NewFromInt(100))
			pnl.ROI = roi.InexactFloat64()
			pnl.ROIFormatted = roi.StringFixed(2) + "%"
		}
	}

	return pnl
}

// IsProfitable returns true if the position is currently profitable.
func (p *Position) IsProfitable() bool {
	pnl := p.CalculatePnL()
	if p.Status == PositionStatusOpen && pnl.UnrealizedPnL != nil {
		return pnl.UnrealizedPnL.Amount.IsPositive()
	}
	if p.Status == PositionStatusClosed && pnl.RealizedPnL != nil {
		return pnl.RealizedPnL.Amount.IsPositive()
	}
	return false
}

// ShouldTakeProfit checks if the position has hit take profit.
func (p *Position) ShouldTakeProfit() bool {
	if p.TakeProfitSet == nil || p.CurrentPrice == nil || p.EntryPrice == nil {
		return false
	}
	return p.CurrentPrice.Amount.GreaterThanOrEqual(p.TakeProfitSet.Amount)
}

// ShouldStopLoss checks if the position has hit stop loss.
func (p *Position) ShouldStopLoss() bool {
	if p.StopLossSet == nil || p.CurrentPrice == nil || p.EntryPrice == nil {
		return false
	}
	return p.CurrentPrice.Amount.LessThanOrEqual(p.StopLossSet.Amount)
}

// Portfolio represents a collection of positions.
type Portfolio struct {
	mu        sync.RWMutex
	positions map[string]*Position
}

// NewPortfolio creates a new empty portfolio.
func NewPortfolio() *Portfolio {
	return &Portfolio{
		positions: make(map[string]*Position),
	}
}

// AddPosition adds a new position to the portfolio.
func (p *Portfolio) AddPosition(pos *Position) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if pos.ID == "" {
		pos.ID = uuid.New().String()
	}

	if _, exists := p.positions[pos.ID]; exists {
		return fmt.Errorf("position with ID %s already exists", pos.ID)
	}

	pos.Status = PositionStatusOpen
	pos.OpenedAt = time.Now()
	p.positions[pos.ID] = pos

	return nil
}

// GetPosition retrieves a position by ID.
func (p *Portfolio) GetPosition(id string) (*Position, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	pos, exists := p.positions[id]
	if !exists {
		return nil, ErrPositionNotFound
	}

	// Return a copy to prevent external modifications
	return p.copyPosition(pos), nil
}

// UpdatePosition updates an existing position.
func (p *Portfolio) UpdatePosition(update PositionUpdate) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pos, exists := p.positions[update.PositionID]
	if !exists {
		return ErrPositionNotFound
	}

	if update.Amount != nil {
		pos.Amount = update.Amount
	}

	if update.CurrentPrice != nil {
		pos.CurrentPrice = update.CurrentPrice
	}

	if update.Status != "" {
		pos.Status = update.Status
		if update.Status == PositionStatusClosed {
			now := time.Now()
			pos.ClosedAt = &now
		}
	}

	if update.Notes != "" {
		pos.Notes = update.Notes
	}

	if update.TakeProfitSet != nil {
		pos.TakeProfitSet = update.TakeProfitSet
	}

	if update.StopLossSet != nil {
		pos.StopLossSet = update.StopLossSet
	}

	return nil
}

// ClosePosition closes a position and records the return.
func (p *Portfolio) ClosePosition(
	id string,
	currentPrice *TokenAmount,
	totalReturn *TokenAmount,
	txnHash string,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pos, exists := p.positions[id]
	if !exists {
		return ErrPositionNotFound
	}

	if pos.Status == PositionStatusClosed {
		return fmt.Errorf("position %s is already closed", id)
	}

	pos.Status = PositionStatusClosed
	pos.CurrentPrice = currentPrice
	pos.TotalReturn = totalReturn
	pos.TxnHashSell = txnHash
	now := time.Now()
	pos.ClosedAt = &now

	return nil
}

// RemovePosition removes a position from the portfolio.
func (p *Portfolio) RemovePosition(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.positions[id]; !exists {
		return ErrPositionNotFound
	}

	delete(p.positions, id)
	return nil
}

// ListPositions returns all positions.
func (p *Portfolio) ListPositions() []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	positions := make([]*Position, 0, len(p.positions))
	for _, pos := range p.positions {
		positions = append(positions, p.copyPosition(pos))
	}

	return positions
}

// ListOpenPositions returns all open positions.
func (p *Portfolio) ListOpenPositions() []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	openPositions := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.Status == PositionStatusOpen {
			openPositions = append(openPositions, p.copyPosition(pos))
		}
	}

	return openPositions
}

// ListClosedPositions returns all closed positions.
func (p *Portfolio) ListClosedPositions() []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	closedPositions := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.Status == PositionStatusClosed {
			closedPositions = append(closedPositions, p.copyPosition(pos))
		}
	}

	return closedPositions
}

// GetPositionsByChain returns all positions for a specific chain.
func (p *Portfolio) GetPositionsByChain(chain Chain) []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	chainPositions := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.Chain == chain {
			chainPositions = append(chainPositions, p.copyPosition(pos))
		}
	}

	return chainPositions
}

// GetPositionsByKey returns all positions for a specific wallet key.
func (p *Portfolio) GetPositionsByKey(keyID string) []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	keyPositions := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.KeyID == keyID {
			keyPositions = append(keyPositions, p.copyPosition(pos))
		}
	}

	return keyPositions
}

// GetPositionByToken returns positions for a specific token.
func (p *Portfolio) GetPositionByToken(tokenAddress string) ([]*Position, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tokenPositions := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.TokenAddress == tokenAddress {
			tokenPositions = append(tokenPositions, p.copyPosition(pos))
		}
	}

	if len(tokenPositions) == 0 {
		return nil, ErrPositionNotFound
	}

	return tokenPositions, nil
}

// CalculateTotalValue calculates the total value of all open positions.
func (p *Portfolio) CalculateTotalValue(chain Chain, baseCurrency string) *TokenAmount {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total := decimal.Zero

	for _, pos := range p.positions {
		if pos.Chain == chain && pos.Status == PositionStatusOpen {
			if pos.Amount != nil && pos.CurrentPrice != nil {
				value := pos.Amount.Amount.Mul(pos.CurrentPrice.Amount)
				total = total.Add(value)
			}
		}
	}

	return NewTokenAmountFromDecimal(total, 18, baseCurrency)
}

// CalculateTotalPnL calculates total realized and unrealized PnL.
func (p *Portfolio) CalculateTotalPnL(chain Chain) (realized, unrealized *TokenAmount) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	realizedTotal := decimal.Zero
	unrealizedTotal := decimal.Zero

	for _, pos := range p.positions {
		if pos.Chain != chain {
			continue
		}

		pnl := pos.CalculatePnL()
		if pnl.RealizedPnL != nil {
			realizedTotal = realizedTotal.Add(pnl.RealizedPnL.Amount)
		}
		if pnl.UnrealizedPnL != nil {
			unrealizedTotal = unrealizedTotal.Add(pnl.UnrealizedPnL.Amount)
		}
	}

	// Assume 18 decimals for base currency
	decimals := uint8(18)
	symbol := "USD" // Default to USD

	if !realizedTotal.IsZero() {
		realized = NewTokenAmountFromDecimal(realizedTotal, decimals, symbol)
	}

	if !unrealizedTotal.IsZero() {
		unrealized = NewTokenAmountFromDecimal(unrealizedTotal, decimals, symbol)
	}

	return realized, unrealized
}

// GetPositionsNeedingAttention returns positions that need attention (hit take profit or stop loss).
func (p *Portfolio) GetPositionsNeedingAttention() []*Position {
	p.mu.RLock()
	defer p.mu.RUnlock()

	needingAttention := make([]*Position, 0)
	for _, pos := range p.positions {
		if pos.Status == PositionStatusOpen {
			if pos.ShouldTakeProfit() || pos.ShouldStopLoss() {
				needingAttention = append(needingAttention, p.copyPosition(pos))
			}
		}
	}

	return needingAttention
}

// copyPosition creates a deep copy of a position.
func (p *Portfolio) copyPosition(pos *Position) *Position {
	// Marshal and unmarshal to create a deep copy
	data, _ := sonic.Marshal(pos)
	copy := &Position{}
	_ = sonic.Unmarshal(data, copy)
	return copy
}

// Save saves the portfolio to a file.
func (p *Portfolio) Save(path string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	data, err := sonic.Marshal(p.positions)
	if err != nil {
		return fmt.Errorf("failed to marshal portfolio: %w", err)
	}

	// Note: In production, this should be encrypted
	if err := WriteFileAtomic(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write portfolio file: %w", err)
	}

	return nil
}

// LoadPortfolio loads a portfolio from a file.
func LoadPortfolio(path string) (*Portfolio, error) {
	data, err := ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read portfolio file: %w", err)
	}

	positions := make(map[string]*Position)
	if err := sonic.Unmarshal(data, &positions); err != nil {
		return nil, fmt.Errorf("failed to parse portfolio file: %w", err)
	}

	return &Portfolio{
		positions: positions,
	}, nil
}

// WriteFileAtomic writes to a file atomically.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tempPath := path + ".tmp"

	if err := os.WriteFile(tempPath, data, perm); err != nil {
		return err
	}

	return os.Rename(tempPath, path)
}

// ReadFile reads a file.
func ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// Statistics represents portfolio statistics.
type Statistics struct {
	TotalPositions  int           `json:"totalPositions"`
	OpenPositions   int           `json:"openPositions"`
	ClosedPositions int           `json:"closedPositions"`
	TotalInvested   *TokenAmount  `json:"totalInvested"`
	TotalReturn     *TokenAmount  `json:"totalReturn"`
	WinCount        int           `json:"winCount"`
	LossCount       int           `json:"lossCount"`
	WinRate         float64       `json:"winRate"`
	RealizedPnL     *TokenAmount  `json:"realizedPnL"`
	UnrealizedPnL   *TokenAmount  `json:"unrealizedPnL"`
	BestTrade       *TokenAmount  `json:"bestTrade"`
	WorstTrade      *TokenAmount  `json:"worstTrade"`
	AverageHoldTime time.Duration `json:"averageHoldTime"`
	LargestWin      *Position     `json:"largestWin,omitempty"`
	LargestLoss     *Position     `json:"largestLoss,omitempty"`
}

// CalculateStatistics calculates portfolio statistics.
func (p *Portfolio) CalculateStatistics(chain Chain) *Statistics {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := &Statistics{}

	totalInvested := decimal.Zero
	totalReturn := decimal.Zero
	var winCount int
	var lossCount int
	var totalHoldTime time.Duration
	var closedCount int
	var bestPnL decimal.Decimal
	var worstPnL decimal.Decimal
	var foundFirstLoss bool

	for _, pos := range p.positions {
		if pos.Chain != chain {
			continue
		}

		stats.TotalPositions++

		switch pos.Status {
		case PositionStatusOpen:
			stats.OpenPositions++
		case PositionStatusClosed:
			stats.ClosedPositions++
			closedCount++

			if pos.TotalInvested != nil {
				totalInvested = totalInvested.Add(pos.TotalInvested.Amount)
			}
			if pos.TotalReturn != nil {
				totalReturn = totalReturn.Add(pos.TotalReturn.Amount)
			}

			if !pos.ClosedAt.IsZero() {
				totalHoldTime += pos.ClosedAt.Sub(pos.OpenedAt)
			}

			pnl := pos.CalculatePnL()
			if pnl.RealizedPnL != nil {
				pnlValue := pnl.RealizedPnL.Amount
				if pnlValue.IsPositive() {
					winCount++
					if pnlValue.GreaterThan(bestPnL) {
						bestPnL = pnlValue
						stats.LargestWin = p.copyPosition(pos)
					}
				} else if pnlValue.IsNegative() {
					lossCount++
					if !foundFirstLoss || pnlValue.LessThan(worstPnL) {
						worstPnL = pnlValue
						stats.LargestLoss = p.copyPosition(pos)
						foundFirstLoss = true
					}
				}
			}
		}
	}

	if !totalInvested.IsZero() {
		stats.TotalInvested = NewTokenAmountFromDecimal(totalInvested, 18, "USD")
	}
	if !totalReturn.IsZero() {
		stats.TotalReturn = NewTokenAmountFromDecimal(totalReturn, 18, "USD")
	}
	if closedCount > 0 {
		stats.WinCount = winCount
		stats.LossCount = lossCount
		stats.WinRate = decimal.NewFromInt(int64(winCount)).
			Div(decimal.NewFromInt(int64(closedCount))).
			Mul(decimal.NewFromInt(100)).
			InexactFloat64()
		stats.AverageHoldTime = totalHoldTime / time.Duration(closedCount)
	}
	if bestPnL.IsPositive() {
		stats.BestTrade = NewTokenAmountFromDecimal(bestPnL, 18, "USD")
	}
	if worstPnL.IsNegative() {
		stats.WorstTrade = NewTokenAmountFromDecimal(worstPnL, 18, "USD")
	}

	// Calculate total PnL
	realized, unrealized := p.CalculateTotalPnL(chain)
	stats.RealizedPnL = realized
	stats.UnrealizedPnL = unrealized

	return stats
}
