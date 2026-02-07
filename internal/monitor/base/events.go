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
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/lilwiggy/bot/internal/monitor"
	"github.com/lilwiggy/bot/pkg/rpc"
	"github.com/lilwiggy/bot/pkg/util"
	"github.com/lxzan/gws"
	"github.com/rs/zerolog"
)

// EventsConfig holds configuration for event log subscription.
type EventsConfig struct {
	RPCPool             *rpc.Pool
	FactoryAddress      string
	WSSEndpoint         string
	ReconnectDelay      time.Duration
	SubscriptionTimeout time.Duration
	ConfirmationBlocks  uint64
	BatchSize           uint64
}

// BlockTracker tracks blocks for reorg detection.
type BlockTracker struct {
	blocks      map[uint64]*ethtypes.Header
	blocksMu    sync.RWMutex
	canonical   map[uint64]bool
	canonicalMu sync.RWMutex
	lastBlock   uint64
	reorgDepth  uint64
}

// EventSubscription subscribes to and processes event logs.
type EventSubscription struct {
	config  EventsConfig
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

	// Block tracking
	blockTracker *BlockTracker

	// Reorg handling
	reorgHandler func(reorgStart, reorgEnd uint64)

	// Channels
	eventChan chan *monitor.TokenEvent
	errorChan chan error
}

// NewEventSubscription creates a new event subscription monitor.
func NewEventSubscription(config EventsConfig) (*EventSubscription, error) {
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
		config.ConfirmationBlocks = 2 // 2 blocks for finality on Base
	}

	if config.BatchSize == 0 {
		config.BatchSize = 100 // Process 100 blocks at a time
	}

	ctx, cancel := context.WithCancel(context.Background())

	factoryAddress := common.HexToAddress(config.FactoryAddress)

	logger := util.WithComponent("event_subscription")

	subscription := &EventSubscription{
		config:         config,
		logger:         &logger,
		handler:        nil,
		ctx:            ctx,
		cancel:         cancel,
		startTime:      time.Now(),
		factoryAddress: factoryAddress,
		blockTracker: &BlockTracker{
			blocks:     make(map[uint64]*ethtypes.Header),
			canonical:  make(map[uint64]bool),
			lastBlock:  0,
			reorgDepth: config.ConfirmationBlocks,
		},
		eventChan: make(chan *monitor.TokenEvent, 100),
		errorChan: make(chan error, 10),
	}

	return subscription, nil
}

// SetHandler sets the event handler for token events.
func (s *EventSubscription) SetHandler(handler monitor.EventHandler) {
	s.handler = handler
}

// SetReorgHandler sets the handler for chain reorganizations.
func (s *EventSubscription) SetReorgHandler(handler func(reorgStart, reorgEnd uint64)) {
	s.reorgHandler = handler
}

// Start begins monitoring event logs.
func (s *EventSubscription) Start() error {
	if s.isRunning.Load() {
		return errors.New("event subscription is already running")
	}

	s.logger.Info().Msg("Starting event subscription")

	// Get WebSocket URL from config or RPC pool
	if s.config.WSSEndpoint == "" {
		endpoint, err := s.config.RPCPool.GetEndpoint()
		if err != nil {
			return fmt.Errorf("failed to get RPC endpoint: %w", err)
		}
		s.wsURL = convertToWSS(endpoint.URL)
	} else {
		s.wsURL = s.config.WSSEndpoint
	}

	// Initialize block tracker
	if err := s.initializeBlockTracker(); err != nil {
		return fmt.Errorf("failed to initialize block tracker: %w", err)
	}

	s.isRunning.Store(true)
	s.startTime = time.Now()

	// Start monitoring goroutines
	go s.monitorLoop()
	go s.eventProcessingLoop()
	go s.reorgDetectionLoop()

	s.logger.Info().
		Str("ws_url", s.wsURL).
		Str("factory", s.factoryAddress.Hex()).
		Uint64("confirmations", s.config.ConfirmationBlocks).
		Msg("Event subscription started")

	return nil
}

// Stop stops the event subscription.
func (s *EventSubscription) Stop() error {
	s.logger.Info().Msg("Stopping event subscription")

	s.isRunning.Store(false)
	s.cancel()

	// Close WebSocket connection
	s.wsMu.Lock()
	if s.wsConn != nil {
		_ = s.wsConn.WriteClose(1000, nil)
		s.wsConn = nil
	}
	s.wsMu.Unlock()

	// Close channels
	close(s.eventChan)
	close(s.errorChan)

	s.logger.Info().Msg("Event subscription stopped")
	return nil
}

// initializeBlockTracker initializes the block tracker with current block.
func (s *EventSubscription) initializeBlockTracker() error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	endpoint, err := s.config.RPCPool.GetEndpoint()
	if err != nil {
		return err
	}

	client, err := ethclient.Dial(endpoint.URL)
	if err != nil {
		return fmt.Errorf("failed to dial RPC: %w", err)
	}
	defer client.Close()

	// Get current block number
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get current block: %w", err)
	}

	s.blockTracker.blocksMu.Lock()
	s.blockTracker.lastBlock = header.Number.Uint64()
	s.blockTracker.blocks[header.Number.Uint64()] = header
	s.blockTracker.blocksMu.Unlock()

	s.logger.Info().
		Uint64("current_block", header.Number.Uint64()).
		Msg("Block tracker initialized")

	return nil
}

// monitorLoop manages the WebSocket connection and subscriptions.
func (s *EventSubscription) monitorLoop() {
	ticker := time.NewTicker(s.config.ReconnectDelay)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			if !s.isRunning.Load() {
				return
			}

			// Connect if not connected
			s.wsMu.Lock()
			connected := s.wsConn != nil
			s.wsMu.Unlock()

			if !connected {
				if err := s.connect(); err != nil {
					s.logger.Error().Err(err).Msg("Failed to connect, will retry")
					<-ticker.C
					continue
				}
			}

			// Subscribe to new blocks and logs
			if err := s.subscribe(); err != nil {
				s.logger.Error().Err(err).Msg("Failed to subscribe, reconnecting")
				s.disconnect()
				<-ticker.C
				continue
			}

			// Read messages
			if err := s.readMessages(); err != nil {
				s.logger.Error().Err(err).Msg("Error reading messages, reconnecting")
				s.disconnect()
			}
		}
	}
}

// connect establishes a WebSocket connection using gws.
func (s *EventSubscription) connect() error {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	socket, _, err := gws.NewClient(s, &gws.ClientOption{
		Addr: s.wsURL,
	})
	if err != nil {
		return fmt.Errorf("websocket dial failed: %w", err)
	}

	s.wsConn = socket
	s.logger.Info().Msg("WebSocket connected")
	return nil
}

// disconnect closes the WebSocket connection.
func (s *EventSubscription) disconnect() {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	if s.wsConn != nil {
		_ = s.wsConn.WriteClose(1000, nil)
		s.wsConn = nil
	}
}

// subscribe subscribes to new blocks and factory logs.
func (s *EventSubscription) subscribe() error {
	// Subscribe to new blocks
	newHeadsSubscription := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_subscribe",
		"params": []any{
			"newHeads",
		},
	}

	// Subscribe to logs with filter
	logsFilter := map[string]any{
		"address": s.factoryAddress.Hex(),
		"topics": []any{
			// PairCreated event signature
			"0x783cca1c0412dd0d695e784568c96da2e9c22ff989357a2e8b1d9b2b4e6b7118",
		},
	}

	logsSubscription := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "eth_subscribe",
		"params": []any{
			"logs",
			logsFilter,
		},
	}

	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	if s.wsConn == nil {
		return errors.New("websocket not connected")
	}

	// Send newHeads subscription
	data, err := sonic.Marshal(newHeadsSubscription)
	if err != nil {
		return fmt.Errorf("failed to marshal newHeads subscription: %w", err)
	}
	if err := s.wsConn.WriteMessage(gws.OpcodeText, data); err != nil {
		return fmt.Errorf("failed to write newHeads subscription: %w", err)
	}

	// Send logs subscription
	data, err = sonic.Marshal(logsSubscription)
	if err != nil {
		return fmt.Errorf("failed to marshal logs subscription: %w", err)
	}
	if err := s.wsConn.WriteMessage(gws.OpcodeText, data); err != nil {
		return fmt.Errorf("failed to write logs subscription: %w", err)
	}

	s.logger.Info().Msg("Subscribed to new blocks and factory logs")
	return nil
}

// readMessages starts the gws read loop.
func (s *EventSubscription) readMessages() error {
	s.wsMu.Lock()
	if s.wsConn == nil {
		s.wsMu.Unlock()
		return errors.New("websocket not connected")
	}
	conn := s.wsConn
	s.wsMu.Unlock()

	conn.ReadLoop()
	return nil
}

// OnMessage is called when a WebSocket message is received.
func (s *EventSubscription) OnMessage(socket *gws.Conn, message *gws.Message) {
	defer message.Close()

	data := message.Bytes()
	if err := s.processMessage(data); err != nil {
		s.logger.Error().Err(err).Msg("Failed to process message")
	}
}

// processMessage processes a WebSocket message.
func (s *EventSubscription) processMessage(data []byte) error {
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

		result, ok := params["result"].(map[string]any)
		if !ok {
			return nil
		}

		// Handle new block headers
		if _, ok := result["hash"]; ok {
			return s.processNewBlock(result)
		}

		// Handle log events
		if _, ok := result["address"]; ok {
			return s.processLogEvent(result)
		}
	}

	return nil
}

// processNewBlock processes a new block header and tracks for reorgs.
func (s *EventSubscription) processNewBlock(headerData map[string]any) error {
	// Parse block header
	numberHex, ok := headerData["number"].(string)
	if !ok {
		return nil
	}

	hash, ok := headerData["hash"].(string)
	if !ok {
		return nil
	}

	parentHash, ok := headerData["parentHash"].(string)
	if !ok {
		return nil
	}

	blockNumber := new(big.Int)
	if _, ok := blockNumber.SetString(numberHex[2:], 16); !ok {
		return errors.New("invalid block number")
	}

	// Check for reorg
	s.blockTracker.blocksMu.RLock()
	lastHeader, exists := s.blockTracker.blocks[blockNumber.Uint64()-1]
	s.blockTracker.blocksMu.RUnlock()

	if exists && lastHeader != nil {
		expectedParentHash := lastHeader.Hash().Hex()
		if parentHash != expectedParentHash {
			// Reorg detected!
			s.logger.Warn().
				Uint64("block", blockNumber.Uint64()).
				Str("expected_parent", expectedParentHash).
				Str("actual_parent", parentHash).
				Msg("Chain reorganization detected")

			// Trigger reorg handler
			if s.reorgHandler != nil {
				go s.reorgHandler(blockNumber.Uint64()-s.blockTracker.reorgDepth, blockNumber.Uint64())
			}

			// Update canonical chain
			s.blockTracker.canonicalMu.Lock()
			s.blockTracker.canonical[blockNumber.Uint64()-1] = false
			s.blockTracker.canonical[blockNumber.Uint64()] = false
			s.blockTracker.canonicalMu.Unlock()
		} else {
			// Mark as canonical
			s.blockTracker.canonicalMu.Lock()
			s.blockTracker.canonical[blockNumber.Uint64()] = true
			s.blockTracker.canonicalMu.Unlock()
		}
	}

	// Store block header
	header := &ethtypes.Header{
		Number: blockNumber,
	}
	s.blockTracker.blocksMu.Lock()
	s.blockTracker.blocks[blockNumber.Uint64()] = header
	s.blockTracker.lastBlock = blockNumber.Uint64()
	s.blockTracker.blocksMu.Unlock()

	s.logger.Debug().
		Uint64("block", blockNumber.Uint64()).
		Str("hash", hash).
		Msg("New block tracked")

	return nil
}

// processLogEvent processes a log event from the factory.
func (s *EventSubscription) processLogEvent(logData map[string]any) error {
	// Verify block is canonical before processing
	blockNumberHex, ok := logData["blockNumber"].(string)
	if !ok {
		return nil
	}

	blockNumber := new(big.Int)
	if _, ok := blockNumber.SetString(blockNumberHex[2:], 16); !ok {
		return nil
	}

	s.blockTracker.canonicalMu.RLock()
	isCanonical := s.blockTracker.canonical[blockNumber.Uint64()]
	s.blockTracker.canonicalMu.RUnlock()

	if !isCanonical && blockNumber.Uint64() < s.blockTracker.lastBlock {
		// Skip log from non-canonical block (old reorg)
		s.logger.Debug().
			Uint64("block", blockNumber.Uint64()).
			Msg("Skipping log from non-canonical block")
		return nil
	}

	// Parse PairCreated event
	// This is similar to uniswap.go but with reorg awareness
	// For now, delegate to the Uniswap monitor for actual parsing
	s.logger.Debug().
		Uint64("block", blockNumber.Uint64()).
		Msg("Log event received")

	return nil
}

// reorgDetectionLoop periodically checks for reorgs.
func (s *EventSubscription) reorgDetectionLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.detectReorgs()
		}
	}
}

// detectReorgs actively checks for chain reorganizations.
func (s *EventSubscription) detectReorgs() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	endpoint, err := s.config.RPCPool.GetEndpoint()
	if err != nil {
		return
	}

	client, err := ethclient.Dial(endpoint.URL)
	if err != nil {
		return
	}
	defer client.Close()

	// Check recent blocks
	s.blockTracker.blocksMu.RLock()
	if len(s.blockTracker.blocks) == 0 {
		s.blockTracker.blocksMu.RUnlock()
		return
	}

	// Get the latest tracked block
	latestBlock := s.blockTracker.lastBlock
	startBlock := max(latestBlock-s.blockTracker.reorgDepth, 0)
	s.blockTracker.blocksMu.RUnlock()

	// Fetch blocks from chain and compare
	for i := startBlock; i <= latestBlock; i++ {
		header, err := client.HeaderByNumber(ctx, big.NewInt(int64(i)))
		if err != nil {
			continue
		}

		s.blockTracker.blocksMu.RLock()
		trackedHeader, exists := s.blockTracker.blocks[i]
		s.blockTracker.blocksMu.RUnlock()

		if exists && trackedHeader.Hash() != header.Hash() {
			// Reorg detected!
			s.logger.Warn().
				Uint64("block", i).
				Str("old_hash", trackedHeader.Hash().Hex()).
				Str("new_hash", header.Hash().Hex()).
				Msg("Reorg detected during active check")

			// Update canonical status
			s.blockTracker.canonicalMu.Lock()
			s.blockTracker.canonical[i] = false
			s.blockTracker.canonicalMu.Unlock()

			// Update tracked header
			s.blockTracker.blocksMu.Lock()
			s.blockTracker.blocks[i] = header
			s.blockTracker.blocksMu.Unlock()
		}
	}

	// Clean up old blocks
	s.cleanupOldBlocks()
}

// cleanupOldBlocks removes blocks that are beyond the reorg depth.
func (s *EventSubscription) cleanupOldBlocks() {
	s.blockTracker.blocksMu.Lock()
	defer s.blockTracker.blocksMu.Unlock()

	cutoff := s.blockTracker.lastBlock - (s.blockTracker.reorgDepth * 2) // Keep 2x reorg depth

	for blockNum := range s.blockTracker.blocks {
		if blockNum < cutoff {
			delete(s.blockTracker.blocks, blockNum)
			delete(s.blockTracker.canonical, blockNum)
		}
	}
}

// GetBlockTrackerStatus returns the block tracker status.
func (s *EventSubscription) GetBlockTrackerStatus() map[string]any {
	s.blockTracker.blocksMu.RLock()
	s.blockTracker.canonicalMu.RLock()
	defer s.blockTracker.blocksMu.RUnlock()
	defer s.blockTracker.canonicalMu.RUnlock()

	status := make(map[string]any)
	status["last_block"] = s.blockTracker.lastBlock
	status["tracked_blocks"] = len(s.blockTracker.blocks)
	status["reorg_depth"] = s.blockTracker.reorgDepth
	status["canonical_blocks"] = len(s.blockTracker.canonical)

	return status
}

// Status returns the current monitor status.
func (s *EventSubscription) Status() monitor.MonitorStatus {
	return monitor.MonitorStatus{
		Name:           "events",
		Chain:          monitor.ChainTypeBase,
		Source:         "events",
		IsRunning:      s.isRunning.Load(),
		EventsDetected: atomic.LoadInt64(&s.stats.TotalEvents),
		LastEventTime:  s.getLastEventTime(),
		ConnectedSince: s.startTime,
	}
}

// Stats returns monitoring statistics.
func (s *EventSubscription) Stats() monitor.MonitorStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()

	stats := s.stats
	if !s.startTime.IsZero() {
		stats.Uptime = time.Since(s.startTime)
	}

	return stats
}

func (s *EventSubscription) incrementEventsDetected() {
	atomic.AddInt64(&s.stats.TotalEvents, 1)
}

func (s *EventSubscription) incrementProcessedEvents() {
	atomic.AddInt64(&s.stats.ProcessedEvents, 1)
}

func (s *EventSubscription) getLastEventTime() time.Time {
	return time.Time{}
}

// eventProcessingLoop processes events from the event channel.
func (s *EventSubscription) eventProcessingLoop() {
	for event := range s.eventChan {
		if s.handler != nil {
			if err := s.handler.HandleTokenEvent(event); err != nil {
				s.logger.Error().Err(err).
					Str("token_mint", event.MintAddress).
					Msg("Handler failed to process event")
			} else {
				s.incrementProcessedEvents()
			}
		}
	}
}

// gws event handler implementation

func (s *EventSubscription) OnOpen(socket *gws.Conn) {
	s.logger.Info().Msg("WebSocket connection opened")
}

func (s *EventSubscription) OnClose(socket *gws.Conn, err error) {
	s.logger.Info().Err(err).Msg("WebSocket connection closed")
}

func (s *EventSubscription) OnError(socket *gws.Conn, err error) {
	s.logger.Error().Err(err).Msg("WebSocket error")
}

func (s *EventSubscription) OnPing(socket *gws.Conn, data []byte) {
	_ = socket.WriteMessage(gws.OpcodePong, data)
}

func (s *EventSubscription) OnPong(socket *gws.Conn, data []byte) {
	s.logger.Debug().Msg("Received WebSocket pong")
}
