package monitor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

// Dispatcher coordinates events from multiple monitors and routes them to analyzers.
type Dispatcher struct {
	logger *zerolog.Logger
	ctx    context.Context
	cancel context.CancelFunc

	// State
	isRunning atomic.Bool

	// Monitors (chain-agnostic)
	monitors   map[ChainType]map[SourceType]TokenMonitor
	monitorsMu sync.RWMutex

	// Handlers (analyzers)
	handlers   []EventHandler
	handlersMu sync.RWMutex

	// Event filtering
	filter        *TokenFilter
	filterEnabled bool

	// Event channels
	eventChan chan *TokenEvent
	errorChan chan error

	// Statistics
	stats   DispatcherStats
	statsMu sync.RWMutex

	// Latency tracking
	detectionLatencies []time.Duration
	latencyMu          sync.Mutex
	maxLatencySamples  int

	// Deduplication
	recentEvents map[string]time.Time
	dedupeMu     sync.RWMutex
	dedupeWindow time.Duration
}

// TokenMonitor interface for all monitors (Solana and Base).
type TokenMonitor interface {
	// Start begins monitoring
	Start() error

	// Stop stops monitoring
	Stop() error

	// SetHandler sets the event handler
	SetHandler(handler EventHandler)

	// Status returns current status
	Status() MonitorStatus

	// IsRunning returns if monitor is running
	IsRunning() bool
}

// DispatcherStats holds dispatcher statistics.
type DispatcherStats struct {
	TotalEventsReceived    int64            `json:"total_events_received"`
	TotalEventsFiltered    int64            `json:"total_events_filtered"`
	TotalEventsDispatched  int64            `json:"total_events_dispatched"`
	TotalEventsFailed      int64            `json:"total_events_failed"`
	EventsBySource         map[string]int64 `json:"events_by_source"`
	EventsByChain          map[string]int64 `json:"events_by_chain"`
	AverageDispatchLatency time.Duration    `json:"average_dispatch_latency"`
	Uptime                 time.Duration    `json:"uptime"`
}

// DispatcherConfig holds dispatcher configuration.
type DispatcherConfig struct {
	// Event queue size
	EventQueueSize int

	// Enable event deduplication
	EnableDeduplication bool

	// Deduplication time window
	DedupeWindow time.Duration

	// Maximum latency samples to track
	MaxLatencySamples int

	// Filter configuration
	Filter *TokenFilter
}

// NewDispatcher creates a new event dispatcher.
func NewDispatcher(config DispatcherConfig) *Dispatcher {
	if config.EventQueueSize == 0 {
		config.EventQueueSize = 1000
	}

	if config.DedupeWindow == 0 {
		config.DedupeWindow = 5 * time.Second
	}

	if config.MaxLatencySamples == 0 {
		config.MaxLatencySamples = 1000
	}

	ctx, cancel := context.WithCancel(context.Background())

	logger := util.WithComponent("dispatcher")

	dispatcher := &Dispatcher{
		logger:            &logger,
		ctx:               ctx,
		cancel:            cancel,
		monitors:          make(map[ChainType]map[SourceType]TokenMonitor),
		handlers:          make([]EventHandler, 0),
		eventChan:         make(chan *TokenEvent, config.EventQueueSize),
		errorChan:         make(chan error, 100),
		filter:            config.Filter,
		filterEnabled:     config.Filter != nil,
		recentEvents:      make(map[string]time.Time),
		dedupeWindow:      config.DedupeWindow,
		maxLatencySamples: config.MaxLatencySamples,
		stats: DispatcherStats{
			EventsBySource: make(map[string]int64),
			EventsByChain:  make(map[string]int64),
		},
	}

	return dispatcher
}

// RegisterMonitor registers a monitor with the dispatcher.
func (d *Dispatcher) RegisterMonitor(monitor TokenMonitor) error {
	d.monitorsMu.Lock()
	defer d.monitorsMu.Unlock()

	// Get monitor status to determine chain and source
	status := monitor.Status()

	if d.monitors[status.Chain] == nil {
		d.monitors[status.Chain] = make(map[SourceType]TokenMonitor)
	}

	// Check if monitor already registered
	if _, exists := d.monitors[status.Chain][status.Source]; exists {
		return fmt.Errorf("monitor already registered for chain %s source %s",
			status.Chain, status.Source)
	}

	// Set the dispatcher as the handler
	monitor.SetHandler(d)

	// Register the monitor
	d.monitors[status.Chain][status.Source] = monitor

	d.logger.Info().
		Str("chain", string(status.Chain)).
		Str("source", string(status.Source)).
		Str("name", status.Name).
		Msg("Registered monitor")

	return nil
}

// UnregisterMonitor unregisters a monitor.
func (d *Dispatcher) UnregisterMonitor(chain ChainType, source SourceType) error {
	d.monitorsMu.Lock()
	defer d.monitorsMu.Unlock()

	if d.monitors[chain] == nil {
		return fmt.Errorf("no monitors registered for chain %s", chain)
	}

	monitor, exists := d.monitors[chain][source]
	if !exists {
		return fmt.Errorf("no monitor registered for chain %s source %s", chain, source)
	}

	// Stop the monitor
	if monitor.IsRunning() {
		if err := monitor.Stop(); err != nil {
			d.logger.Error().Err(err).
				Str("chain", string(chain)).
				Str("source", string(source)).
				Msg("Failed to stop monitor during unregister")
		}
	}

	delete(d.monitors[chain], source)

	d.logger.Info().
		Str("chain", string(chain)).
		Str("source", string(source)).
		Msg("Unregistered monitor")

	return nil
}

// AddHandler adds an event handler (analyzer).
func (d *Dispatcher) AddHandler(handler EventHandler) {
	d.handlersMu.Lock()
	defer d.handlersMu.Unlock()

	d.handlers = append(d.handlers, handler)

	d.logger.Info().
		Int("handler_count", len(d.handlers)).
		Msg("Added event handler")
}

// RemoveHandler removes an event handler.
func (d *Dispatcher) RemoveHandler(handler EventHandler) {
	d.handlersMu.Lock()
	defer d.handlersMu.Unlock()

	for i, h := range d.handlers {
		if h == handler {
			d.handlers = append(d.handlers[:i], d.handlers[i+1:]...)
			d.logger.Info().
				Int("handler_count", len(d.handlers)).
				Msg("Removed event handler")
			return
		}
	}
}

// Start starts the dispatcher and all registered monitors.
func (d *Dispatcher) Start() error {
	if d.isRunning.Load() {
		return errors.New("dispatcher is already running")
	}

	d.logger.Info().Msg("Starting dispatcher")

	// Start event processing loop
	go d.eventLoop()

	// Start error handling loop
	go d.errorLoop()

	// Start deduplication cleanup
	go d.dedupeCleanupLoop()

	// Start all registered monitors
	d.monitorsMu.Lock()
	for chain, monitors := range d.monitors {
		for source, monitor := range monitors {
			if err := monitor.Start(); err != nil {
				d.logger.Error().Err(err).
					Str("chain", string(chain)).
					Str("source", string(source)).
					Msg("Failed to start monitor")

				// Stop any monitors that were started
				d.monitorsMu.Unlock()
				_ = d.Stop()
				return fmt.Errorf("failed to start monitor %s/%s: %w", chain, source, err)
			}

			d.logger.Info().
				Str("chain", string(chain)).
				Str("source", string(source)).
				Msg("Started monitor")
		}
	}
	d.monitorsMu.Unlock()

	d.isRunning.Store(true)
	d.logger.Info().Msg("Dispatcher started")

	return nil
}

// Stop stops the dispatcher and all monitors.
func (d *Dispatcher) Stop() error {
	d.logger.Info().Msg("Stopping dispatcher")

	d.isRunning.Store(false)
	d.cancel()

	// Stop all monitors
	d.monitorsMu.Lock()
	for chain, monitors := range d.monitors {
		for source, monitor := range monitors {
			if monitor.IsRunning() {
				if err := monitor.Stop(); err != nil {
					d.logger.Error().Err(err).
						Str("chain", string(chain)).
						Str("source", string(source)).
						Msg("Failed to stop monitor")
				}
			}
		}
	}
	d.monitorsMu.Unlock()

	// Close channels
	close(d.eventChan)
	close(d.errorChan)

	d.logger.Info().Msg("Dispatcher stopped")
	return nil
}

// HandleTokenEvent implements EventHandler interface for monitors to call.
func (d *Dispatcher) HandleTokenEvent(event *TokenEvent) error {
	if !d.isRunning.Load() {
		return errors.New("dispatcher is not running")
	}

	atomic.AddInt64(&d.stats.TotalEventsReceived, 1)

	// Track detection latency
	if !event.Timestamp.IsZero() {
		latency := time.Since(event.Timestamp)
		d.trackLatency(latency)
	}

	// Apply deduplication
	if d.isDuplicate(event) {
		atomic.AddInt64(&d.stats.TotalEventsFiltered, 1)
		return nil
	}

	// Apply filter if enabled
	if d.filterEnabled && !d.passesFilter(event) {
		atomic.AddInt64(&d.stats.TotalEventsFiltered, 1)
		d.logger.Debug().
			Str("token_mint", event.MintAddress).
			Str("source", string(event.Source)).
			Msg("Event filtered by dispatcher filter")
		return nil
	}

	// Add to recent events for deduplication
	d.addRecentEvent(event)

	// Send to event channel
	select {
	case d.eventChan <- event:
		return nil
	default:
		atomic.AddInt64(&d.stats.TotalEventsFailed, 1)
		return errors.New("event channel full, dropping event")
	}
}

// OnError implements EventHandler interface for monitors to call.
func (d *Dispatcher) OnError(err error) {
	select {
	case d.errorChan <- err:
	default:
		// Error channel full, log and drop
		d.logger.Error().Err(err).Msg("Error channel full, dropping error")
	}
}

// eventLoop processes events from monitors and dispatches to handlers.
func (d *Dispatcher) eventLoop() {
	for event := range d.eventChan {
		d.dispatchToHandlers(event)
	}
}

// dispatchToHandlers dispatches an event to all registered handlers.
func (d *Dispatcher) dispatchToHandlers(event *TokenEvent) {
	d.handlersMu.RLock()
	handlers := make([]EventHandler, len(d.handlers))
	copy(handlers, d.handlers)
	d.handlersMu.RUnlock()

	if len(handlers) == 0 {
		d.logger.Warn().
			Str("event_id", event.ID).
			Str("token_mint", event.MintAddress).
			Msg("No handlers registered, dropping event")
		return
	}

	// Update stats
	d.statsMu.Lock()
	d.stats.EventsBySource[string(event.Source)]++
	d.stats.EventsByChain[string(event.Chain)]++
	d.statsMu.Unlock()

	// Dispatch to all handlers
	successCount := 0
	for _, handler := range handlers {
		if err := handler.HandleTokenEvent(event); err != nil {
			d.logger.Error().Err(err).
				Str("event_id", event.ID).
				Str("token_mint", event.MintAddress).
				Msg("Handler failed to process event")
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		atomic.AddInt64(&d.stats.TotalEventsDispatched, 1)
	} else {
		atomic.AddInt64(&d.stats.TotalEventsFailed, 1)
	}
}

// errorLoop processes errors from monitors.
func (d *Dispatcher) errorLoop() {
	for err := range d.errorChan {
		d.logger.Error().Err(err).Msg("Monitor error")
	}
}

// dedupeCleanupLoop periodically cleans up old events from deduplication map.
func (d *Dispatcher) dedupeCleanupLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.cleanupOldEvents()
		}
	}
}

// isDuplicate checks if an event is a duplicate.
func (d *Dispatcher) isDuplicate(event *TokenEvent) bool {
	key := d.eventKey(event)

	d.dedupeMu.RLock()
	_, exists := d.recentEvents[key]
	d.dedupeMu.RUnlock()

	return exists
}

// addRecentEvent adds an event to recent events for deduplication.
func (d *Dispatcher) addRecentEvent(event *TokenEvent) {
	key := d.eventKey(event)

	d.dedupeMu.Lock()
	d.recentEvents[key] = time.Now()
	d.dedupeMu.Unlock()
}

// cleanupOldEvents removes old events from the deduplication map.
func (d *Dispatcher) cleanupOldEvents() {
	now := time.Now()
	cutoff := now.Add(-d.dedupeWindow)

	d.dedupeMu.Lock()
	defer d.dedupeMu.Unlock()

	for key, timestamp := range d.recentEvents {
		if timestamp.Before(cutoff) {
			delete(d.recentEvents, key)
		}
	}
}

// eventKey generates a unique key for deduplication.
func (d *Dispatcher) eventKey(event *TokenEvent) string {
	return fmt.Sprintf("%s:%s:%s", event.Chain, event.Source, event.MintAddress)
}

// passesFilter checks if an event passes the filter criteria.
func (d *Dispatcher) passesFilter(event *TokenEvent) bool {
	// Check if mint authority is disabled (required for safety)
	if d.filter.RequireMintDisabled && event.MintAuthority != nil && *event.MintAuthority != "" {
		return false
	}

	// Check if freeze authority is disabled (required for safety)
	if d.filter.RequireFreezeDisabled && event.FreezeAuthority != nil && *event.FreezeAuthority != "" {
		return false
	}

	return true
}

// trackLatency tracks detection latency.
func (d *Dispatcher) trackLatency(latency time.Duration) {
	d.latencyMu.Lock()
	defer d.latencyMu.Unlock()

	d.detectionLatencies = append(d.detectionLatencies, latency)

	if len(d.detectionLatencies) > d.maxLatencySamples {
		d.detectionLatencies = d.detectionLatencies[1:]
	}
}

// Stats returns dispatcher statistics.
func (d *Dispatcher) Stats() DispatcherStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()

	// Calculate average latency
	d.latencyMu.Lock()
	if len(d.detectionLatencies) > 0 {
		var total time.Duration
		for _, lat := range d.detectionLatencies {
			total += lat
		}
		d.stats.AverageDispatchLatency = total / time.Duration(len(d.detectionLatencies))
	}
	d.latencyMu.Unlock()

	return d.stats
}

// GetMonitorStatuses returns status of all registered monitors.
func (d *Dispatcher) GetMonitorStatuses() []MonitorStatus {
	d.monitorsMu.RLock()
	defer d.monitorsMu.RUnlock()

	statuses := make([]MonitorStatus, 0)

	for _, monitors := range d.monitors {
		for _, monitor := range monitors {
			statuses = append(statuses, monitor.Status())
		}
	}

	return statuses
}

// IsRunning returns if the dispatcher is running.
func (d *Dispatcher) IsRunning() bool {
	return d.isRunning.Load()
}
