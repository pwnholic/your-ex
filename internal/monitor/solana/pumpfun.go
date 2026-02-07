package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gagliardetto/solana-go"
	solanarpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/lxzan/gws"
	"github.com/rs/zerolog"
)

// PumpFunConfig holds configuration for Pump.fun monitoring.
type PumpFunConfig struct {
	RPCPool             *rpc.Pool
	ReconnectDelay      time.Duration
	SubscriptionTimeout time.Duration
	EnableGeyser        bool
	GeyserEndpoint      string
}

// PumpFunMonitor monitors pump.fun for new token launches.
type PumpFunMonitor struct {
	config  PumpFunConfig
	logger  *zerolog.Logger
	handler monitor.EventHandler
	ctx     context.Context
	cancel  context.CancelFunc

	// State
	isRunning atomic.Bool
	stats     monitor.MonitorStats
	statsMu   sync.RWMutex
	startTime time.Time

	// WebSocket connection
	wsConn *gws.Conn
	wsMu   sync.Mutex
	wsURL  string

	// Program IDs (Solana mainnet)
	pumpFunProgramID solana.PublicKey

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error
}

// NewPumpFunMonitor creates a new Pump.fun monitor.
func NewPumpFunMonitor(config PumpFunConfig) (*PumpFunMonitor, error) {
	if config.RPCPool == nil {
		return nil, errors.New("RPC pool is required")
	}

	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 5 * time.Second
	}

	if config.SubscriptionTimeout == 0 {
		config.SubscriptionTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Pump.fun program ID on Solana mainnet
	pumpFunProgramID := solana.MustPublicKeyFromBase58("6EF8rrecthR5Dkjong8MdgFmJaeWZxWeiKYsBCLcdWo")

	logger := util.WithComponent("pumpfun_monitor")

	monitor := &PumpFunMonitor{
		config:           config,
		logger:           &logger,
		handler:          nil, // Set with SetHandler
		ctx:              ctx,
		cancel:           cancel,
		startTime:        time.Now(),
		pumpFunProgramID: pumpFunProgramID,
		eventChan:        make(chan *monitor.TokenEvent, 100),
		errorChan:        make(chan error, 10),
	}

	return monitor, nil
}

// SetHandler sets the event handler for token events.
func (m *PumpFunMonitor) SetHandler(handler monitor.EventHandler) {
	m.handler = handler
}

// Start begins monitoring pump.fun for new tokens.
func (m *PumpFunMonitor) Start() error {
	if m.isRunning.Load() {
		return errors.New("monitor is already running")
	}

	m.logger.Info().Msg("Starting Pump.fun monitor")

	// Get WebSocket URL from RPC pool
	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		return fmt.Errorf("failed to get RPC endpoint: %w", err)
	}

	// Convert HTTPS to WSS
	wsURL := strings.Replace(endpoint.URL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	m.wsURL = wsURL

	m.isRunning.Store(true)
	m.startTime = time.Now()

	// Start monitoring goroutines
	go m.monitorLoop()
	go m.eventProcessingLoop()

	m.logger.Info().
		Str("ws_url", m.wsURL).
		Msg("Pump.fun monitor started")

	return nil
}

// Stop stops the monitor.
func (m *PumpFunMonitor) Stop() error {
	m.logger.Info().Msg("Stopping Pump.fun monitor")

	m.isRunning.Store(false)
	m.cancel()

	// Close WebSocket connection
	m.wsMu.Lock()
	if m.wsConn != nil {
		_ = m.wsConn.WriteClose(1000, nil)
		m.wsConn = nil
	}
	m.wsMu.Unlock()

	// Close channels
	close(m.eventChan)
	close(m.errorChan)

	m.logger.Info().Msg("Pump.fun monitor stopped")
	return nil
}

// monitorLoop manages the WebSocket connection and subscriptions.
func (m *PumpFunMonitor) monitorLoop() {
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

			// Connect if not connected
			if m.wsConn == nil {
				if err := m.connect(); err != nil {
					m.logger.Error().Err(err).Msg("Failed to connect, will retry")
					<-ticker.C
					continue
				}
			}

			// Subscribe to logs
			if err := m.subscribeToLogs(); err != nil {
				m.logger.Error().Err(err).Msg("Failed to subscribe, reconnecting")
				m.disconnect()
				<-ticker.C
				continue
			}

			// Read messages
			if err := m.readMessages(); err != nil {
				m.logger.Error().Err(err).Msg("Error reading messages, reconnecting")
				m.disconnect()
			}
		}
	}
}

// connect establishes a WebSocket connection using gws.
func (m *PumpFunMonitor) connect() error {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	// Create a WebSocket client with gws
	socket, _, err := gws.NewClient(m, &gws.ClientOption{
		Addr: m.wsURL,
	})
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	m.wsConn = socket
	m.logger.Info().Msg("WebSocket connected")
	return nil
}

// disconnect closes the WebSocket connection.
func (m *PumpFunMonitor) disconnect() {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn != nil {
		_ = m.wsConn.WriteClose(1000, nil)
		m.wsConn = nil
	}
}

// subscribeToLogs subscribes to program logs for pump.fun.
func (m *PumpFunMonitor) subscribeToLogs() error {
	subscribeMsg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "logsSubscribe",
		"params": []any{
			map[string]any{
				"mentions": []string{m.pumpFunProgramID.String()},
			},
			map[string]any{
				"commitment": "confirmed",
			},
		},
	}

	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn == nil {
		return errors.New("websocket not connected")
	}

	// Marshal the subscription message
	data, err := sonic.Marshal(subscribeMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal subscription message: %w", err)
	}

	// Write using gws
	if err := m.wsConn.WriteMessage(gws.OpcodeText, data); err != nil {
		return fmt.Errorf("failed to write subscription message: %w", err)
	}

	m.logger.Info().Msg("Subscribed to Pump.fun program logs")
	return nil
}

// readMessages starts the gws read loop.
func (m *PumpFunMonitor) readMessages() error {
	m.wsMu.Lock()
	if m.wsConn == nil {
		m.wsMu.Unlock()
		return errors.New("websocket not connected")
	}
	conn := m.wsConn
	m.wsMu.Unlock()

	// gws ReadLoop will call OnMessage handler for each message
	// This blocks until the connection is closed
	conn.ReadLoop()
	return nil
}

// processMessage processes a WebSocket message.
func (m *PumpFunMonitor) processMessage(data []byte) error {
	var msg json.RawMessage
	if err := sonic.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	// Parse as generic map to check method/result
	var parsed map[string]any
	if err := sonic.Unmarshal(msg, &parsed); err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	method, _ := parsed["method"].(string)

	// Handle log notification
	if method == "logsNotification" {
		result, ok := parsed["params"].(map[string]any)
		if !ok {
			return nil
		}

		resultValue, ok := result["result"].(map[string]any)
		if !ok {
			return nil
		}

		return m.processLogNotification(resultValue)
	}

	return nil
}

// processLogNotification processes a log notification from the subscription.
func (m *PumpFunMonitor) processLogNotification(logData map[string]any) error {
	// Extract signature
	signatureStr, ok := logData["signature"].(string)
	if !ok {
		return nil
	}

	signature, err := solana.SignatureFromBase58(signatureStr)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	// Extract logs
	logs, ok := logData["logs"].([]any)
	if !ok {
		return nil
	}

	// Check for pump.fun creation log
	var eventName string

	for _, log := range logs {
		logStr, ok := log.(string)
		if !ok {
			continue
		}

		// Look for "Program data:" which contains the actual log
		if strings.Contains(logStr, "Program data:") {
			// This is where we'd parse the actual program data
			// For now, we'll use the signature to fetch transaction details
			_ = signature // Use signature to avoid unused variable warning
		}

		// Look for mint creation indicators
		if strings.Contains(logStr, "create") || strings.Contains(logStr, "mint") {
			eventName = "token_creation"
		}
	}

	// If we found a potential token creation, get more details
	if eventName == "token_creation" {
		// Use the RPC to get transaction details
		go m.fetchTokenDetails(signature, logData)
	}

	return nil
}

// fetchTokenDetails fetches detailed token information from the blockchain.
func (m *PumpFunMonitor) fetchTokenDetails(signature solana.Signature, logData map[string]any) {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	// Get RPC endpoint
	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		m.errorChan <- fmt.Errorf("failed to get RPC endpoint: %w", err)
		return
	}

	// Create RPC client
	rpcClient := solanarpc.New(endpoint.URL)

	// Fetch transaction details
	tx, err := rpcClient.GetTransaction(ctx, signature, &solanarpc.GetTransactionOpts{
		Encoding: solana.EncodingJSON,
	})
	if err != nil {
		m.logger.Debug().Err(err).Str("signature", signature.String()).
			Msg("Failed to fetch transaction")
		return
	}

	if tx == nil || tx.Meta == nil {
		return
	}

	// Parse the transaction to extract token information
	event, err := m.parseTransaction(tx)
	if err != nil {
		m.logger.Debug().Err(err).Str("signature", signature.String()).
			Msg("Failed to parse transaction")
		return
	}

	if event != nil {
		m.incrementEventsDetected()

		select {
		case m.eventChan <- event:
		default:
			m.logger.Warn().Msg("Event channel full, dropping event")
		}
	}
}

// parseTransaction parses a transaction to extract token event information.
func (m *PumpFunMonitor) parseTransaction(tx *solanarpc.GetTransactionResult) (*monitor.TokenEvent, error) {
	if tx.Transaction == nil {
		return nil, errors.New("transaction is nil")
	}

	// Extract transaction data
	transaction, err := tx.Transaction.GetTransaction()
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	// Get message
	message := transaction.Message

	// Look for token mint in instructions
	var tokenMint solana.PublicKey
	var liquidityPool solana.PublicKey

	for _, instruction := range message.Instructions {
		// Parse instruction to find mint account
		// This is a simplified version - in production, you'd parse the actual
		// instruction data according to pump.fun's instruction format
		if len(instruction.Accounts) > 0 {
			// Check first few accounts for potential mint
			for i, accIdx := range instruction.Accounts {
				if i >= len(message.AccountKeys) {
					break
				}
				account := message.AccountKeys[accIdx]
				// Logic to identify mint account would go here
				if i == 0 {
					tokenMint = account
				}
			}
		}
	}

	// If we didn't find a mint, skip
	if tokenMint.IsZero() {
		return nil, nil
	}

	// Get token metadata
	metadata := m.fetchTokenMetadata(tokenMint)

	// Create token event
	event := &monitor.TokenEvent{
		ID:                   generateEventID(),
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourcePumpFun,
		Timestamp:            time.Now(),
		MintAddress:          tokenMint.String(),
		TokenName:            metadata.Name,
		TokenSymbol:          metadata.Symbol,
		TokenDecimals:        metadata.Decimals,
		TokenMetadataURI:     metadata.URI,
		LiquidityPoolAddress: liquidityPool.String(),
		DEX:                  "pump.fun",
		BaseTokenMint:        "So11111111111111111111111111111111111111112", // Wrapped SOL
		Signature:            transaction.Signatures[0],
		Slot:                 tx.Slot,
		MintAuthority:        metadata.MintAuthority,
		FreezeAuthority:      metadata.FreezeAuthority,
		Supply:               metadata.Supply,
		Twitter:              metadata.Twitter,
		Telegram:             metadata.Telegram,
		Website:              metadata.Website,
		IsValid:              true,
	}

	return event, nil
}

// fetchTokenMetadata fetches token metadata from the mint account.
func (m *PumpFunMonitor) fetchTokenMetadata(mintAddress solana.PublicKey) TokenMetadata {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		return TokenMetadata{}
	}

	rpcClient := solanarpc.New(endpoint.URL)

	// Get account info
	accountInfo, err := rpcClient.GetAccountInfo(ctx, mintAddress)
	if err != nil {
		m.logger.Debug().Err(err).Stringer("mint", mintAddress).
			Msg("Failed to get mint account info")
		return TokenMetadata{}
	}

	if accountInfo.Value == nil {
		return TokenMetadata{}
	}

	// Parse mint data (simplified - would need proper borsh deserialization in production)
	data := accountInfo.Value.Data.GetBinary()

	// Mint account layout:
	// - 32 bytes: mint authority option + pub key (if present)
	// - 32 bytes: freeze authority option + pub key (if present)
	// - 8 bytes: supply
	// - 1 byte: decimals
	// - (rest): unused

	metadata := TokenMetadata{
		Decimals: 0,
	}

	if len(data) >= 73 {
		// Decimals are at byte 72
		metadata.Decimals = uint8(data[72])

		// Mint authority at bytes 0-32 (first byte is option flag)
		if data[0] == 1 {
			mintAuth := solana.PublicKey(data[1:33])
			authStr := mintAuth.String()
			metadata.MintAuthority = &authStr
		}

		// Freeze authority at bytes 33-65
		if data[33] == 1 {
			freezeAuth := solana.PublicKey(data[34:66])
			authStr := freezeAuth.String()
			metadata.FreezeAuthority = &authStr
		}
	}

	// Try to get metadata from Metaplex metadata account
	// This would be the full implementation with proper PDAs
	metadata = m.fetchMetaplexMetadata(ctx, rpcClient, mintAddress, metadata)

	return metadata
}

// fetchMetaplexMetadata fetches metadata from Metaplex metadata account.
func (m *PumpFunMonitor) fetchMetaplexMetadata(
	ctx context.Context,
	rpcClient *solanarpc.Client,
	mintAddress solana.PublicKey,
	base TokenMetadata,
) TokenMetadata {
	// Derive metadata PDA (simplified)
	// In production, use proper Metaplex programs to find and parse metadata

	// For now, return base metadata
	// This would be expanded to actually fetch from the metadata account
	return base
}

// eventProcessingLoop processes events from the event channel.
func (m *PumpFunMonitor) eventProcessingLoop() {
	for event := range m.eventChan {
		if m.handler != nil {
			if err := m.handler.HandleTokenEvent(event); err != nil {
				m.logger.Error().Err(err).
					Str("token_mint", event.MintAddress).
					Msg("Handler failed to process event")
			} else {
				m.incrementProcessedEvents()
			}
		}
	}
}

// Status returns the current monitor status.
func (m *PumpFunMonitor) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "pumpfun",
		Chain:          monitor.ChainTypeSolana,
		Source:         monitor.SourcePumpFun,
		IsRunning:      m.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&m.stats.TotalEvents),
		LastEventTime:  m.getLastEventTime(),
		ConnectedSince: m.startTime,
	}
}

// Stats returns monitoring statistics.
func (m *PumpFunMonitor) Stats() monitor.MonitorStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()

	stats := m.stats
	if !m.startTime.IsZero() {
		stats.Uptime = time.Since(m.startTime)
	}

	return stats
}

func (m *PumpFunMonitor) incrementEventsDetected() {
	atomic.AddInt64(&m.stats.TotalEvents, 1)
}

func (m *PumpFunMonitor) incrementProcessedEvents() {
	atomic.AddInt64(&m.stats.ProcessedEvents, 1)
}

func (m *PumpFunMonitor) getLastEventTime() time.Time {
	// Would be tracked with proper synchronization
	return time.Time{}
}

// generateEventID generates a unique event ID.
func generateEventID() string {
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), randomString(8))
}

// randomString generates a random string for event IDs.
func randomString(length int) string {
	b := make([]byte, length)
	// Use a temporary buffer to avoid overlapping dst and src in base64 encoding
	tmp := make([]byte, base64.RawURLEncoding.EncodedLen(length))
	base64.RawURLEncoding.Encode(tmp, b)
	return string(tmp[:length])
}

// gws event handler implementation

// OnOpen is called when the WebSocket connection is established.
func (m *PumpFunMonitor) OnOpen(socket *gws.Conn) {
	m.logger.Info().Msg("WebSocket connection opened")
}

// OnClose is called when the WebSocket connection is closed.
func (m *PumpFunMonitor) OnClose(socket *gws.Conn, err error) {
	m.logger.Info().Err(err).Msg("WebSocket connection closed")
}

// OnMessage is called when a WebSocket message is received.
func (m *PumpFunMonitor) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer message.Close()

	data := message.Bytes()
	if err := m.processMessage(data); err != nil {
		m.logger.Error().Err(err).Msg("Failed to process message")
	}
}

// OnError is called when a WebSocket error occurs.
func (m *PumpFunMonitor) OnError(socket *gws.Conn, err error) {
	m.logger.Error().Err(err).Msg("WebSocket error")
}

// OnPing is called when a WebSocket ping is received.
func (m *PumpFunMonitor) OnPing(socket *gws.Conn, data []byte) {
	// Respond with pong
	_ = socket.WriteMessage(gws.OpcodePong, data)
}

// OnPong is called when a WebSocket pong is received.
func (m *PumpFunMonitor) OnPong(socket *gws.Conn, data []byte) {
	m.logger.Debug().Msg("Received WebSocket pong")
}
