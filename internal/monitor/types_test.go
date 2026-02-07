package monitor

import (
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/lilwiggy/bot/internal/wallet"
	"github.com/stretchr/testify/assert"
)

func TestTokenEventValidation(t *testing.T) {
	tests := []struct {
		name     string
		event    TokenEvent
		expected bool
	}{
		{
			name: "valid event",
			event: TokenEvent{
				ID:          "test-1",
				Chain:       ChainTypeSolana,
				Source:      SourcePumpFun,
				Timestamp:   time.Now(),
				MintAddress: "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU",
				IsValid:     true,
			},
			expected: true,
		},
		{
			name: "invalid event - empty mint",
			event: TokenEvent{
				ID:        "test-2",
				Chain:     ChainTypeSolana,
				Source:    SourcePumpFun,
				Timestamp: time.Now(),
				IsValid:   false,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.event.IsValid)
		})
	}
}

func TestTokenFilter(t *testing.T) {
	tests := []struct {
		name   string
		event  TokenEvent
		filter TokenFilter
		pass   bool
	}{
		{
			name: "mint authority disabled - pass",
			event: TokenEvent{
				MintAddress:     "test-mint",
				MintAuthority:   nil,
				FreezeAuthority: nil,
			},
			filter: TokenFilter{
				RequireMintDisabled:   true,
				RequireFreezeDisabled: true,
			},
			pass: true,
		},
		{
			name: "mint authority enabled - fail",
			event: TokenEvent{
				MintAddress: "test-mint",
				MintAuthority: func() *string {
					s := "some-authority"
					return &s
				}(),
				FreezeAuthority: nil,
			},
			filter: TokenFilter{
				RequireMintDisabled:   true,
				RequireFreezeDisabled: true,
			},
			pass: false,
		},
		{
			name: "freeze authority enabled - fail",
			event: TokenEvent{
				MintAddress:   "test-mint",
				MintAuthority: nil,
				FreezeAuthority: func() *string {
					s := "some-authority"
					return &s
				}(),
			},
			filter: TokenFilter{
				RequireMintDisabled:   true,
				RequireFreezeDisabled: true,
			},
			pass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatcher := &Dispatcher{
				filter:        &tt.filter,
				filterEnabled: true,
			}
			result := dispatcher.passesFilter(&tt.event)
			assert.Equal(t, tt.pass, result)
		})
	}
}

func TestMonitorStatus(t *testing.T) {
	status := MonitorStatus{
		Name:           "test-monitor",
		Chain:          ChainTypeSolana,
		Source:         SourcePumpFun,
		IsRunning:      true,
		EventsDetected: 100,
		ConnectedSince: time.Now(),
	}

	assert.Equal(t, "test-monitor", status.Name)
	assert.Equal(t, ChainTypeSolana, status.Chain)
	assert.Equal(t, SourcePumpFun, status.Source)
	assert.True(t, status.IsRunning)
	assert.Equal(t, int64(100), status.EventsDetected)
}

func TestMonitorStats(t *testing.T) {
	stats := MonitorStats{
		TotalEvents:     1000,
		FilteredEvents:  100,
		ProcessedEvents: 900,
		PeopleReady:     900,
		Uptime:          1 * time.Hour,
	}

	assert.Equal(t, int64(1000), stats.TotalEvents)
	assert.Equal(t, int64(100), stats.FilteredEvents)
	assert.Equal(t, int64(900), stats.ProcessedEvents)
	assert.Equal(t, int64(900), stats.PeopleReady)
	assert.Equal(t, 1*time.Hour, stats.Uptime)
}

func TestPositionUpdate(t *testing.T) {
	update := PositionUpdate{
		KeyID:        "key-1",
		Chain:        wallet.ChainSolana,
		TokenAddress: "test-token",
		TokenSymbol:  "TEST",
		Amount:       "1000",
		PriceUSD:     "0.01",
		ValueUSD:     "10",
		PnL:          "5",
		PnLPercent:   50.0,
		Timestamp:    time.Now(),
		Reason:       "buy",
		Metadata: map[string]string{
			"dex": "pump.fun",
		},
	}

	assert.Equal(t, "key-1", update.KeyID)
	assert.Equal(t, wallet.ChainSolana, update.Chain)
	assert.Equal(t, "test-token", update.TokenAddress)
	assert.Equal(t, "TEST", update.TokenSymbol)
	assert.Equal(t, "1000", update.Amount)
	assert.Equal(t, "buy", update.Reason)
	assert.Equal(t, "pump.fun", update.Metadata["dex"])
}

func TestChainTypeConstants(t *testing.T) {
	assert.Equal(t, ChainTypeSolana, ChainType("solana"))
	assert.Equal(t, ChainTypeBase, ChainType("base"))
}

func TestSourceTypeConstants(t *testing.T) {
	assert.Equal(t, SourcePumpFun, SourceType("pump_fun"))
	assert.Equal(t, SourceRaydium, SourceType("raydium"))
	assert.Equal(t, SourceOrca, SourceType("orca"))
	assert.Equal(t, SourceUniswap, SourceType("uniswap"))
}

func TestTokenEventWithSignature(t *testing.T) {
	signature := solana.MustSignatureFromBase58(
		"5j7s6NiJS3JAkvgkoc18WVAsiSaci2pxB2A6ueCJP4tprA2u2ZSu4f7zn3yiCgE9KctDSYRrMvPZNjGscvHdDRvD",
	)

	event := TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourceRaydium,
		Timestamp:   time.Now(),
		MintAddress: "7xKXtg2CW87d97TXJSDpbD5jBkheTqA83TZRuJosgAsU",
		Signature:   signature,
		Slot:        12345,
		IsValid:     true,
	}

	assert.Equal(t, signature, event.Signature)
	assert.Equal(t, uint64(12345), event.Slot)
}

// MockEventHandler for testing.
type MockEventHandler struct {
	eventsReceived []*TokenEvent
	errors         []error
}

func (m *MockEventHandler) HandleTokenEvent(event *TokenEvent) error {
	m.eventsReceived = append(m.eventsReceived, event)
	return nil
}

func (m *MockEventHandler) OnError(err error) {
	m.errors = append(m.errors, err)
}

func TestMockEventHandler(t *testing.T) {
	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
		errors:         make([]error, 0),
	}

	event := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint",
		IsValid:     true,
	}

	err := handler.HandleTokenEvent(event)
	assert.NoError(t, err)
	assert.Len(t, handler.eventsReceived, 1)
	assert.Equal(t, event, handler.eventsReceived[0])

	testErr := assert.AnError
	handler.OnError(testErr)
	assert.Len(t, handler.errors, 1)
}
