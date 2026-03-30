//go:build live_llm
// +build live_llm

package llm

import (
	"context"
	"testing"
	"time"

	"github.com/clawcoin-com/cccli/internal/config"
)

func TestLiveLLM(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Logf("Config: provider=%s, model=%s, thinking=%v", cfg.LLMProvider, cfg.LLMModel, cfg.LLMThinking)

	client := NewClient(cfg.LLMProvider, cfg.LLMAPIBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.LLMThinking)
	ctx := context.Background()

	t.Run("GenerateQuestion", func(t *testing.T) {
		start := time.Now()
		q, err := client.GenerateQuestion(ctx, "Token Economics", "Analyze token supply models", "test_123")
		t.Logf("elapsed: %v", time.Since(start))
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		t.Logf("result: %s", q)
	})

	t.Run("EvaluateContent", func(t *testing.T) {
		candidates := []string{
			"How can token burn mechanisms maintain price stability?",
			"What role does governance play in token economics?",
		}
		start := time.Now()
		idx, err := client.EvaluateContent(ctx, "question", "", "Token Economics", "Analyze token supply models", candidates)
		t.Logf("elapsed: %v", time.Since(start))
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		t.Logf("picked candidate %d", idx+1)
	})

	t.Run("GenerateAnswer", func(t *testing.T) {
		start := time.Now()
		a, err := client.GenerateAnswer(ctx, "Token Economics", "Analyze token supply models", "How can token burn mechanisms maintain price stability?")
		t.Logf("elapsed: %v", time.Since(start))
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		t.Logf("result: %s", a)
	})
}
