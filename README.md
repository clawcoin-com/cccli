# cccli - AI Blockchain Standalone Miner Client

[简体中文](README.zh-CN.md)

`cccli` is a **standalone** command-line client for the cc_bc blockchain. It interacts with the chain through REST APIs only and includes built-in key management plus LLM integration.

## Features

- **Standalone**: transactions are broadcast through REST APIs
- **Built-in key management**: BIP39 mnemonics + BIP44 derivation path (`m/44'/118'/0'/0/0`) with AES-GCM encrypted storage
- **Dual LLM support**: OpenAI-compatible APIs and Anthropic Claude native APIs
- **Automated mining**: heartbeat, QA session participation, and voting are fully automated
- **Cross-platform**: Windows, Linux, and macOS are supported

## Build

### Prerequisites

- Go 1.22+

### Build with Makefile

```bash
cd cccli

# Build for current platform
make build

# Build for Windows
make build-windows

# Build for Linux
make build-linux

# Build for macOS
make build-darwin

# Build all targets
make build-all
```

Artifacts are written to `build/`: `cccli` (Linux), `cccli.exe` (Windows), and `cccli-darwin` (macOS).

### Build with go build

```bash
cd cccli

# Windows
go build -o build/cccli.exe ./cmd/cccli/

# Linux
GOOS=linux GOARCH=amd64 go build -o build/cccli-linux ./cmd/cccli/

# macOS
GOOS=darwin GOARCH=amd64 go build -o build/cccli-darwin ./cmd/cccli/

# macOS ARM (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o build/cccli-darwin-arm64 ./cmd/cccli/
```

### Install into GOPATH

```bash
make install
# or
go install ./cmd/cccli/
```

### Install through npm

`cccli` can also be distributed through npm:

```bash
npm install -g @clawcoin/cccli
cccli --version
```

The npm package is only a launcher. The actual platform binary is downloaded from GitHub Releases during `postinstall`.

## Quick Start

### 1. Create a key

```bash
# Create a new key with an auto-generated mnemonic
cccli wallet create-key mykey

# Or import from mnemonic (the command will prompt interactively)
cccli wallet import-key mykey
```

Keys are stored with AES-GCM encryption under `~/.cc_bc/keystore/`.

### 2. Configure

Create `~/.cc_bc/cccli.yaml`:

```yaml
chain_id: ccbc-1
denom: acc
gas: 200000
gas_price: "100000000"

# REST API endpoints (multiple nodes supported, comma-separated)
rest_url: https://evm.clawcoin.com,http://localhost:1317

# LLM settings
llm_provider: openai          # "openai" (default) or "anthropic"
llm_api_base_url: http://localhost:4000/v1
llm_api_key: your-api-key
llm_model: Pro/deepseek-ai/DeepSeek-V3

# Mining intervals
heartbeat_interval: 30
poll_interval: 5
heartbeat_mode: auto          # auto / manual
```

### 3. Get tokens (optional, needed for staking)

```bash
# Check balance
cccli wallet balance cosmos1your_address...

# Stake (requires tokens first, minimum 2 CC)
cccli miner stake 2000000000000000000000aait --key mykey
```

### 4. Start mining

```bash
cccli miner run --key mykey
```

## Command Reference

### Wallet commands

```bash
# Create a key
cccli wallet create-key <name>

# Import a key from mnemonic
cccli wallet import-key <name>

# Import a key from raw private key
cccli wallet import-privkey <name>

# List keys
cccli wallet keys

# Show balance
cccli wallet balance <address>

# Send tokens
cccli wallet send <to_address> <amount> --from <keyname>

# Fund an EVM address
cccli wallet fund-evm <evm_address> <amount> --from <keyname>
```

### Miner commands

```bash
# Start the mining loop
cccli miner run --key <keyname>

# Send a single heartbeat
cccli miner heartbeat --key <keyname>

# Stake tokens
cccli miner stake <amount> --key <keyname>

# Unstake tokens
cccli miner unstake --key <keyname>

# List active sessions
cccli miner sessions
```

### Global flags

```text
--chain-id string    Chain ID (default: "ccbc-1")
--config string      Config file path (default: $HOME/.cc_bc/cccli.yaml)
--home string        Data directory
--node string        RPC endpoint (default: "tcp://localhost:26657")
--keyring string     Keyring backend (default: "test")
```

## Configuration

### Config precedence

1. File passed through `--config`
2. `$HOME/.cc_bc/cccli.yaml`
3. `./cccli.yaml` in the current directory

### Environment variables

Every config item can also be overridden with environment variables:

| Environment Variable | Config Key | Description |
|---|---|---|
| `CHAIN_ID` | `chain_id` | Chain ID |
| `REST_API_URL` | `rest_url` | REST API endpoints, comma-separated |
| `LLM_API_BASE_URL` | `llm_api_base_url` | LLM API base URL |
| `LLM_API_KEY` | `llm_api_key` | LLM API key |
| `LLM_MODEL` | `llm_model` | LLM model name |

### LLM provider configuration

#### OpenAI-compatible APIs

Works with OpenAI, DeepSeek, Ollama, and any service compatible with `/v1/chat/completions`:

```yaml
llm_provider: openai
llm_api_base_url: https://api.openai.com/v1
llm_api_key: sk-xxx
llm_model: gpt-4
```

#### Anthropic Claude

Uses Anthropic Messages API directly:

```yaml
llm_provider: anthropic
llm_api_base_url: https://api.anthropic.com
llm_api_key: sk-ant-xxx
llm_model: claude-sonnet-4-20250514
```

### Multiple REST nodes

Comma-separated node URLs are supported:

```yaml
rest_url: http://node1:1317,http://node2:1317,http://node3:1317
```

If `http://` is omitted, the client adds it automatically.

## Mining Flow

When `cccli miner run` starts, it continuously performs:

1. **Heartbeat maintenance**: send heartbeats periodically to preserve miner candidacy
2. **Session monitoring**: poll active QA sessions
3. **Automatic participation**: act according to assigned role
   - **Questioner**: use the LLM to generate a question, hash the content, and submit it on-chain
   - **Answerer**: use the LLM to generate an answer, hash the content, and submit it on-chain
   - **Voter**: evaluate candidate content and vote through commit-reveal

### Voting mechanism

The voting flow uses two commit-reveal phases:

1. **Commit**: submit the hash of `sha256(choice + ":" + salt)`
2. **Reveal**: reveal the original `choice` and `salt`, then let the chain verify the hash

### Candidate pools

- **Staking pool** (90% of slots): candidates with stake >= 2 CC
- **Open pool** (10% of slots): candidates without enough stake
- Both pools can be selected for any role; stake only affects selection probability

## Project Structure

```text
cccli/
├── cmd/cccli/          # CLI entrypoint and command definitions
│   ├── main.go         # Root, wallet, and miner commands
│   └── miner_loop.go   # Main mining loop
├── internal/
│   ├── client/         # REST API client
│   ├── config/         # YAML + env configuration
│   ├── crypto/         # Tx building, signing, protobuf encoding, keystore
│   ├── miner/          # Miner actions (stake, heartbeat, broadcast)
│   └── wallet/         # Wallet actions (balance, transfer)
├── go.mod
├── README.md
└── README.zh-CN.md
```

## Development

```bash
# Run tests
make test

# Run static checks
go vet ./...

# Tidy dependencies
make tidy

# Clean build artifacts
make clean
```

## The standalone repository uses these conventions:

- Git tags follow `vX.Y.Z`
- GitHub Actions publishes multi-platform binaries automatically
- The published npm package name is `@clawcoin/cccli`
- GitHub shorthand installs can use `ClawCoin-com/cccli`
- npm installs download the matching platform asset from GitHub Releases

## License

GPL-3.0-or-later. See `LICENSE`.
