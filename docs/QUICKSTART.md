# Quick Start Guide

Get your meme coin sniper bot running in 15 minutes.

## Prerequisites Check

Before starting, ensure you have:

- [ ] Go 1.23+ installed
- [ ] Docker installed (optional, but recommended)
- [ ] A Solana wallet address with some SOL
- [ ] Basic understanding of command line

## Step 1: Installation (2 minutes)

### Option A: Using Docker (Recommended)

```bash
# Clone the repository
git clone https://github.com/lilwiggy/bot.git
cd bot

# Build the Docker image
docker build -t meme-sniper:latest .
```

### Option B: Build from Source

```bash
# Clone the repository
git clone https://github.com/lilwiggy/bot.git
cd bot

# Install dependencies and build
go mod download
go build -o bin/sniper ./cmd/sniper

# Add to PATH (optional)
sudo cp bin/sniper /usr/local/bin/
```

## Step 2: Get RPC Access (3 minutes)

You need a Solana RPC endpoint. Free options:

### Option 1: Helius (Free tier available)

1. Visit https://www.helius.dev
2. Sign up for free account
3. Create a new project
4. Copy your RPC URL

### Option 2: QuickNode (Free tier available)

1. Visit https://www.quicknode.com
2. Sign up for free account
3. Create Solana endpoint
4. Copy your RPC URL

### Option 3: Public Endpoint (Not recommended for production)

```
https://api.mainnet-beta.solana.com
```

## Step 3: Configure the Bot (5 minutes)

### Create Configuration File

```bash
cp config.example.yaml config.yaml
```

### Edit config.yaml

```yaml
# Basic settings
bot:
  name: my-first-sniper
  dry_run: true  # IMPORTANT: Start with dry_run = true
  log_level: info
  max_concurrent_trades: 1

# Solana configuration
chains:
  solana:
    enabled: true
    network: mainnet
    rpc_endpoints:
      - url: YOUR_RPC_URL_HERE  # Paste your RPC URL
        name: primary
        weight: 100

# Monitoring - Start with one monitor
monitoring:
  solana:
    pumpfun:
      enabled: true  # Pump.fun is most popular
    raydium:
      enabled: false  # Disable others for now
    orca:
      enabled: false

# Analysis - Be conservative at first
analysis:
  min_liquidity_usd: 5000  # Only trade tokens with $5000+ liquidity
  min_score: 80            # High quality threshold
  check_rug_pull: true
  check_honeypot: true

# Strategy - Conservative settings
strategies:
  mode: monitor  # Start in monitor mode (no trades)
  max_position_size_usd: 10  # Small position size
  take_profit_percent: 100    # 2x target
  stop_loss_percent: 30       # 30% loss limit

# Wallet settings
wallets:
  data_dir: ./wallets
  encryption:
    enabled: true
```

## Step 4: Create Wallet (2 minutes)

```bash
# Create a new Solana wallet
sniper wallet create --chain solana

# You'll be prompted for a password - REMEMBER THIS!
# The bot will display your new wallet address

# Fund your wallet with SOL (at least 0.1 SOL for trading + fees)
# Send SOL to the displayed address
```

## Step 5: Test Run (3 minutes)

### Start in Monitor Mode

```bash
# Start the bot (monitor mode - no trades)
sniper start --mode monitor
```

You should see output like:

```
INFO Starting bot
INFO Initializing wallet manager
INFO Connecting to Solana RPC
INFO Starting monitor: PumpFun
INFO Bot started successfully
INFO Monitoring for token events...
```

### Verify Connection

The bot should now be monitoring Pump.fun for new tokens. When a new token is detected, you'll see:

```
INFO Token detected: TOKEN_NAME
INFO Token address: TokenAddress...
INFO Liquidity: $15,000
INFO Security score: 85/100
INFO Monitor mode: skipping trade
```

## Step 6: Enable Manual Trading (Optional)

Once comfortable with monitor mode:

1. **Edit config.yaml:**
   ```yaml
   strategies:
     mode: manual  # Change from monitor to manual
   ```

2. **Restart bot:**
   ```bash
   sniper start --mode manual
   ```

3. **Approve trades manually** when prompted

## Step 7: Enable Auto Trading (Advanced)

⚠️ **Only enable auto trading after:**
- [ ] Successfully tested in monitor mode
- [ ] Successfully tested in manual mode
- [ ] Understand the risks
- [ ] Started with small amounts

1. **Edit config.yaml:**
   ```yaml
   bot:
     dry_run: false  # Enable live trading

   strategies:
     mode: auto
   ```

2. **Start with very small amounts:**
   ```yaml
   strategies:
     max_position_size_usd: 5  # Start small!
   ```

3. **Run the bot:**
   ```bash
   sniper start --mode auto
   ```

## Common First-Time Issues

### Issue: "Connection refused"

**Fix:** Check your RPC URL is correct and accessible:
```bash
curl -X POST YOUR_RPC_URL \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}'
```

### Issue: "Wallet not found"

**Fix:** Create a wallet first:
```bash
sniper wallet create --chain solana
```

### Issue: "Insufficient funds"

**Fix:** Fund your wallet with SOL:
- Get your address: `sniper wallet list`
- Send at least 0.1 SOL to the address

### Issue: "No tokens detected"

**Fix:**
- Check if monitor is running correctly
- Verify network connectivity
- Try during high-activity periods

## Next Steps

1. **Monitor the bot** for a few hours in monitor mode
2. **Check the logs** regularly
3. **Adjust settings** based on your preferences
4. **Read the full documentation** for advanced features

## Safety Checklist

Before enabling live trading:

- [ ] Tested in monitor mode for 24+ hours
- [ ] Tested in manual mode with small amounts
- [ ] Understand all configuration options
- [ ] Have a stop-loss configured
- [ ] Only using funds you can afford to lose
- [ ] RPC endpoint is reliable
- [ ] Wallet password is stored securely
- [ ] Understand the tax implications
- [ ] Have a monitoring system in place

## Getting Help

If you encounter issues:

1. Check the [Troubleshooting](../README.md#troubleshooting) section
2. Search [GitHub Issues](https://github.com/lilwiggy/bot/issues)
3. Join our [Discord community](https://discord.gg/xxx)
4. Create a new issue with logs attached

## Disclaimer

⚠️ **Trading meme coins is extremely risky.**

- Never invest more than you can afford to lose
- Past performance doesn't guarantee future results
- This bot is a tool, not financial advice
- You are solely responsible for your trading decisions

---

**Ready to start?** Jump back to [Installation](../README.md#installation) or [Usage](../README.md#usage).
