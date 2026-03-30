package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/clawcoin-com/cccli/internal/llm"
	"github.com/clawcoin-com/cccli/internal/miner"
)

// isSequenceMismatch checks if an error is a Cosmos SDK account sequence mismatch.
func isSequenceMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "account sequence mismatch")
}

// logInfo writes a timestamped INFO line to logOut.
func logInfo(format string, args ...interface{}) {
	now := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(logOut, "[%s][INFO] %s", now, msg)
}

// logWarn writes a timestamped WARN line to both logOut (main log) and errOut (error-only log).
func logWarn(format string, args ...interface{}) {
	now := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s][WARN] %s", now, msg)
	fmt.Fprint(logOut, line)
	if errOut != nil {
		fmt.Fprint(errOut, line)
	}
}

// Package-level clients initialized from config
var (
	llmClient *llm.Client
)

// runMinerLoop runs the main miner loop
func runMinerLoop(ctx context.Context, m *miner.Miner) error {
	// Initialize LLM client from config
	llmClient = llm.NewClient(cfg.LLMProvider, cfg.LLMAPIBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMThinking)

	// Log sweep configuration
	if cfg.SweepTo != "" {
		logInfo("Sweep enabled: to=%s, threshold=%d CC, keep=%d CC\n", cfg.SweepTo, cfg.SweepThreshold, cfg.SweepKeep)
		if cfg.SweepKeep >= cfg.SweepThreshold {
			logWarn("sweep_keep (%d) >= sweep_threshold (%d), sweep will never trigger\n", cfg.SweepKeep, cfg.SweepThreshold)
		}
	}

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	var pollCount int

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sigCh:
			logInfo("Shutting down...\n")
			return nil
		case <-ticker.C:
			pollCount++

			// Send heartbeat
			if err := m.SendHeartbeat(ctx); err != nil {
				logWarn("Heartbeat error: %v\n", err)
			} else if m.HeartbeatJustSent() {
				logInfo("Heartbeat sent via %s (total: %d)\n", m.HeartbeatMode(), m.HeartbeatCount())
			}

			// Check and handle sessions
			sessionIDs, err := handleSessionsWithCount(ctx, m)
			if err != nil {
				logWarn("Session handling error: %v\n", err)
			} else if len(sessionIDs) > 0 {
				logInfo("Poll: %d active session(s): %s\n", len(sessionIDs), strings.Join(sessionIDs, ", "))
			} else {
				logInfo("Poll: no active sessions\n")
			}

			// Sweep excess balance to human wallet (every 10 polls)
			if cfg.SweepTo != "" && pollCount%10 == 0 {
				swept, amountAcc, err := m.SweepExcess(ctx, cfg.SweepTo, cfg.SweepThreshold, cfg.SweepKeep)
				if err != nil {
					if isSequenceMismatch(err) {
						logWarn("Sweep: sequence mismatch (harmless, will retry next round)\n")
					} else {
						logWarn("Sweep error: %v\n", err)
					}
				} else if swept {
					ccAmount := accToCC(amountAcc)
					logInfo("Swept %s CC to %s\n", ccAmount, cfg.SweepTo)
				}
			}
		}
	}
}

// phasePriority returns the processing priority for a session phase.
// Higher value = process first. Reveal > Commit > Submit
func phasePriority(phase string) int {
	switch phase {
	case "SESSION_PHASE_REVEAL_ANSWER":
		return 6
	case "SESSION_PHASE_REVEAL_QUESTION":
		return 5
	case "SESSION_PHASE_COMMIT_ANSWER":
		return 4
	case "SESSION_PHASE_COMMIT_QUESTION":
		return 3
	case "SESSION_PHASE_SUBMIT_ANSWER":
		return 2
	case "SESSION_PHASE_SUBMIT_QUESTION":
		return 1
	default:
		return 0
	}
}


// handleSessionsWithCount checks and processes active QA sessions, returning active session IDs.
func handleSessionsWithCount(ctx context.Context, m *miner.Miner) ([]string, error) {
	sessions, err := m.GetSessions(ctx)
	if err != nil {
		return nil, err
	}

	// Sort sessions by phase priority: Reveal > Commit > Submit
	sort.Slice(sessions, func(i, j int) bool {
		return phasePriority(sessions[i].Phase) > phasePriority(sessions[j].Phase)
	})

	// Build active session ID set and clean up stale acted/secrets state
	activeIDs := make(map[string]bool, len(sessions))
	var ids []string
	for _, session := range sessions {
		if session.Phase == "SESSION_PHASE_SETTLED" {
			continue
		}
		activeIDs[session.ID] = true
		ids = append(ids, session.ID)
	}
	m.CleanupStaleSessions(activeIDs)

	for _, session := range sessions {
		if session.Phase == "SESSION_PHASE_SETTLED" {
			continue
		}
		if err := handleSession(ctx, m, &session); err != nil {
			if isSequenceMismatch(err) {
				logWarn("Session %s: sequence mismatch (harmless, will retry next round)\n", session.ID)
			} else {
				logWarn("Session %s error: %v\n", session.ID, err)
			}
		}
	}

	return ids, nil
}

// handleSession processes a single session based on its phase.
func handleSession(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	switch session.Phase {
	case "SESSION_PHASE_REVEAL_QUESTION":
		return handleRevealQuestion(ctx, m, session)
	case "SESSION_PHASE_REVEAL_ANSWER":
		return handleRevealAnswer(ctx, m, session)
	case "SESSION_PHASE_COMMIT_QUESTION":
		return handleCommitQuestion(ctx, m, session)
	case "SESSION_PHASE_COMMIT_ANSWER":
		return handleCommitAnswer(ctx, m, session)
	case "SESSION_PHASE_SUBMIT_QUESTION":
		return handleSubmitQuestion(ctx, m, session)
	case "SESSION_PHASE_SUBMIT_ANSWER":
		return handleSubmitAnswer(ctx, m, session)
	}
	return nil
}

func handleSubmitQuestion(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if m.HasActed(session.ID, "submit_question") {
		return nil
	}

	if !m.IsQuestioner(session) {
		return nil
	}

	logInfo("Session %s: Selected as QUESTIONER\n", session.ID)

	// Generate question using LLM
	question, err := llmClient.GenerateQuestion(ctx, session.TopicTitle, session.TopicDescription, fmt.Sprintf("%s_%s", session.ID, time.Now().Format("20060102150405")))
	if err != nil {
		logWarn("LLM generate question failed: %v (skipping)\n", err)
		return nil // LLM failure: skip this round
	}

	contentHash := computeContentHash([]byte(question))
	sid := mustParseSessionID(session.ID)

	if err := m.SubmitQuestion(ctx, session.ID, contentHash); err != nil {
		return err
	}

	logInfo("Session %s: Question submitted (hash: %s)\n", session.ID, contentHash[:16])
	m.MarkActed(session.ID, "submit_question")

	// Push content to P2P cache so voters can evaluate it
	if err := m.PostContent(ctx, sid, "question", question, contentHash); err != nil {
		logWarn("Session %s: Failed to post question content: %v\n", session.ID, err)
	}

	return nil
}

func handleCommitQuestion(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if !m.IsQuestionVoter(session) {
		return nil
	}

	// Skip if commit already confirmed on-chain
	if m.HasActed(session.ID, "commit_question_confirmed") {
		return nil
	}

	isNew := !m.HasVoteSecret(session.ID, "question")

	// Only run expensive LLM evaluation on first attempt.
	// When secret already exists, CommitVote re-broadcasts with saved secret.
	var choice string
	if isNew {
		sid := mustParseSessionID(session.ID)
		choice = evaluateCandidates(ctx, m, sid, "question", "", session.TopicTitle, session.TopicDescription)
		if choice == "" {
			return nil
		}
	}

	confirmed, err := m.CommitVote(ctx, session.ID, "question", choice)
	if err != nil {
		return err
	}
	if isNew {
		logInfo("Session %s: Question vote committed for %s\n", session.ID, choice)
	}
	if confirmed {
		m.MarkActed(session.ID, "commit_question_confirmed")
	}
	return nil
}

func handleRevealQuestion(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if !m.IsQuestionVoter(session) {
		return nil
	}

	// Don't use MarkActed for reveals: BROADCAST_MODE_SYNC code=0 only means
	// CheckTx passed. If DeliverTx fails we need to retry. RevealVote returns
	// (false, nil) when no secret exists (already done), so we only log on
	// actual attempts.
	attempted, err := m.RevealVote(ctx, session.ID, "question")
	if err != nil {
		return err
	}
	if attempted {
		logInfo("Session %s: Question vote revealed\n", session.ID)
	}
	return nil
}

func handleSubmitAnswer(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if m.HasActed(session.ID, "submit_answer") {
		return nil
	}

	if !m.IsAnswerer(session) {
		return nil
	}

	logInfo("Session %s: Selected as ANSWERER\n", session.ID)

	sid := mustParseSessionID(session.ID)

	// Fetch actual best question from P2P content cache
	questionText := fetchBestQuestion(ctx, m, sid, session.BestQuestionAuthor)

	answer, err := llmClient.GenerateAnswer(ctx, session.TopicTitle, session.TopicDescription, questionText)
	if err != nil {
		logWarn("LLM generate answer failed: %v (skipping)\n", err)
		return nil
	}

	contentHash := computeContentHash([]byte(answer))

	if err := m.SubmitAnswer(ctx, session.ID, contentHash); err != nil {
		return err
	}

	logInfo("Session %s: Answer submitted (hash: %s)\n", session.ID, contentHash[:16])
	m.MarkActed(session.ID, "submit_answer")

	// Push answer content to P2P cache so voters can evaluate it
	if err := m.PostContent(ctx, sid, "answer", answer, contentHash); err != nil {
		logWarn("Session %s: Failed to post answer content: %v\n", session.ID, err)
	}

	return nil
}

func handleCommitAnswer(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if !m.IsAnswerVoter(session) {
		return nil
	}

	// Skip if commit already confirmed on-chain
	if m.HasActed(session.ID, "commit_answer_confirmed") {
		return nil
	}

	isNew := !m.HasVoteSecret(session.ID, "answer")

	var choice string
	if isNew {
		sid := mustParseSessionID(session.ID)
		// Fetch best question text for context
		questionContext := ""
		if session.BestQuestionAuthor != "" {
			items, err := m.GetContent(ctx, sid, "question")
			if err == nil {
				for _, item := range items {
					if item.Author == session.BestQuestionAuthor && item.Content != "" {
						questionContext = item.Content
						break
					}
				}
			}
		}
		choice = evaluateCandidates(ctx, m, sid, "answer", questionContext, session.TopicTitle, session.TopicDescription)
		if choice == "" {
			return nil
		}
	}

	confirmed, err := m.CommitVote(ctx, session.ID, "answer", choice)
	if err != nil {
		return err
	}
	if isNew {
		logInfo("Session %s: Answer vote committed for %s\n", session.ID, choice)
	}
	if confirmed {
		m.MarkActed(session.ID, "commit_answer_confirmed")
	}
	return nil
}

func handleRevealAnswer(ctx context.Context, m *miner.Miner, session *miner.Session) error {
	if !m.IsAnswerVoter(session) {
		return nil
	}

	// Don't use MarkActed for reveals — see handleRevealQuestion comment.
	attempted, err := m.RevealVote(ctx, session.ID, "answer")
	if err != nil {
		return err
	}
	if attempted {
		logInfo("Session %s: Answer vote revealed\n", session.ID)
	}
	return nil
}

// fetchBestQuestion fetches the best question text from the P2P content cache.
// Matches bash miner_client.sh: try best_q_author → first available → "" (no generic fallback).
func fetchBestQuestion(ctx context.Context, m *miner.Miner, sessionID uint64, bestQuestionAuthor string) string {
	items, err := m.GetContent(ctx, sessionID, "question")
	if err != nil {
		logWarn("Session %d: Failed to fetch question content: %v\n", sessionID, err)
		return ""
	}

	// Try best question author first
	if bestQuestionAuthor != "" {
		for _, item := range items {
			if item.Author == bestQuestionAuthor && item.Content != "" {
				return item.Content
			}
		}
	}

	// Fallback to first available
	if len(items) > 0 && items[0].Content != "" {
		return items[0].Content
	}

	return ""
}

// evaluateCandidates queries on-chain submissions and uses LLM to pick the best.
// questionContext is passed for answer evaluation (the question being answered); empty for question evaluation.
// Returns "" if no submissions or on error (caller should skip voting).
func evaluateCandidates(ctx context.Context, m *miner.Miner, sessionID uint64, contentType, questionContext, topicTitle, topicDescription string) string {
	// 1. Query on-chain submissions (matching bash miner_client.sh behavior)
	subs, err := m.GetSubmissions(ctx, sessionID, contentType)
	if err != nil {
		logWarn("Failed to query %s submissions: %v (skipping vote)\n", contentType, err)
		return ""
	}
	if len(subs) == 0 {
		return ""
	}

	// Build candidate list from actual submitters
	candidates := make([]string, len(subs))
	for i, s := range subs {
		candidates[i] = s.Author
	}

	if len(candidates) == 1 {
		return candidates[0]
	}

	// 2. Fetch P2P content for LLM evaluation
	items, err := m.GetContent(ctx, sessionID, contentType)
	if err != nil {
		logWarn("Failed to fetch %s content for evaluation: %v (skipping vote)\n", contentType, err)
		return ""
	}

	// Build content map: author → content
	contentMap := make(map[string]string)
	for _, item := range items {
		if item.Content != "" {
			contentMap[item.Author] = item.Content
		}
	}

	// Build candidate content list in submission order
	var contents []string
	for _, addr := range candidates {
		if text, ok := contentMap[addr]; ok {
			contents = append(contents, text)
		} else {
			contents = append(contents, "(content unavailable)")
		}
	}

	// 3. LLM evaluation
	bestIdx, err := llmClient.EvaluateContent(ctx, contentType, questionContext, topicTitle, topicDescription, contents)
	if err != nil {
		logWarn("LLM evaluation failed: %v (skipping vote)\n", err)
		return ""
	}

	if bestIdx >= 0 && bestIdx < len(candidates) {
		return candidates[bestIdx]
	}
	return ""
}

// computeContentHash computes SHA256 hash of content and returns hex string.
func computeContentHash(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// mustParseSessionID parses a session ID string to uint64, returning 0 on error.
func mustParseSessionID(id string) uint64 {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// accToCC converts an acc amount string to a human-readable CC string (e.g. "32.000000").
func accToCC(accAmount string) string {
	base := new(big.Int)
	base.SetString(accAmount, 10)
	scale := new(big.Float).SetFloat64(1e18)
	result := new(big.Float).Quo(new(big.Float).SetInt(base), scale)
	cc, _ := result.Float64()
	return strconv.FormatFloat(cc, 'f', 6, 64)
}
