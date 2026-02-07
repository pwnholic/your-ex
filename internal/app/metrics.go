// Package app provides Prometheus metrics collection
package app

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

// MetricsServer handles Prometheus metrics exposition.
type MetricsServer struct {
	server    *http.Server
	registry  *prometheus.Registry
	logger    *zerolog.Logger
	port      int
	endpoint  string
	startOnce sync.Once
	stopOnce  sync.Once
	startTime time.Time
}

// Metrics holds all bot metrics.
type Metrics struct {
	// Trade metrics
	TradesTotal   *prometheus.CounterVec
	TradesSuccess *prometheus.CounterVec
	TradesFailed  *prometheus.CounterVec
	TradeAmount   *prometheus.HistogramVec
	TradeDuration *prometheus.HistogramVec

	// Monitoring metrics
	EventsDetected  *prometheus.CounterVec
	EventsProcessed *prometheus.CounterVec
	MonitorStatus   *prometheus.GaugeVec

	// System metrics
	BotUptime   prometheus.Gauge
	ErrorsTotal *prometheus.CounterVec
	RPCRequests *prometheus.CounterVec
	RPCLatency  *prometheus.HistogramVec

	// Portfolio metrics
	PortfolioValue prometheus.Gauge
	UnrealizedPnL  prometheus.Gauge
	PositionCount  prometheus.Gauge
}

var (
	metrics     *Metrics
	metricsOnce sync.Once
)

// GetMetrics returns the singleton metrics instance.
func GetMetrics() *Metrics {
	metricsOnce.Do(func() {
		metrics = &Metrics{
			// Trade metrics
			TradesTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_trades_total",
					Help: "Total number of trades executed",
				},
				[]string{"chain", "dex"},
			),
			TradesSuccess: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_trades_success_total",
					Help: "Total number of successful trades",
				},
				[]string{"chain", "dex"},
			),
			TradesFailed: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_trades_failed_total",
					Help: "Total number of failed trades",
				},
				[]string{"chain", "dex", "reason"},
			),
			TradeAmount: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sniper_trade_amount",
					Help:    "Trade amount distribution",
					Buckets: []float64{0.001, 0.01, 0.1, 1, 10, 100},
				},
				[]string{"chain"},
			),
			TradeDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sniper_trade_duration_seconds",
					Help:    "Trade execution duration",
					Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
				},
				[]string{"chain"},
			),

			// Monitoring metrics
			EventsDetected: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_events_detected_total",
					Help: "Total number of token events detected",
				},
				[]string{"chain", "source"},
			),
			EventsProcessed: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_events_processed_total",
					Help: "Total number of events processed",
				},
				[]string{"chain", "source"},
			),
			MonitorStatus: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Name: "sniper_monitor_up",
					Help: "Monitor status (1 = up, 0 = down)",
				},
				[]string{"name", "chain"},
			),

			// System metrics
			BotUptime: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sniper_bot_uptime_seconds",
					Help: "Bot uptime in seconds",
				},
			),
			ErrorsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_errors_total",
					Help: "Total number of errors",
				},
				[]string{"component", "type"},
			),
			RPCRequests: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Name: "sniper_rpc_requests_total",
					Help: "Total number of RPC requests",
				},
				[]string{"chain", "method", "status"},
			),
			RPCLatency: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "sniper_rpc_latency_seconds",
					Help:    "RPC request latency",
					Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
				},
				[]string{"chain", "method"},
			),

			// Portfolio metrics
			PortfolioValue: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sniper_portfolio_value_usd",
					Help: "Total portfolio value in USD",
				},
			),
			UnrealizedPnL: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sniper_portfolio_unrealized_pnl_usd",
					Help: "Unrealized P&L in USD",
				},
			),
			PositionCount: prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name: "sniper_portfolio_position_count",
					Help: "Number of open positions",
				},
			),
		}
	})
	return metrics
}

// NewMetricsServer creates a new metrics server.
func NewMetricsServer(port int, endpoint string) (*MetricsServer, error) {
	registry := prometheus.NewRegistry()
	metrics := GetMetrics()

	// Register all metrics
	registry.MustRegister(
		metrics.TradesTotal,
		metrics.TradesSuccess,
		metrics.TradesFailed,
		metrics.TradeAmount,
		metrics.TradeDuration,
		metrics.EventsDetected,
		metrics.EventsProcessed,
		metrics.MonitorStatus,
		metrics.BotUptime,
		metrics.ErrorsTotal,
		metrics.RPCRequests,
		metrics.RPCLatency,
		metrics.PortfolioValue,
		metrics.UnrealizedPnL,
		metrics.PositionCount,
	)

	mux := http.NewServeMux()
	mux.Handle(endpoint, promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: nil}).With().Str("component", "metrics").Logger()

	return &MetricsServer{
		server:    server,
		registry:  registry,
		logger:    &logger,
		port:      port,
		endpoint:  endpoint,
		startTime: time.Now(),
	}, nil
}

// Start begins serving metrics.
func (m *MetricsServer) Start() error {
	var err error
	m.startOnce.Do(func() {
		m.logger.Info().
			Int("port", m.port).
			Str("endpoint", m.endpoint).
			Msg("Starting metrics server")

		// Start uptime updater
		go m.updateUptime()

		err = m.server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			m.logger.Error().Err(err).Msg("Metrics server error")
		}
	})
	return err
}

// Stop stops the metrics server.
func (m *MetricsServer) Stop() error {
	var err error
	m.stopOnce.Do(func() {
		m.logger.Info().Msg("Stopping metrics server")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = m.server.Shutdown(ctx)
		if err != nil {
			m.logger.Error().Err(err).Msg("Error shutting down metrics server")
		}
	})
	return err
}

// updateUptime periodically updates the uptime metric.
func (m *MetricsServer) updateUptime() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		GetMetrics().BotUptime.Set(time.Since(m.startTime).Seconds())
	}
}

// RecordTrade records a trade execution.
func RecordTrade(chain, dex string, amount float64, duration time.Duration, success bool, reason string) {
	m := GetMetrics()
	m.TradesTotal.WithLabelValues(chain, dex).Inc()
	m.TradeAmount.WithLabelValues(chain).Observe(amount)
	m.TradeDuration.WithLabelValues(chain).Observe(duration.Seconds())

	if success {
		m.TradesSuccess.WithLabelValues(chain, dex).Inc()
	} else {
		m.TradesFailed.WithLabelValues(chain, dex, reason).Inc()
	}
}

// RecordEvent records a detected/processed event.
func RecordEvent(chain, source string, processed bool) {
	m := GetMetrics()
	if processed {
		m.EventsProcessed.WithLabelValues(chain, source).Inc()
	} else {
		m.EventsDetected.WithLabelValues(chain, source).Inc()
	}
}

// RecordError records an error.
func RecordError(component, errorType string) {
	GetMetrics().ErrorsTotal.WithLabelValues(component, errorType).Inc()
}

// RecordRPCRequest records an RPC request.
func RecordRPCRequest(chain, method, status string, latency time.Duration) {
	m := GetMetrics()
	m.RPCRequests.WithLabelValues(chain, method, status).Inc()
	m.RPCLatency.WithLabelValues(chain, method).Observe(latency.Seconds())
}

// UpdateMonitorStatus updates monitor status.
func UpdateMonitorStatus(name, chain string, up bool) {
	value := 0.0
	if up {
		value = 1.0
	}
	GetMetrics().MonitorStatus.WithLabelValues(name, chain).Set(value)
}

// UpdatePortfolio updates portfolio metrics.
func UpdatePortfolio(valueUSD, pnlUSD float64, positionCount int) {
	m := GetMetrics()
	m.PortfolioValue.Set(valueUSD)
	m.UnrealizedPnL.Set(pnlUSD)
	m.PositionCount.Set(float64(positionCount))
}
