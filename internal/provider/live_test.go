//go:build e2e

package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/genai"

	"phi-agent-code/internal/api"
)

// runLivePingSmoke executes a shared minimal E2E round-trip against any configured Provider.
func runLivePingSmoke(t *testing.T, prov Provider, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	messages := []api.Message{
		{
			Role: api.RoleUser,
			Content: []api.Block{
				{Type: api.BlockText, Text: "Respond with the single word 'PONG'."},
			},
		},
	}

	resp, err := prov.Send(ctx, messages, nil)
	if err != nil {
		t.Fatalf("live %s API call failed: %v", prov.Model(), err)
	}
	if len(resp.Content) == 0 {
		t.Fatalf("expected at least one content block from live provider %s, got 0", prov.Model())
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected StopReason 'end_turn', got %q", resp.StopReason)
	}

	t.Logf("Live response verified (%s): %q (stop reason: %s)", prov.Model(), resp.Content[0].Text, resp.StopReason)
}

// TestLiveAnthropicSmoke exercises a live HTTP transaction with the Anthropic Messages API.
//
// WHEN TO RUN THIS TEST:
// Run this test when you want to audit live Anthropic API connectivity, verify that your
// ANTHROPIC_API_KEY is valid and has active quota, or detect breaking wire-format changes in
// upstream JSON schemas and response headers.
//
// COST & TOKEN CONSUMPTION WARNING:
// This test makes a REAL API CALL to api.anthropic.com. It consumes tokens.
//
// ACTIVATION INSTRUCTIONS:
// This test uses the Go build tag 'e2e' and is EXCLUDED BY DEFAULT from standard 'go test ./...'
// suites to guarantee that normal local builds and CI pipelines remain 100% offline, free and lightning-fast (< 0.05s).
//
// To execute this live smoke test:
//
//	$env:ANTHROPIC_API_KEY = "your-live-api-key"
//	go test -v -tags e2e ./internal/provider/ -run TestLiveAnthropicSmoke
func TestLiveAnthropicSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("skipping live Anthropic E2E smoke test; ANTHROPIC_API_KEY environment variable is not set")
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	prov := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 50, "You are a test agent. Be ultra-concise.")

	runLivePingSmoke(t, prov, 10*time.Second)
}

// TestLiveOllamaSmoke exercises a live HTTP transaction against a local or remote Ollama daemon.
//
// ACTIVATION INSTRUCTIONS:
// Set OLLAMA_MODEL to an installed model tag (and optionally OLLAMA_BASE_URL if not localhost:11434):
//
//	$env:OLLAMA_MODEL = "llama3.2"
//	go test -v -tags e2e ./internal/provider/ -run TestLiveOllamaSmoke
func TestLiveOllamaSmoke(t *testing.T) {
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		t.Skip("skipping live Ollama E2E smoke test; OLLAMA_MODEL environment variable is not set")
	}

	baseURL := os.Getenv("OLLAMA_BASE_URL")
	prov := NewOllamaProvider(baseURL, model, "You are a test agent. Be ultra-concise.")

	// Local model cold-starts can take longer than cloud APIs.
	runLivePingSmoke(t, prov, 30*time.Second)
}

// TestLiveGeminiSmoke exercises a live HTTP transaction with the Google Gemini API via google.golang.org/genai.
//
// COST & TOKEN CONSUMPTION WARNING:
// This test makes a REAL API CALL to generativelanguage.googleapis.com. It consumes tokens.
//
// ACTIVATION INSTRUCTIONS:
// Set GEMINI_API_KEY (or GOOGLE_API_KEY) and optionally GEMINI_MODEL (defaults to gemini-4-hydrogen):
//
//	$env:GEMINI_API_KEY = "your-live-api-key"
//	go test -v -tags e2e ./internal/provider/ -run TestLiveGeminiSmoke
func TestLiveGeminiSmoke(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		t.Skip("skipping live Gemini E2E smoke test; GEMINI_API_KEY or GOOGLE_API_KEY environment variable is not set")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-4-hydrogen"
	}

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("failed creating live genai client: %v", err)
	}

	// Leave maxTokens=0 so Gemini 2.5 thinking tokens do not truncate short output replies.
	prov := NewGeminiProvider(client, model, 0, "You are a test agent. Be ultra-concise.")

	runLivePingSmoke(t, prov, 15*time.Second)
}
