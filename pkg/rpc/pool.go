package rpc

import (
	"context"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lilwiggy/bot/pkg/util"
	"github.com/rs/zerolog"
)

// ChainType represents the blockchain type.
type ChainType string

const (
	ChainTypeSolana ChainType = "solana"
	ChainTypeBase   ChainType = "base"
	ChainTypeEVM    ChainType = "evm"
)

// EndpointStatus represents the health status of an RPC endpoint.
type EndpointStatus string

const (
	StatusHealthy   EndpointStatus = "healthy"
	StatusDegraded  EndpointStatus = "degraded"
	StatusUnhealthy EndpointStatus = "unhealthy"
)

// Endpoint represents a single RPC endpoint with health tracking.
type Endpoint struct {
	URL          string
	Name         string
	Weight       float64
	Status       EndpointStatus
	Client       *http.Client
	LastCheck    time.Time
	ErrorCount   int64
	RequestCount int64
	AvgLatency   time.Duration
	mu           sync.RWMutex
}

// PoolConfig holds configuration for the RPC pool.
type PoolConfig struct {
	ChainType           ChainType
	Endpoints           []EndpointConfig
	HealthCheckInterval time.Duration
	RequestTimeout      time.Duration
	MaxIdleConns        int
	IdleConnTimeout     time.Duration
}

// EndpointConfig holds configuration for a single endpoint.
type EndpointConfig struct {
	URL    string
	Name   string
	Weight float64
}

// Pool manages a pool of RPC endpoints with load balancing and health checks.
type Pool struct {
	chainType    ChainType
	endpoints    []*Endpoint
	totalWeight  float64
	mu           sync.RWMutex
	config       PoolConfig
	httpClient   *http.Client
	logger       *zerolog.Logger
	healthTicker *time.Ticker
	stopChan     chan struct{}
	healthWg     sync.WaitGroup
}

// NewPool creates a new RPC connection pool.
func NewPool(config PoolConfig) (*Pool, error) {
	if len(config.Endpoints) == 0 {
		return nil, errors.New("at least one endpoint is required")
	}

	if config.HealthCheckInterval <= 0 {
		config.HealthCheckInterval = 30 * time.Second
	}

	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}

	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 100
	}

	if config.IdleConnTimeout <= 0 {
		config.IdleConnTimeout = 90 * time.Second
	}

	logger := util.WithComponent("rpc_pool")
	pool := &Pool{
		chainType:  config.ChainType,
		config:     config,
		httpClient: createHTTPClient(config),
		logger:     &logger,
		stopChan:   make(chan struct{}),
	}

	// Initialize endpoints
	if err := pool.initEndpoints(); err != nil {
		return nil, err
	}

	// Start health check routine
	pool.startHealthChecks()

	return pool, nil
}

// createHTTPClient creates a configured HTTP client.
func createHTTPClient(config PoolConfig) *http.Client {
	return &http.Client{
		Timeout: config.RequestTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          config.MaxIdleConns,
			IdleConnTimeout:       config.IdleConnTimeout,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

// initEndpoints initializes all endpoints in the pool.
func (p *Pool) initEndpoints() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.endpoints = make([]*Endpoint, 0, len(p.config.Endpoints))
	p.totalWeight = 0

	for _, cfg := range p.config.Endpoints {
		if _, err := url.Parse(cfg.URL); err != nil {
			return fmt.Errorf("invalid endpoint URL %s: %w", cfg.URL, err)
		}

		if cfg.Weight <= 0 {
			cfg.Weight = 1.0
		}

		endpoint := &Endpoint{
			URL:    cfg.URL,
			Name:   cfg.Name,
			Weight: cfg.Weight,
			Status: StatusHealthy,
			Client: p.httpClient,
		}

		p.endpoints = append(p.endpoints, endpoint)
		p.totalWeight += cfg.Weight
	}

	return nil
}

// GetEndpoint returns an endpoint using weighted random selection
// It prioritizes healthy endpoints and excludes unhealthy ones.
func (p *Pool) GetEndpoint() (*Endpoint, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.endpoints) == 0 {
		return nil, errors.New("no endpoints available")
	}

	// Filter healthy endpoints
	healthyEndpoints := make([]*Endpoint, 0)
	totalWeight := 0.0

	for _, ep := range p.endpoints {
		ep.mu.RLock()
		if ep.Status == StatusHealthy || ep.Status == StatusDegraded {
			healthyEndpoints = append(healthyEndpoints, ep)
			totalWeight += ep.Weight
		}
		ep.mu.RUnlock()
	}

	if len(healthyEndpoints) == 0 {
		// If no healthy endpoints, return any endpoint as last resort
		return p.endpoints[0], errors.New("all endpoints unhealthy, returning first endpoint")
	}

	// Weighted random selection
	r := mrand.Float64() * totalWeight
	for _, ep := range healthyEndpoints {
		r -= ep.Weight
		if r <= 0 {
			// Increment request count
			atomic.AddInt64(&ep.RequestCount, 1)
			return ep, nil
		}
	}

	// Fallback to last healthy endpoint
	last := healthyEndpoints[len(healthyEndpoints)-1]
	atomic.AddInt64(&last.RequestCount, 1)
	return last, nil
}

// GetEndpointByIndex returns an endpoint at the specified index.
func (p *Pool) GetEndpointByIndex(index int) (*Endpoint, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if index < 0 || index >= len(p.endpoints) {
		return nil, fmt.Errorf("endpoint index out of range: %d", index)
	}

	return p.endpoints[index], nil
}

// Do executes an HTTP request using a pooled endpoint.
func (p *Pool) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	endpoint, err := p.GetEndpoint()
	if err != nil {
		return nil, err
	}

	// Update request URL to use the endpoint
	parsedURL, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint URL: %w", err)
	}

	// Update request with endpoint URL
	req.URL.Scheme = parsedURL.Scheme
	req.URL.Host = parsedURL.Host

	// Execute request with timing
	start := time.Now()
	resp, err := p.httpClient.Do(req.WithContext(ctx))
	latency := time.Since(start)

	// Update endpoint metrics
	endpoint.mu.Lock()
	endpoint.AvgLatency = (endpoint.AvgLatency*time.Duration(endpoint.RequestCount-1) + latency) / time.Duration(
		endpoint.RequestCount,
	)
	endpoint.mu.Unlock()

	// Track errors
	if err != nil {
		atomic.AddInt64(&endpoint.ErrorCount, 1)
		p.updateEndpointHealth(endpoint, false)
	}

	return resp, err
}

// startHealthChecks begins the health check routine.
func (p *Pool) startHealthChecks() {
	p.healthTicker = time.NewTicker(p.config.HealthCheckInterval)

	p.healthWg.Add(1)
	go func() {
		defer p.healthWg.Done()
		for {
			select {
			case <-p.healthTicker.C:
				p.runHealthChecks()
			case <-p.stopChan:
				return
			}
		}
	}()
}

// runHealthChecks checks the health of all endpoints.
func (p *Pool) runHealthChecks() {
	p.mu.RLock()
	endpoints := make([]*Endpoint, len(p.endpoints))
	copy(endpoints, p.endpoints)
	p.mu.RUnlock()

	for _, endpoint := range endpoints {
		go p.checkEndpointHealth(endpoint)
	}
}

// checkEndpointHealth performs a health check on a single endpoint.
func (p *Pool) checkEndpointHealth(endpoint *Endpoint) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a simple health check request
	// For Solana: getHealth, for Base: eth_blockNumber
	var req *http.Request
	var err error

	switch p.chainType {
	case ChainTypeSolana:
		// Solana getHealth request
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, nil)
		if err != nil {
			p.updateEndpointHealth(endpoint, false)
			return
		}
		req.Header.Set("Content-Type", "application/json")
	case ChainTypeBase, ChainTypeEVM:
		// Base/EVM eth_blockNumber request
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, nil)
		if err != nil {
			p.updateEndpointHealth(endpoint, false)
			return
		}
		req.Header.Set("Content-Type", "application/json")
	default:
		p.updateEndpointHealth(endpoint, false)
		return
	}

	start := time.Now()
	resp, err := p.httpClient.Do(req)
	latency := time.Since(start)

	healthy := err == nil && resp != nil && resp.StatusCode < 500

	// Update endpoint status
	endpoint.mu.Lock()
	endpoint.LastCheck = time.Now()
	endpoint.mu.Unlock()

	p.updateEndpointHealth(endpoint, healthy)

	if resp != nil {
		_ = resp.Body.Close()
	}

	// Log health check results
	p.logger.Debug().
		Str("endpoint", endpoint.Name).
		Str("url", endpoint.URL).
		Bool("healthy", healthy).
		Dur("latency", latency).
		Msg("health check completed")
}

// updateEndpointHealth updates the health status of an endpoint.
func (p *Pool) updateEndpointHealth(endpoint *Endpoint, healthy bool) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()

	if healthy {
		endpoint.Status = StatusHealthy
		atomic.AddInt64(&endpoint.ErrorCount, -int64(endpoint.ErrorCount)) // Reset error count
	} else {
		endpoint.Status = StatusUnhealthy
	}
}

// GetStatus returns the current status of all endpoints.
func (p *Pool) GetStatus() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := make(map[string]any)
	endpoints := make([]map[string]any, 0, len(p.endpoints))

	for _, ep := range p.endpoints {
		ep.mu.RLock()
		endpoints = append(endpoints, map[string]any{
			"name":          ep.Name,
			"url":           ep.URL,
			"status":        ep.Status,
			"weight":        ep.Weight,
			"request_count": atomic.LoadInt64(&ep.RequestCount),
			"error_count":   atomic.LoadInt64(&ep.ErrorCount),
			"avg_latency":   ep.AvgLatency.String(),
			"last_check":    ep.LastCheck,
		})
		ep.mu.RUnlock()
	}

	status["chain_type"] = p.chainType
	status["endpoints"] = endpoints
	status["total_endpoints"] = len(p.endpoints)

	return status
}

// Close closes the pool and stops health checks.
func (p *Pool) Close() error {
	p.healthTicker.Stop()
	close(p.stopChan)
	p.healthWg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.endpoints = nil

	p.logger.Info().Msg("rpc pool closed")
	return nil
}

// MarkEndpointUnhealthy manually marks an endpoint as unhealthy.
func (p *Pool) MarkEndpointUnhealthy(name string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, ep := range p.endpoints {
		if ep.Name == name {
			ep.mu.Lock()
			ep.Status = StatusUnhealthy
			ep.mu.Unlock()
			p.logger.Warn().Str("endpoint", name).Msg("endpoint marked unhealthy")
			return nil
		}
	}

	return fmt.Errorf("endpoint not found: %s", name)
}

// MarkEndpointHealthy manually marks an endpoint as healthy.
func (p *Pool) MarkEndpointHealthy(name string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, ep := range p.endpoints {
		if ep.Name == name {
			ep.mu.Lock()
			ep.Status = StatusHealthy
			ep.mu.Unlock()
			p.logger.Info().Str("endpoint", name).Msg("endpoint marked healthy")
			return nil
		}
	}

	return fmt.Errorf("endpoint not found: %s", name)
}
