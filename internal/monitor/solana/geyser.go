package solana

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

// GeyserConfig holds configuration for Geyser/GRPC monitoring.
type GeyserConfig struct {
	// Geyser endpoint (if using Geyser plugin)
	Endpoint string

	// Subscription filters
	SubscribeAccounts     bool
	SubscribeTransactions bool
	SubscribeBlocks       bool
	SubscribeSlots        bool

	// Program IDs to monitor
	ProgramIDs []solana.PublicKey

	// Account subscriptions
	AccountsToWatch []solana.PublicKey

	// RPC pool for fallback
	RPCPool *rpc.Pool

	// Connection settings
	ReconnectDelay       time.Duration
	MaxReconnectAttempts int
	ConnectTimeout       time.Duration
}

// GeyserMonitor provides low-latency monitoring using Geyser or GRPC subscriptions.
type GeyserMonitor struct {
	config  GeyserConfig
	logger  *zerolog.Logger
	handler monitor.EventHandler

	ctx    context.Context
	cancel context.CancelFunc

	// State
	isRunning   atomic.Bool
	isConnected atomic.Bool
	stats       monitor.MonitorStats
	statsMu     sync.RWMutex
	startTime   time.Time

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error

	// Connection management (abstracted for multiple implementations)
	conn   geyserConnection
	connMu sync.RWMutex
}

// geyserConnection interface allows for different Geyser implementations.
type geyserConnection interface {
	Connect(ctx context.Context) error
	Disconnect() error
	Subscribe(ctx context.Context, config GeyserConfig) (<-chan GeyserMessage, error)
	IsConnected() bool
}

// GeyserMessage represents a message from Geyser.
type GeyserMessage struct {
	Type      string // "account", "transaction", "block", "slot"
	Timestamp time.Time
	Data      any // Type-specific data
}

// AccountUpdate represents an account update from Geyser.
type AccountUpdate struct {
	Account      solana.PublicKey
	Lamports     uint64
	Data         []byte
	Owner        solana.PublicKey
	Executable   bool
	RentEpoch    uint64
	WriteVersion uint64
	Slot         uint64
}

// TransactionUpdate represents a transaction update from Geyser.
type TransactionUpdate struct {
	Signature solana.Signature
	Slot      uint64
	Success   bool
	Message   *solana.Message
	Logs      []string
}

// NewGeyserMonitor creates a new Geyser monitor.
func NewGeyserMonitor(config GeyserConfig) (*GeyserMonitor, error) {
	if config.RPCPool == nil {
		return nil, errors.New("RPC pool is required for fallback")
	}

	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 5 * time.Second
	}

	if config.MaxReconnectAttempts == 0 {
		config.MaxReconnectAttempts = 10
	}

	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	logger := util.WithComponent("geyser_monitor")

	monitor := &GeyserMonitor{
		config:    config,
		logger:    &logger,
		handler:   nil,
		ctx:       ctx,
		cancel:    cancel,
		startTime: time.Now(),
		eventChan: make(chan *monitor.TokenEvent, 100),
		errorChan: make(chan error, 10),
	}

	// Create appropriate connection type based on endpoint
	if err := monitor.initConnection(); err != nil {
		cancel()
		return nil, err
	}

	return monitor, nil
}

// initConnection creates the appropriate connection type.
func (m *GeyserMonitor) initConnection() error {
	// For now, use WebSocket-based implementation as fallback
	// In production, this would create an actual GRPC client
	m.conn = NewWebSocketGeyserConnection(m.config.Endpoint, m.logger)
	return nil
}

// SetHandler sets the event handler.
func (m *GeyserMonitor) SetHandler(handler monitor.EventHandler) {
	m.handler = handler
}

// Start begins monitoring.
func (m *GeyserMonitor) Start() error {
	if m.isRunning.Load() {
		return errors.New("monitor is already running")
	}

	m.logger.Info().Msg("Starting Geyser monitor")

	m.isRunning.Store(true)
	m.startTime = time.Now()

	// Start monitoring goroutines
	go m.monitorLoop()
	go m.eventProcessingLoop()

	m.logger.Info().Msg("Geyser monitor started")
	return nil
}

// Stop stops the monitor.
func (m *GeyserMonitor) Stop() error {
	m.logger.Info().Msg("Stopping Geyser monitor")

	m.isRunning.Store(false)
	m.cancel()

	m.connMu.Lock()
	if m.conn != nil {
		_ = m.conn.Disconnect()
	}
	m.connMu.Unlock()

	close(m.eventChan)
	close(m.errorChan)

	m.logger.Info().Msg("Geyser monitor stopped")
	return nil
}

// monitorLoop manages the connection and subscriptions.
func (m *GeyserMonitor) monitorLoop() {
	reconnectAttempts := 0
	ticker := time.NewTicker(m.config.ReconnectDelay)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
			if !m.isRunning.Load() {
				return
			}

			// Check if connected
			if !m.checkIsConnected() {
				if reconnectAttempts >= m.config.MaxReconnectAttempts {
					m.logger.Error().
						Int("attempts", reconnectAttempts).
						Msg("Max reconnection attempts reached, stopping")
					return
				}

				m.logger.Info().
					Int("attempt", reconnectAttempts+1).
					Msg("Attempting to connect to Geyser")

				if err := m.connect(); err != nil {
					m.logger.Error().Err(err).
						Int("attempt", reconnectAttempts+1).
						Msg("Connection failed, will retry")
					reconnectAttempts++
					<-ticker.C
					continue
				}

				reconnectAttempts = 0
				m.setIsConnected(true)
			}

			// Subscribe to events
			msgChan, err := m.subscribe()
			if err != nil {
				m.logger.Error().Err(err).Msg("Subscription failed, reconnecting")
				m.setIsConnected(false)
				_ = m.disconnect()
				<-ticker.C
				continue
			}

			// Process messages
			if err := m.processMessages(msgChan); err != nil {
				m.logger.Error().Err(err).Msg("Message processing failed, reconnecting")
				m.setIsConnected(false)
				_ = m.disconnect()
				<-ticker.C
				continue
			}
		}
	}
}

// connect establishes connection to Geyser.
func (m *GeyserMonitor) connect() error {
	ctx, cancel := context.WithTimeout(m.ctx, m.config.ConnectTimeout)
	defer cancel()

	m.connMu.Lock()
	defer m.connMu.Unlock()

	return m.conn.Connect(ctx)
}

// disconnect closes the connection.
func (m *GeyserMonitor) disconnect() error {
	m.connMu.Lock()
	defer m.connMu.Unlock()

	m.setIsConnected(false)
	return m.conn.Disconnect()
}

// checkIsConnected checks if the connection is active.
func (m *GeyserMonitor) checkIsConnected() bool {
	m.connMu.RLock()
	defer m.connMu.RUnlock()

	return m.conn != nil && m.conn.IsConnected()
}

// setIsConnected sets the connection status.
func (m *GeyserMonitor) setIsConnected(connected bool) {
	m.isConnected.Store(connected)
}

// subscribe sets up subscriptions.
func (m *GeyserMonitor) subscribe() (<-chan GeyserMessage, error) {
	m.connMu.Lock()
	defer m.connMu.Unlock()

	return m.conn.Subscribe(m.ctx, m.config)
}

// processMessages processes messages from the subscription.
func (m *GeyserMonitor) processMessages(msgChan <-chan GeyserMessage) error {
	for {
		select {
		case <-m.ctx.Done():
			return nil
		case msg, ok := <-msgChan:
			if !ok {
				return errors.New("message channel closed")
			}

			if err := m.processMessage(msg); err != nil {
				m.logger.Error().Err(err).
					Str("type", msg.Type).
					Msg("Failed to process message")
			}
		}
	}
}

// processMessage processes a single Geyser message.
func (m *GeyserMonitor) processMessage(msg GeyserMessage) error {
	switch msg.Type {
	case "account":
		account, ok := msg.Data.(AccountUpdate)
		if !ok {
			return errors.New("invalid account update type")
		}
		return m.processAccountUpdate(account)

	case "transaction":
		tx, ok := msg.Data.(TransactionUpdate)
		if !ok {
			return errors.New("invalid transaction update type")
		}
		return m.processTransactionUpdate(tx)

	case "block":
		// Block updates - could extract transactions from here
		return nil

	case "slot":
		// Slot updates - useful for tracking chain progress
		return nil

	default:
		m.logger.Debug().Str("type", msg.Type).Msg("Unknown message type")
		return nil
	}
}

// processAccountUpdate processes an account update.
func (m *GeyserMonitor) processAccountUpdate(update AccountUpdate) error {
	// Check if this is an account we're watching
	for _, acc := range m.config.AccountsToWatch {
		if acc.Equals(update.Account) {
			m.logger.Debug().
				Stringer("account", update.Account).
				Uint64("slot", update.Slot).
				Msg("Received watched account update")

			// Could trigger token analysis here
			return nil
		}
	}

	// Check if this is a program account we're interested in
	for _, progID := range m.config.ProgramIDs {
		if update.Owner.Equals(progID) {
			// This is an account owned by a program we're monitoring
			m.logger.Debug().
				Stringer("account", update.Account).
				Stringer("owner", update.Owner).
				Uint64("slot", update.Slot).
				Msg("Received program account update")
		}
	}

	return nil
}

// processTransactionUpdate processes a transaction update.
func (m *GeyserMonitor) processTransactionUpdate(update TransactionUpdate) error {
	if !update.Success {
		return nil
	}

	// Check if transaction involves our programs of interest
	if update.Message == nil {
		return nil
	}

	// Look for instructions that involve our monitored programs
	for _, instruction := range update.Message.Instructions {
		// Check if program ID is one we're monitoring
		for _, progID := range m.config.ProgramIDs {
			if len(update.Message.AccountKeys) > 0 &&
				uint32(instruction.ProgramIDIndex) < uint32(len(update.Message.AccountKeys)) {
				programAccount := update.Message.AccountKeys[instruction.ProgramIDIndex]
				if programAccount.Equals(progID) {
					// Found a transaction for a monitored program
					return m.createTokenEvent(update)
				}
			}
		}
	}

	return nil
}

// createTokenEvent creates a token event from a transaction update.
func (m *GeyserMonitor) createTokenEvent(update TransactionUpdate) error {
	// Extract token information from the transaction
	event := &monitor.TokenEvent{
		ID:        generateEventID(),
		Chain:     monitor.ChainTypeSolana,
		Source:    monitor.SourcePumpFun, // Would be determined by program ID
		Timestamp: time.Now(),
		Signature: update.Signature,
		Slot:      update.Slot,
		IsValid:   true,
	}

	// Parse transaction to extract details
	// This is simplified - production would parse actual instruction data

	atomic.AddInt64(&m.stats.TotalEvents, 1)

	select {
	case m.eventChan <- event:
	default:
		m.logger.Warn().Msg("Event channel full, dropping event")
	}

	return nil
}

// eventProcessingLoop processes events from the event channel.
func (m *GeyserMonitor) eventProcessingLoop() {
	for event := range m.eventChan {
		if m.handler != nil {
			if err := m.handler.HandleTokenEvent(event); err != nil {
				m.logger.Error().Err(err).
					Str("event_id", event.ID).
					Msg("Handler failed to process event")
			} else {
				atomic.AddInt64(&m.stats.ProcessedEvents, 1)
			}
		}
	}
}

// Status returns the current monitor status.
func (m *GeyserMonitor) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "geyser",
		Chain:          monitor.ChainTypeSolana,
		Source:         monitor.SourcePumpFun,
		IsRunning:      m.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&m.stats.TotalEvents),
		ConnectedSince: m.startTime,
	}
}

// Stats returns monitoring statistics.
func (m *GeyserMonitor) Stats() monitor.MonitorStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()

	stats := m.stats
	if !m.startTime.IsZero() {
		stats.Uptime = time.Since(m.startTime)
	}

	return stats
}

// WebSocketGeyserConnection is a WebSocket-based fallback implementation.
type WebSocketGeyserConnection struct {
	endpoint  string
	logger    *zerolog.Logger
	connected atomic.Bool
}

// NewWebSocketGeyserConnection creates a new WebSocket Geyser connection.
func NewWebSocketGeyserConnection(endpoint string, logger *zerolog.Logger) *WebSocketGeyserConnection {
	return &WebSocketGeyserConnection{
		endpoint: endpoint,
		logger:   logger,
	}
}

// Connect establishes WebSocket connection.
func (c *WebSocketGeyserConnection) Connect(ctx context.Context) error {
	// This would implement actual WebSocket connection
	// For now, mark as connected to enable the monitor
	c.connected.Store(true)
	c.logger.Info().Str("endpoint", c.endpoint).
		Msg("WebSocket Geyser connection established (simulated)")
	return nil
}

// Disconnect closes the connection.
func (c *WebSocketGeyserConnection) Disconnect() error {
	c.connected.Store(false)
	c.logger.Info().Msg("WebSocket Geyser connection closed")
	return nil
}

// Subscribe sets up subscriptions.
func (c *WebSocketGeyserConnection) Subscribe(ctx context.Context, config GeyserConfig) (<-chan GeyserMessage, error) {
	msgChan := make(chan GeyserMessage, 100)

	// Start a goroutine to simulate messages (for testing)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				close(msgChan)
				return
			case <-ticker.C:
				if c.connected.Load() {
					// Send a heartbeat message
					msgChan <- GeyserMessage{
						Type:      "slot",
						Timestamp: time.Now(),
					}
				}
			}
		}
	}()

	c.logger.Info().Msg("Subscribed to Geyser updates")
	return msgChan, nil
}

// IsConnected returns connection status.
func (c *WebSocketGeyserConnection) IsConnected() bool {
	return c.connected.Load()
}
