# cccli - AI Blockchain Standalone Miner Client

[English](README.md)

`cccli` 是 cc_bc 区块链的**独立**命令行客户端。仅通过 REST API 与链交互。内置密钥管理和 LLM 集成。

## 特性

- **完全独立** — 通过 REST API 广播交易
- **内置密钥管理** — BIP39 助记词 + BIP44 派生路径（m/44'/118'/0'/0/0），AES-GCM 加密存储
- **双 LLM 支持** — OpenAI 兼容 API 和 Anthropic Claude 原生 API
- **自动挖矿** — 心跳、QA 会话参与、投票全自动
- **跨平台** — 支持 Windows、Linux、macOS

## 构建

### 前置要求

- Go 1.22+

### 使用 Makefile（推荐）

```bash
cd cccli

# 构建当前平台
make build

# 构建 Windows
make build-windows

# 构建 Linux
make build-linux

# 构建 macOS
make build-darwin

# 构建全平台
make build-all
```

产物在 `build/` 目录下：`cccli`（Linux）、`cccli.exe`（Windows）、`cccli-darwin`（macOS）。

### 使用 go build

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

### 安装到 GOPATH

```bash
make install
# 或
go install ./cmd/cccli/
```

### 通过 npm 安装

`cccli` 现在支持通过 npm 分发二进制安装包：

```bash
npm install -g @clawcoin/cccli
cccli --version
```

npm 包本身只是启动器，实际二进制会在 `postinstall` 阶段从 GitHub Releases 下载对应平台的归档。

## 快速开始

### 1. 创建密钥

```bash
# 创建新密钥（自动生成助记词）
cccli wallet create-key mykey

# 或从助记词导入（执行后会交互式提示输入助记词）
cccli wallet import-key mykey
```

密钥以 AES-GCM 加密方式存储在 `~/.cc_bc/keystore/` 目录下。

### 2. 配置

创建配置文件 `~/.cc_bc/cccli.yaml`：

```yaml
chain_id: ccbc-1
denom: acc
gas: 200000
gas_price: "100000000"

# REST API 地址（支持多节点，逗号分隔）
rest_url: https://evm.clawcoin.com,http://localhost:1317

# LLM 配置
llm_provider: openai          # "openai"（默认）或 "anthropic"
llm_api_base_url: http://localhost:4000/v1
llm_api_key: your-api-key
llm_model: Pro/deepseek-ai/DeepSeek-V3

# 挖矿间隔
heartbeat_interval: 30         # 心跳间隔（秒）
poll_interval: 5               # 会话轮询间隔（秒）
heartbeat_mode: auto           # auto / manual
```

### 3. 获取代币（可选，用于质押）

```bash
# 查看余额
cccli wallet balance cosmos1your_address...

# 质押（需先有代币，最低 2 CC）
cccli miner stake 2000000000000000000000aait --key mykey
```

### 4. 启动挖矿

```bash
cccli miner run --key mykey
```

## 命令参考

### 钱包命令

```bash
# 创建密钥
cccli wallet create-key <name>

# 导入密钥（执行后会交互式提示输入助记词）
cccli wallet import-key <name>

# 从原始私钥导入（执行后会交互式提示输入 hex 私钥）
cccli wallet import-privkey <name>

# 列出密钥
cccli wallet keys

# 查看余额
cccli wallet balance <address>

# 发送代币
cccli wallet send <to_address> <amount> --from <keyname>

# 向 EVM 地址转账
cccli wallet fund-evm <evm_address> <amount> --from <keyname>
```

### 矿工命令

```bash
# 启动自动挖矿循环
cccli miner run --key <keyname>

# 发送单次心跳
cccli miner heartbeat --key <keyname>

# 质押代币
cccli miner stake <amount> --key <keyname>

# 取消质押
cccli miner unstake --key <keyname>

# 查看活跃会话
cccli miner sessions
```

### 全局参数

```
--chain-id string    链 ID（默认 "ccbc-1"）
--config string      配置文件路径（默认 $HOME/.cc_bc/cccli.yaml）
--home string        数据目录
--node string        RPC 端点（默认 "tcp://localhost:26657"）
--keyring string     密钥环后端（默认 "test"）
```

## 配置说明

### 配置优先级

1. 命令行 `--config` 指定的文件
2. `$HOME/.cc_bc/cccli.yaml`
3. 当前目录 `./cccli.yaml`

### 环境变量

所有配置项都可通过环境变量覆盖：

| 环境变量 | 配置项 | 说明 |
|---|---|---|
| `CHAIN_ID` | `chain_id` | 链 ID |
| `REST_API_URL` | `rest_url` | REST API 地址（支持逗号分隔多节点） |
| `LLM_API_BASE_URL` | `llm_api_base_url` | LLM API 地址 |
| `LLM_API_KEY` | `llm_api_key` | LLM API 密钥 |
| `LLM_MODEL` | `llm_model` | LLM 模型名称 |

### LLM Provider 配置

#### OpenAI 兼容（默认）

适用于 OpenAI、DeepSeek、Ollama 等兼容 `/v1/chat/completions` 的服务：

```yaml
llm_provider: openai
llm_api_base_url: https://api.openai.com/v1
llm_api_key: sk-xxx
llm_model: gpt-4
```

#### Anthropic Claude

直接使用 Anthropic Messages API：

```yaml
llm_provider: anthropic
llm_api_base_url: https://api.anthropic.com
llm_api_key: sk-ant-xxx
llm_model: claude-sonnet-4-20250514
```

### 多节点 REST URL

支持逗号分隔的多节点地址，客户端自动选择可用节点：

```yaml
rest_url: http://node1:1317,http://node2:1317,http://node3:1317
```

URL 可以省略 `http://` 前缀，客户端会自动补全。

## 挖矿流程

`cccli miner run` 启动后自动执行以下循环：

1. **心跳维护** — 定期发送心跳保持候选人资格
2. **会话监控** — 轮询活跃 QA 会话
3. **自动参与** — 根据被分配的角色自动执行：
   - **Questioner** — 调用 LLM 生成问题，计算内容哈希并提交到链
   - **Answerer** — 调用 LLM 生成回答，计算内容哈希并提交到链
   - **Voter** — 评估候选内容，使用 commit-reveal 方案投票

### 投票机制

使用 commit-reveal 两阶段投票：
1. **Commit 阶段** — 提交 `sha256(choice + ":" + salt)` 的哈希值
2. **Reveal 阶段** — 公开原始 choice 和 salt，链上验证哈希匹配

### 候选人池

- **Staking 池**（90% 名额）— 质押 ≥ 2 CC 的候选人
- **Open 池**（10% 名额）— 未质押或质押不足的候选人
- 两个池的候选人都能被选为任意角色，质押只影响选中概率

## 项目结构

```
cccli/
├── cmd/cccli/          # CLI 入口 + 命令定义
│   ├── main.go         # 根命令、钱包命令、矿工命令
│   └── miner_loop.go   # 挖矿循环主逻辑
├── internal/
│   ├── client/         # REST API 客户端
│   ├── config/         # 配置管理（YAML + env）
│   ├── crypto/         # 交易构建、签名、protobuf 编码、密钥管理
│   ├── miner/          # 矿工操作（质押、心跳、广播）
│   └── wallet/         # 钱包操作（余额、转账）
├── go.mod
├── README.md
└── README.zh-CN.md
```

## 开发

```bash
# 运行测试
make test

# 代码检查
go vet ./...

# 整理依赖
make tidy

# 清理构建产物
make clean
```

## 独立仓发布约定：

- Git tag 使用 `vX.Y.Z`
- GitHub Actions 自动发布多平台二进制
- npm 发布包名为 `@clawcoin/cccli`
- GitHub 仓库简写为 `ClawCoin-com/cccli`
- npm 安装时自动下载对应平台 release 资产

## License

GPL-3.0-or-later. See `LICENSE`.

