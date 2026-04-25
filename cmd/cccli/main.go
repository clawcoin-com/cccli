// Package main is the entry point for cccli
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/clawcoin-com/cccli/internal/client"
	"github.com/clawcoin-com/cccli/internal/config"
	"github.com/clawcoin-com/cccli/internal/crypto"
	"github.com/clawcoin-com/cccli/internal/llm"
	"github.com/clawcoin-com/cccli/internal/miner"
	"github.com/clawcoin-com/cccli/internal/wallet"
	"github.com/spf13/cobra"
)

var (
	Version = "v0.0.1"
)

var (
	cfgFile string
	cfg     *config.Config
	// logOut is the writer used by the miner loop for all log output.
	// Defaults to stdout; set to MultiWriter when --log-file is given.
	logOut io.Writer = os.Stdout
	// errOut receives WARN/ERROR lines only (in addition to logOut).
	// nil when no --log-file is given; set alongside logOut.
	errOut io.Writer
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "cccli",
	Short: "ClawCoin Blockchain CLI - Client tools for cc_bc",
	Long: `cccli is a command-line interface for interacting with the cc_bc blockchain.

It provides tools for:
  - Miner operations (heartbeat, stake, unstake)
  - QA session participation (submit questions/answers, vote)
  - Wallet operations (balance, send, fund EVM, key management)
  - Status and query commands`,
	Version: Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for version command
		if cmd.Name() == "version" {
			return nil
		}
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return err
		}
		// Override config with explicit command-line flags (only if user explicitly set them)
		if f := cmd.Flags().Lookup("chain-id"); f != nil && f.Changed {
			cfg.ChainID = f.Value.String()
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.cc_bc/cccli.yaml)")
	rootCmd.PersistentFlags().String("chain-id", "ccbc-1", "chain ID")

	// Add subcommands
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(minerCmd)
	rootCmd.AddCommand(walletCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(versionCmd)
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("cccli %s\n", Version)
		return nil
	},
}

// newKeystore creates a keystore instance from config
func newKeystore() (*crypto.Keystore, error) {
	keystoreDir := filepath.Join(cfg.HomeDir, "keystore")
	return crypto.NewKeystore(keystoreDir, "")
}

// newMiner creates a Miner instance using keystore (no cc_bcd dependency)
func newMiner(keyName string) (*miner.Miner, error) {
	cli := client.New(cfg)
	ks, err := newKeystore()
	if err != nil {
		return nil, err
	}
	return miner.New(cfg, cli, ks, keyName)
}

// preflightCheck verifies REST API and LLM service before starting the miner.
func preflightCheck(ctx context.Context) error {
	logInfo("--- Pre-flight checks ---\n")
	ok := true

	// 1. REST API
	cli := client.New(cfg)
	status, err := cli.GetStatus(ctx)
	if err != nil {
		logWarn("REST API (%s): %v\n", cfg.RESTURL, err)
		ok = false
	} else {
		logInfo("REST API: block height %s, network %s\n", status.SyncInfo.LatestBlockHeight, status.NodeInfo.Network)
	}

	// 2. LLM
	llmTest := llm.NewClient(cfg.LLMProvider, cfg.LLMAPIBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMMaxTokens, cfg.LLMThinking)
	testQ, err := llmTest.GenerateQuestion(ctx, "test", "", "preflight")
	if err != nil {
		logWarn("LLM (%s, model=%s): %v\n", cfg.LLMAPIBaseURL, cfg.LLMModel, err)
		ok = false
	} else if len(testQ) == 0 {
		logWarn("LLM (%s, model=%s): returned empty content, model may not be compatible\n", cfg.LLMAPIBaseURL, cfg.LLMModel)
		ok = false
	} else {
		preview := testQ
		if len(preview) > 20 {
			preview = preview[:20] + "..."
		}
		logInfo("LLM: model=%s, thinking=%v (test response: %d chars, preview: %q)\n", cfg.LLMModel, cfg.LLMThinking, len(testQ), preview)
	}

	if !ok {
		return fmt.Errorf("pre-flight checks failed, please fix the issues above before mining")
	}
	logInfo("--- All checks passed ---\n")
	return nil
}

// ============================================================================
// Status Command
// ============================================================================

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show node status",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		status, err := cli.GetStatus(cmd.Context())
		if err != nil {
			return err
		}

		fmt.Printf("Network:      %s\n", status.NodeInfo.Network)
		fmt.Printf("Moniker:      %s\n", status.NodeInfo.Moniker)
		fmt.Printf("Block Height: %s\n", status.SyncInfo.LatestBlockHeight)
		fmt.Printf("Catching Up:  %v\n", status.SyncInfo.CatchingUp)
		return nil
	},
}

// ============================================================================
// Miner Commands
// ============================================================================

var minerCmd = &cobra.Command{
	Use:   "miner",
	Short: "Miner operations",
}

func init() {
	minerCmd.AddCommand(minerStakeCmd)
	minerCmd.AddCommand(minerUnstakeCmd)
	minerCmd.AddCommand(minerHeartbeatCmd)
	minerCmd.AddCommand(minerRunCmd)
	minerCmd.AddCommand(minerSessionsCmd)
}

var minerStakeCmd = &cobra.Command{
	Use:   "stake [amount] --key [key-name]",
	Short: "Stake tokens to become a miner (add --endpoint to become a reporter)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		keyName, _ := cmd.Flags().GetString("key")
		if keyName == "" {
			return fmt.Errorf("--key is required")
		}

		endpoint, _ := cmd.Flags().GetString("endpoint")

		m, err := newMiner(keyName)
		if err != nil {
			return err
		}

		// Strip denom suffix (e.g. "4000acc" → "4000")
		amount := strings.TrimRight(args[0], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
		if endpoint != "" {
			fmt.Printf("Staking %s for %s (reporter endpoint: %s)...\n", amount, m.Address(), endpoint)
		} else {
			fmt.Printf("Staking %s for %s (plain miner, no reporter endpoint)...\n", amount, m.Address())
		}
		if err := m.Stake(cmd.Context(), amount, endpoint); err != nil {
			return err
		}

		fmt.Println("Stake successful!")
		return nil
	},
}

var minerUnstakeCmd = &cobra.Command{
	Use:   "unstake --key [key-name]",
	Short: "Unstake tokens",
	RunE: func(cmd *cobra.Command, args []string) error {
		keyName, _ := cmd.Flags().GetString("key")
		if keyName == "" {
			return fmt.Errorf("--key is required")
		}

		m, err := newMiner(keyName)
		if err != nil {
			return err
		}

		fmt.Printf("Unstaking for %s...\n", m.Address())
		if err := m.Unstake(cmd.Context()); err != nil {
			return err
		}

		fmt.Println("Unstake successful!")
		return nil
	},
}

var minerHeartbeatCmd = &cobra.Command{
	Use:   "heartbeat --key [key-name]",
	Short: "Send a single heartbeat",
	RunE: func(cmd *cobra.Command, args []string) error {
		keyName, _ := cmd.Flags().GetString("key")
		if keyName == "" {
			return fmt.Errorf("--key is required")
		}

		m, err := newMiner(keyName)
		if err != nil {
			return err
		}

		fmt.Printf("Sending heartbeat for %s...\n", m.Address())
		// Force send by resetting interval check
		cfg.HeartbeatInterval = 0
		if err := m.SendHeartbeat(cmd.Context()); err != nil {
			return err
		}

		fmt.Println("Heartbeat sent!")
		return nil
	},
}

var minerRunCmd = &cobra.Command{
	Use:   "run --key [key-name]",
	Short: "Run the miner client loop",
	Long: `Run the miner client in a continuous loop.

The miner will:
  - Send periodic heartbeats
  - Monitor QA sessions
  - Auto-participate in sessions as questioner/answerer/voter`,
	RunE: func(cmd *cobra.Command, args []string) error {
		keyName, _ := cmd.Flags().GetString("key")
		if keyName == "" {
			return fmt.Errorf("--key is required")
		}

		// Set up log output: stdout by default, tee to file if --log-file given
		if logFile, _ := cmd.Flags().GetString("log-file"); logFile != "" {
			if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
				return fmt.Errorf("create log dir: %w", err)
			}
			f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return fmt.Errorf("open log file: %w", err)
			}
			defer f.Close()
			logOut = io.MultiWriter(os.Stdout, f)

			// Open a separate error log file (same dir, <name>_error.<ext>)
			ext := filepath.Ext(logFile)
			errLogFile := strings.TrimSuffix(logFile, ext) + "_error" + ext
			ef, err := os.OpenFile(errLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return fmt.Errorf("open error log file: %w", err)
			}
			defer ef.Close()
			errOut = ef

			logInfo("Logging to: %s (errors: %s)\n", logFile, errLogFile)
		}

		m, err := newMiner(keyName)
		if err != nil {
			return err
		}

		logInfo("Starting miner for %s\n", m.Address())
		logInfo("REST: %s, Chain: %s\n", cfg.RESTURL, cfg.ChainID)
		logInfo("Heartbeat interval: %ds, Poll interval: %ds\n", cfg.HeartbeatInterval, cfg.PollInterval)

		if err := preflightCheck(cmd.Context()); err != nil {
			return err
		}

		logInfo("Press Ctrl+C to stop\n")

		return runMinerLoop(cmd.Context(), m)
	},
}

var minerSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List active QA sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		sessions, err := cli.GetActiveSessions(cmd.Context())
		if err != nil {
			return err
		}

		if len(sessions) == 0 {
			fmt.Println("No active sessions")
			return nil
		}

		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	},
}

func init() {
	minerStakeCmd.Flags().String("key", "", "key name in keystore (required)")
	minerStakeCmd.Flags().String("endpoint", "", "reporter network endpoint (required to become a reporter, e.g. http://ip:port)")
	minerUnstakeCmd.Flags().String("key", "", "key name in keystore (required)")
	minerHeartbeatCmd.Flags().String("key", "", "key name in keystore (required)")
	minerRunCmd.Flags().String("key", "", "key name in keystore (required)")
	minerRunCmd.Flags().String("log-file", "", "write logs to this file (in addition to stdout)")
}

// ============================================================================
// Wallet Commands
// ============================================================================

var walletCmd = &cobra.Command{
	Use:   "wallet",
	Short: "Wallet operations",
}

func init() {
	walletCmd.AddCommand(walletBalanceCmd)
	walletCmd.AddCommand(walletSendCmd)
	walletCmd.AddCommand(walletFundEVMCmd)
	walletCmd.AddCommand(walletKeysCmd)
	walletCmd.AddCommand(walletCreateKeyCmd)
	walletCmd.AddCommand(walletImportKeyCmd)
	walletCmd.AddCommand(walletImportPrivKeyCmd)
	walletCmd.AddCommand(walletDelegateCmd)
	walletCmd.AddCommand(walletUndelegateCmd)
	walletCmd.AddCommand(walletDelegationsCmd)
	walletCmd.AddCommand(walletValidatorsCmd)
	walletCmd.AddCommand(walletWithdrawRewardCmd)
	walletCmd.AddCommand(walletRewardsCmd)
	walletCmd.AddCommand(walletAddrToEVMCmd)
	walletCmd.AddCommand(walletAddrFromEVMCmd)
}

var walletBalanceCmd = &cobra.Command{
	Use:   "balance [address]",
	Short: "Show balance for an address",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		balances, err := w.GetBalance(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if len(balances) == 0 {
			fmt.Println("No balances found")
			return nil
		}

		fmt.Printf("Balances for %s:\n", args[0])
		for _, bal := range balances {
			fmt.Printf("  %s\n", wallet.FormatBalance(bal))
		}
		return nil
	},
}

var walletSendCmd = &cobra.Command{
	Use:   "send [to-address] [amount] --from [key-name]",
	Short: "Send tokens to an address",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromKey, _ := cmd.Flags().GetString("from")
		if fromKey == "" {
			return fmt.Errorf("--from is required")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		fmt.Printf("Sending %s to %s...\n", args[1], args[0])
		if err := w.Send(cmd.Context(), fromKey, args[0], args[1]); err != nil {
			return err
		}

		fmt.Println("Transfer successful!")
		return nil
	},
}

var walletFundEVMCmd = &cobra.Command{
	Use:   "fund-evm [evm-address] [amount-ait] --from [key-name]",
	Short: "Fund an EVM address",
	Long: `Fund an EVM address by converting and sending tokens.

The EVM address will be converted to its corresponding Cosmos address,
and the specified amount of AIT (in human readable format) will be sent.

Example:
  cccli wallet fund-evm 0x926d... 100 --from validator`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromKey, _ := cmd.Flags().GetString("from")
		if fromKey == "" {
			return fmt.Errorf("--from is required")
		}

		var amount float64
		if _, err := fmt.Sscanf(args[1], "%f", &amount); err != nil {
			return fmt.Errorf("invalid amount: %s", args[1])
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		fmt.Printf("Funding EVM address %s with %.2f AIT...\n", args[0], amount)
		if err := w.FundEVM(cmd.Context(), fromKey, args[0], amount); err != nil {
			return err
		}

		fmt.Println("Funding successful!")
		return nil
	},
}

var walletKeysCmd = &cobra.Command{
	Use:   "keys",
	Short: "List keys in keystore",
	RunE: func(cmd *cobra.Command, args []string) error {
		ks, err := newKeystore()
		if err != nil {
			return err
		}

		keys, err := ks.ListKeys()
		if err != nil {
			return err
		}

		if len(keys) == 0 {
			fmt.Println("No keys found. Use 'cccli wallet create-key' or 'cccli wallet import-key' to add one.")
			return nil
		}

		fmt.Println("Keys:")
		for _, k := range keys {
			fmt.Printf("  %s:\n    Address:     %s\n    EVM Address: %s\n", k.Name, k.Address, k.EVMAddress)
		}
		return nil
	},
}

var walletCreateKeyCmd = &cobra.Command{
	Use:   "create-key [name]",
	Short: "Create a new key with generated mnemonic",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		keyInfo, mnemonic, err := w.CreateKey(args[0])
		if err != nil {
			return err
		}

		fmt.Printf("Key created successfully!\n\n")
		fmt.Printf("Name:        %s\n", keyInfo.Name)
		fmt.Printf("Address:     %s\n", keyInfo.Address)
		fmt.Printf("EVM Address: %s\n", keyInfo.EVMAddress)
		fmt.Printf("\n**IMPORTANT** Save your mnemonic phrase (24 words):\n\n")
		fmt.Printf("  %s\n\n", mnemonic)
		fmt.Printf("This is the ONLY way to recover your key. Keep it safe and secret!\n")
		return nil
	},
}

var walletImportKeyCmd = &cobra.Command{
	Use:   "import-key [name]",
	Short: "Import a key from mnemonic phrase",
	Long: `Import a key from a BIP39 mnemonic phrase.

Supports mnemonics from:
  - Keplr, Leap, Cosmostation (Cosmos wallets)
  - Any BIP39-compatible wallet with Cosmos derivation path (m/44'/118'/0'/0/0)

Example:
  cccli wallet import-key mykey
  cccli wallet import-key mykey --force   # skip checksum validation`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")

		fmt.Print("Enter mnemonic phrase: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		mnemonic := strings.TrimSpace(scanner.Text())

		if mnemonic == "" {
			return fmt.Errorf("mnemonic cannot be empty")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		keyInfo, err := w.ImportKey(args[0], mnemonic, force)
		if err != nil {
			return err
		}

		fmt.Printf("Key imported successfully!\n\n")
		fmt.Printf("Name:        %s\n", keyInfo.Name)
		fmt.Printf("Address:     %s\n", keyInfo.Address)
		fmt.Printf("EVM Address: %s\n", keyInfo.EVMAddress)
		return nil
	},
}

var walletImportPrivKeyCmd = &cobra.Command{
	Use:   "import-privkey [name]",
	Short: "Import a key from a raw hex private key",
	Long: `Import a key from a raw secp256k1 private key (hex-encoded, 32 bytes / 64 hex chars).

Accepts keys with or without 0x prefix.

Example:
  cccli wallet import-privkey mykey`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print("Enter private key (hex): ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		privKeyHex := strings.TrimSpace(scanner.Text())

		if privKeyHex == "" {
			return fmt.Errorf("private key cannot be empty")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		keyInfo, err := w.ImportPrivateKey(args[0], privKeyHex)
		if err != nil {
			return err
		}

		fmt.Printf("Private key imported successfully!\n\n")
		fmt.Printf("Name:        %s\n", keyInfo.Name)
		fmt.Printf("Address:     %s\n", keyInfo.Address)
		fmt.Printf("EVM Address: %s\n", keyInfo.EVMAddress)
		return nil
	},
}

var walletDelegateCmd = &cobra.Command{
	Use:   "delegate [validator] [amount] --from [key-name]",
	Short: "Delegate tokens to a validator",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromKey, _ := cmd.Flags().GetString("from")
		if fromKey == "" {
			return fmt.Errorf("--from is required")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		// Strip denom suffix (e.g. "2000000000000000000acc" → "2000000000000000000")
		amount := strings.TrimRight(args[1], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

		fmt.Printf("Delegating %s to %s...\n", args[1], args[0])
		if err := w.Delegate(cmd.Context(), fromKey, args[0], amount); err != nil {
			return err
		}

		fmt.Println("Delegation successful!")
		return nil
	},
}

var walletUndelegateCmd = &cobra.Command{
	Use:   "undelegate [validator] [amount] --from [key-name]",
	Short: "Undelegate tokens from a validator",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromKey, _ := cmd.Flags().GetString("from")
		if fromKey == "" {
			return fmt.Errorf("--from is required")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		amount := strings.TrimRight(args[1], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

		fmt.Printf("Undelegating %s from %s...\n", args[1], args[0])
		if err := w.Undelegate(cmd.Context(), fromKey, args[0], amount); err != nil {
			return err
		}

		fmt.Println("Undelegation successful!")
		return nil
	},
}

var walletDelegationsCmd = &cobra.Command{
	Use:   "delegations [address]",
	Short: "Query delegations for an address",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		delegations, err := cli.GetDelegations(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if len(delegations) == 0 {
			fmt.Println("No delegations found")
			return nil
		}

		fmt.Printf("Delegations for %s:\n", args[0])
		for _, d := range delegations {
			fmt.Printf("  Validator: %s\n", d.Delegation.ValidatorAddress)
			fmt.Printf("  Amount:    %s\n", wallet.FormatBalance(wallet.Balance{
				Denom:  d.Balance.Denom,
				Amount: d.Balance.Amount,
			}))
			fmt.Println()
		}
		return nil
	},
}

var walletValidatorsCmd = &cobra.Command{
	Use:   "validators",
	Short: "List active validators",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		validators, err := cli.GetValidators(cmd.Context())
		if err != nil {
			return err
		}

		if len(validators) == 0 {
			fmt.Println("No active validators found")
			return nil
		}

		fmt.Println("Active Validators:")
		for _, v := range validators {
			fmt.Printf("  %s\n", v.OperatorAddress)
			fmt.Printf("    Moniker:    %s\n", v.Description.Moniker)
			fmt.Printf("    Tokens:     %s\n", wallet.FormatBalance(wallet.Balance{
				Denom:  cfg.Denom,
				Amount: v.Tokens,
			}))
			fmt.Printf("    Commission: %s\n", v.Commission.CommissionRates.Rate)
			fmt.Println()
		}
		return nil
	},
}

var walletWithdrawRewardCmd = &cobra.Command{
	Use:   "withdraw-reward [validator] --from [key-name]",
	Short: "Withdraw staking rewards from a validator",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fromKey, _ := cmd.Flags().GetString("from")
		if fromKey == "" {
			return fmt.Errorf("--from is required")
		}

		cli := client.New(cfg)
		w, err := wallet.New(cfg, cli, "")
		if err != nil {
			return err
		}

		fmt.Printf("Withdrawing rewards from %s...\n", args[0])
		if err := w.WithdrawReward(cmd.Context(), fromKey, args[0]); err != nil {
			return err
		}

		fmt.Println("Withdraw reward successful!")
		return nil
	},
}

var walletRewardsCmd = &cobra.Command{
	Use:   "rewards [address]",
	Short: "Query pending staking rewards for an address",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli := client.New(cfg)
		rewards, total, err := cli.GetDelegationRewards(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		if len(rewards) == 0 {
			fmt.Println("No pending rewards")
			return nil
		}

		fmt.Printf("Pending rewards for %s:\n", args[0])
		for _, r := range rewards {
			fmt.Printf("  Validator: %s\n", r.ValidatorAddress)
			for _, coin := range r.Reward {
				fmt.Printf("    %s\n", wallet.FormatBalance(wallet.Balance{
					Denom:  coin.Denom,
					Amount: coin.Amount,
				}))
			}
		}
		if len(total) > 0 {
			fmt.Println("  Total:")
			for _, coin := range total {
				fmt.Printf("    %s\n", wallet.FormatBalance(wallet.Balance{
					Denom:  coin.Denom,
					Amount: coin.Amount,
				}))
			}
		}
		return nil
	},
}

var walletAddrToEVMCmd = &cobra.Command{
	Use:   "addr-to-evm [cc1-address]",
	Short: "Convert a Cosmos address (cc1...) to an EVM address (0x...)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr := args[0]
		if strings.HasPrefix(addr, "0x") || strings.HasPrefix(addr, "0X") {
			return fmt.Errorf("already an EVM address: %s", addr)
		}
		evmAddr, err := wallet.CosmosToEVMAddress(addr)
		if err != nil {
			return err
		}
		fmt.Printf("Cosmos: %s\nEVM:    %s\n", addr, evmAddr)
		return nil
	},
}

var walletAddrFromEVMCmd = &cobra.Command{
	Use:   "addr-from-evm [0x-address]",
	Short: "Convert an EVM address (0x...) to a Cosmos address (cc1...)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr := args[0]
		if strings.HasPrefix(addr, crypto.Bech32PrefixAccAddr+"1") {
			return fmt.Errorf("already a Cosmos address: %s", addr)
		}
		cosmosAddr, err := wallet.EVMToCosmosAddr(addr)
		if err != nil {
			return err
		}
		fmt.Printf("EVM:    %s\nCosmos: %s\n", addr, cosmosAddr)
		return nil
	},
}

func init() {
	walletSendCmd.Flags().String("from", "", "key name to send from (required)")
	walletFundEVMCmd.Flags().String("from", "", "key name to send from (required)")
	walletImportKeyCmd.Flags().Bool("force", false, "skip BIP39 checksum validation (for non-standard mnemonics)")
	walletDelegateCmd.Flags().String("from", "", "key name to delegate from (required)")
	walletUndelegateCmd.Flags().String("from", "", "key name to undelegate from (required)")
	walletWithdrawRewardCmd.Flags().String("from", "", "key name to withdraw from (required)")
}

// ============================================================================
// Config Commands
// ============================================================================

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configInitCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Chain ID:           %s\n", cfg.ChainID)
		fmt.Printf("Home Directory:     %s\n", cfg.HomeDir)
		fmt.Printf("Denomination:       %s\n", cfg.Denom)
		fmt.Printf("Heartbeat Interval: %ds\n", cfg.HeartbeatInterval)
		fmt.Printf("Poll Interval:      %ds\n", cfg.PollInterval)
		fmt.Printf("REST URL:           %s\n", cfg.RESTURL)
		fmt.Printf("LLM API URL:        %s\n", cfg.LLMAPIBaseURL)
		fmt.Printf("LLM Model:          %s\n", cfg.LLMModel)
		if cfg.SweepTo != "" {
			fmt.Printf("Sweep To:           %s\n", cfg.SweepTo)
			fmt.Printf("Sweep Threshold:    %d CC\n", cfg.SweepThreshold)
			fmt.Printf("Sweep Keep:         %d CC\n", cfg.SweepKeep)
		} else {
			fmt.Printf("Sweep:              disabled\n")
		}
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := cfgFile
		if path == "" {
			path = fmt.Sprintf("%s/cccli.yaml", cfg.HomeDir)
		}

		if err := cfg.Save(path); err != nil {
			return err
		}

		fmt.Printf("Configuration saved to %s\n", path)
		return nil
	},
}
