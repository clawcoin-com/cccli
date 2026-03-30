// Package crypto provides lightweight cryptographic operations for Cosmos transactions
// without depending on the full Cosmos SDK
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/sha3"
)

const (
	// BIP44 derivation path for EVM-compatible chain: m/44'/60'/0'/0/0
	CosmosCoinType = 60
	DefaultAccount = 0
	DefaultChange  = 0
	DefaultIndex   = 0

	// Bech32 prefix for cc_bc chain addresses
	Bech32PrefixAccAddr = "cc"

	// Key derivation parameters
	pbkdf2Iterations = 100000
	saltSize         = 32
	keySize          = 32
)

// Key represents a stored key
type Key struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	PubKeyHex  string `json:"pubkey_hex"`
	PrivKeyEnc string `json:"privkey_enc,omitempty"` // Encrypted private key (hex)
	Salt       string `json:"salt,omitempty"`        // PBKDF2 salt (hex)
	Mnemonic   string `json:"mnemonic,omitempty"`    // Only present during creation, not stored
}

// Keystore manages key storage and operations
type Keystore struct {
	dir      string
	password string // Optional password for encryption
}

// NewKeystore creates a new keystore.
// If password is empty, it falls back to the KEYSTORE_PASSWORD environment variable.
func NewKeystore(dir string, password string) (*Keystore, error) {
	if password == "" {
		password = os.Getenv("KEYSTORE_PASSWORD")
	}
	if password == "" {
		password = "ccbc-miner"
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create keystore dir: %w", err)
	}
	return &Keystore{dir: dir, password: password}, nil
}

// GenerateMnemonic creates a new BIP39 mnemonic
func GenerateMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(256) // 24 words
	if err != nil {
		return "", fmt.Errorf("failed to generate entropy: %w", err)
	}
	return bip39.NewMnemonic(entropy)
}

// DeriveKeyFromMnemonic derives a secp256k1 private key from a mnemonic.
// Set force=true to skip BIP39 checksum validation (for non-standard mnemonics).
func DeriveKeyFromMnemonic(mnemonic string, index uint32, force ...bool) (*secp256k1.PrivateKey, error) {
	skipValidation := len(force) > 0 && force[0]
	words := strings.Fields(mnemonic)
	validCounts := map[int]bool{12: true, 15: true, 18: true, 21: true, 24: true}
	if !validCounts[len(words)] {
		return nil, fmt.Errorf("invalid mnemonic: need 12/15/18/21/24 words, got %d", len(words))
	}
	if !skipValidation && !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid mnemonic: checksum mismatch (got %d words). Check for typos, or use --force to skip validation", len(words))
	}

	// Generate seed from mnemonic (with empty passphrase)
	seed := bip39.NewSeed(mnemonic, "")

	// Derive master key
	masterKey, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, fmt.Errorf("failed to derive master key: %w", err)
	}

	// Derive path: m/44'/118'/0'/0/index
	// Purpose (hardened)
	purpose, err := masterKey.NewChildKey(bip32.FirstHardenedChild + 44)
	if err != nil {
		return nil, fmt.Errorf("failed to derive purpose: %w", err)
	}

	// Coin type (hardened)
	coinType, err := purpose.NewChildKey(bip32.FirstHardenedChild + CosmosCoinType)
	if err != nil {
		return nil, fmt.Errorf("failed to derive coin type: %w", err)
	}

	// Account (hardened)
	account, err := coinType.NewChildKey(bip32.FirstHardenedChild + DefaultAccount)
	if err != nil {
		return nil, fmt.Errorf("failed to derive account: %w", err)
	}

	// Change (not hardened)
	change, err := account.NewChildKey(DefaultChange)
	if err != nil {
		return nil, fmt.Errorf("failed to derive change: %w", err)
	}

	// Index (not hardened)
	child, err := change.NewChildKey(index)
	if err != nil {
		return nil, fmt.Errorf("failed to derive index: %w", err)
	}

	// Create secp256k1 private key from derived key bytes
	privKey := secp256k1.PrivKeyFromBytes(child.Key)
	return privKey, nil
}

// PubKeyToAddress converts a secp256k1 public key to a bech32 address (EVM/Ethereum style).
// Uses keccak256(uncompressed_pubkey[1:])[-20:] — matches ethsecp256k1 address derivation.
func PubKeyToAddress(pubKey *secp256k1.PublicKey, prefix string) (string, error) {
	// Get uncompressed public key bytes (65 bytes: 04 || x || y)
	uncompressed := pubKey.SerializeUncompressed()

	// keccak256 of x||y (skip 04 prefix byte)
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	hash := h.Sum(nil)

	// Last 20 bytes is the address
	addr := hash[12:]

	// Convert to bech32
	conv, err := bech32.ConvertBits(addr, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("failed to convert bits: %w", err)
	}

	return bech32.Encode(prefix, conv)
}

// Keccak256 computes the Ethereum-compatible keccak256 hash of data.
func Keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// CreateKey creates a new key from a mnemonic and stores it
func (ks *Keystore) CreateKey(name, mnemonic string, force ...bool) (*Key, error) {
	// Derive private key
	privKey, err := DeriveKeyFromMnemonic(mnemonic, DefaultIndex, force...)
	if err != nil {
		return nil, err
	}

	// Get public key and address
	pubKey := privKey.PubKey()
	address, err := PubKeyToAddress(pubKey, Bech32PrefixAccAddr)
	if err != nil {
		return nil, err
	}

	// Encrypt private key
	privKeyEnc, salt, err := ks.encryptPrivKey(privKey.Serialize())
	if err != nil {
		return nil, err
	}

	key := &Key{
		Name:       name,
		Address:    address,
		PubKeyHex:  hex.EncodeToString(pubKey.SerializeCompressed()),
		PrivKeyEnc: hex.EncodeToString(privKeyEnc),
		Salt:       hex.EncodeToString(salt),
	}

	// Save key
	if err := ks.saveKey(key); err != nil {
		return nil, err
	}

	// Return key with mnemonic for user to backup (not stored)
	keyWithMnemonic := *key
	keyWithMnemonic.Mnemonic = mnemonic
	return &keyWithMnemonic, nil
}

// ImportKey imports a key from a mnemonic.
// Set force=true to skip BIP39 checksum validation.
func (ks *Keystore) ImportKey(name, mnemonic string, force ...bool) (*Key, error) {
	return ks.CreateKey(name, mnemonic, force...)
}

// ImportPrivateKey imports a key from a raw hex-encoded secp256k1 private key.
func (ks *Keystore) ImportPrivateKey(name, privKeyHex string) (*Key, error) {
	privKeyHex = strings.TrimPrefix(strings.TrimSpace(privKeyHex), "0x")
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key hex: %w", err)
	}
	if len(privKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: expected 32 bytes, got %d", len(privKeyBytes))
	}

	privKey := secp256k1.PrivKeyFromBytes(privKeyBytes)
	pubKey := privKey.PubKey()
	address, err := PubKeyToAddress(pubKey, Bech32PrefixAccAddr)
	if err != nil {
		return nil, err
	}

	privKeyEnc, salt, err := ks.encryptPrivKey(privKey.Serialize())
	if err != nil {
		return nil, err
	}

	key := &Key{
		Name:       name,
		Address:    address,
		PubKeyHex:  hex.EncodeToString(pubKey.SerializeCompressed()),
		PrivKeyEnc: hex.EncodeToString(privKeyEnc),
		Salt:       hex.EncodeToString(salt),
	}

	if err := ks.saveKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

// GetKey retrieves a key by name
func (ks *Keystore) GetKey(name string) (*Key, error) {
	path := filepath.Join(ks.dir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}

	var key Key
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}

	return &key, nil
}

// GetPrivateKey retrieves and decrypts a private key
func (ks *Keystore) GetPrivateKey(name string) (*secp256k1.PrivateKey, error) {
	key, err := ks.GetKey(name)
	if err != nil {
		return nil, err
	}

	privKeyEnc, err := hex.DecodeString(key.PrivKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted key: %w", err)
	}

	salt, err := hex.DecodeString(key.Salt)
	if err != nil {
		return nil, fmt.Errorf("failed to decode salt: %w", err)
	}

	privKeyBytes, err := ks.decryptPrivKey(privKeyEnc, salt)
	if err != nil {
		return nil, err
	}

	return secp256k1.PrivKeyFromBytes(privKeyBytes), nil
}

// ListKeys lists all stored keys
func (ks *Keystore) ListKeys() ([]*Key, error) {
	entries, err := os.ReadDir(ks.dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read keystore dir: %w", err)
	}

	var keys []*Key
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".json")
		key, err := ks.GetKey(name)
		if err != nil {
			continue
		}
		// Don't include encrypted data in listing
		key.PrivKeyEnc = ""
		key.Salt = ""
		keys = append(keys, key)
	}

	return keys, nil
}

// DeleteKey removes a key
func (ks *Keystore) DeleteKey(name string) error {
	path := filepath.Join(ks.dir, name+".json")
	return os.Remove(path)
}

// saveKey saves a key to disk
func (ks *Keystore) saveKey(key *Key) error {
	data, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal key: %w", err)
	}

	path := filepath.Join(ks.dir, key.Name+".json")
	return os.WriteFile(path, data, 0600)
}

// encryptPrivKey encrypts a private key using AES-GCM with PBKDF2 key derivation
func (ks *Keystore) encryptPrivKey(privKey []byte) ([]byte, []byte, error) {
	// Generate salt
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive encryption key using PBKDF2
	password := ks.password
	if password == "" {
		return nil, nil, fmt.Errorf("keystore password is required; set via --password flag or KEYSTORE_PASSWORD environment variable")
	}
	encKey := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, keySize, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nonce, nonce, privKey, nil)
	return ciphertext, salt, nil
}

// decryptPrivKey decrypts a private key
func (ks *Keystore) decryptPrivKey(ciphertext, salt []byte) ([]byte, error) {
	// Derive encryption key using PBKDF2
	password := ks.password
	if password == "" {
		return nil, fmt.Errorf("keystore password is required; set via --password flag or KEYSTORE_PASSWORD environment variable")
	}
	encKey := pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, keySize, sha256.New)

	// Create AES cipher
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// Create GCM mode
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

// SignMessage signs a message with a private key using keccak256 (Ethereum/ethsecp256k1 style).
func SignMessage(privKey *secp256k1.PrivateKey, message []byte) ([]byte, error) {
	hash := Keccak256(message)
	sig := Sign(privKey, hash)
	return sig, nil
}

// Sign signs a hash with a private key (returns compact signature)
func Sign(privKey *secp256k1.PrivateKey, hash []byte) []byte {
	// Sign using ECDSA
	sig := signCompact(privKey, hash)
	return sig
}

// signCompact creates a compact 64-byte signature (R || S)
func signCompact(privKey *secp256k1.PrivateKey, hash []byte) []byte {
	// Create signature using ecdsa package
	sig := ecdsa.Sign(privKey, hash)

	// Serialize to compact format (64 bytes: R || S)
	var sigBytes [64]byte
	r := sig.R()
	s := sig.S()
	r.PutBytesUnchecked(sigBytes[:32])
	s.PutBytesUnchecked(sigBytes[32:])

	return sigBytes[:]
}

// GetAddressFromPubKeyHex converts a hex-encoded public key to a Cosmos address
func GetAddressFromPubKeyHex(pubKeyHex, prefix string) (string, error) {
	pubKeyBytes, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode pubkey hex: %w", err)
	}

	pubKey, err := secp256k1.ParsePubKey(pubKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse pubkey: %w", err)
	}

	return PubKeyToAddress(pubKey, prefix)
}

// BytesToBech32 converts raw bytes to a bech32 address
func BytesToBech32(prefix string, data []byte) (string, error) {
	conv, err := bech32.ConvertBits(data, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("failed to convert bits: %w", err)
	}
	return bech32.Encode(prefix, conv)
}

// Bech32ToBytes decodes a bech32 address and returns the HRP and raw address bytes.
func Bech32ToBytes(address string) (string, []byte, error) {
	hrp, data, err := bech32.Decode(address)
	if err != nil {
		return "", nil, fmt.Errorf("invalid bech32 address: %w", err)
	}
	conv, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert bits: %w", err)
	}
	return hrp, conv, nil
}
