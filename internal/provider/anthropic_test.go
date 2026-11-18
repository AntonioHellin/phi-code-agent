package provider

import (
	"context"
	"encoding/json"
	"io"
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

// TestAnthropicProvider_Stream_WithHTTPTest verifies SSE streaming over /v1/messages:
// it asserts "stream": true on the outbound JSON request, real-time TextDelta callback delivery,
// input_json_delta tool argument accumulation without leaking fragments to onChunk,
// nil-callback safety, and HTTP error propagation.
func TestAnthropicProvider_Stream_WithHTTPTest(t *testing.T) {
	var nilProv *AnthropicProvider
	if _, err := nilProv.Stream(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil provider error, got %v", err)
	}
	if _, err := NewAnthropicProvider(nil, "claude-haiku-5-5", 100, "").Stream(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client error, got %v", err)
	}

	ssePayload := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-haiku-5-5","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Quick"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" sort"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_1","name":"bash","input":{}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"pwd\"}"}}`,
		``,
		`event: content_block_stop`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":20}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	var failNext bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		var req struct {
			Stream bool   `json:"stream"`
			Model  string `json:"model"`
		}
		if err := json.Unmarshal(rawBody, &req); err != nil {
			t.Fatalf("failed decoding request JSON: %v", err)
		}
		if !req.Stream {
			t.Fatalf("expected request JSON to include stream=true, got %s", string(rawBody))
		}

		if failNext {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"overloaded"}}`))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer srv.Close()

	client := anthropic.NewClient(
		option.WithAPIKey("test-anthropic-key"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)
	prov := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 4096, "system")

	var chunks []string
	resp, err := prov.Stream(
		context.Background(),
		[]api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "hi"}}}},
		nil,
		func(chunk string) {
			chunks = append(chunks, chunk)
		},
	)
	if err != nil {
		t.Fatalf("unexpected Stream error: %v", err)
	}

	if len(chunks) != 2 || chunks[0] != "Quick" || chunks[1] != " sort" {
		t.Fatalf("expected chunks [Quick  sort], got %#v", chunks)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected StopReason 'tool_use', got %q", resp.StopReason)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 accumulated blocks (text + tool_use), got %+v", resp.Content)
	}
	if resp.Content[0].Type != api.BlockText || resp.Content[0].Text != "Quick sort" {
		t.Errorf("unexpected text block: %+v", resp.Content[0])
	}
	toolBlock := resp.Content[1]
	if toolBlock.Type != api.BlockToolUse || toolBlock.ToolUseID != "call_1" || toolBlock.ToolName != "bash" || toolBlock.ToolInput != `{"command":"pwd"}` {
		t.Errorf("unexpected accumulated tool_use block: %+v", toolBlock)
	}

	// Nil onChunk callback must succeed safely without panicking.
	nilSinkResp, err := prov.Stream(
		context.Background(),
		[]api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "hi"}}}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected nil-callback Stream error: %v", err)
	}
	if len(nilSinkResp.Content) != 2 || nilSinkResp.Content[0].Text != "Quick sort" {
		t.Errorf("unexpected response with nil callback: %+v", nilSinkResp)
	}

	// Server error must be surfaced, not swallowed as nil.
	failNext = true
	if _, err := prov.Stream(context.Background(), []api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "hi"}}}}, nil, nil); err == nil {
		t.Fatalf("expected Stream to propagate HTTP 500 error")
	}
}

// TestAnthropicProvider_OmitsEmptySystem verifies buildParams omits blank System prompts
// (which Anthropic's live Messages API rejects) while preserving non-empty instructions.
func TestAnthropicProvider_OmitsEmptySystem(t *testing.T) {
	var capturedPayloads []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawBody, &payload); err != nil {
			t.Fatalf("failed decoding JSON payload: %v", err)
		}
		capturedPayloads = append(capturedPayloads, payload)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_sys",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"model": "claude-haiku-5-5",
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 2}
		}`))
	}))
	defer srv.Close()

	client := anthropic.NewClient(
		option.WithAPIKey("test-anthropic-key"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)

	emptySysProv := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 1024, "   ")
	if _, err := emptySysProv.Send(context.Background(), []api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("unexpected error with blank system prompt: %v", err)
	}
	if _, hasSystem := capturedPayloads[0]["system"]; hasSystem {
		t.Errorf("expected 'system' key omitted when system prompt is blank, got %v", capturedPayloads[0])
	}

	withSysProv := NewAnthropicProvider(&client, string(anthropic.ModelClaudeHaiku5_5), 1024, "Be concise.")
	if _, err := withSysProv.Send(context.Background(), []api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "hi"}}}}, nil); err != nil {
		t.Fatalf("unexpected error with system prompt: %v", err)
	}
	if _, hasSystem := capturedPayloads[1]["system"]; !hasSystem {
		t.Errorf("expected 'system' key present when system prompt is set, got %v", capturedPayloads[1])
	}
}

