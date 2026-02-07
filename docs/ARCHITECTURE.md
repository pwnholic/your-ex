# Architecture Documentation

Deep dive into the bot's internal architecture and design decisions.

## Table of Contents

- [Design Principles](#design-principles)
- [Component Architecture](#component-architecture)
- [Data Flow](#data-flow)
- [Concurrency Model](#concurrency-model)
- [Error Handling](#error-handling)
- [Performance Considerations](#performance-considerations)
- [Security Architecture](#security-architecture)
- [Testing Strategy](#testing-strategy)

## Design Principles

### 1. Separation of Concerns

```mermaid
graph TB
    subgraph "Layers"
        A[Presentation - CLI]
        B[Application - Bot Orchestration]
        C[Domain - Trading Logic]
        D[Infrastructure - RPC/Wallet]
    end

    A --> B
    B --> C
    C --> D
```

Each layer has specific responsibilities:

- **Presentation**: CLI commands and user interaction
- **Application**: Workflow orchestration
- **Domain**: Business logic (trading, analysis)
- **Infrastructure**: External services (RPC, wallet storage)

### 2. Interface-Based Design

Core components use interfaces for flexibility:

```go
// Monitor interface - all monitors implement this
type Monitor interface {
    SetHandler(handler EventHandler)
    Start() error
    Stop() error
    Status() MonitorStatus
}

// EventHandler interface - handles token events
type EventHandler interface {
    HandleTokenEvent(event *TokenEvent) error
    OnError(err error)
}
```

### 3. Dependency Injection

Components receive dependencies via constructor:

```go
func NewBot(config Config) (*Bot, error) {
    // All dependencies passed in config
    return &Bot{
        walletManager: config.WalletManager,
        monitors:      config.Monitors,
        // ...
    }, nil
}
```

## Component Architecture

### Monitoring Layer

```mermaid
graph LR
    subgraph "Monitoring Layer"
        A[BaseMonitor Interface]
        B[PumpFunMonitor]
        C[RaydiumMonitor]
        D[OrcaMonitor]
        E[UniswapMonitor]

        B --> A
        C --> A
        D --> A
        E --> A
    end

    A --> F[EventHandler]
    F --> G[EventChannel]
    G --> H[EventProcessor]
```

### Monitor Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Stopped
    Stopped --> Starting: Start()
    Starting --> Connecting
    Connecting --> Connected
    Connecting --> Failed: connection error
    Connected --> Monitoring
    Monitoring --> Processing: event received
    Processing --> Monitoring
    Monitoring --> Reconnecting: connection lost
    Reconnecting --> Connected
    Monitoring --> Stopping: Stop()
    Stopping --> Stopped
    Failed --> Stopped
```

### Analysis Pipeline

```mermaid
graph TB
    A[TokenEvent] --> B[TokenAnalyzer]
    B --> C[MetadataFetch]
    B --> D[SecurityAnalyzer]
    B --> E[LiquidityAnalyzer]

    C --> F[TokenMetadata]
    D --> G[SecurityAnalysis]
    E --> H[LiquidityAnalysis]

    F --> I[Scorer]
    G --> I
    H --> I

    I --> J[FinalScore]
    J --> K[TradeDecision]
```

### Trading Layer

```mermaid
graph TB
    subgraph "Decision Making"
        A[StrategyManager]
        B[EntryCriteria]
        C[ExitCriteria]
        D[RiskManager]
    end

    subgraph "Execution"
        E[SolanaExecutor]
        F[BaseExecutor]
        G[MEVProtection]
    end

    A --> B
    A --> C
    A --> D

    B --> E
    B --> F
    C --> E
    C --> F

    E --> G
    F --> G
```

## Data Flow

### Event Processing Flow

```mermaid
sequenceDiagram
    participant WS as WebSocket
    participant Monitor as Monitor
    participant Channel as Event Channel
    participant Processor as Event Processor
    participant Analyzer as Analyzer
    participant Strategy as Strategy
    participant Executor as Executor

    WS->>Monitor: Token Event
    Monitor->>Channel: Queue Event
    Channel->>Processor: Dequeue Event
    Processor->>Analyzer: Analyze Token
    Analyzer-->>Processor: Score + Metadata
    Processor->>Strategy: Evaluate Entry
    Strategy-->>Processor: Trade Decision
    alt Trade Approved
        Processor->>Executor: Execute Trade
        Executor-->>Processor: Transaction Hash
    else Trade Rejected
        Processor->>Processor: Log Rejection
    end
```

### WebSocket Message Handling

```mermaid
graph TB
    A[WebSocket Message] --> B{Message Type}
    B -->|Ping| C[Send Pong]
    B -->|Subscription| D[Handle Subscription]
    B -->|Data| E[Parse Data]
    B -->|Error| F[Handle Error]

    E --> G{Data Type}
    G -->|New Pool| H[Create Token Event]
    G -->|New Block| I[Process Block]
    G -->|Transaction| J[Process Transaction]

    H --> K[Send to Channel]
    I --> K
    J --> K
```

## Concurrency Model

### Goroutine Usage

```mermaid
graph TB
    subgraph "Main Goroutine"
        A[Bot.Start]
    end

    subgraph "Background Goroutines"
        B[Monitor 1]
        C[Monitor 2]
        D[Monitor N]
        E[Event Processor]
        F[Health Checker]
        G[Metrics Server]
    end

    A --> B
    A --> C
    A --> D
    A --> E
    A --> F
    A --> G
```

### Channel Communication

```go
// Event channel - buffered for performance
eventChan chan *TokenEvent

// Error channel - separate from events
errorChan chan error

// Shutdown coordination
shutdownCtx context.Context
shutdownCancel context.CancelFunc
```

### Synchronization

```go
// For connection state
connMu sync.RWMutex
wsConn *gws.Conn

// For metrics
statsMu sync.RWMutex
stats  Metrics

// For cache
cache sync.Map // concurrent map
```

## Error Handling

### Error Handling Strategy

```mermaid
graph TB
    A[Error Occurs] --> B{Error Type}
    B -->|Temporary| C[Retry with Backoff]
    B -->|Permanent| D[Log & Skip]
    B -->|Critical| E[Shutdown Component]

    C --> F{Retry Limit}
    F -->|Not Reached| C
    F -->|Reached| D

    E --> G{Component Type}
    G -->|Monitor| H[Continue without Monitor]
    G -->|Core| I[Shutdown Bot]
```

### Error Categories

| Category       | Examples                              | Handling                       |
| -------------- | ------------------------------------- | ------------------------------ |
| **Network**    | RPC timeout, WebSocket disconnect     | Retry with exponential backoff |
| **Validation** | Invalid token, insufficient liquidity | Skip token, log warning        |
| **Execution**  | Transaction failed, insufficient gas  | Log error, update metrics      |
| **Critical**   | Wallet corrupted, config invalid      | Immediate shutdown             |

### Retry Strategy

```go
// Exponential backoff with jitter
func RetryWithBackoff(
    fn func() error,
    maxAttempts int,
    initialDelay time.Duration,
    maxDelay time.Duration,
) error {
    delay := initialDelay
    for i := 0; i < maxAttempts; i++ {
        if err := fn(); err == nil {
            return nil
        }
        // Add jitter to prevent thundering herd
        jitter := time.Duration(rand.Float64() * float64(delay) * 0.1)
        time.Sleep(delay + jitter)
        delay = time.Duration(float64(delay) * 1.5)
        if delay > maxDelay {
            delay = maxDelay
        }
    }
    return ErrMaxRetriesExceeded
}
```

## Performance Considerations

### Latency Optimization

```mermaid
graph LR
    A[Event Detected] --> B[Quick Validation]
    B --> C[Full Analysis]
    C --> D[Trade Decision]
    D --> E[Transaction Submission]

    style A fill:#90EE90
    style B fill:#FFD700
    style C fill:#FFA500
    style D fill:#FF6347
    style E fill:#FF4500
```

### Optimization Strategies

1. **Parallel Analysis**

   ```go
   // Run analyzers in parallel
   var wg sync.WaitGroup
   errChan := make(chan error, 3)

   wg.Add(3)
   go func() { defer wg.Done(); errChan <- a.fetchMetadata(ctx, event) }()
   go func() { defer wg.Done(); errChan <- a.analyzeSecurity(ctx, event) }()
   go func() { defer wg.Done(); errChan <- a.analyzeLiquidity(ctx, event) }()

   wg.Wait()
   ```

2. **Response Caching**

   ```go
   // Cache expensive RPC calls
   cacheKey := fmt.Sprintf("metadata:%s", mint)
   if cached, ok := cache.Load(cacheKey); ok {
       return cached.(*TokenMetadata), nil
   }
   ```

3. **Connection Pooling**
   ```go
   // Multiple RPC endpoints with load balancing
   type RPCPool struct {
       endpoints []*RPCEndpoint
       currentIndex uint32
   }
   ```

### Memory Management

```go
// Limit event channel size
eventChan := make(chan *TokenEvent, 100)

// Use bounded cache
type BoundedCache struct {
    maxEntries int
    entries    map[string]interface{}
    mu         sync.RWMutex
}

// Periodic cleanup
func (c *BoundedCache) cleanup() {
    if len(c.entries) > c.maxEntries {
        // Remove oldest entries
    }
}
```

## Security Architecture

### Wallet Security

```mermaid
graph TB
    A[Private Key] --> B[Encryption]
    B --> C[AES-256-GCM]
    C --> D[Scrypt KDF]
    D --> E[Encrypted Storage]

    F[Password] --> D
    E --> G[File System]

    style F fill:#FFB6C1
    style G fill:#87CEEB
```

### Encryption Flow

```go
// Key derivation with scrypt
func deriveKey(password string, salt []byte) []byte {
    return scrypt.Key(
        []byte(password),
        salt,
        32768,  // N - CPU/memory cost
        8,      // r - block size
        1,      // p - parallelization
        32,     // key length
    )
}

// AES-256-GCM encryption
func encrypt(key, plaintext []byte) ([]byte, error) {
    block, err := aes.NewCipher(key)
    if err != nil {
        return nil, err
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := rand.Read(nonce); err != nil {
        return nil, err
    }

    return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
```

### MEV Protection

```mermaid
graph TB
    A[Trade Request] --> B{MEV Provider}
    B -->|None| C[Direct Submission]
    B -->|Flashbots| D[Flashbots Protect]
    B -->|Merkle| E[Merkle Private Pool]

    D --> F[Private Mempool]
    E --> F

    F --> G[Miner Inclusion]
    C --> H[Public Mempool]
    H --> G
```

### Security Checks

| Check               | Purpose                       | Implementation             |
| ------------------- | ----------------------------- | -------------------------- |
| **Rug Pull**        | Detect liquidity removal      | Monitor pool ownership     |
| **Honeypot**        | Detect sell restrictions      | Simulate sell transaction  |
| **Tax Check**       | Detect high transaction taxes | Calculate buy/sell taxes   |
| **Liquidity Lock**  | Verify liquidity is locked    | Check lock contracts       |
| **Holder Analysis** | Detect concentrated ownership | Analyze token distribution |

## Testing Strategy

### Test Pyramid

```mermaid
graph TB
    A[Unit Tests - Fast]
    B[Integration Tests - Medium]
    C[E2E Tests - Slow]

    A --> D[70% Coverage]
    B --> E[20% Coverage]
    C --> F[10% Coverage]
```

### Unit Tests

```go
func TestTokenAnalyzer_ScoreCalculation(t *testing.T) {
    analyzer := NewTokenAnalyzer(config)

    tests := []struct {
        name     string
        token    TokenMetadata
        expected float64
    }{
        {
            name: "high quality token",
            token: TokenMetadata{
                Liquidity:  decimal.NewFromFloat(100000),
                Holders:    1000,
                SocialScore: 90,
            },
            expected: 85,
        },
        // ... more test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            score := analyzer.CalculateScore(tt.token)
            assert.InDelta(t, tt.expected, score, 5)
        })
    }
}
```

### Integration Tests

```go
func TestBotLifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    bot := setupTestBot(t)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start bot
    go func() {
        if err := bot.Start(ctx); err != nil {
            t.Errorf("Start failed: %v", err)
        }
    }()

    // Wait for startup
    time.Sleep(500 * time.Millisecond)

    // Verify status
    status := bot.Status()
    assert.True(t, status.IsRunning)

    // Stop bot
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer shutdownCancel()
    err := bot.Stop(shutdownCtx)
    assert.NoError(t, err)
}
```

### Mock RPC Server

```go
// Mock RPC for testing
type MockRPCServer struct {
    mu     sync.Mutex
    responses map[string]interface{}
}

func (m *MockRPCServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    var req RPCRequest
    json.NewDecoder(r.Body).Decode(&req)

    m.mu.Lock()
    response, ok := m.responses[req.Method]
    m.mu.Unlock()

    if !ok {
        w.WriteHeader(404)
        return
    }

    json.NewEncoder(w).Encode(response)
}
```

## Extension Points

### Adding a New Monitor

```go
// 1. Implement Monitor interface
type NewDEXMonitor struct {
    config   NewDEXConfig
    handler  EventHandler
    // ...
}

func (m *NewDEXMonitor) Start() error {
    // Connect to WebSocket
    // Subscribe to events
    // Handle messages
}

func (m *NewDEXMonitor) SetHandler(h EventHandler) {
    m.handler = h
}

// 2. Add factory function
func NewNewDEXMonitor(config NewDEXConfig) (*NewDEXMonitor, error) {
    return &NewDEXMonitor{config: config}, nil
}

// 3. Register in bot initialization
monitor, err := NewNewDEXMonitor(monitorConfig)
monitors = append(monitors, monitor)
```

### Adding a New Analyzer

```go
// 1. Define analyzer interface
type CustomAnalyzer interface {
    Analyze(ctx context.Context, event TokenEvent) (CustomResult, error)
}

// 2. Implement analyzer
type MyAnalyzer struct {
    config MyAnalyzerConfig
}

func (a *MyAnalyzer) Analyze(ctx context.Context, event TokenEvent) (CustomResult, error) {
    // Custom analysis logic
    return CustomResult{}, nil
}

// 3. Integrate into analysis pipeline
result, err := myAnalyzer.Analyze(ctx, event)
score := calculateScore(result)
```

### Adding a New Chain

```go
// 1. Define chain types
const ChainTypeNewChain = "newchain"

// 2. Implement chain-specific trader
type NewChainTrader struct {
    config NewChainConfig
}

func (t *NewChainTrader) ExecuteSwap(ctx context.Context, params SwapParams) (*SwapResult, error) {
    // Chain-specific swap logic
    return &SwapResult{}, nil
}

// 3. Add chain configuration
chains:
  newchain:
    enabled: true
    network: mainnet
    rpc_endpoints:
      - url: https://newchain-rpc.com
```

## Monitoring and Observability

### Metrics Collected

```go
// Trade metrics
TradesTotal        *prometheus.CounterVec
TradesSuccess      *prometheus.CounterVec
TradesFailed       *prometheus.CounterVec
TradeAmount        *prometheus.HistogramVec
TradeDuration      *prometheus.HistogramVec

// Monitoring metrics
EventsDetected     *prometheus.CounterVec
EventsProcessed    *prometheus.CounterVec
MonitorStatus      *prometheus.GaugeVec

// System metrics
BotUptime          prometheus.Gauge
ErrorsTotal        *prometheus.CounterVec
RPCRequests        *prometheus.CounterVec
RPCLatency         *prometheus.HistogramVec
```

### Health Checks

```go
type HealthStatus struct {
    Status      string            `json:"status"`
    Version     string            `json:"version"`
    Uptime      time.Duration     `json:"uptime"`
    Monitors    []MonitorHealth   `json:"monitors"`
    Wallets     []WalletHealth    `json:"wallets"`
}

func (b *Bot) HealthCheck() HealthStatus {
    return HealthStatus{
        Status: "healthy",
        // ...
    }
}
```

---

For more details, see:

- [Quick Start Guide](QUICKSTART.md)
- [Main README](../README.md)
- [API Documentation](API.md)
