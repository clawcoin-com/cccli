// Package miner provides miner client functionality
package miner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/clawcoin-com/cccli/internal/client"
	"github.com/clawcoin-com/cccli/internal/config"
	"github.com/clawcoin-com/cccli/internal/crypto"
)

// Miner handles miner operations (independent of cc_bcd binary)
type Miner struct {
	cfg       *config.Config
	client    *client.Client
	keystore  *crypto.Keystore
	txBuilder *crypto.TxBuilder
	keyName   string
	address   string
	pubKeyHex string

	// State tracking
	lastHeartbeat     time.Time
	heartbeatCount    int
	heartbeatJustSent bool
	heartbeatMode     string // "chain" or "p2p"
	actedSessions     map[string]bool
	voteSecrets       map[string]VoteSecret
	secretsFile       string
}

// VoteSecret stores commit-reveal vote data
type VoteSecret struct {
	Choice string
	Salt   string
}

// New creates a new Miner instance using keystore (no cc_bcd dependency)
func New(cfg *config.Config, cli *client.Client, ks *crypto.Keystore, keyName string) (*Miner, error) {
	keyInfo, err := ks.GetKey(keyName)
	if err != nil {
		return nil, fmt.Errorf("key '%s' not found in keystore: %w\nUse 'cccli wallet create-key' or 'cccli wallet import-key' first", keyName, err)
	}

	tb := crypto.NewTxBuilder(cfg.ChainID, cfg.Denom)
	if cfg.Gas > 0 {
		tb.SetGasLimit(cfg.Gas)
	}
	if cfg.GasPrice != "" {
		tb.SetGasPrice(cfg.GasPrice)
	}

	m := &Miner{
		cfg:           cfg,
		client:        cli,
		keystore:      ks,
		txBuilder:     tb,
		keyName:       keyName,
		address:       keyInfo.Address,
		pubKeyHex:     keyInfo.PubKeyHex,
		actedSessions: make(map[string]bool),
		voteSecrets:   make(map[string]VoteSecret),
		secretsFile:   filepath.Join(cfg.HomeDir, fmt.Sprintf("vote_secrets_%s.txt", keyName)),
	}

	// Load persisted vote secrets from file
	m.loadVoteSecrets()

	return m, nil
}

// Address returns the miner's address
func (m *Miner) Address() string {
	return m.address
}

// ============================================================================
// Transaction helper — sign + broadcast via REST (no cc_bcd needed)
// ============================================================================

func (m *Miner) broadcastMsg(ctx context.Context, msg interface{}) (*client.TxResponse, error) {
	privKey, err := m.keystore.GetPrivateKey(m.keyName)
	if err != nil {
		return nil, fmt.Errorf("get private key: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(m.pubKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode pubkey: %w", err)
	}

	return m.client.ExecTx(ctx, m.address, func(acct *client.AccountInfo) ([]byte, error) {
		return m.txBuilder.BuildSignedTx(privKey, pubKeyBytes, acct.AccountNumber, acct.Sequence, msg)
	})
}

// ============================================================================
// Miner operations
// ============================================================================

// Stake stakes the given amount. endpoint is optional (required for reporter nodes).
func (m *Miner) Stake(ctx context.Context, amount, endpoint string) error {
	txRandomDelay()
	msg := crypto.MsgStake(m.address, amount, endpoint)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("stake failed (code=%d): %s", resp.Code, resp.RawLog)
	}
	return nil
}

// Unstake unstakes all tokens
func (m *Miner) Unstake(ctx context.Context) error {
	txRandomDelay()
	msg := crypto.MsgUnstake(m.address)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("unstake failed (code=%d): %s", resp.Code, resp.RawLog)
	}
	return nil
}

// SendHeartbeat sends a heartbeat via P2P mode directly.
func (m *Miner) SendHeartbeat(ctx context.Context) error {
	m.heartbeatJustSent = false

	// Check if enough time has passed
	if time.Since(m.lastHeartbeat) < time.Duration(m.cfg.HeartbeatInterval)*time.Second {
		return nil
	}

	return m.sendP2PHeartbeat(ctx)
}

// sendP2PHeartbeat sends heartbeat via REST API with proper signing
func (m *Miner) sendP2PHeartbeat(ctx context.Context) error {
	privKey, err := m.keystore.GetPrivateKey(m.keyName)
	if err != nil {
		return fmt.Errorf("get private key for P2P heartbeat: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(m.pubKeyHex)
	if err != nil {
		return fmt.Errorf("decode pubkey: %w", err)
	}

	req, err := crypto.BuildHeartbeatRequest(privKey, m.address, pubKeyBytes)
	if err != nil {
		return fmt.Errorf("build heartbeat request: %w", err)
	}

	restURL := m.client.RestURL()
	url := fmt.Sprintf("%s/cc_bc/v1/hb/p2p_heartbeat", restURL)
	respBytes, err := m.client.HTTPPost(ctx, url, req)
	if err != nil {
		return err
	}

	var result struct {
		Accepted   bool   `json:"accepted"`
		Reason     string `json:"reason"`
		RetryAfter int    `json:"retry_after"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return err
	}

	if result.Accepted {
		m.heartbeatCount++
		m.lastHeartbeat = time.Now()
		m.heartbeatJustSent = true
		m.heartbeatMode = "p2p"
		return nil
	}

	// Handle rate-limit: update lastHeartbeat to avoid frequent retries
	if result.Reason == "rate-limited" {
		if result.RetryAfter > 0 {
			m.lastHeartbeat = time.Now().Add(time.Duration(result.RetryAfter-m.cfg.HeartbeatInterval) * time.Second)
		} else {
			m.lastHeartbeat = time.Now()
		}
		return nil
	}

	return fmt.Errorf("P2P heartbeat rejected: %s", result.Reason)
}

// HeartbeatJustSent returns true if the last SendHeartbeat call actually sent a heartbeat.
func (m *Miner) HeartbeatJustSent() bool {
	return m.heartbeatJustSent
}

// HeartbeatMode returns the mode of the last heartbeat ("chain" or "p2p").
func (m *Miner) HeartbeatMode() string {
	return m.heartbeatMode
}

// HeartbeatCount returns the number of successful heartbeats
func (m *Miner) HeartbeatCount() int {
	return m.heartbeatCount
}

// ============================================================================
// Sweep (auto-transfer excess balance to human wallet)
// ============================================================================

// SweepExcess checks the miner's balance and, if it exceeds the threshold,
// sends the excess (balance - keepCC) to the specified address.
// Returns whether a transfer was executed, the amount transferred (in acc), and any error.
func (m *Miner) SweepExcess(ctx context.Context, toAddress string, thresholdCC, keepCC int) (swept bool, amount string, err error) {
	balances, err := m.client.GetBalance(ctx, m.address)
	if err != nil {
		return false, "", fmt.Errorf("sweep: get balance: %w", err)
	}

	// Find balance for the configured denom
	var balanceAcc string
	for _, b := range balances {
		if b.Denom == m.cfg.Denom {
			balanceAcc = b.Amount
			break
		}
	}
	if balanceAcc == "" {
		return false, "", nil // no balance
	}

	balance := new(big.Int)
	if _, ok := balance.SetString(balanceAcc, 10); !ok {
		return false, "", fmt.Errorf("sweep: invalid balance amount: %s", balanceAcc)
	}

	// Convert thresholdCC and keepCC to acc (×1e18)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	thresholdAcc := new(big.Int).Mul(big.NewInt(int64(thresholdCC)), scale)

	if balance.Cmp(thresholdAcc) < 0 {
		return false, "", nil // below threshold
	}

	keepAcc := new(big.Int).Mul(big.NewInt(int64(keepCC)), scale)
	sweepAmount := new(big.Int).Sub(balance, keepAcc)
	if sweepAmount.Sign() <= 0 {
		return false, "", nil
	}

	// Convert EVM address to Cosmos address if needed
	cosmosTo, err := evmToCosmosAddress(toAddress)
	if err != nil {
		return false, "", fmt.Errorf("sweep: address conversion: %w", err)
	}

	// Build and broadcast MsgSend
	sweepAmountStr := sweepAmount.String()
	msg := crypto.MsgSend(m.address, cosmosTo, sweepAmountStr, m.cfg.Denom)
	txRandomDelay()
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return false, "", fmt.Errorf("sweep: broadcast: %w", err)
	}
	if resp.Code != 0 {
		return false, "", fmt.Errorf("sweep: tx failed (code=%d): %s", resp.Code, resp.RawLog)
	}

	return true, sweepAmountStr, nil
}

// evmToCosmosAddress converts an EVM 0x address to a Cosmos cc1 address.
// If the address is already a Cosmos address, it is returned as-is.
func evmToCosmosAddress(addr string) (string, error) {
	if strings.HasPrefix(addr, crypto.Bech32PrefixAccAddr+"1") {
		return addr, nil
	}

	if !strings.HasPrefix(addr, "0x") && !strings.HasPrefix(addr, "0X") {
		return "", fmt.Errorf("invalid address format: must start with '%s1' (Cosmos) or '0x' (EVM), got %q", crypto.Bech32PrefixAccAddr, addr)
	}

	evmHex := addr[2:] // strip "0x" / "0X"
	addrBytes, err := hex.DecodeString(evmHex)
	if err != nil {
		return "", fmt.Errorf("invalid EVM address hex: %w", err)
	}
	if len(addrBytes) != 20 {
		return "", fmt.Errorf("EVM address must be 20 bytes, got %d", len(addrBytes))
	}

	return crypto.BytesToBech32(crypto.Bech32PrefixAccAddr, addrBytes)
}

// ============================================================================
// QA Session operations
// ============================================================================

// Session represents a QA session (mirrors client.Session for miner use).
type Session struct {
	ID                 string   `json:"id"`
	Phase              string   `json:"phase"`
	Questioners        []string `json:"questioners"`
	QuestionVoters     []string `json:"question_voters"`
	Answerers          []string `json:"answerers"`
	AnswerVoters       []string `json:"answer_voters"`
	BestQuestionAuthor string   `json:"best_question_author"`
	BestAnswerAuthor   string   `json:"best_answer_author"`
	TopicID            string   `json:"topic_id"`
	TopicTitle         string   `json:"topic_title"`
	TopicDescription   string   `json:"topic_description"`
}

// GetSessions retrieves QA sessions for this miner via /cc_bc/v1/qa/my_sessions.
func (m *Miner) GetSessions(ctx context.Context) ([]Session, error) {
	clientSessions, err := m.client.GetMySessions(ctx, m.address)
	if err != nil {
		return nil, err
	}

	var sessions []Session
	for _, cs := range clientSessions {
		sessions = append(sessions, Session{
			ID:                 cs.ID,
			Phase:              cs.Phase,
			Questioners:        cs.Questioners,
			QuestionVoters:     cs.QuestionVoters,
			Answerers:          cs.Answerers,
			AnswerVoters:       cs.AnswerVoters,
			BestQuestionAuthor: cs.BestQuestionAuthor,
			BestAnswerAuthor:   cs.BestAnswerAuthor,
			TopicID:            cs.TopicID,
			TopicTitle:         cs.TopicTitle,
			TopicDescription:   cs.TopicDescription,
		})
	}
	return sessions, nil
}

func parseSessionID(sessionID string) (uint64, error) {
	return strconv.ParseUint(sessionID, 10, 64) //nolint:wrapcheck
}

// SubmitQuestion submits a question for a session
func (m *Miner) SubmitQuestion(ctx context.Context, sessionID, contentHash string) error {
	txRandomDelay()
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return fmt.Errorf("invalid session ID: %w", err)
	}

	msg := crypto.MsgSubmitQuestion(m.address, sid, contentHash)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return err
	}

	if resp.Code != 0 {
		if isAlreadySubmittedError(resp.RawLog) {
			return nil
		}
		return fmt.Errorf("submit question failed: %s", resp.RawLog)
	}
	return nil
}

// SubmitAnswer submits an answer for a session
func (m *Miner) SubmitAnswer(ctx context.Context, sessionID, contentHash string) error {
	txRandomDelay()
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return fmt.Errorf("invalid session ID: %w", err)
	}

	msg := crypto.MsgSubmitAnswer(m.address, sid, contentHash)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return err
	}

	if resp.Code != 0 {
		if isAlreadySubmittedError(resp.RawLog) {
			return nil
		}
		return fmt.Errorf("submit answer failed: %s", resp.RawLog)
	}
	return nil
}

// CommitVote commits a vote for a session.
// If a secret already exists (from a previous attempt or loaded from file),
// re-broadcasts the same commit to confirm it landed on-chain.
//
// Return values:
//   - (true,  nil) — commit confirmed on-chain ("already committed"); caller should stop retrying.
//   - (false, nil) — broadcast sent (code=0 = CheckTx passed); NOT confirmed yet, caller should retry.
//   - (false, err) — error; caller should retry next poll.
func (m *Miner) CommitVote(ctx context.Context, sessionID, voteType, choice string) (bool, error) {
	key := fmt.Sprintf("%s:%s", sessionID, voteType)
	secret, exists := m.voteSecrets[key]

	if !exists {
		// First attempt: generate salt and persist BEFORE broadcasting.
		salt, err := generateSalt()
		if err != nil {
			return false, err
		}
		secret = VoteSecret{Choice: choice, Salt: salt}
		m.voteSecrets[key] = secret
		m.saveVoteSecret(sessionID, voteType, choice, salt)
	}
	// When secret exists: re-broadcast with saved choice+salt (retry).

	txRandomDelay()
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return false, fmt.Errorf("invalid session ID: %w", err)
	}

	msg := crypto.MsgCommitVote(m.address, sid, voteType, secret.Choice, secret.Salt)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		// Network/transport error — secret stays, will retry next poll.
		return false, err
	}

	if resp.Code != 0 {
		if isAlreadySubmittedError(resp.RawLog) {
			if !exists {
				// We just generated a NEW secret but chain already has a commit
				// from a previous run whose secret was lost (file deleted/corrupted).
				// Our new secret won't match the on-chain hash. Vote is unrecoverable.
				// Discard the wrong secret so RevealVote naturally no-ops.
				fmt.Printf("[WARN] Commit exists on chain but local secret was lost for %s:%s; vote unrecoverable\n", sessionID, voteType)
				delete(m.voteSecrets, key)
				m.rewriteSecretsFile()
			}
			// Confirmed on-chain — caller should stop retrying.
			return true, nil
		}
		// Code 19 = TxInMempoolError: tx already in mempool, waiting to be included.
		// Treat as in-progress — not an error, just retry next poll.
		if resp.Code == 19 {
			return false, nil
		}
		return false, fmt.Errorf("commit vote failed (code %d): %s", resp.Code, resp.RawLog)
	}

	// code=0: CheckTx passed, but DeliverTx not yet confirmed.
	// Caller should keep retrying until "already committed" confirms delivery.
	return false, nil
}

// RevealVote reveals a previously committed vote.
// Returns (true, nil) if a reveal was attempted, (false, nil) if no secret
// exists (already revealed or commit was never done), or (false, err) on error.
func (m *Miner) RevealVote(ctx context.Context, sessionID, voteType string) (bool, error) {
	key := fmt.Sprintf("%s:%s", sessionID, voteType)
	secret, ok := m.voteSecrets[key]
	if !ok {
		// No secret means reveal already succeeded or commit was never done.
		return false, nil
	}

	txRandomDelay()
	sid, err := parseSessionID(sessionID)
	if err != nil {
		return false, fmt.Errorf("invalid session ID: %w", err)
	}

	msg := crypto.MsgRevealVote(m.address, sid, voteType, secret.Choice, secret.Salt)
	resp, err := m.broadcastMsg(ctx, msg)
	if err != nil {
		return false, err
	}

	if resp.Code != 0 {
		if isAlreadySubmittedError(resp.RawLog) {
			// Chain already has this reveal — safe to discard the secret
			delete(m.voteSecrets, key)
			m.rewriteSecretsFile()
			return true, nil
		}
		// Code 19 = TxInMempoolError: tx already in mempool, waiting to be included.
		// The previous broadcast is pending — treat the same as code=0.
		if resp.Code == 19 {
			delete(m.voteSecrets, key)
			m.rewriteSecretsFile()
			return true, nil
		}
		return false, fmt.Errorf("reveal vote failed (code %d): %s", resp.Code, resp.RawLog)
	}

	// code=0: CheckTx passed. Delete secret to avoid redundant retries.
	// In the rare case DeliverTx fails, the vote is lost for this session,
	// which is acceptable vs. broadcasting 10+ redundant reveals per poll.
	delete(m.voteSecrets, key)
	m.rewriteSecretsFile()

	return true, nil
}

// ============================================================================
// P2P Content operations
// ============================================================================

// ContentItem represents a P2P content entry (mirrors client.ContentItem).
type ContentItem struct {
	Author      string
	Content     string
	ContentHash string
}

// PostContent submits content to the P2P content cache after a successful TX.
// Signs the content with the miner's private key (keccak256 over the signed-data string).
func (m *Miner) PostContent(ctx context.Context, sessionID uint64, contentType, content, contentHash string) error {
	privKey, err := m.keystore.GetPrivateKey(m.keyName)
	if err != nil {
		return fmt.Errorf("get private key for content: %w", err)
	}

	pubKeyBytes, err := hex.DecodeString(m.pubKeyHex)
	if err != nil {
		return fmt.Errorf("decode pubkey: %w", err)
	}

	req, err := crypto.BuildContentRequest(privKey, pubKeyBytes, sessionID, m.address, contentType, content, contentHash)
	if err != nil {
		return fmt.Errorf("build content request: %w", err)
	}

	restURL := m.client.RestURL()
	url := fmt.Sprintf("%s/cc_bc/v1/qa/p2p_content", restURL)
	respBytes, err := m.client.HTTPPost(ctx, url, req)
	if err != nil {
		return fmt.Errorf("post p2p content: %w", err)
	}

	var result struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return fmt.Errorf("parse content response: %w", err)
	}
	if !result.Accepted {
		return fmt.Errorf("content rejected: %s", result.Reason)
	}
	return nil
}

// GetSubmissions queries on-chain submissions for a session.
// subType must be "question" or "answer".
func (m *Miner) GetSubmissions(ctx context.Context, sessionID uint64, subType string) ([]client.SubmissionEntry, error) {
	return m.client.GetSubmissions(ctx, sessionID, subType)
}

// GetContent fetches content items from the P2P content cache for a session.
// contentType must be "question" or "answer".
func (m *Miner) GetContent(ctx context.Context, sessionID uint64, contentType string) ([]ContentItem, error) {
	restURL := m.client.RestURL()
	url := fmt.Sprintf("%s/cc_bc/v1/qa/p2p_content/%d?type=%s", restURL, sessionID, contentType)
	data, err := m.client.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("get p2p content: %w", err)
	}

	var items []struct {
		Author      string `json:"author"`
		Content     string `json:"content"`
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse content items: %w", err)
	}

	result := make([]ContentItem, len(items))
	for i, it := range items {
		result[i] = ContentItem{Author: it.Author, Content: it.Content, ContentHash: it.ContentHash}
	}
	return result, nil
}

// ============================================================================
// Role checks
// ============================================================================

// IsQuestioner checks if this miner is a questioner for the session
func (m *Miner) IsQuestioner(session *Session) bool {
	for _, q := range session.Questioners {
		if q == m.address {
			return true
		}
	}
	return false
}

// IsAnswerer checks if this miner is an answerer for the session
func (m *Miner) IsAnswerer(session *Session) bool {
	for _, a := range session.Answerers {
		if a == m.address {
			return true
		}
	}
	return false
}

// IsQuestionVoter checks if this miner is a question voter for the session
func (m *Miner) IsQuestionVoter(session *Session) bool {
	for _, v := range session.QuestionVoters {
		if v == m.address {
			return true
		}
	}
	return false
}

// IsAnswerVoter checks if this miner is an answer voter for the session
func (m *Miner) IsAnswerVoter(session *Session) bool {
	for _, v := range session.AnswerVoters {
		if v == m.address {
			return true
		}
	}
	return false
}

// HasVoteSecret checks if a vote secret exists for a session+type.
func (m *Miner) HasVoteSecret(sessionID, voteType string) bool {
	key := fmt.Sprintf("%s:%s", sessionID, voteType)
	_, ok := m.voteSecrets[key]
	return ok
}

// MarkActed marks a session action as completed
func (m *Miner) MarkActed(sessionID, action string) {
	key := fmt.Sprintf("%s:%s", sessionID, action)
	m.actedSessions[key] = true
}

// HasActed checks if a session action was already done
func (m *Miner) HasActed(sessionID, action string) bool {
	key := fmt.Sprintf("%s:%s", sessionID, action)
	return m.actedSessions[key]
}

// ============================================================================
// Vote secrets file persistence
// ============================================================================

// loadVoteSecrets reads persisted vote secrets from file
func (m *Miner) loadVoteSecrets() {
	f, err := os.Open(m.secretsFile)
	if err != nil {
		return // File doesn't exist yet, that's fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: sessionID:voteType:choice:salt
		parts := strings.SplitN(line, ":", 4)
		if len(parts) != 4 {
			continue
		}
		key := fmt.Sprintf("%s:%s", parts[0], parts[1])
		m.voteSecrets[key] = VoteSecret{Choice: parts[2], Salt: parts[3]}
	}
}

// rewriteSecretsFile rewrites the secrets file with only the remaining in-memory entries.
// Uses atomic write (temp file + rename) to prevent data loss on crash.
func (m *Miner) rewriteSecretsFile() {
	tmpFile := m.secretsFile + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Printf("[WARN] Failed to create temp secrets file: %v\n", err)
		return
	}

	for key, secret := range m.voteSecrets {
		// key format is "sessionID:voteType"
		line := fmt.Sprintf("%s:%s:%s\n", key, secret.Choice, secret.Salt)
		if _, err := f.WriteString(line); err != nil {
			fmt.Printf("[WARN] Failed to write secret entry: %v\n", err)
			f.Close()
			os.Remove(tmpFile)
			return
		}
	}
	if err := f.Close(); err != nil {
		fmt.Printf("[WARN] Failed to close temp secrets file: %v\n", err)
		os.Remove(tmpFile)
		return
	}
	if err := os.Rename(tmpFile, m.secretsFile); err != nil {
		fmt.Printf("[WARN] Failed to rename temp secrets file: %v\n", err)
		os.Remove(tmpFile)
	}
}

// CleanupStaleSessions removes actedSessions and voteSecrets entries
// for sessions that are no longer active (settled or expired).
func (m *Miner) CleanupStaleSessions(activeSessionIDs map[string]bool) {
	for key := range m.actedSessions {
		// key format is "sessionID:action"
		parts := strings.SplitN(key, ":", 2)
		if !activeSessionIDs[parts[0]] {
			delete(m.actedSessions, key)
		}
	}

	// Also clean up vote secrets for sessions no longer active
	needRewrite := false
	for key := range m.voteSecrets {
		// key format is "sessionID:voteType"
		parts := strings.SplitN(key, ":", 2)
		if !activeSessionIDs[parts[0]] {
			delete(m.voteSecrets, key)
			needRewrite = true
		}
	}
	if needRewrite {
		m.rewriteSecretsFile()
	}
}

// saveVoteSecret appends a vote secret to the persistence file
func (m *Miner) saveVoteSecret(sessionID, voteType, choice, salt string) {
	f, err := os.OpenFile(m.secretsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Printf("[WARN] Failed to save vote secret: %v\n", err)
		return
	}
	defer f.Close()

	line := fmt.Sprintf("%s:%s:%s:%s\n", sessionID, voteType, choice, salt)
	if _, err := f.WriteString(line); err != nil {
		fmt.Printf("[WARN] Failed to write vote secret: %v\n", err)
	}
}

// ============================================================================
// Utility functions
// ============================================================================

// generateSalt generates a cryptographically secure random salt (32 hex chars = 128 bit entropy)
func generateSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// txRandomDelay adds 0.1-1.0s random delay before TX to prevent mempool collisions
func txRandomDelay() {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		time.Sleep(500 * time.Millisecond) // fallback delay
		return
	}
	n := binary.LittleEndian.Uint64(b[:])
	delayMs := 100 + (n % 901)
	time.Sleep(time.Duration(delayMs) * time.Millisecond)
}

// isAlreadySubmittedError checks if a TX error indicates the action was already
// done on-chain. Must NOT match mempool/transport errors like "duplicate tx"
// which do not confirm the action succeeded.
func isAlreadySubmittedError(rawLog string) bool {
	lower := strings.ToLower(rawLog)
	return strings.Contains(lower, "already submitted") ||
		strings.Contains(lower, "already committed") ||
		strings.Contains(lower, "already revealed")
}
