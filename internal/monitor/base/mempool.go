package base

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/lxzan/gws"
	"github.com/rs/zerolog"
)

// MempoolConfig holds configuration for mempool monitoring.
type MempoolConfig struct {
	RPCPool             *rpc.Pool
	FactoryAddress      string
	WSSEndpoint         string
	ReconnectDelay      time.Duration
	SubscriptionTimeout time.Duration
}

// PendingTransaction represents a pending transaction in mempool.
type PendingTransaction struct {
	Hash               common.Hash
	From               common.Address
	To                 *common.Address
	Value              *big.Int
	GasPrice           *big.Int
	GasLimit           uint64
	Data               []byte
	Nonce              uint64
	FirstSeen          time.Time
	IsPotentialUniswap bool
}

// MempoolMonitor monitors pending transactions for MEV awareness.
type MempoolMonitor struct {
	config  MempoolConfig
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

	// Factory contract
	factoryAddress common.Address

	// Pending transaction tracking
	pendingTxs   map[common.Hash]*PendingTransaction
	pendingTxsMu sync.RWMutex

	// MEV detection
	susiciousTxs     int64
	frontrunDetected int64

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error
}

// NewMempoolMonitor creates a new mempool monitor.
func NewMempoolMonitor(config MempoolConfig) (*MempoolMonitor, error) {
	if config.RPCPool == nil {
		return nil, errors.New("RPC pool is required")
	}

	if config.FactoryAddress == "" {
		config.FactoryAddress = UniswapV3FactoryMainnet
	}

	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 5 * time.Second
	}

	if config.SubscriptionTimeout == 0 {
		config.SubscriptionTimeout = 30 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())

	factoryAddress := common.HexToAddress(config.FactoryAddress)

	logger := util.WithComponent("mempool_monitor")

	monitor := &MempoolMonitor{
		config:         config,
		logger:         &logger,
		handler:        nil,
		ctx:            ctx,
		cancel:         cancel,
		startTime:      time.Now(),
		factoryAddress: factoryAddress,
		pendingTxs:     make(map[common.Hash]*PendingTransaction),
		eventChan:      make(chan *monitor.TokenEvent, 100),
		errorChan:      make(chan error, 10),
	}

	return monitor, nil
}

// SetHandler sets the event handler for token events.
func (m *MempoolMonitor) SetHandler(handler monitor.EventHandler) {
	m.handler = handler
}

// Start begins monitoring pending transactions.
func (m *MempoolMonitor) Start() error {
	if m.isRunning.Load() {
		return errors.New("monitor is already running")
	}

	m.logger.Info().Msg("Starting mempool monitor")

	// Get WebSocket URL from config or RPC pool
	if m.config.WSSEndpoint == "" {
		endpoint, err := m.config.RPCPool.GetEndpoint()
		if err != nil {
			return fmt.Errorf("failed to get RPC endpoint: %w", err)
		}
		m.wsURL = convertToWSS(endpoint.URL)
	} else {
		m.wsURL = m.config.WSSEndpoint
	}

	m.isRunning.Store(true)
	m.startTime = time.Now()

	// Start monitoring goroutines
	go m.monitorLoop()
	go m.eventProcessingLoop()
	go m.pendingTxCleanup()

	m.logger.Info().
		Str("ws_url", m.wsURL).
		Str("factory", m.factoryAddress.Hex()).
		Msg("Mempool monitor started")

	return nil
}

// Stop stops the monitor.
func (m *MempoolMonitor) Stop() error {
	m.logger.Info().Msg("Stopping mempool monitor")

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

	m.logger.Info().Msg("Mempool monitor stopped")
	return nil
}

// monitorLoop manages the WebSocket connection and subscriptions.
func (m *MempoolMonitor) monitorLoop() {
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
			m.wsMu.Lock()
			connected := m.wsConn != nil
			m.wsMu.Unlock()

			if !connected {
				if err := m.connect(); err != nil {
					m.logger.Error().Err(err).Msg("Failed to connect, will retry")
					<-ticker.C
					continue
				}
			}

			// Subscribe to pending transactions
			if err := m.subscribe(); err != nil {
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
func (m *MempoolMonitor) connect() error {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

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
func (m *MempoolMonitor) disconnect() {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn != nil {
		_ = m.wsConn.WriteClose(1000, nil)
		m.wsConn = nil
	}
}

// subscribe subscribes to pending transactions.
func (m *MempoolMonitor) subscribe() error {
	// Subscribe to pending transactions
	pendingTxSubscription := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_subscribe",
		"params": []any{
			"newPendingTransactions",
		},
	}

	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn == nil {
		return errors.New("websocket not connected")
	}

	// Send pending transaction subscription
	data, err := sonic.Marshal(pendingTxSubscription)
	if err != nil {
		return fmt.Errorf("failed to marshal pending tx subscription: %w", err)
	}
	if err := m.wsConn.WriteMessage(gws.OpcodeText, data); err != nil {
		return fmt.Errorf("failed to write pending tx subscription: %w", err)
	}

	m.logger.Info().Msg("Subscribed to pending transactions")
	return nil
}

// readMessages starts the gws read loop.
func (m *MempoolMonitor) readMessages() error {
	m.wsMu.Lock()
	if m.wsConn == nil {
		m.wsMu.Unlock()
		return errors.New("websocket not connected")
	}
	conn := m.wsConn
	m.wsMu.Unlock()

	conn.ReadLoop()
	return nil
}

// OnMessage is called when a WebSocket message is received.
func (m *MempoolMonitor) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer message.Close()

	data := message.Bytes()
	if err := m.processMessage(data); err != nil {
		m.logger.Error().Err(err).Msg("Failed to process message")
	}
}

// processMessage processes a WebSocket message.
func (m *MempoolMonitor) processMessage(data []byte) error {
	var msg map[string]any
	if err := sonic.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("json unmarshal failed: %w", err)
	}

	method, _ := msg["method"].(string)

	// Handle subscription notification
	if method == "eth_subscription" {
		params, ok := msg["params"].(map[string]any)
		if !ok {
			return nil
		}

		result, ok := params["result"].(string)
		if !ok {
			return nil
		}

		// Process pending transaction hash
		txHash := common.HexToHash(result)
		return m.processPendingTransaction(txHash)
	}

	return nil
}

// processPendingTransaction processes a pending transaction.
func (m *MempoolMonitor) processPendingTransaction(txHash common.Hash) error {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		return fmt.Errorf("failed to get RPC endpoint: %w", err)
	}

	client, err := ethclient.Dial(endpoint.URL)
	if err != nil {
		return fmt.Errorf("failed to dial RPC: %w", err)
	}
	defer client.Close()

	// Get transaction from mempool
	tx, pending, err := client.TransactionByHash(ctx, txHash)
	if err != nil || !pending {
		// Transaction already mined or invalid
		return nil
	}

	// Parse transaction
	pendingTx, err := m.parsePendingTransaction(tx)
	if err != nil {
		return err
	}

	// Check if this is a Uniswap-related transaction
	if m.isUniswapTransaction(pendingTx) {
		pendingTx.IsPotentialUniswap = true

		// Track for MEV awareness
		m.pendingTxsMu.Lock()
		m.pendingTxs[txHash] = pendingTx
		m.pendingTxsMu.Unlock()

		m.logger.Debug().
			Str("tx_hash", txHash.Hex()).
			Str("from", pendingTx.From.Hex()).
			Msg("Detected pending Uniswap transaction")

		// Check for potential frontrun
		m.checkForFrontrun(pendingTx)
	}

	return nil
}

// parsePendingTransaction parses a transaction into a PendingTransaction.
func (m *MempoolMonitor) parsePendingTransaction(tx *types.Transaction) (*PendingTransaction, error) {
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return nil, err
	}

	pendingTx := &PendingTransaction{
		Hash:      tx.Hash(),
		From:      from,
		To:        tx.To(),
		Value:     tx.Value(),
		GasPrice:  tx.GasPrice(),
		GasLimit:  tx.Gas(),
		Data:      tx.Data(),
		Nonce:     tx.Nonce(),
		FirstSeen: time.Now(),
	}

	return pendingTx, nil
}

// isUniswapTransaction checks if a transaction interacts with Uniswap factory.
func (m *MempoolMonitor) isUniswapTransaction(pendingTx *PendingTransaction) bool {
	if pendingTx.To == nil {
		return false
	}

	// Check if transaction is to Uniswap factory
	if pendingTx.To.Cmp(m.factoryAddress) == 0 {
		return true
	}

	// Check if transaction data contains Uniswap-related function selectors
	// Common Uniswap V3 function selectors:
	// - createPool(address,address,uint24): 0x5dee5714
	// - createPair(address,address): 0xc9c65396 (V2)
	if len(pendingTx.Data) >= 4 {
		selector := pendingTx.Data[:4]
		uniswapSelectors := [][]byte{
			hexutil.MustDecode("0x5dee5714"), // createPool V3
			hexutil.MustDecode("0xc9c65396"), // createPair V2
		}

		for _, us := range uniswapSelectors {
			if string(selector) == string(us) {
				return true
			}
		}
	}

	return false
}

// checkForFrontrun checks if this transaction might be frontrunning.
func (m *MempoolMonitor) checkForFrontrun(pendingTx *PendingTransaction) {
	m.pendingTxsMu.RLock()
	defer m.pendingTxsMu.RUnlock()

	// Look for similar transactions in mempool
	for _, existingTx := range m.pendingTxs {
		if existingTx.Hash == pendingTx.Hash {
			continue
		}

		// Check if transactions are similar (same sender, similar gas price)
		if existingTx.From == pendingTx.From {
			// Higher gas price might indicate frontrun attempt
			if pendingTx.GasPrice != nil && existingTx.GasPrice != nil {
				if pendingTx.GasPrice.Cmp(existingTx.GasPrice) > 0 {
					atomic.AddInt64(&m.frontrunDetected, 1)
					m.logger.Warn().
						Str("tx_hash", pendingTx.Hash.Hex()).
						Str("existing_tx", existingTx.Hash.Hex()).
						Msg("Potential frontrun detected")
				}
			}
		}
	}
}

// pendingTxCleanup periodically cleans up old pending transactions.
func (m *MempoolMonitor) pendingTxCleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupOldPendingTxs()
		}
	}
}

// cleanupOldPendingTxs removes transactions that have been in mempool too long.
func (m *MempoolMonitor) cleanupOldPendingTxs() {
	m.pendingTxsMu.Lock()
	defer m.pendingTxsMu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute) // 5 minutes

	for hash, tx := range m.pendingTxs {
		if tx.FirstSeen.Before(cutoff) {
			delete(m.pendingTxs, hash)
		}
	}
}

// GetMempoolState returns the current mempool state.
func (m *MempoolMonitor) GetMempoolState() map[string]any {
	m.pendingTxsMu.RLock()
	defer m.pendingTxsMu.RUnlock()

	state := make(map[string]any)
	state["pending_tx_count"] = len(m.pendingTxs)
	state["frontrun_detected"] = atomic.LoadInt64(&m.frontrunDetected)
	state["suspicious_txs"] = atomic.LoadInt64(&m.susiciousTxs)

	// Include recent pending transactions
	txs := make([]map[string]any, 0, min(len(m.pendingTxs), 10))
	for hash, tx := range m.pendingTxs {
		if len(txs) >= 10 {
			break
		}
		txData := map[string]any{
			"hash":       hash.Hex(),
			"from":       tx.From.Hex(),
			"value":      tx.Value.String(),
			"gas_price":  tx.GasPrice.String(),
			"is_uniswap": tx.IsPotentialUniswap,
			"first_seen": tx.FirstSeen,
		}
		if tx.To != nil {
			txData["to"] = tx.To.Hex()
		} else {
			txData["to"] = ""
		}
		txs = append(txs, txData)
	}
	state["recent_transactions"] = txs

	return state
}

// Status returns the current monitor status.
func (m *MempoolMonitor) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "mempool",
		Chain:          monitor.ChainTypeBase,
		Source:         "mempool",
		IsRunning:      m.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&m.stats.TotalEvents),
		LastEventTime:  m.getLastEventTime(),
		ConnectedSince: m.startTime,
	}
}

// Stats returns monitoring statistics.
func (m *MempoolMonitor) Stats() monitor.MonitorStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()

	stats := m.stats
	if !m.startTime.IsZero() {
		stats.Uptime = time.Since(m.startTime)
	}

	return stats
}

func (m *MempoolMonitor) incrementEventsDetected() {
	atomic.AddInt64(&m.stats.TotalEvents, 1)
}

func (m *MempoolMonitor) incrementProcessedEvents() {
	atomic.AddInt64(&m.stats.ProcessedEvents, 1)
}

func (m *MempoolMonitor) getLastEventTime() time.Time {
	return time.Time{}
}

// eventProcessingLoop processes events from the event channel.
func (m *MempoolMonitor) eventProcessingLoop() {
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

// gws event handler implementation

func (m *MempoolMonitor) OnOpen(socket *gws.Conn) {
	m.logger.Info().Msg("WebSocket connection opened")
}

func (m *MempoolMonitor) OnClose(socket *gws.Conn, err error) {
	m.logger.Info().Err(err).Msg("WebSocket connection closed")
}

func (m *MempoolMonitor) OnError(socket *gws.Conn, err error) {
	m.logger.Error().Err(err).Msg("WebSocket error")
}

func (m *MempoolMonitor) OnPing(socket *gws.Conn, data []byte) {
	_ = socket.WriteMessage(gws.OpcodePong, data)
}

func (m *MempoolMonitor) OnPong(socket *gws.Conn, data []byte) {
	m.logger.Debug().Msg("Received WebSocket pong")
}
