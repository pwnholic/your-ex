package solana

import (
	"context"
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

// OrcaConfig holds configuration for Orca monitoring.
type OrcaConfig struct {
	RPCPool             *rpc.Pool
	ReconnectDelay      time.Duration
	SubscriptionTimeout time.Duration
}

// OrcaMonitor monitors Orca whirlpools for new liquidity pools.
type OrcaMonitor struct {
	config  OrcaConfig
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

	// Program IDs
	orcaWhirlpoolProgramID solana.PublicKey

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error
}

// NewOrcaMonitor creates a new Orca monitor.
func NewOrcaMonitor(config OrcaConfig) (*OrcaMonitor, error) {
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

	// Orca Whirlpool program ID on Solana mainnet
	orcaWhirlpoolProgramID := solana.MustPublicKeyFromBase58("whirLbMiicVdio4qvUfM5KAg6Ct8VwpYzGff3uctyCc")

	logger := util.WithComponent("orca_monitor")

	monitor := &OrcaMonitor{
		config:                 config,
		logger:                 &logger,
		handler:                nil,
		ctx:                    ctx,
		cancel:                 cancel,
		startTime:              time.Now(),
		orcaWhirlpoolProgramID: orcaWhirlpoolProgramID,
		eventChan:              make(chan *monitor.TokenEvent, 100),
		errorChan:              make(chan error, 10),
	}

	return monitor, nil
}

// SetHandler sets the event handler for token events.
func (m *OrcaMonitor) SetHandler(handler monitor.EventHandler) {
	m.handler = handler
}

// Start begins monitoring Orca for new pools.
func (m *OrcaMonitor) Start() error {
	if m.isRunning.Load() {
		return errors.New("monitor is already running")
	}

	m.logger.Info().Msg("Starting Orca monitor")

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
		Msg("Orca monitor started")

	return nil
}

// Stop stops the monitor.
func (m *OrcaMonitor) Stop() error {
	m.logger.Info().Msg("Stopping Orca monitor")

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

	m.logger.Info().Msg("Orca monitor stopped")
	return nil
}

// monitorLoop manages the WebSocket connection and subscriptions.
func (m *OrcaMonitor) monitorLoop() {
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

			// Subscribe to logs for Orca program
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

// connect establishes a WebSocket connection.
func (m *OrcaMonitor) connect() error {
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
func (m *OrcaMonitor) disconnect() {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn != nil {
		_ = m.wsConn.WriteClose(1000, nil)
		m.wsConn = nil
	}
}

// subscribeToLogs subscribes to program logs for Orca.
func (m *OrcaMonitor) subscribeToLogs() error {
	subscribeMsg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "logsSubscribe",
		"params": []any{
			map[string]any{
				"mentions": []string{m.orcaWhirlpoolProgramID.String()},
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

	m.logger.Info().Msg("Subscribed to Orca program logs")
	return nil
}

// readMessages starts the gws read loop.
func (m *OrcaMonitor) readMessages() error {
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
func (m *OrcaMonitor) processMessage(data []byte) error {
	var msg json.RawMessage
	if err := sonic.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	var parsed map[string]any
	if err := sonic.Unmarshal(msg, &parsed); err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	method, _ := parsed["method"].(string)

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

// processLogNotification processes a log notification.
func (m *OrcaMonitor) processLogNotification(logData map[string]any) error {
	signatureStr, ok := logData["signature"].(string)
	if !ok {
		return nil
	}

	signature, err := solana.SignatureFromBase58(signatureStr)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	logs, ok := logData["logs"].([]any)
	if !ok {
		return nil
	}

	// Check for whirlpool creation logs
	var isWhirlpoolCreation bool

	for _, log := range logs {
		logStr, ok := log.(string)
		if !ok {
			continue
		}

		// Orca whirlpool creation indicators
		if strings.Contains(logStr, "initialize_whirlpool") ||
			strings.Contains(logStr, "InitWhirlpool") ||
			strings.Contains(logStr, "whirlpool") {
			isWhirlpoolCreation = true
		}
	}

	if isWhirlpoolCreation {
		// Fetch transaction details asynchronously
		go m.fetchWhirlpoolDetails(signature, logData)
	}

	return nil
}

// fetchWhirlpoolDetails fetches detailed whirlpool information.
func (m *OrcaMonitor) fetchWhirlpoolDetails(signature solana.Signature, logData map[string]any) {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		m.errorChan <- fmt.Errorf("failed to get RPC endpoint: %w", err)
		return
	}

	rpcClient := solanarpc.New(endpoint.URL)

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

	// Parse the transaction
	event, err := m.parseWhirlpoolCreationTransaction(tx)
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

// parseWhirlpoolCreationTransaction parses a whirlpool creation transaction.
func (m *OrcaMonitor) parseWhirlpoolCreationTransaction(
	tx *solanarpc.GetTransactionResult,
) (*monitor.TokenEvent, error) {
	if tx.Transaction == nil {
		return nil, errors.New("transaction is nil")
	}

	transaction, err := tx.Transaction.GetTransaction()
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction: %w", err)
	}

	message := transaction.Message

	// Look for whirlpool creation instruction
	var tokenMintA, tokenMintB, whirlpoolAddress solana.PublicKey

	for _, instruction := range message.Instructions {
		// Parse instruction to find whirlpool and mint accounts
		// Orca whirlpool creation typically has:
		// - Whirlpool account
		// - Token A mint
		// - Token B mint
		// - Tick array accounts
		if len(instruction.Accounts) >= 8 {
			if len(instruction.Accounts) > 0 && int(instruction.Accounts[0]) < len(message.AccountKeys) {
				whirlpoolAddress = message.AccountKeys[instruction.Accounts[0]]
			}
			if len(instruction.Accounts) > 1 && int(instruction.Accounts[1]) < len(message.AccountKeys) {
				tokenMintA = message.AccountKeys[instruction.Accounts[1]]
			}
			if len(instruction.Accounts) > 2 && int(instruction.Accounts[2]) < len(message.AccountKeys) {
				tokenMintB = message.AccountKeys[instruction.Accounts[2]]
			}
		}
	}

	// Check if we found a new whirlpool with a new token
	if whirlpoolAddress.IsZero() || tokenMintA.IsZero() {
		return nil, nil
	}

	// Determine which token is new (not SOL or USDC)
	wrappedSol := solana.MustPublicKeyFromBase58("So11111111111111111111111111111111111111112")
	usdc := solana.MustPublicKeyFromBase58("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")

	var newTokenMint solana.PublicKey
	var baseToken string

	if !tokenMintA.Equals(wrappedSol) && !tokenMintA.Equals(usdc) {
		newTokenMint = tokenMintA
		baseToken = tokenMintB.String()
	} else if !tokenMintB.Equals(wrappedSol) && !tokenMintB.Equals(usdc) {
		newTokenMint = tokenMintB
		baseToken = tokenMintA.String()
	} else {
		// Both are base tokens, not a new token launch
		return nil, nil
	}

	// Get token metadata
	metadata := m.fetchTokenMetadata(newTokenMint)

	event := &monitor.TokenEvent{
		ID:                   generateEventID(),
		Chain:                monitor.ChainTypeSolana,
		Source:               monitor.SourceOrca,
		Timestamp:            time.Now(),
		MintAddress:          newTokenMint.String(),
		TokenName:            metadata.Name,
		TokenSymbol:          metadata.Symbol,
		TokenDecimals:        metadata.Decimals,
		TokenMetadataURI:     metadata.URI,
		LiquidityPoolAddress: whirlpoolAddress.String(),
		DEX:                  "orca",
		BaseTokenMint:        baseToken,
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
func (m *OrcaMonitor) fetchTokenMetadata(mintAddress solana.PublicKey) TokenMetadata {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		return TokenMetadata{}
	}

	rpcClient := solanarpc.New(endpoint.URL)

	accountInfo, err := rpcClient.GetAccountInfo(ctx, mintAddress)
	if err != nil {
		m.logger.Debug().Err(err).Stringer("mint", mintAddress).
			Msg("Failed to get mint account info")
		return TokenMetadata{}
	}

	if accountInfo.Value == nil {
		return TokenMetadata{}
	}

	data := accountInfo.Value.Data.GetBinary()

	metadata := TokenMetadata{
		Decimals: 0,
	}

	if len(data) >= 73 {
		metadata.Decimals = uint8(data[72])

		if data[0] == 1 {
			mintAuth := solana.PublicKey(data[1:33])
			authStr := mintAuth.String()
			metadata.MintAuthority = &authStr
		}

		if data[33] == 1 {
			freezeAuth := solana.PublicKey(data[34:66])
			authStr := freezeAuth.String()
			metadata.FreezeAuthority = &authStr
		}
	}

	return metadata
}

// eventProcessingLoop processes events from the event channel.
func (m *OrcaMonitor) eventProcessingLoop() {
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
func (m *OrcaMonitor) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "orca",
		Chain:          monitor.ChainTypeSolana,
		Source:         monitor.SourceOrca,
		IsRunning:      m.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&m.stats.TotalEvents),
		ConnectedSince: m.startTime,
	}
}

// Stats returns monitoring statistics.
func (m *OrcaMonitor) Stats() monitor.MonitorStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()

	stats := m.stats
	if !m.startTime.IsZero() {
		stats.Uptime = time.Since(m.startTime)
	}

	return stats
}

func (m *OrcaMonitor) incrementEventsDetected() {
	atomic.AddInt64(&m.stats.TotalEvents, 1)
}

func (m *OrcaMonitor) incrementProcessedEvents() {
	atomic.AddInt64(&m.stats.ProcessedEvents, 1)
}

// gws event handler implementation

// OnOpen is called when the WebSocket connection is established.
func (m *OrcaMonitor) OnOpen(socket *gws.Conn) {
	m.logger.Info().Msg("WebSocket connection opened")
}

// OnClose is called when the WebSocket connection is closed.
func (m *OrcaMonitor) OnClose(socket *gws.Conn, err error) {
	m.logger.Info().Err(err).Msg("WebSocket connection closed")
}

// OnMessage is called when a WebSocket message is received.
func (m *OrcaMonitor) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer message.Close()

	data := message.Bytes()
	if err := m.processMessage(data); err != nil {
		m.logger.Error().Err(err).Msg("Failed to process message")
	}
}

// OnError is called when a WebSocket error occurs.
func (m *OrcaMonitor) OnError(socket *gws.Conn, err error) {
	m.logger.Error().Err(err).Msg("WebSocket error")
}

// OnPing is called when a WebSocket ping is received.
func (m *OrcaMonitor) OnPing(socket *gws.Conn, data []byte) {
	// Respond with pong
	_ = socket.WriteMessage(gws.OpcodePong, data)
}

// OnPong is called when a WebSocket pong is received.
func (m *OrcaMonitor) OnPong(socket *gws.Conn, data []byte) {
	m.logger.Debug().Msg("Received WebSocket pong")
}
