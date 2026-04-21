// Package llm provides a lightweight LLM client supporting OpenAI and Anthropic APIs
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Provider identifies the LLM API format to use
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

// Client is a lightweight LLM client supporting OpenAI and Anthropic APIs
type Client struct {
	provider    string // "openai" or "anthropic"
	baseURL     string
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	thinking    bool // enable thinking/reasoning mode
	httpClient  *http.Client
}

// NewClient creates a new LLM client.
// provider: "openai" (default) or "anthropic"
// thinking: enable thinking/reasoning mode (e.g., Qwen3 <think> tags). Default false for faster responses.
func NewClient(provider, baseURL, apiKey, model string, maxTokens int, thinking bool) *Client {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = ProviderOpenAI
	}

	if baseURL == "" {
		switch provider {
		case ProviderAnthropic:
			baseURL = "https://api.anthropic.com"
		default:
			baseURL = "http://localhost:4000/v1"
		}
	}
	if model == "" {
		switch provider {
		case ProviderAnthropic:
			model = "claude-haiku-4-5-20251001"
		default:
			model = "Pro/deepseek-ai/DeepSeek-V3"
		}
	}

	return &Client{
		provider:    provider,
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: 0.7,
		thinking:    thinking,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DisableCompression: true, // some proxies hang on gzip negotiation
			},
		},
	}
}

// ---------- OpenAI types ----------

type openAIRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	MaxTokens      int           `json:"max_tokens,omitempty"`
	Temperature    float64       `json:"temperature,omitempty"`
	EnableThinking *bool         `json:"enable_thinking,omitempty"` // disable thinking for Qwen3/DeepSeek-R1 etc.
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------- Anthropic types ----------

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------- complete dispatcher ----------

// complete sends a chat completion request using the configured provider
func (c *Client) complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	switch c.provider {
	case ProviderAnthropic:
		return c.completeAnthropic(ctx, systemPrompt, userPrompt)
	default:
		return c.completeOpenAI(ctx, systemPrompt, userPrompt)
	}
}

// ---------- OpenAI implementation ----------

func (c *Client) completeOpenAI(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	req := openAIRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
	}

	// Disable thinking/reasoning for models that support it (e.g., Qwen3, DeepSeek-R1)
	// unless explicitly enabled via config. This significantly reduces response latency.
	if !c.thinking {
		f := false
		req.EnableThinking = &f
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB max
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body := string(respBody)
		if len(body) > 500 {
			body = body[:500] + "..."
		}
		return "", fmt.Errorf("llm error (status %d): %s", resp.StatusCode, body)
	}

	var chatResp openAIResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("llm api error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	content := chatResp.Choices[0].Message.Content
	if content == "" {
		return "", fmt.Errorf("LLM returned empty content (model may need more max_tokens or does not support non-thinking mode)")
	}
	return stripThinkTags(content), nil
}

// ---------- Anthropic implementation ----------

func (c *Client) completeAnthropic(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	req := anthropicRequest{
		Model:       c.model,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		System:      systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB max
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body := string(respBody)
		if len(body) > 500 {
			body = body[:500] + "..."
		}
		return "", fmt.Errorf("llm error (status %d): %s", resp.StatusCode, body)
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if anthropicResp.Error != nil {
		return "", fmt.Errorf("llm api error [%s]: %s", anthropicResp.Error.Type, anthropicResp.Error.Message)
	}

	// Extract text from content blocks
	var texts []string
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("no text content in Anthropic response")
	}

	return strings.TrimSpace(strings.Join(texts, "\n")), nil
}

// ---------- Public API (unchanged) ----------

// GenerateQuestion generates a question for a QA session.
// topicTitle and topicDescription provide the session's bound topic context.
func (c *Client) GenerateQuestion(ctx context.Context, topicTitle, topicDescription, sessionTimestamp string) (string, error) {
	var systemPrompt string
	if topicTitle != "" {
		topicLine := "Topic: " + topicTitle
		if topicDescription != "" {
			topicLine += " — " + topicDescription
		}
		systemPrompt = fmt.Sprintf(
			"You are helping a decentralized AI network. Generate a thought-provoking question on the following topic. Be concise (2-3 sentences max). Output only the question, no preamble.\n\n%s",
			topicLine,
		)
	} else {
		systemPrompt = "You are helping a decentralized AI network. Generate a thought-provoking question about AI, blockchain technology, or their intersection. Be concise (2-3 sentences max). Output only the question, no preamble."
	}
	userPrompt := fmt.Sprintf("Generate a unique and insightful question. Session timestamp: %s", sessionTimestamp)
	return c.complete(ctx, systemPrompt, userPrompt)
}

// GenerateAnswer generates an answer for a given question.
// If question is empty, generates a generic insight (matching bash miner_client.sh behavior).
// topicTitle and topicDescription provide the session's bound topic context.
func (c *Client) GenerateAnswer(ctx context.Context, topicTitle, topicDescription, question string) (string, error) {
	var systemPrompt, userPrompt string
	if question != "" {
		if topicTitle != "" {
			topicLine := "Topic: " + topicTitle
			if topicDescription != "" {
				topicLine += " — " + topicDescription
			}
			systemPrompt = fmt.Sprintf(
				"You are helping a decentralized AI network. Provide a clear, well-reasoned answer to the given question on the following topic. Be concise (3-5 sentences). Output only the answer, no preamble.\n\n%s",
				topicLine,
			)
		} else {
			systemPrompt = "You are helping a decentralized AI network. Provide a clear, well-reasoned answer to the given question. Be concise (3-5 sentences). Output only the answer, no preamble."
		}
		userPrompt = fmt.Sprintf("Answer the following question:\n\n%s", question)
	} else {
		// No question available — generate generic insight (matches bash fallback)
		systemPrompt = "You are helping a decentralized AI network. Provide a clear, well-reasoned insight about AI and blockchain technology. Be concise (3-5 sentences). Output only the content, no preamble."
		userPrompt = fmt.Sprintf("Share an insightful perspective on the convergence of AI and blockchain. Session timestamp: %d", time.Now().Unix())
	}
	return c.complete(ctx, systemPrompt, userPrompt)
}

// EvaluateContent evaluates candidates and returns the 0-indexed best choice.
// questionContext is the question being answered (for answer evaluation); empty for question evaluation.
// topicTitle and topicDescription provide the session's bound topic context.
func (c *Client) EvaluateContent(ctx context.Context, contentType, questionContext, topicTitle, topicDescription string, candidates []string) (int, error) {
	if len(candidates) == 0 {
		return -1, fmt.Errorf("no candidates to evaluate")
	}
	if len(candidates) == 1 {
		return 0, nil
	}

	// Build system prompt matching bash miner_client.sh
	var systemPrompt string
	if contentType == "question" {
		systemPrompt = "You are evaluating questions submitted to a decentralized AI Q&A network. Pick the most insightful, thought-provoking, and well-formed question. Reply with ONLY the candidate number (e.g. '1' or '2'). No explanation."
	} else {
		systemPrompt = "You are evaluating answers submitted to a decentralized AI Q&A network. Pick the most accurate, comprehensive, and well-reasoned answer to the given question. Reply with ONLY the candidate number (e.g. '1' or '2'). No explanation."
	}

	// Build candidate list
	var candidatesText strings.Builder
	for i, c := range candidates {
		preview := c
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		candidatesText.WriteString(fmt.Sprintf("\nCandidate %d: %s", i+1, preview))
	}

	// Build user prompt matching bash miner_client.sh
	var sb strings.Builder
	topicCtx := ""
	if topicTitle != "" {
		topicCtx = "Topic: " + topicTitle
		if topicDescription != "" {
			topicCtx += " — " + topicDescription
		}
	}

	if contentType == "answer" && questionContext != "" {
		if topicCtx != "" {
			sb.WriteString(topicCtx)
			sb.WriteString("\n\n")
		}
		sb.WriteString("The question being answered:\n")
		sb.WriteString(questionContext)
		sb.WriteString("\n\nWhich of these answers is the best?")
	} else {
		if topicCtx != "" {
			sb.WriteString(topicCtx)
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("Which of these %ss is the best?", contentType))
	}
	sb.WriteString(candidatesText.String())
	sb.WriteString("\n\nReply with ONLY the number of the best candidate.")

	response, err := c.complete(ctx, systemPrompt, sb.String())
	if err != nil {
		return 0, err // default to first on error
	}

	// Parse the number from response (match bash: grep -oE '[0-9]+' | head -1)
	response = strings.TrimSpace(response)
	re := regexp.MustCompile(`[0-9]+`)
	match := re.FindString(response)
	if match == "" {
		return -1, fmt.Errorf("no number in LLM response: %q", response)
	}
	choice, err := strconv.Atoi(match)
	if err != nil || choice < 1 || choice > len(candidates) {
		return -1, fmt.Errorf("invalid pick %d from LLM response: %q", choice, response)
	}

	return choice - 1, nil
}

// stripThinkTags removes <think>...</think> blocks from thinking model responses.
// This is a safety measure — even when thinking is disabled, some models may still
// include think tags. We always strip them to return clean content.
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)

func stripThinkTags(s string) string {
	s = thinkTagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
