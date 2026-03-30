// Package crypto provides lightweight transaction building and signing
package crypto

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// SIGN_MODE_DIRECT = 1 in cosmos.tx.signing.v1beta1.SignMode enum
const signModeDirect = 1

// TxBuilder helps construct and sign transactions using protobuf encoding
type TxBuilder struct {
	chainID  string
	denom    string
	gasLimit uint64
	gasPrice string
}

// NewTxBuilder creates a new transaction builder
func NewTxBuilder(chainID, denom string) *TxBuilder {
	return &TxBuilder{
		chainID:  chainID,
		denom:    denom,
		gasLimit: 200000,
		gasPrice: "0",
	}
}

// SetGasLimit sets the gas limit
func (b *TxBuilder) SetGasLimit(limit uint64) {
	b.gasLimit = limit
}

// SetGasPrice sets the gas price
func (b *TxBuilder) SetGasPrice(price string) {
	b.gasPrice = price
}

// BuildSignedTx builds and signs a transaction using protobuf binary encoding.
// msgs must be protobuf-encoded Any messages (use Msg* helpers which return []byte).
func (b *TxBuilder) BuildSignedTx(
	privKey *secp256k1.PrivateKey,
	pubKeyBytes []byte,
	accountNumber string,
	sequence string,
	msgs ...interface{},
) ([]byte, error) {
	// Convert msgs to protobuf-encoded Any bytes
	var msgAnys [][]byte
	for _, msg := range msgs {
		switch v := msg.(type) {
		case []byte:
			msgAnys = append(msgAnys, v)
		default:
			return nil, fmt.Errorf("BuildSignedTx: messages must be []byte (protobuf-encoded Any), got %T", msg)
		}
	}

	// Parse account number and sequence
	acctNum, err := strconv.ParseUint(accountNumber, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse account number: %w", err)
	}
	seq, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse sequence: %w", err)
	}

	// Calculate fee amount: gasPrice * gasLimit
	feeAmount := "0"
	if b.gasPrice != "0" && b.gasPrice != "" {
		gp := new(big.Int)
		if _, ok := gp.SetString(b.gasPrice, 10); ok {
			fee := new(big.Int).Mul(gp, new(big.Int).SetUint64(b.gasLimit))
			feeAmount = fee.String()
		}
	}

	// ---- Protobuf encode TxBody ----
	txBodyBytes := EncodeTxBody(msgAnys, "", 0)

	// ---- Protobuf encode AuthInfo ----
	// PubKey → Any (cosmos/evm uses cosmos.evm.crypto.v1.ethsecp256k1.PubKey)
	pubKeyProto := EncodePubKeySecp256k1(pubKeyBytes)
	pubKeyAny := EncodeAny("/cosmos.evm.crypto.v1.ethsecp256k1.PubKey", pubKeyProto)

	// ModeInfo (SIGN_MODE_DIRECT = 1)
	modeInfo := EncodeModeInfoSingle(signModeDirect)

	// SignerInfo
	signerInfo := EncodeSignerInfo(pubKeyAny, modeInfo, seq)

	// Fee
	coin := EncodeCoin(b.denom, feeAmount)
	fee := EncodeFee([][]byte{coin}, b.gasLimit)

	// AuthInfo
	authInfoBytes := EncodeAuthInfo([][]byte{signerInfo}, fee)

	// ---- Protobuf encode SignDoc ----
	signDocBytes := EncodeSignDoc(txBodyBytes, authInfoBytes, b.chainID, acctNum)

	// ---- Hash and sign ----
	// ethsecp256k1 accounts use keccak256 for SIGN_MODE_DIRECT
	hash := Keccak256(signDocBytes)
	sig := Sign(privKey, hash)

	// ---- Protobuf encode complete Tx ----
	txBytes := EncodeTx(txBodyBytes, authInfoBytes, [][]byte{sig})

	return txBytes, nil
}

// ============================================================================
// Message builders — return protobuf-encoded google.protobuf.Any
// ============================================================================

// MsgSend builds a bank send message (protobuf Any)
func MsgSend(fromAddr, toAddr, amount, denom string) []byte {
	coin := EncodeCoin(denom, amount)
	msgBytes := EncodeMsgSend(fromAddr, toAddr, [][]byte{coin})
	return EncodeAny("/cosmos.bank.v1beta1.MsgSend", msgBytes)
}

// MsgWithdrawReward builds a cosmos distribution withdraw delegator reward message (protobuf Any)
func MsgWithdrawReward(delegator, validator string) []byte {
	msgBytes := EncodeMsgWithdrawDelegatorReward(delegator, validator)
	return EncodeAny("/cosmos.distribution.v1beta1.MsgWithdrawDelegatorReward", msgBytes)
}

// MsgDelegate builds a cosmos staking delegate message (protobuf Any)
func MsgDelegate(delegator, validator, amount, denom string) []byte {
	coin := EncodeCoin(denom, amount)
	msgBytes := EncodeMsgDelegate(delegator, validator, coin)
	return EncodeAny("/cosmos.staking.v1beta1.MsgDelegate", msgBytes)
}

// MsgUndelegate builds a cosmos staking undelegate message (protobuf Any)
func MsgUndelegate(delegator, validator, amount, denom string) []byte {
	coin := EncodeCoin(denom, amount)
	msgBytes := EncodeMsgUndelegate(delegator, validator, coin)
	return EncodeAny("/cosmos.staking.v1beta1.MsgUndelegate", msgBytes)
}

// MsgStake builds a POH stake message (protobuf Any).
// endpoint is optional — only required for reporter nodes.
func MsgStake(miner, amount, endpoint string) []byte {
	msgBytes := EncodeMsgStake(miner, amount, endpoint)
	return EncodeAny("/cc_bc.poh.v1.MsgStake", msgBytes)
}

// MsgUnstake builds a POH unstake message (protobuf Any)
func MsgUnstake(miner string) []byte {
	msgBytes := EncodeMsgUnstake(miner)
	return EncodeAny("/cc_bc.poh.v1.MsgUnstake", msgBytes)
}

// MsgHeartbeat builds a POH heartbeat message (protobuf Any)
func MsgHeartbeat(miner string) []byte {
	msgBytes := EncodeMsgHeartbeat(miner)
	return EncodeAny("/cc_bc.poh.v1.MsgHeartbeat", msgBytes)
}

// MsgSubmitQuestion builds a QA submit question message (protobuf Any)
func MsgSubmitQuestion(author string, sessionID uint64, contentHash string) []byte {
	msgBytes := EncodeMsgSubmitQuestion(author, sessionID, contentHash)
	return EncodeAny("/cc_bc.qa.v1.MsgSubmitQuestion", msgBytes)
}

// MsgSubmitAnswer builds a QA submit answer message (protobuf Any)
func MsgSubmitAnswer(author string, sessionID uint64, contentHash string) []byte {
	msgBytes := EncodeMsgSubmitAnswer(author, sessionID, contentHash)
	return EncodeAny("/cc_bc.qa.v1.MsgSubmitAnswer", msgBytes)
}

// MsgCommitVote builds a QA commit vote message (protobuf Any)
func MsgCommitVote(voter string, sessionID uint64, phase, choice, salt string) []byte {
	voteHash := ComputeVoteHash(voter, sessionID, phase, choice, salt)
	msgBytes := EncodeMsgCommitVote(voter, sessionID, phase, voteHash)
	return EncodeAny("/cc_bc.qa.v1.MsgCommitVote", msgBytes)
}

// MsgRevealVote builds a QA reveal vote message (protobuf Any)
func MsgRevealVote(voter string, sessionID uint64, phase, choice, salt string) []byte {
	msgBytes := EncodeMsgRevealVote(voter, sessionID, phase, choice, salt)
	return EncodeAny("/cc_bc.qa.v1.MsgRevealVote", msgBytes)
}

// ============================================================================
// Utility functions
// ============================================================================

// ComputeVoteHash computes the vote hash for commit-reveal: sha256(voter:sessionID:phase:choice:salt).
// Matches chain-side verification in x/qa/keeper/msg_reveal_vote.go.
func ComputeVoteHash(voter string, sessionID uint64, phase, choice, salt string) string {
	input := fmt.Sprintf("%s:%d:%s:%s:%s", voter, sessionID, phase, choice, salt)
	hash := sha256.Sum256([]byte(input))
	return hex.EncodeToString(hash[:])
}

// HeartbeatRequest represents a P2P heartbeat request
type HeartbeatRequest struct {
	Address   string `json:"address"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
	Pubkey    string `json:"pubkey"`
}

// BuildHeartbeatRequest creates a signed heartbeat request for the P2P endpoint
// The signature is over: "cc_bc/heartbeat:" + address + ":" + timestamp
func BuildHeartbeatRequest(privKey *secp256k1.PrivateKey, address string, pubKeyBytes []byte) (*HeartbeatRequest, error) {
	timestamp := time.Now().Unix()

	// Build the message to sign
	signMsg := fmt.Sprintf("cc_bc/heartbeat:%s:%d", address, timestamp)

	// Sign the message
	sig, err := SignMessage(privKey, []byte(signMsg))
	if err != nil {
		return nil, fmt.Errorf("sign heartbeat: %w", err)
	}

	return &HeartbeatRequest{
		Address:   address,
		Timestamp: timestamp,
		Signature: base64.StdEncoding.EncodeToString(sig),
		Pubkey:    base64.StdEncoding.EncodeToString(pubKeyBytes),
	}, nil
}

// GenerateSalt generates a cryptographically secure random salt for commit-reveal voting
func GenerateSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// BuildContentRequest creates a signed P2P content submission request.
// Signature format: keccak256("cc_bc/content:{session_id}:{content_type}:{author}:{content_hash}")
// Matches chain-side MakeContentSignedData in x/qa/types/p2p_wrap.go.
func BuildContentRequest(
	privKey *secp256k1.PrivateKey,
	pubKeyBytes []byte,
	sessionID uint64,
	author, contentType, content, contentHash string,
) (*ContentRequest, error) {
	signedData := []byte(fmt.Sprintf("cc_bc/content:%d:%s:%s:%s", sessionID, contentType, author, contentHash))
	sig, err := SignMessage(privKey, signedData)
	if err != nil {
		return nil, fmt.Errorf("sign content: %w", err)
	}

	return &ContentRequest{
		SessionID:   sessionID,
		Author:      author,
		ContentType: contentType,
		Content:     content,
		ContentHash: contentHash,
		Signature:   base64.StdEncoding.EncodeToString(sig),
		Pubkey:      base64.StdEncoding.EncodeToString(pubKeyBytes),
	}, nil
}

// ContentRequest is the JSON body for the P2P content injection endpoint.
type ContentRequest struct {
	SessionID   uint64 `json:"session_id"`
	Author      string `json:"author"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
	Signature   string `json:"signature"`
	Pubkey      string `json:"pubkey"`
}
