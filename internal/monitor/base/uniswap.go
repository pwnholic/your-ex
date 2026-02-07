package base

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/lxzan/gws"
	"github.com/rs/zerolog"
)

// Uniswap V3/V2 Factory addresses on Base mainnet.
const (
	UniswapV3FactoryMainnet = "0x33128a8fC17869897dcE68Ed026d694621f6FDfD"
	UniswapV2FactoryMainnet = "0x8909Dc15e40173Ff4699343b6eB8132c65e18eC6"
	WETHMainnet             = "0x4200000000000000000000000000000000000006"
	USDCMainnet             = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
)

// UniswapConfig holds configuration for Uniswap monitoring.
type UniswapConfig struct {
	RPCPool             *rpc.Pool
	FactoryAddress      string
	WSSEndpoint         string
	ReconnectDelay      time.Duration
	SubscriptionTimeout time.Duration
	ConfirmationBlocks  uint64
}

// UniswapMonitor monitors Uniswap for new pair/pool creation.
type UniswapMonitor struct {
	config  UniswapConfig
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
	factoryABI     abi.ABI

	// Base token addresses (WETH, USDC)
	baseTokens map[common.Address]string

	// Block confirmation tracking
	pendingBlocks     map[uint64]*ethtypes.Block
	pendingBlocksMu   sync.RWMutex
	finalizedBlocks   map[uint64]bool
	finalizedBlocksMu sync.RWMutex

	// Reorg detection
	lastBlockNumber uint64
	reorgCount      atomic.Int64

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error
}

// NewUniswapMonitor creates a new Uniswap monitor.
func NewUniswapMonitor(config UniswapConfig) (*UniswapMonitor, error) {
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

	if config.ConfirmationBlocks == 0 {
		config.ConfirmationBlocks = 1 // 1 block confirmation for fast sniping
	}

	ctx, cancel := context.WithCancel(context.Background())

	factoryAddress := common.HexToAddress(config.FactoryAddress)

	// Uniswap V3 Factory ABI (PairCreated event)
	factoryABI, err := abi.JSON(strings.NewReader(uniswapV3FactoryABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse factory ABI: %w", err)
	}

	logger := util.WithComponent("uniswap_monitor")

	monitor := &UniswapMonitor{
		config:         config,
		logger:         &logger,
		handler:        nil,
		ctx:            ctx,
		cancel:         cancel,
		startTime:      time.Now(),
		factoryAddress: factoryAddress,
		factoryABI:     factoryABI,
		baseTokens: map[common.Address]string{
			common.HexToAddress(WETHMainnet): "WETH",
			common.HexToAddress(USDCMainnet): "USDC",
		},
		pendingBlocks:   make(map[uint64]*ethtypes.Block),
		finalizedBlocks: make(map[uint64]bool),
		eventChan:       make(chan *monitor.TokenEvent, 100),
		errorChan:       make(chan error, 10),
	}

	return monitor, nil
}

// SetHandler sets the event handler for token events.
func (m *UniswapMonitor) SetHandler(handler monitor.EventHandler) {
	m.handler = handler
}

// Start begins monitoring Uniswap for new pools.
func (m *UniswapMonitor) Start() error {
	if m.isRunning.Load() {
		return errors.New("monitor is already running")
	}

	m.logger.Info().Msg("Starting Uniswap monitor")

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
	go m.confirmationTracker()

	m.logger.Info().
		Str("ws_url", m.wsURL).
		Str("factory", m.factoryAddress.Hex()).
		Uint64("confirmations", m.config.ConfirmationBlocks).
		Msg("Uniswap monitor started")

	return nil
}

// Stop stops the monitor.
func (m *UniswapMonitor) Stop() error {
	m.logger.Info().Msg("Stopping Uniswap monitor")

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

	m.logger.Info().Msg("Uniswap monitor stopped")
	return nil
}

// monitorLoop manages the WebSocket connection and subscriptions.
func (m *UniswapMonitor) monitorLoop() {
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

			// Subscribe to new blocks and logs
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
func (m *UniswapMonitor) connect() error {
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
func (m *UniswapMonitor) disconnect() {
	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn != nil {
		_ = m.wsConn.WriteClose(1000, nil)
		m.wsConn = nil
	}
}

// subscribe subscribes to new blocks and factory logs.
func (m *UniswapMonitor) subscribe() error {
	// Subscribe to new blocks
	newHeadsSubscription := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_subscribe",
		"params": []any{
			"newHeads",
		},
	}

	// Subscribe to factory logs (PairCreated events)
	logsSubscription := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "eth_subscribe",
		"params": []any{
			"logs",
			map[string]any{
				"address": m.factoryAddress.Hex(),
				"topics": []any{
					// PairCreated(uint256,address,address,address,uint256)
					"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118",
				},
			},
		},
	}

	m.wsMu.Lock()
	defer m.wsMu.Unlock()

	if m.wsConn == nil {
		return errors.New("websocket not connected")
	}

	// Send newHeads subscription
	data, err := sonic.Marshal(newHeadsSubscription)
	if err != nil {
		return fmt.Errorf("failed to marshal newHeads subscription: %w", err)
	}
	if err := m.wsConn.WriteMessage(gws.OpcodeText, data); err != nil {
		return fmt.Errorf("failed to write newHeads subscription: %w", err)
	}

	// Send logs subscription
	logData, err := sonic.Marshal(logsSubscription)
	if err != nil {
		return fmt.Errorf("failed to marshal logs subscription: %w", err)
	}
	if err := m.wsConn.WriteMessage(gws.OpcodeText, logData); err != nil {
		return fmt.Errorf("failed to write logs subscription: %w", err)
	}

	m.logger.Info().Msg("Subscribed to new blocks and Uniswap factory logs")
	return nil
}

// readMessages starts the gws read loop.
func (m *UniswapMonitor) readMessages() error {
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
func (m *UniswapMonitor) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer message.Close()

	data := message.Bytes()
	if err := m.processMessage(data); err != nil {
		m.logger.Error().Err(err).Msg("Failed to process message")
	}
}

// processMessage processes a WebSocket message.
func (m *UniswapMonitor) processMessage(data []byte) error {
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

		_, _ = params["subscription"].(string)
		// if !ok {
		// 	return nil
		// }

		result, ok := params["result"].(map[string]any)
		if !ok {
			return nil
		}

		// Handle new block headers
		if _, ok := result["hash"]; ok {
			return m.processNewBlock(result)
		}

		// Handle log events
		if _, ok := result["address"]; ok {
			return m.processLogEvent(result)
		}
	}

	return nil
}

// processNewBlock processes a new block header.
func (m *UniswapMonitor) processNewBlock(header map[string]any) error {
	numberHex, ok := header["number"].(string)
	if !ok {
		return nil
	}

	blockNumber := new(big.Int)
	if _, ok := blockNumber.SetString(numberHex[2:], 16); !ok {
		return errors.New("invalid block number")
	}

	// Check for reorg
	if m.lastBlockNumber > 0 && blockNumber.Uint64() <= m.lastBlockNumber {
		m.reorgCount.Add(1)
		m.logger.Warn().
			Uint64("old_block", m.lastBlockNumber).
			Uint64("new_block", blockNumber.Uint64()).
			Int64("reorg_count", m.reorgCount.Load()).
			Msg("Potential reorg detected")
	}

	m.lastBlockNumber = blockNumber.Uint64()

	m.logger.Debug().
		Uint64("block", blockNumber.Uint64()).
		Msg("New block received")

	return nil
}

// processLogEvent processes a log event from Uniswap factory.
func (m *UniswapMonitor) processLogEvent(logData map[string]any) error {
	// Parse the log to extract PairCreated event data
	event, err := m.parsePairCreatedEvent(logData)
	if err != nil {
		m.logger.Debug().Err(err).Msg("Failed to parse PairCreated event")
		return nil
	}

	if event != nil {
		m.incrementEventsDetected()

		select {
		case m.eventChan <- event:
		default:
			m.logger.Warn().Msg("Event channel full, dropping event")
		}
	}

	return nil
}

// parsePairCreatedEvent parses a PairCreated log event.
func (m *UniswapMonitor) parsePairCreatedEvent(logData map[string]any) (*monitor.TokenEvent, error) {
	// Extract log data
	topics, ok := logData["topics"].([]any)
	if len(topics) == 0 {
		return nil, nil
	}

	topic0Str, ok := topics[0].(string)
	if !ok || topic0Str != "0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118" {
		return nil, nil
	}

	// Parse transaction hash
	_, _ = logData["transactionHash"].(string)

	// Parse block data
	blockNumberHex, _ := logData["blockNumber"].(string)
	blockNumber := new(big.Int)
	blockNumber.SetString(blockNumberHex[2:], 16)

	// Parse log index
	logIndexHex, _ := logData["logIndex"].(string)
	logIndex := new(big.Int)
	logIndex.SetString(logIndexHex[2:], 16)

	// Need at least 4 topics for PairCreated event
	if len(topics) < 4 {
		return nil, errors.New("invalid topics format")
	}
	_ = logIndex // Use to avoid unused variable warning

	// Topic 1: token0 address
	token0Hex, ok := topics[1].(string)
	if !ok {
		return nil, errors.New("invalid token0 address")
	}
	token0 := common.HexToAddress(token0Hex)

	// Topic 2: token1 address
	token1Hex, ok := topics[2].(string)
	if !ok {
		return nil, errors.New("invalid token1 address")
	}
	token1 := common.HexToAddress(token1Hex)

	// Topic 3: pair address
	pairHex, ok := topics[3].(string)
	if !ok {
		return nil, errors.New("invalid pair address")
	}
	pairAddress := common.HexToAddress(pairHex)

	// Determine which token is the new one (not WETH or USDC)
	var newTokenAddress common.Address
	var baseTokenAddress common.Address
	var baseTokenSymbol string

	if _, isBase := m.baseTokens[token0]; isBase {
		newTokenAddress = token1
		baseTokenAddress = token0
		baseTokenSymbol = m.baseTokens[token0]
	} else if _, isBase := m.baseTokens[token1]; isBase {
		newTokenAddress = token0
		baseTokenAddress = token1
		baseTokenSymbol = m.baseTokens[token1]
	} else {
		// Neither token is a base token, skip
		return nil, nil
	}

	// Fetch token metadata for the new token
	tokenMetadata, err := m.fetchTokenMetadata(newTokenAddress)
	if err != nil {
		m.logger.Debug().Err(err).Str("address", newTokenAddress.Hex()).
			Msg("Failed to fetch token metadata")
	}

	// Create token event
	// pairID := fmt.Sprintf("%s-%s", token0.Hex(), token1.Hex()) // Commented to avoid unused variable warning
	event := &monitor.TokenEvent{
		ID:                   generateEventID(),
		Chain:                monitor.ChainTypeBase,
		Source:               monitor.SourceUniswap,
		Timestamp:            time.Now(),
		MintAddress:          newTokenAddress.Hex(),
		TokenName:            tokenMetadata.Name,
		TokenSymbol:          tokenMetadata.Symbol,
		TokenDecimals:        tokenMetadata.Decimals,
		TokenMetadataURI:     tokenMetadata.URI,
		LiquidityPoolAddress: pairAddress.Hex(),
		DEX:                  "uniswap_v3",
		BaseTokenMint:        baseTokenAddress.Hex(),
		InitialPrice:         "", // Would need pool initialization data
		IsValid:              true,
	}

	m.logger.Info().
		Str("token", event.TokenSymbol).
		Str("address", newTokenAddress.Hex()).
		Str("pair", pairAddress.Hex()).
		Str("base_token", baseTokenSymbol).
		Uint64("block", blockNumber.Uint64()).
		Msg("Detected new Uniswap pair")

	return event, nil
}

// fetchTokenMetadata fetches token metadata from the contract.
func (m *UniswapMonitor) fetchTokenMetadata(tokenAddress common.Address) (TokenMetadata, error) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	endpoint, err := m.config.RPCPool.GetEndpoint()
	if err != nil {
		return TokenMetadata{}, err
	}

	client, err := ethclient.Dial(endpoint.URL)
	if err != nil {
		return TokenMetadata{}, fmt.Errorf("failed to dial RPC: %w", err)
	}
	defer client.Close()

	// ERC20 ABI for name, symbol, decimals
	erc20ABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return TokenMetadata{}, fmt.Errorf("failed to parse ERC20 ABI: %w", err)
	}

	metadata := TokenMetadata{
		Name:     "",
		Symbol:   "",
		Decimals: 18, // Default to 18
		URI:      "",
	}

	// Try to call name() function
	nameData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenAddress,
		Data: erc20ABI.Methods["name"].ID,
	}, nil)
	if err == nil && len(nameData) > 0 {
		nameStr, err := erc20ABI.Unpack("name", nameData)
		if err == nil && len(nameStr) > 0 {
			if name, ok := nameStr[0].(string); ok {
				metadata.Name = name
			}
		}
	}

	// Try to call symbol() function
	symbolData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenAddress,
		Data: erc20ABI.Methods["symbol"].ID,
	}, nil)
	if err == nil && len(symbolData) > 0 {
		symbolStr, err := erc20ABI.Unpack("symbol", symbolData)
		if err == nil && len(symbolStr) > 0 {
			if symbol, ok := symbolStr[0].(string); ok {
				metadata.Symbol = symbol
			}
		}
	}

	// Try to call decimals() function
	decimalsData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &tokenAddress,
		Data: erc20ABI.Methods["decimals"].ID,
	}, nil)
	if err == nil && len(decimalsData) > 0 {
		decimalsStr, err := erc20ABI.Unpack("decimals", decimalsData)
		if err == nil && len(decimalsStr) > 0 {
			if decimals, ok := decimalsStr[0].(uint8); ok {
				metadata.Decimals = decimals
			}
		}
	}

	return metadata, nil
}

// confirmationTracker tracks block confirmations and handles reorgs.
func (m *UniswapMonitor) confirmationTracker() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.processConfirmations()
		}
	}
}

// processConfirmations processes block confirmations.
func (m *UniswapMonitor) processConfirmations() {
	m.pendingBlocksMu.Lock()
	defer m.pendingBlocksMu.Unlock()

	// Check if pending blocks have reached confirmation threshold
	currentBlock := m.lastBlockNumber

	for blockNum := range m.pendingBlocks {
		if currentBlock-blockNum >= m.config.ConfirmationBlocks {
			// Block is confirmed
			m.finalizedBlocksMu.Lock()
			m.finalizedBlocks[blockNum] = true
			m.finalizedBlocksMu.Unlock()

			delete(m.pendingBlocks, blockNum)

			m.logger.Debug().
				Uint64("block", blockNum).
				Uint64("confirmations", currentBlock-blockNum).
				Msg("Block confirmed")
		}
	}
}

// Status returns the current monitor status.
func (m *UniswapMonitor) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "uniswap",
		Chain:          monitor.ChainTypeBase,
		Source:         monitor.SourceUniswap,
		IsRunning:      m.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&m.stats.TotalEvents),
		LastEventTime:  m.getLastEventTime(),
		ConnectedSince: m.startTime,
	}
}

// Stats returns monitoring statistics.
func (m *UniswapMonitor) Stats() monitor.MonitorStats {
	m.statsMu.RLock()
	defer m.statsMu.RUnlock()

	stats := m.stats
	if !m.startTime.IsZero() {
		stats.Uptime = time.Since(m.startTime)
	}

	return stats
}

func (m *UniswapMonitor) incrementEventsDetected() {
	atomic.AddInt64(&m.stats.TotalEvents, 1)
}

func (m *UniswapMonitor) incrementProcessedEvents() {
	atomic.AddInt64(&m.stats.ProcessedEvents, 1)
}

func (m *UniswapMonitor) getLastEventTime() time.Time {
	return time.Time{}
}

// gws event handler implementation

func (m *UniswapMonitor) OnOpen(socket *gws.Conn) {
	m.logger.Info().Msg("WebSocket connection opened")
}

func (m *UniswapMonitor) OnClose(socket *gws.Conn, err error) {
	m.logger.Info().Err(err).Msg("WebSocket connection closed")
}

func (m *UniswapMonitor) OnError(socket *gws.Conn, err error) {
	m.logger.Error().Err(err).Msg("WebSocket error")
}

func (m *UniswapMonitor) OnPing(socket *gws.Conn, data []byte) {
	_ = socket.WriteMessage(gws.OpcodePong, data)
}

func (m *UniswapMonitor) OnPong(socket *gws.Conn, data []byte) {
	m.logger.Debug().Msg("Received WebSocket pong")
}

// eventProcessingLoop processes events from the event channel.
func (m *UniswapMonitor) eventProcessingLoop() {
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

// TokenMetadata holds token metadata.
type TokenMetadata struct {
	Name     string
	Symbol   string
	Decimals uint8
	URI      string
}

// convertToWSS converts an HTTP(S) URL to WS(S).
func convertToWSS(url string) string {
	wsURL := strings.Replace(url, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	return wsURL
}

// generateEventID generates a unique event ID.
func generateEventID() string {
	return fmt.Sprintf("base-%d-%s", time.Now().UnixNano(), randomHex(4))
}

// randomHex generates a random hex string.
func randomHex(length int) string {
	b := make([]byte, length/2) // We need length/2 random bytes to get length hex chars
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based random if crypto rand fails
		b = fmt.Appendf(nil, "%x", time.Now().UnixNano())
	}
	result := hex.EncodeToString(b)
	if len(result) > length {
		return result[:length]
	}
	return result
}

// Uniswap V3 Factory ABI (PairCreated event)
// PairCreated(uint256 indexed, address indexed token0, address indexed token1, address pair, uint256).
const uniswapV3FactoryABI = `[
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "internalType": "uint256", "name": "", "type": "uint256"},
			{"indexed": true, "internalType": "address", "name": "token0", "type": "address"},
			{"indexed": true, "internalType": "address", "name": "token1", "type": "address"},
			{"indexed": false, "internalType": "address", "name": "pair", "type": "address"},
			{"indexed": false, "internalType": "uint256", "name": "", "type": "uint256"}
		],
		"name": "PairCreated",
		"type": "event"
	}
]`

// ERC20 ABI for name, symbol, decimals.
const erc20ABI = `[
	{
		"constant": true,
		"inputs": [],
		"name": "name",
		"outputs": [{"name": "", "type": "string"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "symbol",
		"outputs": [{"name": "", "type": "string"}],
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "decimals",
		"outputs": [{"name": "", "type": "uint8"}],
		"type": "function"
	}
]`
