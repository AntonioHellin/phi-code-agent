//go:build e2e

package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"phi-agent-code/internal/api"
)

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
//   $env:ANTHROPIC_API_KEY = "your-live-api-key"
//   go test -v -tags e2e ./internal/provider/ -run TestLiveAnthropicSmoke
func TestLiveAnthropicSmoke(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("skipping live Anthropic E2E smoke test; ANTHROPIC_API_KEY environment variable is not set")
	}

	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	prov := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 50, "You are a test agent. Be ultra-concise.")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
		t.Fatalf("live Anthropic API call failed: %v", err)
	}

	if len(resp.Content) == 0 {
		t.Fatal("expected at least one content block from live provider, got 0")
	}

	if resp.StopReason != "end_turn" {
		t.Errorf("expected StopReason 'end_turn', got %q", resp.StopReason)
	}

	t.Logf("Live Anthropic response verified: %q (stop reason: %s)", resp.Content[0].Text, resp.StopReason)
}