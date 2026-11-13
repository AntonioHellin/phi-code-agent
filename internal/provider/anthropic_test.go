package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"phi-agent-code/internal/api"
)

// TestToAnthropicMessages verifies domain-to-Anthropic wire translation for user text,
// assistant tool_use blocks, and tool_result blocks wrapped inside a user turn.
func TestToAnthropicMessages(t *testing.T) {
	domainMessages := []api.Message{
		{
			Role: api.RoleUser,
			Content: []api.Block{
				{
					Type: api.BlockText,
					Text: "Hello assistant",
				},
			},
		},
		{
			Role: api.RoleAssistant,
			Content: []api.Block{
				{
					Type:      api.BlockToolUse,
					ToolUseID: "tool_call_123",
					ToolName:  "read_file",
					ToolInput: `{"path":"main.go"}`,
				},
			},
		},
		{
			Role: api.RoleUser,
			Content: []api.Block{
				{
					Type:       api.BlockToolResult,
					ToolUseID:  "tool_call_123",
					ToolResult: "package main",
					IsError:    false,
				},
			},
		},
	}

	anthropicParams := toAnthropicMessages(domainMessages)

	if len(anthropicParams) != 3 {
		t.Fatalf("expected 3 message params, got %d", len(anthropicParams))
	}

	// 1. Verify User message with text block
	userMsg := anthropicParams[0]
	if userMsg.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", userMsg.Role)
	}
	if len(userMsg.Content) != 1 {
		t.Fatalf("expected 1 content block in user message, got %d", len(userMsg.Content))
	}

	// 2. Verify Assistant message with tool use block
	assistantMsg := anthropicParams[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected role 'assistant', got '%s'", assistantMsg.Role)
	}
	if len(assistantMsg.Content) != 1 {
		t.Fatalf("expected 1 content block in assistant message, got %d", len(assistantMsg.Content))
	}

	// 3. Verify User message wrapping tool execution result
	toolResultMsg := anthropicParams[2]
	if toolResultMsg.Role != "user" {
		t.Errorf("expected tool result container role to be 'user', got '%s'", toolResultMsg.Role)
	}
	if len(toolResultMsg.Content) != 1 {
		t.Fatalf("expected 1 content block in tool result message, got %d", len(toolResultMsg.Content))
	}
}

// TestToAnthropicTools verifies conversion of domain ToolDefs into Anthropic tool schemas,
// preserving tool names, descriptions, and required input fields.
func TestToAnthropicTools(t *testing.T) {
	domainTools := []api.ToolDef{
		{
			Name:        "read_file",
			Description: "Read file contents",
			InputSchema: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to read",
				},
			},
			Required: []string{"path"},
		},
		{
			Name:        "write_file",
			Description: "Write file contents",
			InputSchema: map[string]any{
				"path": map[string]any{
					"type": "string",
				},
				"content": map[string]any{
					"type": "string",
				},
			},
			Required: []string{"path", "content"},
		},
	}

	anthropicTools := toAnthropicTools(domainTools)

	if len(anthropicTools) != 2 {
		t.Fatalf("expected 2 anthropic tool params, got %d", len(anthropicTools))
	}

	firstTool := anthropicTools[0].OfTool
	if firstTool == nil {
		t.Fatalf("expected OfTool to be non-nil")
	}
	if firstTool.Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got '%s'", firstTool.Name)
	}
	if firstTool.Description.Value != "Read file contents" {
		t.Errorf("expected description 'Read file contents', got '%s'", firstTool.Description.Value)
	}
	if len(firstTool.InputSchema.Required) != 1 || firstTool.InputSchema.Required[0] != "path" {
		t.Errorf("expected required field ['path'], got %v", firstTool.InputSchema.Required)
	}
}

// TestAnthropicProviderModelAccessors verifies Model returns the initialized identifier
// and SetModel updates it dynamically.
func TestAnthropicProviderModelAccessors(t *testing.T) {
	provider := NewAnthropicProvider(nil, "initial-model", 4096, "test-prompt")

	if provider.Model() != "initial-model" {
		t.Errorf("expected model 'initial-model', got '%s'", provider.Model())
	}

	provider.SetModel("updated-model")
	if provider.Model() != "updated-model" {
		t.Errorf("expected model 'updated-model', got '%s'", provider.Model())
	}
}

// TestAnthropicProvider_ListModels_WithHTTPTest verifies paginated /v1/models discovery,
// retired lifecycle filtering, and nil-receiver/client guards using httptest.NewServer.
func TestAnthropicProvider_ListModels_WithHTTPTest(t *testing.T) {
	var nilProv *AnthropicProvider
	if _, err := nilProv.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil provider error, got %v", err)
	}
	if _, err := NewAnthropicProvider(nil, "claude-haiku-5-5", 100, "").ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client error, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "claude-retired" {
			_, _ = w.Write([]byte(`{
				"data": [
					{
						"id": "claude-haiku-5-5",
						"display_name": "Claude Haiku 5.5",
						"type": "model",
						"created_at": "2026-01-01T00:00:00Z",
						"lifecycle": "active"
					}
				],
				"has_more": false,
				"first_id": "claude-haiku-5-5",
				"last_id": "claude-haiku-5-5"
			}`))
			return
		}

		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "claude-sonnet-5-5",
					"display_name": "Claude Sonnet 5.5",
					"type": "model",
					"created_at": "2026-01-01T00:00:00Z",
					"lifecycle": "active"
				},
				{
					"id": "claude-retired",
					"display_name": "Retired Model",
					"type": "model",
					"created_at": "2024-01-01T00:00:00Z",
					"lifecycle": "retired"
				}
			],
			"has_more": true,
			"first_id": "claude-sonnet-5-5",
			"last_id": "claude-retired"
		}`))
	}))
	defer srv.Close()

	client := anthropic.NewClient(
		option.WithAPIKey("test-anthropic-key"),
		option.WithBaseURL(srv.URL),
	)
	prov := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 4096, "")

	models, err := prov.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected ListModels error: %v", err)
	}

	if len(models) != 2 || models[0] != "claude-sonnet-5-5" || models[1] != "claude-haiku-5-5" {
		t.Fatalf("expected [claude-sonnet-5-5 claude-haiku-5-5], got %v", models)
	}
}

