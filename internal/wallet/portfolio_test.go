package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestNewTokenAmount(t *testing.T) {
	amount := NewTokenAmount(1.5, 9, "SOL")
	if amount.ToFloat() != 1.5 {
		t.Errorf("expected 1.5, got %f", amount.ToFloat())
	}
	if amount.Decimals != 9 {
		t.Errorf("expected decimals 9, got %d", amount.Decimals)
	}
	if amount.Symbol != "SOL" {
		t.Errorf("expected symbol SOL, got %s", amount.Symbol)
	}
}

func TestNewTokenAmountFromString(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		decimals uint8
		symbol   string
		want     string
		wantErr  bool
	}{
		{"simple", "1.5", 9, "SOL", "1.500000000", false},
		{"large", "1234.5678", 6, "TEST", "1234.567800", false},
		{"zero", "0", 9, "ETH", "0.000000000", false},
		{"negative", "-0.5", 9, "SOL", "-0.500000000", false},
		{"invalid", "abc", 9, "SOL", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := NewTokenAmountFromString(tt.value, tt.decimals, tt.symbol)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTokenAmountFromString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && amount.ToFormattedString() != tt.want {
				t.Errorf("NewTokenAmountFromString() = %v, want %v", amount.ToFormattedString(), tt.want)
			}
		})
	}
}

func TestNewTokenAmountFromDecimal(t *testing.T) {
	d := decimal.NewFromFloat(123.456789)
	amount := NewTokenAmountFromDecimal(d, 3, "TEST")

	if amount.Symbol != "TEST" {
		t.Errorf("expected symbol TEST, got %s", amount.Symbol)
	}

	// Should round to 3 decimals
	expected := decimal.NewFromFloat(123.457)
	if !amount.Amount.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, amount.Amount)
	}
}

func TestTokenAmountToFloat(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals uint8
		expected float64
	}{
		{"zero", 0, 9, 0},
		{"one", 1, 9, 1},
		{"fractional", 0.5, 9, 0.5},
		{"large", 1000.123456789, 9, 1000.123456789},
		{"eth_decimals", 1.5, 18, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := NewTokenAmount(tt.value, tt.decimals, "TEST")
			result := amount.ToFloat()
			// Use approximate comparison for floating point
			diff := result - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.000001 {
				t.Errorf("expected %f, got %f (diff: %f)", tt.expected, result, diff)
			}
		})
	}
}

func TestTokenAmountString(t *testing.T) {
	amount := NewTokenAmount(123.456789, 9, "SOL")
	str := amount.String()
	if str == "" {
		t.Error("String returned empty")
	}

	// Check that the string contains the symbol
	if len(str) < 4 || str[len(str)-3:] != "SOL" {
		t.Errorf("String doesn't contain symbol properly: %s", str)
	}
}

func TestTokenAmountToFormattedString(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals uint8
		expected string
	}{
		{"simple", 1.5, 2, "1.50"},
		{"more decimals", 1.5, 6, "1.500000"},
		{"rounding", 1.5678, 2, "1.57"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := NewTokenAmount(tt.value, tt.decimals, "TEST")
			result := amount.ToFormattedString()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestTokenAmountAdd(t *testing.T) {
	amount1 := NewTokenAmount(1.5, 9, "SOL")
	amount2 := NewTokenAmount(2.5, 9, "SOL")

	sum, err := amount1.Add(amount2)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if sum.ToFloat() != 4.0 {
		t.Errorf("expected 4.0, got %f", sum.ToFloat())
	}
}

func TestTokenAmountAddDifferentDecimals(t *testing.T) {
	amount1 := NewTokenAmount(1.5, 9, "SOL")
	amount2 := NewTokenAmount(2.5, 18, "ETH")

	_, err := amount1.Add(amount2)
	if err == nil {
		t.Error("expected error adding amounts with different decimals")
	}
}

func TestTokenAmountSub(t *testing.T) {
	amount1 := NewTokenAmount(5.0, 9, "SOL")
	amount2 := NewTokenAmount(2.0, 9, "SOL")

	diff, err := amount1.Sub(amount2)
	if err != nil {
		t.Fatalf("Sub failed: %v", err)
	}

	if diff.ToFloat() != 3.0 {
		t.Errorf("expected 3.0, got %f", diff.ToFloat())
	}
}

func TestTokenAmountSubInsufficientBalance(t *testing.T) {
	amount1 := NewTokenAmount(1.0, 9, "SOL")
	amount2 := NewTokenAmount(2.0, 9, "SOL")

	_, err := amount1.Sub(amount2)
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestTokenAmountMul(t *testing.T) {
	amount := NewTokenAmount(10.0, 9, "SOL")

	result := amount.Mul(decimal.NewFromFloat(2.5))

	if result.ToFloat() != 25.0 {
		t.Errorf("expected 25.0, got %f", result.ToFloat())
	}
}

func TestTokenAmountDiv(t *testing.T) {
	amount := NewTokenAmount(10.0, 9, "SOL")

	result, err := amount.Div(decimal.NewFromFloat(2.5))
	if err != nil {
		t.Fatalf("Div failed: %v", err)
	}

	if result.ToFloat() != 4.0 {
		t.Errorf("expected 4.0, got %f", result.ToFloat())
	}

	// Test division by zero
	_, err = amount.Div(decimal.Zero)
	if err == nil {
		t.Error("expected error dividing by zero")
	}
}

func TestTokenAmountCmp(t *testing.T) {
	amount1 := NewTokenAmount(5.0, 9, "SOL")
	amount2 := NewTokenAmount(3.0, 9, "SOL")
	amount3 := NewTokenAmount(5.0, 9, "SOL")
	amount4 := NewTokenAmount(7.0, 9, "SOL")

	// amount1 > amount2
	cmp, _ := amount1.Cmp(amount2)
	if cmp != 1 {
		t.Errorf("expected 1, got %d", cmp)
	}

	// amount1 == amount3
	cmp, _ = amount1.Cmp(amount3)
	if cmp != 0 {
		t.Errorf("expected 0, got %d", cmp)
	}

	// amount1 < amount4
	cmp, _ = amount1.Cmp(amount4)
	if cmp != -1 {
		t.Errorf("expected -1, got %d", cmp)
	}

	// Different decimals should error
	amount5 := NewTokenAmount(5.0, 18, "ETH")
	_, err := amount1.Cmp(amount5)
	if err == nil {
		t.Error("expected error comparing amounts with different decimals")
	}
}

func TestTokenAmountIsZero(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		expected bool
	}{
		{"zero", 0, true},
		{"positive", 1.0, false},
		{"negative", -1.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := NewTokenAmount(tt.value, 9, "TEST")
			if amount.IsZero() != tt.expected {
				t.Errorf("IsZero() = %v, want %v", amount.IsZero(), tt.expected)
			}
		})
	}
}

func TestTokenAmountIsNegative(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		expected bool
	}{
		{"zero", 0, false},
		{"positive", 1.0, false},
		{"negative", -1.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := NewTokenAmount(tt.value, 9, "TEST")
			if amount.IsNegative() != tt.expected {
				t.Errorf("IsNegative() = %v, want %v", amount.IsNegative(), tt.expected)
			}
		})
	}
}

func TestTokenAmountIsPositive(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		expected bool
	}{
		{"zero", 0, false},
		{"positive", 1.0, true},
		{"negative", -1.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount := NewTokenAmount(tt.value, 9, "TEST")
			if amount.IsPositive() != tt.expected {
				t.Errorf("IsPositive() = %v, want %v", amount.IsPositive(), tt.expected)
			}
		})
	}
}

func TestNewPortfolio(t *testing.T) {
	p := NewPortfolio()
	if p == nil {
		t.Fatal("NewPortfolio returned nil")
	}
	if p.positions == nil {
		t.Error("positions map is nil")
	}
}

func TestPortfolioAddPosition(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-123",
		Chain:         ChainSolana,
		TokenAddress:  "token-addr",
		TokenSymbol:   "MEME",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "MEME"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	err := p.AddPosition(pos)
	if err != nil {
		t.Fatalf("AddPosition failed: %v", err)
	}

	if pos.ID == "" {
		t.Error("position ID not generated")
	}

	if pos.Status != PositionStatusOpen {
		t.Errorf("expected status %s, got %s", PositionStatusOpen, pos.Status)
	}

	if pos.OpenedAt.IsZero() {
		t.Error("OpenedAt not set")
	}
}

func TestPortfolioGetPosition(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-123",
		Chain:         ChainSolana,
		TokenAddress:  "token-addr",
		TokenSymbol:   "MEME",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "MEME"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	_ = p.AddPosition(pos)

	retrieved, err := p.GetPosition(pos.ID)
	if err != nil {
		t.Fatalf("GetPosition failed: %v", err)
	}

	if retrieved.ID != pos.ID {
		t.Errorf("ID mismatch: expected %s, got %s", pos.ID, retrieved.ID)
	}

	if retrieved.TokenSymbol != pos.TokenSymbol {
		t.Errorf("TokenSymbol mismatch: expected %s, got %s", pos.TokenSymbol, retrieved.TokenSymbol)
	}
}

func TestPortfolioGetPositionNotFound(t *testing.T) {
	p := NewPortfolio()

	_, err := p.GetPosition("non-existent")
	if !errors.Is(err, ErrPositionNotFound) {
		t.Errorf("expected ErrPositionNotFound, got %v", err)
	}
}

func TestPortfolioUpdatePosition(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-123",
		Chain:         ChainSolana,
		TokenAddress:  "token-addr",
		TokenSymbol:   "MEME",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "MEME"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	_ = p.AddPosition(pos)

	// Update price
	newPrice := NewTokenAmount(0.02, 9, "SOL")
	update := PositionUpdate{
		PositionID:   pos.ID,
		CurrentPrice: newPrice,
	}

	err := p.UpdatePosition(update)
	if err != nil {
		t.Fatalf("UpdatePosition failed: %v", err)
	}

	// Verify update
	retrieved, _ := p.GetPosition(pos.ID)
	if retrieved.CurrentPrice.Amount.LessThan(decimal.NewFromFloat(0.02)) ||
		retrieved.CurrentPrice.Amount.GreaterThan(decimal.NewFromFloat(0.02)) {
		t.Errorf("price not updated: expected 0.02, got %s", retrieved.CurrentPrice.Amount)
	}
}

func TestPortfolioClosePosition(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-123",
		Chain:         ChainSolana,
		TokenAddress:  "token-addr",
		TokenSymbol:   "MEME",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "MEME"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	p.AddPosition(pos)

	// Close position
	finalPrice := NewTokenAmount(0.025, 9, "SOL")
	totalReturn := NewTokenAmount(25, 9, "SOL")
	txnHash := "txn-123"

	err := p.ClosePosition(pos.ID, finalPrice, totalReturn, txnHash)
	if err != nil {
		t.Fatalf("ClosePosition failed: %v", err)
	}

	// Verify close
	retrieved, _ := p.GetPosition(pos.ID)
	if retrieved.Status != PositionStatusClosed {
		t.Errorf("status not closed: expected %s, got %s", PositionStatusClosed, retrieved.Status)
	}

	if retrieved.ClosedAt == nil {
		t.Error("ClosedAt not set")
	}

	if retrieved.TxnHashSell != txnHash {
		t.Errorf("txn hash not set: expected %s, got %s", txnHash, retrieved.TxnHashSell)
	}
}

func TestPortfolioRemovePosition(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-123",
		Chain:         ChainSolana,
		TokenAddress:  "token-addr",
		TokenSymbol:   "MEME",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "MEME"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	p.AddPosition(pos)

	err := p.RemovePosition(pos.ID)
	if err != nil {
		t.Fatalf("RemovePosition failed: %v", err)
	}

	// Verify removed
	_, err = p.GetPosition(pos.ID)
	if !errors.Is(err, ErrPositionNotFound) {
		t.Errorf("expected ErrPositionNotFound after removal, got %v", err)
	}
}

func TestPortfolioListPositions(t *testing.T) {
	p := NewPortfolio()

	// Add multiple positions
	pos1 := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "TOKEN1",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(100, 9, "TOKEN1"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(1, 9, "SOL"),
	}

	pos2 := &Position{
		KeyID:         "key-2",
		Chain:         ChainBase,
		TokenAddress:  "token-2",
		TokenSymbol:   "TOKEN2",
		TokenDecimals: 18,
		Amount:        NewTokenAmount(100, 18, "TOKEN2"),
		EntryPrice:    NewTokenAmount(0.001, 18, "ETH"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(0.1, 18, "ETH"),
	}

	p.AddPosition(pos1)
	p.AddPosition(pos2)

	// List all
	all := p.ListPositions()
	if len(all) != 2 {
		t.Errorf("expected 2 positions, got %d", len(all))
	}

	// List open
	open := p.ListOpenPositions()
	if len(open) != 2 {
		t.Errorf("expected 2 open positions, got %d", len(open))
	}

	// Close one and check
	now := time.Now()
	pos1.Status = PositionStatusClosed
	pos1.ClosedAt = &now

	closed := p.ListClosedPositions()
	if len(closed) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(closed))
	}

	open = p.ListOpenPositions()
	if len(open) != 1 {
		t.Errorf("expected 1 open position, got %d", len(open))
	}
}

func TestPortfolioGetPositionsByChain(t *testing.T) {
	p := NewPortfolio()

	// Add positions on different chains
	pos1 := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "SOLTOKEN",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(100, 9, "SOLTOKEN"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(1, 9, "SOL"),
	}

	pos2 := &Position{
		KeyID:         "key-2",
		Chain:         ChainBase,
		TokenAddress:  "token-2",
		TokenSymbol:   "BASETOKEN",
		TokenDecimals: 18,
		Amount:        NewTokenAmount(100, 18, "BASETOKEN"),
		EntryPrice:    NewTokenAmount(0.001, 18, "ETH"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(0.1, 18, "ETH"),
	}

	p.AddPosition(pos1)
	p.AddPosition(pos2)

	// Get Solana positions
	solanaPositions := p.GetPositionsByChain(ChainSolana)
	if len(solanaPositions) != 1 {
		t.Errorf("expected 1 Solana position, got %d", len(solanaPositions))
	}
	if solanaPositions[0].Chain != ChainSolana {
		t.Error("returned position is not on Solana chain")
	}

	// Get Base positions
	basePositions := p.GetPositionsByChain(ChainBase)
	if len(basePositions) != 1 {
		t.Errorf("expected 1 Base position, got %d", len(basePositions))
	}
	if basePositions[0].Chain != ChainBase {
		t.Error("returned position is not on Base chain")
	}
}

func TestPortfolioGetPositionsByKey(t *testing.T) {
	p := NewPortfolio()

	pos1 := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "TOKEN1",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(100, 9, "TOKEN1"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(1, 9, "SOL"),
	}

	pos2 := &Position{
		KeyID:         "key-2",
		Chain:         ChainSolana,
		TokenAddress:  "token-2",
		TokenSymbol:   "TOKEN2",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(100, 9, "TOKEN2"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(1, 9, "SOL"),
	}

	p.AddPosition(pos1)
	p.AddPosition(pos2)

	// Get positions for key-1
	keyPositions := p.GetPositionsByKey("key-1")
	if len(keyPositions) != 1 {
		t.Errorf("expected 1 position, got %d", len(keyPositions))
	}
	if keyPositions[0].KeyID != "key-1" {
		t.Error("returned position has wrong key ID")
	}
}

func TestPositionCalculatePnL(t *testing.T) {
	tests := []struct {
		name         string
		entryPrice   float64
		currentPrice float64
		amount       float64
		invested     float64
		status       PositionStatus
		hasReturn    bool
		totalReturn  float64
		expectedROI  float64
		isProfitable bool
	}{
		{
			name:         "open profitable position",
			entryPrice:   0.01,
			currentPrice: 0.02,
			amount:       1000,
			invested:     10,
			status:       PositionStatusOpen,
			hasReturn:    false,
			isProfitable: true,
		},
		{
			name:         "open losing position",
			entryPrice:   0.02,
			currentPrice: 0.01,
			amount:       1000,
			invested:     20,
			status:       PositionStatusOpen,
			hasReturn:    false,
			isProfitable: false,
		},
		{
			name:         "closed profitable trade",
			entryPrice:   0.01,
			currentPrice: 0.02,
			amount:       1000,
			invested:     10,
			status:       PositionStatusClosed,
			hasReturn:    true,
			totalReturn:  20,
			expectedROI:  100,
			isProfitable: true,
		},
		{
			name:         "closed losing trade",
			entryPrice:   0.01,
			currentPrice: 0.005,
			amount:       1000,
			invested:     10,
			status:       PositionStatusClosed,
			hasReturn:    true,
			totalReturn:  5,
			expectedROI:  -50,
			isProfitable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := &Position{
				KeyID:         "test-key",
				Chain:         ChainSolana,
				TokenAddress:  "test-token",
				TokenSymbol:   "TEST",
				TokenDecimals: 9,
				Amount:        NewTokenAmount(tt.amount, 9, "TEST"),
				EntryPrice:    NewTokenAmount(tt.entryPrice, 9, "SOL"),
				CurrentPrice:  NewTokenAmount(tt.currentPrice, 9, "SOL"),
				Type:          PositionTypeLong,
				Status:        tt.status,
				TotalInvested: NewTokenAmount(tt.invested, 9, "SOL"),
			}

			if tt.hasReturn {
				pos.TotalReturn = NewTokenAmount(tt.totalReturn, 9, "SOL")
			}

			pnl := pos.CalculatePnL()

			// Check profitability
			if pos.IsProfitable() != tt.isProfitable {
				t.Errorf("IsProfitable: expected %v, got %v", tt.isProfitable, pos.IsProfitable())
			}

			// Check ROI for closed positions
			if tt.status == PositionStatusClosed && tt.hasReturn {
				if pnl.ROI != tt.expectedROI {
					t.Errorf("ROI: expected %f, got %f", tt.expectedROI, pnl.ROI)
				}
			}
		})
	}
}

func TestPositionShouldTakeProfit(t *testing.T) {
	pos := &Position{
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		TakeProfitSet: NewTokenAmount(0.015, 9, "SOL"),
	}

	if !pos.ShouldTakeProfit() {
		t.Error("expected take profit to trigger")
	}

	pos.TakeProfitSet = NewTokenAmount(0.025, 9, "SOL")
	if pos.ShouldTakeProfit() {
		t.Error("expected take profit not to trigger")
	}
}

func TestPositionShouldStopLoss(t *testing.T) {
	pos := &Position{
		EntryPrice:   NewTokenAmount(0.02, 9, "SOL"),
		CurrentPrice: NewTokenAmount(0.01, 9, "SOL"),
		StopLossSet:  NewTokenAmount(0.015, 9, "SOL"),
	}

	if !pos.ShouldStopLoss() {
		t.Error("expected stop loss to trigger")
	}

	pos.StopLossSet = NewTokenAmount(0.005, 9, "SOL")
	if pos.ShouldStopLoss() {
		t.Error("expected stop loss not to trigger")
	}
}

func TestPortfolioCalculateTotalValue(t *testing.T) {
	p := NewPortfolio()

	// Add open positions
	pos1 := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "TOKEN1",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN1"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		Type:          PositionTypeLong,
		Status:        PositionStatusOpen,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	pos2 := &Position{
		KeyID:         "key-2",
		Chain:         ChainSolana,
		TokenAddress:  "token-2",
		TokenSymbol:   "TOKEN2",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(500, 9, "TOKEN2"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		Status:        PositionStatusOpen,
		TotalInvested: NewTokenAmount(5, 9, "SOL"),
	}

	p.AddPosition(pos1)
	p.AddPosition(pos2)

	// Calculate total value: (1000 * 0.02) + (500 * 0.015) = 20 + 7.5 = 27.5
	totalValue := p.CalculateTotalValue(ChainSolana, "SOL")
	expectedValue := decimal.NewFromFloat(27.5)

	// Use decimal comparison
	if !totalValue.Amount.Equal(expectedValue) {
		t.Errorf("expected total value %s, got %s", expectedValue, totalValue.Amount)
	}
}

func TestPortfolioGetPositionsNeedingAttention(t *testing.T) {
	p := NewPortfolio()

	// Position at take profit
	pos1 := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "TOKEN1",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN1"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		TakeProfitSet: NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		Status:        PositionStatusOpen,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	// Position at stop loss
	pos2 := &Position{
		KeyID:         "key-2",
		Chain:         ChainSolana,
		TokenAddress:  "token-2",
		TokenSymbol:   "TOKEN2",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN2"),
		EntryPrice:    NewTokenAmount(0.02, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.01, 9, "SOL"),
		StopLossSet:   NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		Status:        PositionStatusOpen,
		TotalInvested: NewTokenAmount(20, 9, "SOL"),
	}

	// Normal position
	pos3 := &Position{
		KeyID:         "key-3",
		Chain:         ChainSolana,
		TokenAddress:  "token-3",
		TokenSymbol:   "TOKEN3",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN3"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.012, 9, "SOL"),
		Type:          PositionTypeLong,
		Status:        PositionStatusOpen,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}

	p.AddPosition(pos1)
	p.AddPosition(pos2)
	p.AddPosition(pos3)

	// Closed position should not be included
	pos4 := &Position{
		KeyID:         "key-4",
		Chain:         ChainSolana,
		TokenAddress:  "token-4",
		TokenSymbol:   "TOKEN4",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN4"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		TakeProfitSet: NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}
	_ = p.AddPosition(pos4)
	// Now close it - update after adding
	update := PositionUpdate{
		PositionID: pos4.ID,
		Status:     PositionStatusClosed,
	}
	_ = p.UpdatePosition(update)

	needingAttention := p.GetPositionsNeedingAttention()
	if len(needingAttention) != 2 {
		t.Errorf("expected 2 positions needing attention, got %d", len(needingAttention))
	}
}

func TestPortfolioSaveAndLoad(t *testing.T) {
	p := NewPortfolio()

	pos := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "TOKEN1",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "TOKEN1"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
		Notes:         "Test position",
	}

	p.AddPosition(pos)

	// Save
	tmpPath := t.TempDir() + "/portfolio.json"
	err := p.Save(tmpPath)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load
	p2, err := LoadPortfolio(tmpPath)
	if err != nil {
		t.Fatalf("LoadPortfolio failed: %v", err)
	}

	// Verify
	loadedPositions := p2.ListPositions()
	if len(loadedPositions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(loadedPositions))
	}

	if loadedPositions[0].TokenSymbol != "TOKEN1" {
		t.Errorf("token symbol mismatch: expected TOKEN1, got %s", loadedPositions[0].TokenSymbol)
	}

	if loadedPositions[0].Notes != "Test position" {
		t.Errorf("notes mismatch: expected 'Test position', got %s", loadedPositions[0].Notes)
	}
}

func TestPortfolioCalculateStatistics(t *testing.T) {
	p := NewPortfolio()

	// Add winning position
	winPos := &Position{
		KeyID:         "key-1",
		Chain:         ChainSolana,
		TokenAddress:  "token-1",
		TokenSymbol:   "WIN",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "WIN"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.02, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}
	p.AddPosition(winPos)
	// Close it properly with profit
	err := p.ClosePosition(winPos.ID, NewTokenAmount(0.02, 9, "SOL"), NewTokenAmount(20, 9, "SOL"), "txn-win")
	if err != nil {
		t.Fatalf("failed to close win position: %v", err)
	}

	// Add losing position
	lossPos := &Position{
		KeyID:         "key-2",
		Chain:         ChainSolana,
		TokenAddress:  "token-2",
		TokenSymbol:   "LOSS",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "LOSS"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.005, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}
	p.AddPosition(lossPos)
	// Close it properly with loss
	err = p.ClosePosition(lossPos.ID, NewTokenAmount(0.005, 9, "SOL"), NewTokenAmount(5, 9, "SOL"), "txn-loss")
	if err != nil {
		t.Fatalf("failed to close loss position: %v", err)
	}

	// Add open position
	openPos := &Position{
		KeyID:         "key-3",
		Chain:         ChainSolana,
		TokenAddress:  "token-3",
		TokenSymbol:   "OPEN",
		TokenDecimals: 9,
		Amount:        NewTokenAmount(1000, 9, "OPEN"),
		EntryPrice:    NewTokenAmount(0.01, 9, "SOL"),
		CurrentPrice:  NewTokenAmount(0.015, 9, "SOL"),
		Type:          PositionTypeLong,
		TotalInvested: NewTokenAmount(10, 9, "SOL"),
	}
	p.AddPosition(openPos)

	stats := p.CalculateStatistics(ChainSolana)

	// Check totals
	if stats.TotalPositions != 3 {
		t.Errorf("expected 3 total positions, got %d", stats.TotalPositions)
	}

	if stats.OpenPositions != 1 {
		t.Errorf("expected 1 open position, got %d", stats.OpenPositions)
	}

	if stats.ClosedPositions != 2 {
		t.Errorf("expected 2 closed positions, got %d", stats.ClosedPositions)
	}

	// Check win rate
	if stats.WinRate != 50.0 {
		t.Errorf("expected win rate 50.0, got %f", stats.WinRate)
	}

	// Check PnL
	if stats.RealizedPnL == nil {
		t.Error("realized PnL is nil")
	} else {
		// Should be 10 - 5 = 5 profit
		expectedRealized := decimal.NewFromInt(5)
		if !stats.RealizedPnL.Amount.Equal(expectedRealized) {
			t.Errorf("expected realized PnL %s, got %s", expectedRealized, stats.RealizedPnL.Amount)
		}
	}

	// Check best/worst trades
	if stats.BestTrade == nil {
		t.Error("best trade is nil")
	}

	if stats.WorstTrade == nil {
		t.Error("worst trade is nil")
	}
}
