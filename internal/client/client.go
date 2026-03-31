// Package client provides HTTP and RPC client utilities
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/clawcoin-com/cccli/internal/config"
)

// Client wraps chain interactions
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
}

// New creates a new Client
func New(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   false,
				ForceAttemptHTTP2:   false,
			},
		},
	}
}

// restURL picks a random REST URL from the comma-separated config value.
// Auto-adds http:// if no scheme is present.
func (c *Client) restURL() string {
	parts := strings.Split(c.cfg.RESTURL, ",")
	var urls []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
			p = "http://" + p
		}
		urls = append(urls, strings.TrimRight(p, "/"))
	}
	if len(urls) == 0 {
		return "http://localhost:1317"
	}
	if len(urls) == 1 {
		return urls[0]
	}
	return urls[rand.Intn(len(urls))]
}

const maxRetries = 5

// HTTPPost performs an HTTP POST request with retry
func (c *Client) HTTPPost(ctx context.Context, url string, body interface{}) ([]byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(i) * 500 * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("client error: %d, body: %s", resp.StatusCode, truncateBody(data))
		}

		return data, nil
	}
	return nil, lastErr
}

// HTTPGet performs an HTTP GET request with retry
func (c *Client) HTTPGet(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(i) * 500 * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("client error: %d, body: %s", resp.StatusCode, truncateBody(data))
		}

		return data, nil
	}
	return nil, lastErr
}

// truncateBody truncates response body for error messages
func truncateBody(data []byte) string {
	if len(data) > 500 {
		return string(data[:500]) + "..."
	}
	return string(data)
}

// Status represents node status
type Status struct {
	NodeInfo struct {
		Network string `json:"network"`
		Moniker string `json:"moniker"`
	} `json:"node_info"`
	SyncInfo struct {
		LatestBlockHeight string `json:"latest_block_height"`
		CatchingUp        bool   `json:"catching_up"`
	} `json:"sync_info"`
}

// GetStatus retrieves node status via REST API.
// All queries use the same base URL to avoid mixing state from different nodes.
func (c *Client) GetStatus(ctx context.Context) (*Status, error) {
	baseURL := c.restURL()

	// Get node info
	nodeURL := fmt.Sprintf("%s/cosmos/base/tendermint/v1beta1/node_info", baseURL)
	nodeData, err := c.HTTPGet(ctx, nodeURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get node info: %w", err)
	}

	var nodeResp struct {
		DefaultNodeInfo struct {
			Network string `json:"network"`
			Moniker string `json:"moniker"`
		} `json:"default_node_info"`
	}
	if err := json.Unmarshal(nodeData, &nodeResp); err != nil {
		return nil, fmt.Errorf("failed to parse node info: %w", err)
	}

	// Get latest block
	blockURL := fmt.Sprintf("%s/cosmos/base/tendermint/v1beta1/blocks/latest", baseURL)
	blockData, err := c.HTTPGet(ctx, blockURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest block: %w", err)
	}

	var blockResp struct {
		Block struct {
			Header struct {
				Height string `json:"height"`
			} `json:"header"`
		} `json:"block"`
	}
	if err := json.Unmarshal(blockData, &blockResp); err != nil {
		return nil, fmt.Errorf("failed to parse block info: %w", err)
	}

	// Get syncing status
	syncURL := fmt.Sprintf("%s/cosmos/base/tendermint/v1beta1/syncing", baseURL)
	catchingUp := false
	syncData, err := c.HTTPGet(ctx, syncURL)
	if err == nil {
		var syncResp struct {
			Syncing bool `json:"syncing"`
		}
		if json.Unmarshal(syncData, &syncResp) == nil {
			catchingUp = syncResp.Syncing
		}
	}

	return &Status{
		NodeInfo: struct {
			Network string `json:"network"`
			Moniker string `json:"moniker"`
		}{
			Network: nodeResp.DefaultNodeInfo.Network,
			Moniker: nodeResp.DefaultNodeInfo.Moniker,
		},
		SyncInfo: struct {
			LatestBlockHeight string `json:"latest_block_height"`
			CatchingUp        bool   `json:"catching_up"`
		}{
			LatestBlockHeight: blockResp.Block.Header.Height,
			CatchingUp:        catchingUp,
		},
	}, nil
}

// AccountInfo represents account information from the chain
type AccountInfo struct {
	Address       string `json:"address"`
	AccountNumber string `json:"account_number"`
	Sequence      string `json:"sequence"`
}

// GetAccountInfo retrieves account information needed for signing
func (c *Client) GetAccountInfo(ctx context.Context, address string) (*AccountInfo, error) {
	return c.getAccountInfoFrom(ctx, c.restURL(), address)
}

// getAccountInfoFrom retrieves account information from a specific REST base URL.
func (c *Client) getAccountInfoFrom(ctx context.Context, baseURL, address string) (*AccountInfo, error) {
	url := fmt.Sprintf("%s/cosmos/auth/v1beta1/accounts/%s", baseURL, address)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	// Parse the response - handle both BaseAccount and other account types
	var resp struct {
		Account struct {
			Type          string `json:"@type"`
			Address       string `json:"address"`
			AccountNumber string `json:"account_number"`
			Sequence      string `json:"sequence"`
			// For EthAccount and other types that nest BaseAccount
			BaseAccount *struct {
				Address       string `json:"address"`
				AccountNumber string `json:"account_number"`
				Sequence      string `json:"sequence"`
			} `json:"base_account,omitempty"`
		} `json:"account"`
	}

	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse account info: %w", err)
	}

	var info *AccountInfo
	// Handle EthAccount or similar types with nested base_account
	if resp.Account.BaseAccount != nil {
		info = &AccountInfo{
			Address:       resp.Account.BaseAccount.Address,
			AccountNumber: resp.Account.BaseAccount.AccountNumber,
			Sequence:      resp.Account.BaseAccount.Sequence,
		}
	} else {
		info = &AccountInfo{
			Address:       resp.Account.Address,
			AccountNumber: resp.Account.AccountNumber,
			Sequence:      resp.Account.Sequence,
		}
	}

	// Default empty values to "0" (uninitialized accounts)
	if info.AccountNumber == "" {
		info.AccountNumber = "0"
	}
	if info.Sequence == "" {
		info.Sequence = "0"
	}

	return info, nil
}

// BroadcastTx broadcasts a signed transaction to the chain
func (c *Client) BroadcastTx(ctx context.Context, txBytes []byte, mode string) (*TxResponse, error) {
	return c.broadcastTxTo(ctx, c.restURL(), txBytes, mode)
}

// broadcastTxTo broadcasts a signed transaction to a specific REST base URL.
func (c *Client) broadcastTxTo(ctx context.Context, baseURL string, txBytes []byte, mode string) (*TxResponse, error) {
	if mode == "" {
		mode = "BROADCAST_MODE_SYNC"
	}

	req := struct {
		TxBytes string `json:"tx_bytes"`
		Mode    string `json:"mode"`
	}{
		TxBytes: encodeBase64(txBytes),
		Mode:    mode,
	}

	url := fmt.Sprintf("%s/cosmos/tx/v1beta1/txs", baseURL)
	data, err := c.HTTPPost(ctx, url, req)
	if err != nil {
		return nil, fmt.Errorf("failed to broadcast tx: %w", err)
	}

	var resp struct {
		TxResponse TxResponse `json:"tx_response"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse broadcast response: %w", err)
	}

	return &resp.TxResponse, nil
}

// ExecTx pins a single REST node for the query+broadcast lifecycle.
// It queries account info and broadcasts the transaction to the same node,
// preventing sequence mismatches when multiple REST nodes are configured.
func (c *Client) ExecTx(ctx context.Context, address string,
	buildTx func(*AccountInfo) ([]byte, error)) (*TxResponse, error) {
	baseURL := c.restURL()

	acctInfo, err := c.getAccountInfoFrom(ctx, baseURL, address)
	if err != nil {
		return nil, fmt.Errorf("get account info: %w", err)
	}

	txBytes, err := buildTx(acctInfo)
	if err != nil {
		return nil, fmt.Errorf("build tx: %w", err)
	}

	return c.broadcastTxTo(ctx, baseURL, txBytes, "BROADCAST_MODE_SYNC")
}

// RestURL returns a random REST URL from the configured list.
// Exported for callers that need to pin a URL for non-tx HTTP calls.
func (c *Client) RestURL() string {
	return c.restURL()
}

// TxResponse represents a transaction response from the chain
type TxResponse struct {
	Height    string `json:"height"`
	TxHash    string `json:"txhash"`
	Code      uint32 `json:"code"`
	RawLog    string `json:"raw_log"`
	GasWanted string `json:"gas_wanted"`
	GasUsed   string `json:"gas_used"`
}

// GetBalance retrieves balance for an address
func (c *Client) GetBalance(ctx context.Context, address string) ([]Balance, error) {
	url := fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", c.restURL(), address)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	var resp struct {
		Balances []Balance `json:"balances"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse balance: %w", err)
	}

	return resp.Balances, nil
}

// Balance represents a coin balance
type Balance struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// GetActiveSessions retrieves all active QA sessions (used by `miner sessions` command).
func (c *Client) GetActiveSessions(ctx context.Context) ([]Session, error) {
	url := fmt.Sprintf("%s/cc_bc/qa/v1/sessions", c.restURL())

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}

	var resp struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse sessions: %w", err)
	}

	return resp.Sessions, nil
}

// GetMySessions retrieves QA sessions for a specific participant (server-side filtered).
// Uses /cc_bc/qa/v1/my_sessions/{participant} with a 5-second LRU cache on the server.
func (c *Client) GetMySessions(ctx context.Context, participant string) ([]Session, error) {
	url := fmt.Sprintf("%s/cc_bc/qa/v1/my_sessions/%s", c.restURL(), participant)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get my sessions: %w", err)
	}

	var resp struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse my sessions: %w", err)
	}

	return resp.Sessions, nil
}

// Session represents a QA session, matching the chain's Session proto type.
// ID is a string because Cosmos REST API serializes uint64 as JSON string.
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

// SubmissionEntry represents an on-chain submission (question or answer).
type SubmissionEntry struct {
	SessionID   string `json:"session_id"`
	Author      string `json:"author"`
	ContentHash string `json:"content_hash"`
}

// GetSubmissions queries on-chain submissions for a session.
// subType must be "question" or "answer".
func (c *Client) GetSubmissions(ctx context.Context, sessionID uint64, subType string) ([]SubmissionEntry, error) {
	url := fmt.Sprintf("%s/cc_bc/qa/v1/submissions/%d/%s", c.restURL(), sessionID, subType)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get submissions: %w", err)
	}

	var resp struct {
		Submissions []SubmissionEntry `json:"submissions"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse submissions: %w", err)
	}

	return resp.Submissions, nil
}

// ContentItem represents a P2P content cache entry.
type ContentItem struct {
	Author      string `json:"author"`
	Content     string `json:"content"`
	ContentHash string `json:"content_hash"`
}

// PostP2PContent submits content to the P2P content cache.
func (c *Client) PostP2PContent(ctx context.Context, req interface{}) error {
	url := fmt.Sprintf("%s/cc_bc/qa/p2p-content", c.restURL())
	data, err := c.HTTPPost(ctx, url, req)
	if err != nil {
		return fmt.Errorf("failed to post p2p content: %w", err)
	}

	var resp struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason,omitempty"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("failed to parse content response: %w", err)
	}
	if !resp.Accepted {
		return fmt.Errorf("content rejected: %s", resp.Reason)
	}
	return nil
}

// GetP2PContent retrieves content from the P2P content cache for a session.
// contentType must be "question" or "answer".
func (c *Client) GetP2PContent(ctx context.Context, sessionID uint64, contentType string) ([]ContentItem, error) {
	url := fmt.Sprintf("%s/cc_bc/qa/p2p-content/%d?type=%s", c.restURL(), sessionID, contentType)
	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get p2p content: %w", err)
	}

	var items []ContentItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("failed to parse content items: %w", err)
	}
	return items, nil
}

// GetMinerInfo retrieves miner information
func (c *Client) GetMinerInfo(ctx context.Context, address string) (*MinerInfo, error) {
	url := fmt.Sprintf("%s/cc_bc/hb/v1/miner/%s", c.restURL(), address)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get miner info: %w", err)
	}

	var resp struct {
		Miner MinerInfo `json:"miner"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse miner info: %w", err)
	}

	return &resp.Miner, nil
}

// MinerInfo represents miner information
type MinerInfo struct {
	Address       string `json:"address"`
	Stake         string `json:"stake"`
	LastHeartbeat string `json:"last_heartbeat"`
	IsActive      bool   `json:"is_active"`
}

// Validator represents a staking validator
type Validator struct {
	OperatorAddress string `json:"operator_address"`
	Status          string `json:"status"`
	Tokens          string `json:"tokens"`
	Description     struct {
		Moniker string `json:"moniker"`
	} `json:"description"`
	Commission struct {
		CommissionRates struct {
			Rate string `json:"rate"`
		} `json:"commission_rates"`
	} `json:"commission"`
}

// GetValidators retrieves the list of bonded validators
func (c *Client) GetValidators(ctx context.Context) ([]Validator, error) {
	url := fmt.Sprintf("%s/cosmos/staking/v1beta1/validators?status=BOND_STATUS_BONDED", c.restURL())

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get validators: %w", err)
	}

	var resp struct {
		Validators []Validator `json:"validators"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse validators: %w", err)
	}

	return resp.Validators, nil
}

// Delegation represents a staking delegation
type Delegation struct {
	Delegation struct {
		DelegatorAddress string `json:"delegator_address"`
		ValidatorAddress string `json:"validator_address"`
	} `json:"delegation"`
	Balance Balance `json:"balance"`
}

// GetDelegations retrieves delegations for a delegator address
func (c *Client) GetDelegations(ctx context.Context, delegatorAddr string) ([]Delegation, error) {
	url := fmt.Sprintf("%s/cosmos/staking/v1beta1/delegations/%s", c.restURL(), delegatorAddr)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("failed to get delegations: %w", err)
	}

	var resp struct {
		DelegationResponses []Delegation `json:"delegation_responses"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse delegations: %w", err)
	}

	return resp.DelegationResponses, nil
}

// DelegationReward represents rewards for a single delegation
type DelegationReward struct {
	ValidatorAddress string    `json:"validator_address"`
	Reward           []Balance `json:"reward"`
}

// GetDelegationRewards retrieves pending staking rewards for a delegator
func (c *Client) GetDelegationRewards(ctx context.Context, delegatorAddr string) ([]DelegationReward, []Balance, error) {
	url := fmt.Sprintf("%s/cosmos/distribution/v1beta1/delegators/%s/rewards", c.restURL(), delegatorAddr)

	data, err := c.HTTPGet(ctx, url)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get rewards: %w", err)
	}

	var resp struct {
		Rewards []DelegationReward `json:"rewards"`
		Total   []Balance          `json:"total"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("failed to parse rewards: %w", err)
	}

	return resp.Rewards, resp.Total, nil
}

// encodeBase64 encodes bytes to base64 string
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Config accessor for the client
func (c *Client) Config() *config.Config {
	return c.cfg
}
