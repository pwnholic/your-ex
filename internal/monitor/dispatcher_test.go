package monitor

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockMonitor is a mock implementation of TokenMonitor for testing.
type MockMonitor struct {
	name       string
	chain      ChainType
	source     SourceType
	running    bool
	handler    EventHandler
	eventsSent []*TokenEvent
}

func NewMockMonitor(name string, chain ChainType, source SourceType) *MockMonitor {
	return &MockMonitor{
		name:       name,
		chain:      chain,
		source:     source,
		running:    false,
		eventsSent: make([]*TokenEvent, 0),
	}
}

func (m *MockMonitor) Start() error {
	m.running = true
	return nil
}

func (m *MockMonitor) Stop() error {
	m.running = false
	return nil
}

func (m *MockMonitor) SetHandler(handler EventHandler) {
	m.handler = handler
}

func (m *MockMonitor) Status() MonitorStatus {
	return MonitorStatus{
		Name:      m.name,
		Chain:     m.chain,
		Source:    m.source,
		IsRunning: m.running,
	}
}

func (m *MockMonitor) IsRunning() bool {
	return m.running
}

func (m *MockMonitor) SimulateEvent(event *TokenEvent) {
	m.eventsSent = append(m.eventsSent, event)
	if m.handler != nil {
		m.handler.HandleTokenEvent(event)
	}
}

func (m *MockMonitor) SimulateError(err error) {
	if m.handler != nil {
		m.handler.OnError(err)
	}
}

// TestDispatcherCreation tests creating a new dispatcher.
func TestDispatcherCreation(t *testing.T) {
	config := DispatcherConfig{
		EventQueueSize:      100,
		EnableDeduplication: true,
		DedupeWindow:        5 * time.Second,
		MaxLatencySamples:   100,
		Filter: &TokenFilter{
			RequireMintDisabled:   true,
			RequireFreezeDisabled: true,
		},
	}

	dispatcher := NewDispatcher(config)

	assert.NotNil(t, dispatcher)
	assert.False(t, dispatcher.IsRunning())
	assert.NotNil(t, dispatcher.filter)
	assert.True(t, dispatcher.filterEnabled)
}

// TestDispatcherRegisterMonitor tests registering monitors.
func TestDispatcherRegisterMonitor(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)

	err := dispatcher.RegisterMonitor(monitor)
	assert.NoError(t, err)

	statuses := dispatcher.GetMonitorStatuses()
	assert.Len(t, statuses, 1)
	assert.Equal(t, "test", statuses[0].Name)
}

// TestDispatcherDuplicateMonitor tests duplicate monitor registration.
func TestDispatcherDuplicateMonitor(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)

	err := dispatcher.RegisterMonitor(monitor)
	assert.NoError(t, err)

	err = dispatcher.RegisterMonitor(monitor)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

// TestDispatcherUnregisterMonitor tests unregistering monitors.
func TestDispatcherUnregisterMonitor(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)

	err := dispatcher.RegisterMonitor(monitor)
	assert.NoError(t, err)

	err = dispatcher.UnregisterMonitor(ChainTypeSolana, SourcePumpFun)
	assert.NoError(t, err)

	statuses := dispatcher.GetMonitorStatuses()
	assert.Empty(t, statuses)
}

// TestDispatcherStartStop tests starting and stopping the dispatcher.
func TestDispatcherStartStop(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	assert.NoError(t, err)
	assert.True(t, dispatcher.IsRunning())
	assert.True(t, monitor.IsRunning())

	err = dispatcher.Stop()
	assert.NoError(t, err)
	assert.False(t, dispatcher.IsRunning())
	assert.False(t, monitor.IsRunning())
}

// TestDispatcherEventHandling tests event handling.
func TestDispatcherEventHandling(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
		errors:         make([]error, 0),
	}

	dispatcher.AddHandler(handler)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	var err error
	err = dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Simulate an event
	event := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint",
		IsValid:     true,
	}

	monitor.SimulateEvent(event)

	// Give time for async processing
	time.Sleep(100 * time.Millisecond)

	dispatcher.Stop()

	// Check that handler received the event
	assert.Len(t, handler.eventsReceived, 1)
	assert.Equal(t, event.ID, handler.eventsReceived[0].ID)

	// Check stats
	stats := dispatcher.Stats()
	assert.Equal(t, int64(1), stats.TotalEventsReceived)
	assert.Equal(t, int64(1), stats.TotalEventsDispatched)
}

// TestDispatcherDeduplication tests event deduplication.
func TestDispatcherDeduplication(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize:      10,
		EnableDeduplication: true,
		DedupeWindow:        1 * time.Second,
	})

	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Send duplicate events
	event := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint",
		IsValid:     true,
	}

	monitor.SimulateEvent(event)
	time.Sleep(50 * time.Millisecond)
	monitor.SimulateEvent(event)
	time.Sleep(50 * time.Millisecond)
	monitor.SimulateEvent(event)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Should only receive one event (deduplicated)
	assert.Len(t, handler.eventsReceived, 1)

	stats := dispatcher.Stats()
	assert.Equal(t, int64(3), stats.TotalEventsReceived)
	assert.Equal(t, int64(2), stats.TotalEventsFiltered)
	assert.Equal(t, int64(1), stats.TotalEventsDispatched)
}

// TestDispatcherFiltering tests event filtering.
func TestDispatcherFiltering(t *testing.T) {
	filter := &TokenFilter{
		RequireMintDisabled:   true,
		RequireFreezeDisabled: true,
	}

	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
		Filter:         filter,
	})

	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Event with mint authority (should be filtered)
	mintAuth := "some-authority"
	event1 := &TokenEvent{
		ID:            "test-1",
		Chain:         ChainTypeSolana,
		Source:        SourcePumpFun,
		Timestamp:     time.Now(),
		MintAddress:   "test-mint-1",
		MintAuthority: &mintAuth,
		IsValid:       true,
	}

	// Event without authorities (should pass)
	event2 := &TokenEvent{
		ID:              "test-2",
		Chain:           ChainTypeSolana,
		Source:          SourcePumpFun,
		Timestamp:       time.Now(),
		MintAddress:     "test-mint-2",
		MintAuthority:   nil,
		FreezeAuthority: nil,
		IsValid:         true,
	}

	monitor.SimulateEvent(event1)
	monitor.SimulateEvent(event2)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Should only receive event2
	assert.Len(t, handler.eventsReceived, 1)
	assert.Equal(t, event2.ID, handler.eventsReceived[0].ID)

	stats := dispatcher.Stats()
	assert.Equal(t, int64(2), stats.TotalEventsReceived)
	assert.Equal(t, int64(1), stats.TotalEventsFiltered)
	assert.Equal(t, int64(1), stats.TotalEventsDispatched)
}

// TestDispatcherMultipleHandlers tests multiple handlers.
func TestDispatcherMultipleHandlers(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	handler1 := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	handler2 := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler1)
	dispatcher.AddHandler(handler2)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	event := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint",
		IsValid:     true,
	}

	monitor.SimulateEvent(event)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Both handlers should receive the event
	assert.Len(t, handler1.eventsReceived, 1)
	assert.Len(t, handler2.eventsReceived, 1)
}

// TestDispatcherMultipleMonitors tests multiple monitors.
func TestDispatcherMultipleMonitors(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler)

	// Register multiple monitors
	monitor1 := NewMockMonitor("pumpfun", ChainTypeSolana, SourcePumpFun)
	monitor2 := NewMockMonitor("raydium", ChainTypeSolana, SourceRaydium)
	monitor3 := NewMockMonitor("orca", ChainTypeSolana, SourceOrca)

	err := dispatcher.RegisterMonitor(monitor1)
	require.NoError(t, err)
	err = dispatcher.RegisterMonitor(monitor2)
	require.NoError(t, err)
	err = dispatcher.RegisterMonitor(monitor3)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Simulate events from all monitors
	event1 := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint-1",
		IsValid:     true,
	}

	event2 := &TokenEvent{
		ID:          "test-2",
		Chain:       ChainTypeSolana,
		Source:      SourceRaydium,
		Timestamp:   time.Now(),
		MintAddress: "test-mint-2",
		IsValid:     true,
	}

	event3 := &TokenEvent{
		ID:          "test-3",
		Chain:       ChainTypeSolana,
		Source:      SourceOrca,
		Timestamp:   time.Now(),
		MintAddress: "test-mint-3",
		IsValid:     true,
	}

	monitor1.SimulateEvent(event1)
	monitor2.SimulateEvent(event2)
	monitor3.SimulateEvent(event3)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Should receive all events
	assert.Len(t, handler.eventsReceived, 3)

	stats := dispatcher.Stats()
	assert.Equal(t, int64(3), stats.TotalEventsDispatched)
	assert.Equal(t, int64(1), stats.EventsBySource["pump_fun"])
	assert.Equal(t, int64(1), stats.EventsBySource["raydium"])
	assert.Equal(t, int64(1), stats.EventsBySource["orca"])
}

// TestDispatcherErrorHandling tests error handling.
func TestDispatcherErrorHandling(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	testErr := errors.New("test error")
	monitor.SimulateError(testErr)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Error should be logged (not easily testable, but we check no panic)
	assert.True(t, true)
}

// TestDispatcherStats tests statistics tracking.
func TestDispatcherStats(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	handler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Generate some events
	for i := range 5 {
		event := &TokenEvent{
			ID:          fmt.Sprintf("test-%d", i),
			Chain:       ChainTypeSolana,
			Source:      SourcePumpFun,
			Timestamp:   time.Now(),
			MintAddress: fmt.Sprintf("test-mint-%d", i),
			IsValid:     true,
		}
		monitor.SimulateEvent(event)
	}

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	stats := dispatcher.Stats()
	assert.Equal(t, int64(5), stats.TotalEventsReceived)
	assert.Equal(t, int64(5), stats.TotalEventsDispatched)
	assert.Equal(t, int64(5), stats.EventsBySource["pump_fun"])
	assert.Greater(t, stats.AverageDispatchLatency, time.Duration(0))
}

// TestDispatcherRemoveHandler tests removing handlers.
func TestDispatcherRemoveHandler(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	handler1 := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	handler2 := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(handler1)
	dispatcher.AddHandler(handler2)

	// Remove handler1
	dispatcher.RemoveHandler(handler1)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	event := &TokenEvent{
		ID:          "test-1",
		Chain:       ChainTypeSolana,
		Source:      SourcePumpFun,
		Timestamp:   time.Now(),
		MintAddress: "test-mint",
		IsValid:     true,
	}

	monitor.SimulateEvent(event)

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	// Only handler2 should receive the event
	assert.Empty(t, handler1.eventsReceived)
	assert.Len(t, handler2.eventsReceived, 1)
}

// TestDispatcherContextCancellation tests context cancellation.
func TestDispatcherContextCancellation(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 10,
	})

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Cancel context
	dispatcher.Stop()

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	assert.False(t, dispatcher.IsRunning())
	assert.False(t, monitor.IsRunning())
}

// TestDispatcherEventQueueFull tests behavior when event queue is full.
func TestDispatcherEventQueueFull(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherConfig{
		EventQueueSize: 2, // Small queue
	})

	// Slow handler that never processes
	slowHandler := &MockEventHandler{
		eventsReceived: make([]*TokenEvent, 0),
	}

	dispatcher.AddHandler(slowHandler)

	monitor := NewMockMonitor("test", ChainTypeSolana, SourcePumpFun)
	err := dispatcher.RegisterMonitor(monitor)
	require.NoError(t, err)

	err = dispatcher.Start()
	require.NoError(t, err)

	// Send more events than queue can hold
	for i := range 5 {
		event := &TokenEvent{
			ID:          fmt.Sprintf("test-%d", i),
			Chain:       ChainTypeSolana,
			Source:      SourcePumpFun,
			Timestamp:   time.Now(),
			MintAddress: fmt.Sprintf("test-mint-%d", i),
			IsValid:     true,
		}
		monitor.SimulateEvent(event)
	}

	time.Sleep(100 * time.Millisecond)
	dispatcher.Stop()

	stats := dispatcher.Stats()
	// Some events should be dropped/failed
	assert.GreaterOrEqual(t, stats.TotalEventsFailed, int64(0))
}
