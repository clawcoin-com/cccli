// Package wallet provides wallet operations using lightweight crypto
package wallet

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"

	"github.com/clawcoin-com/cccli/internal/client"
	"github.com/clawcoin-com/cccli/internal/config"
	"github.com/clawcoin-com/cccli/internal/crypto"
)

// Wallet handles wallet operations
type Wallet struct {
	cfg       *config.Config
	client    *client.Client
	keystore  *crypto.Keystore
	txBuilder *crypto.TxBuilder
}

// New creates a new Wallet instance
func New(cfg *config.Config, cli *client.Client, password string) (*Wallet, error) {
	// Create keystore in the home directory
	keystoreDir := filepath.Join(cfg.HomeDir, "keystore")
	ks, err := crypto.NewKeystore(keystoreDir, password)
	if err != nil {
		return nil, fmt.Errorf("failed to create keystore: %w", err)
	}

	tb := crypto.NewTxBuilder(cfg.ChainID, cfg.Denom)
	if cfg.Gas > 0 {
		tb.SetGasLimit(cfg.Gas)
	}
	if cfg.GasPrice != "" {
		tb.SetGasPrice(cfg.GasPrice)
	}

	return &Wallet{
		cfg:       cfg,
		client:    cli,
		keystore:  ks,
		txBuilder: tb,
	}, nil
}

// Balance represents an account balance
type Balance struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// GetBalance retrieves balance for an address via REST API
func (w *Wallet) GetBalance(ctx context.Context, address string) ([]Balance, error) {
	balances, err := w.client.GetBalance(ctx, address)
	if err != nil {
		return nil, err
	}

	var result []Balance
	for _, b := range balances {
		result = append(result, Balance{
			Denom:  b.Denom,
			Amount: b.Amount,
		})
	}
	return result, nil
}

// Send sends tokens from one address to another
func (w *Wallet) Send(ctx context.Context, fromKeyName, toAddress, amount string) error {
	privKey, err := w.keystore.GetPrivateKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	keyInfo, err := w.keystore.GetKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get key info: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(keyInfo.PubKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode pubkey: %w", err)
	}

	msg := crypto.MsgSend(keyInfo.Address, toAddress, amount, w.cfg.Denom)

	resp, err := w.client.ExecTx(ctx, keyInfo.Address, func(acct *client.AccountInfo) ([]byte, error) {
		return w.txBuilder.BuildSignedTx(privKey, pubKeyBytes, acct.AccountNumber, acct.Sequence, msg)
	})
	if err != nil {
		return fmt.Errorf("failed to exec tx: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("tx failed (code %d): %s", resp.Code, resp.RawLog)
	}

	return nil
}

// Delegate delegates tokens to a validator
func (w *Wallet) Delegate(ctx context.Context, fromKeyName, validatorAddr, amount string) error {
	privKey, err := w.keystore.GetPrivateKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	keyInfo, err := w.keystore.GetKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get key info: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(keyInfo.PubKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode pubkey: %w", err)
	}

	msg := crypto.MsgDelegate(keyInfo.Address, validatorAddr, amount, w.cfg.Denom)

	resp, err := w.client.ExecTx(ctx, keyInfo.Address, func(acct *client.AccountInfo) ([]byte, error) {
		return w.txBuilder.BuildSignedTx(privKey, pubKeyBytes, acct.AccountNumber, acct.Sequence, msg)
	})
	if err != nil {
		return fmt.Errorf("failed to exec tx: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("tx failed (code %d): %s", resp.Code, resp.RawLog)
	}

	return nil
}

// Undelegate undelegates tokens from a validator
func (w *Wallet) Undelegate(ctx context.Context, fromKeyName, validatorAddr, amount string) error {
	privKey, err := w.keystore.GetPrivateKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	keyInfo, err := w.keystore.GetKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get key info: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(keyInfo.PubKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode pubkey: %w", err)
	}

	msg := crypto.MsgUndelegate(keyInfo.Address, validatorAddr, amount, w.cfg.Denom)

	resp, err := w.client.ExecTx(ctx, keyInfo.Address, func(acct *client.AccountInfo) ([]byte, error) {
		return w.txBuilder.BuildSignedTx(privKey, pubKeyBytes, acct.AccountNumber, acct.Sequence, msg)
	})
	if err != nil {
		return fmt.Errorf("failed to exec tx: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("tx failed (code %d): %s", resp.Code, resp.RawLog)
	}

	return nil
}

// WithdrawReward withdraws staking rewards from a validator
func (w *Wallet) WithdrawReward(ctx context.Context, fromKeyName, validatorAddr string) error {
	privKey, err := w.keystore.GetPrivateKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get private key: %w", err)
	}

	keyInfo, err := w.keystore.GetKey(fromKeyName)
	if err != nil {
		return fmt.Errorf("failed to get key info: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(keyInfo.PubKeyHex)
	if err != nil {
		return fmt.Errorf("failed to decode pubkey: %w", err)
	}

	msg := crypto.MsgWithdrawReward(keyInfo.Address, validatorAddr)

	resp, err := w.client.ExecTx(ctx, keyInfo.Address, func(acct *client.AccountInfo) ([]byte, error) {
		return w.txBuilder.BuildSignedTx(privKey, pubKeyBytes, acct.AccountNumber, acct.Sequence, msg)
	})
	if err != nil {
		return fmt.Errorf("failed to exec tx: %w", err)
	}

	if resp.Code != 0 {
		return fmt.Errorf("tx failed (code %d): %s", resp.Code, resp.RawLog)
	}

	return nil
}


// KeyInfo represents key information
type KeyInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	PubKey  string `json:"pubkey"`
}

// CreateKey creates a new key with a generated mnemonic
func (w *Wallet) CreateKey(name string) (*KeyInfo, string, error) {
	mnemonic, err := crypto.GenerateMnemonic()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate mnemonic: %w", err)
	}

	key, err := w.keystore.CreateKey(name, mnemonic)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create key: %w", err)
	}

	return &KeyInfo{
		Name:    key.Name,
		Address: key.Address,
		PubKey:  key.PubKeyHex,
	}, mnemonic, nil
}

// ImportKey imports a key from a mnemonic.
// Set force=true to skip BIP39 checksum validation.
func (w *Wallet) ImportKey(name, mnemonic string, force ...bool) (*KeyInfo, error) {
	key, err := w.keystore.ImportKey(name, mnemonic, force...)
	if err != nil {
		return nil, fmt.Errorf("failed to import key: %w", err)
	}

	return &KeyInfo{
		Name:    key.Name,
		Address: key.Address,
		PubKey:  key.PubKeyHex,
	}, nil
}

// ImportPrivateKey imports a key from a raw hex-encoded private key.
func (w *Wallet) ImportPrivateKey(name, privKeyHex string) (*KeyInfo, error) {
	key, err := w.keystore.ImportPrivateKey(name, privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("failed to import private key: %w", err)
	}

	return &KeyInfo{
		Name:    key.Name,
		Address: key.Address,
		PubKey:  key.PubKeyHex,
	}, nil
}

// ListKeys lists all stored keys
func (w *Wallet) ListKeys() ([]*KeyInfo, error) {
	keys, err := w.keystore.ListKeys()
	if err != nil {
		return nil, err
	}

	var result []*KeyInfo
	for _, k := range keys {
		result = append(result, &KeyInfo{
			Name:    k.Name,
			Address: k.Address,
			PubKey:  k.PubKeyHex,
		})
	}
	return result, nil
}

// GetKey retrieves a key by name
func (w *Wallet) GetKey(name string) (*KeyInfo, error) {
	key, err := w.keystore.GetKey(name)
	if err != nil {
		return nil, err
	}

	return &KeyInfo{
		Name:    key.Name,
		Address: key.Address,
		PubKey:  key.PubKeyHex,
	}, nil
}

// DeleteKey removes a key
func (w *Wallet) DeleteKey(name string) error {
	return w.keystore.DeleteKey(name)
}

// GetAddress returns the address for a key
func (w *Wallet) GetAddress(name string) (string, error) {
	key, err := w.keystore.GetKey(name)
	if err != nil {
		return "", err
	}
	return key.Address, nil
}

// FundEVM funds an EVM address by converting and sending tokens
func (w *Wallet) FundEVM(ctx context.Context, fromKey, evmAddress string, amountAIT float64) error {
	// Convert EVM address to Cosmos address
	cosmosAddress, err := w.EVMToCosmosAddress(evmAddress)
	if err != nil {
		return err
	}

	// Convert amount to base denomination (18 decimals)
	amountBase := aitToBase(amountAIT)
	amountStr := amountBase.String()

	return w.Send(ctx, fromKey, cosmosAddress, amountStr)
}

// EVMToCosmosAddress converts an EVM address to a Cosmos address.
// Delegates to the package-level EVMToCosmosAddr function.
func (w *Wallet) EVMToCosmosAddress(evmAddress string) (string, error) {
	return EVMToCosmosAddr(evmAddress)
}

// EVMToCosmosAddr converts an EVM address (0x...) to a Cosmos address (cc1...).
// Handles both 0x and 0X prefixes. If already a cc1... address, returns as-is.
func EVMToCosmosAddr(evmAddress string) (string, error) {
	// If already a Cosmos address, return as-is
	if strings.HasPrefix(evmAddress, crypto.Bech32PrefixAccAddr+"1") {
		return evmAddress, nil
	}

	// Strip 0x/0X prefix
	evmHex := strings.TrimPrefix(strings.TrimPrefix(evmAddress, "0x"), "0X")

	addrBytes, err := hex.DecodeString(evmHex)
	if err != nil {
		return "", fmt.Errorf("invalid EVM address: %w", err)
	}
	if len(addrBytes) != 20 {
		return "", fmt.Errorf("EVM address must be 20 bytes, got %d", len(addrBytes))
	}
	return crypto.BytesToBech32(crypto.Bech32PrefixAccAddr, addrBytes)
}

// CosmosToEVMAddress converts a Cosmos bech32 address (cc1...) to an EVM hex address (0x...).
// Validates that the address uses the expected "cc" prefix.
func CosmosToEVMAddress(cosmosAddress string) (string, error) {
	hrp, addrBytes, err := crypto.Bech32ToBytes(cosmosAddress)
	if err != nil {
		return "", fmt.Errorf("invalid Cosmos address: %w", err)
	}
	if hrp != crypto.Bech32PrefixAccAddr {
		return "", fmt.Errorf("unexpected address prefix %q, expected %q", hrp, crypto.Bech32PrefixAccAddr)
	}
	if len(addrBytes) != 20 {
		return "", fmt.Errorf("address must be 20 bytes, got %d", len(addrBytes))
	}
	return "0x" + hex.EncodeToString(addrBytes), nil
}

// aitToBase converts AIT (human readable) to base denomination
func aitToBase(amount float64) *big.Int {
	// 18 decimal places
	scale := new(big.Float).SetFloat64(1e18)
	amountFloat := new(big.Float).SetFloat64(amount)
	result := new(big.Float).Mul(amountFloat, scale)

	intResult := new(big.Int)
	result.Int(intResult)
	return intResult
}

// BaseToAIT converts base denomination to AIT (human readable).
// Accepts both integer strings ("1000000") and DecCoin strings ("1000000.500000000000000000").
func BaseToAIT(amount string) float64 {
	// Try parsing as big.Float first to handle DecCoin format
	baseFloat, _, err := big.ParseFloat(amount, 10, 256, big.ToNearestEven)
	if err != nil {
		return 0
	}

	scale := new(big.Float).SetFloat64(1e18)
	result := new(big.Float).Quo(baseFloat, scale)

	f, _ := result.Float64()
	return f
}

// FormatBalance formats a balance for display
func FormatBalance(bal Balance) string {
	amount := BaseToAIT(bal.Amount)
	return fmt.Sprintf("%.6f %s", amount, strings.TrimPrefix(bal.Denom, "a"))
}

// Keystore returns the underlying keystore
func (w *Wallet) Keystore() *crypto.Keystore {
	return w.keystore
}

// TxBuilder returns the transaction builder
func (w *Wallet) TxBuilder() *crypto.TxBuilder {
	return w.txBuilder
}

// Config returns the configuration
func (w *Wallet) Config() *config.Config {
	return w.cfg
}

// Client returns the client
func (w *Wallet) Client() *client.Client {
	return w.client
}
