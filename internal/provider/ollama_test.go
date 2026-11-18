package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
)

// TestNewOllamaProvider_DefaultsAndAccessors verifies baseURL normalization, default endpoint
// fallback, and dynamic Model/SetModel accessors.
func TestNewOllamaProvider_DefaultsAndAccessors(t *testing.T) {
	defaultProv := NewOllamaProvider("   ", "llama3.2", "You are helpful.")
	if defaultProv.baseURL != defaultOllamaBaseURL {
		t.Fatalf("expected default baseURL %q, got %q", defaultOllamaBaseURL, defaultProv.baseURL)
	}
	if defaultProv.Model() != "llama3.2" {
		t.Fatalf("expected initial model 'llama3.2', got %q", defaultProv.Model())
	}

	customProv := NewOllamaProvider("http://127.0.0.1:11434///", "qwen2.5-coder", "")
	if customProv.baseURL != "http://127.0.0.1:11434" {
		t.Fatalf("expected trimmed baseURL 'http://127.0.0.1:11434', got %q", customProv.baseURL)
	}

	customProv.SetModel("deepseek-r1")
	if customProv.Model() != "deepseek-r1" {
		t.Fatalf("expected updated model 'deepseek-r1', got %q", customProv.Model())
	}

	// Verify Client.Timeout is zero (so long streams are not aborted mid-body) while
	// transport dial and response header timeouts protect against unresponsive daemons.
	if defaultProv.client.Timeout != 0 {
		t.Errorf("expected Client.Timeout=0 to avoid truncating active streams, got %v", defaultProv.client.Timeout)
	}
	tr, ok := defaultProv.client.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != defaultOllamaHeaderTimeout {
		t.Errorf("expected http.Transport with ResponseHeaderTimeout=%v, got %#v", defaultOllamaHeaderTimeout, defaultProv.client.Transport)
	}
}

// TestOllamaProvider_SendTextAndWireFormat verifies POST /api/chat serialization
// (explicit stream=false, system prompt injection, and plain-text response normalization).
func TestOllamaProvider_SendTextAndWireFormat(t *testing.T) {
	var capturedPath string
	var capturedMethod string
	var capturedContentType string
	var rawPayload map[string]any
	var decodedReq ollamaChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		capturedContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed reading request body: %v", err)
		}
		if err := json.Unmarshal(body, &rawPayload); err != nil {
			t.Fatalf("failed unmarshaling raw JSON: %v", err)
		}
		if err := json.Unmarshal(body, &decodedReq); err != nil {
			t.Fatalf("failed unmarshaling ollamaChatRequest: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"message": {"role": "assistant", "content": "Hello from local Ollama"},
			"done": true,
			"done_reason": "stop"
		}`))
	}))
	defer srv.Close()

	prov := NewOllamaProvider(srv.URL, "llama3.2", "Be ultra concise.")
	resp, err := prov.Send(context.Background(), []api.Message{
		{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: "Say hi"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("expected POST method, got %q", capturedMethod)
	}
	if capturedPath != "/api/chat" {
		t.Errorf("expected /api/chat path, got %q", capturedPath)
	}
	if capturedContentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %q", capturedContentType)
	}

	// Ensure "stream": false is explicitly serialized on the wire, not omitted.
	streamVal, hasStream := rawPayload["stream"]
	if !hasStream || streamVal != false {
		t.Fatalf("expected explicit 'stream': false in payload, got present=%v value=%v", hasStream, streamVal)
	}
	if _, hasTools := rawPayload["tools"]; hasTools {
		t.Errorf("expected 'tools' key to be omitted when no tools are passed")
	}

	if decodedReq.Model != "llama3.2" {
		t.Errorf("expected model 'llama3.2', got %q", decodedReq.Model)
	}
	if len(decodedReq.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(decodedReq.Messages))
	}
	if decodedReq.Messages[0].Role != "system" || decodedReq.Messages[0].Content != "Be ultra concise." {
		t.Errorf("unexpected leading system message: %+v", decodedReq.Messages[0])
	}
	if decodedReq.Messages[1].Role != "user" || decodedReq.Messages[1].Content != "Say hi" {
		t.Errorf("unexpected user message: %+v", decodedReq.Messages[1])
	}

	if resp.StopReason != "end_turn" {
		t.Errorf("expected StopReason 'end_turn', got %q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != api.BlockText || resp.Content[0].Text != "Hello from local Ollama" {
		t.Fatalf("unexpected normalized response content: %+v", resp.Content)
	}
}

// TestOllamaProvider_SendToolCallsAndMultiTurnTranslation verifies tool schema serialization,
// multi-turn history translation (assistant tool_use + error tool_result -> role="tool"),
// synthetic call ID generation when omitted by Ollama, and StopReason normalization to "tool_use".
func TestOllamaProvider_SendToolCallsAndMultiTurnTranslation(t *testing.T) {
	var decodedReq ollamaChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&decodedReq); err != nil {
			t.Fatalf("failed decoding request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		// First tool_call carries an explicit ID; second intentionally omits ID to test synthetic fallback.
		_, _ = w.Write([]byte(`{
			"message": {
				"role": "assistant",
				"content": "Let me inspect those files.",
				"tool_calls": [
					{
						"id": "tc_explicit",
						"function": {
							"name": "read_file",
							"arguments": {"path": "go.mod"}
						}
					},
					{
						"function": {
							"name": "list_dir",
							"arguments": {}
						}
					}
				]
			},
			"done": true,
			"done_reason": "stop"
		}`))
	}))
	defer srv.Close()

	prov := NewOllamaProvider(srv.URL, "qwen2.5-coder", "")
	history := []api.Message{
		{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: "Check missing.txt"}},
		},
		{
			Role: api.RoleAssistant,
			Content: []api.Block{
				{
					Type:      api.BlockToolUse,
					ToolUseID: "call_1",
					ToolName:  "read_file",
					ToolInput: `{"path":"missing.txt"}`,
				},
			},
		},
		{
			Role: api.RoleUser,
			Content: []api.Block{
				{
					Type:       api.BlockToolResult,
					ToolUseID:  "call_1",
					ToolResult: "file not found",
					IsError:    true,
				},
			},
		},
	}

	tools := []api.ToolDef{
		{
			Name:        "read_file",
			Description: "Reads a file from disk",
			InputSchema: map[string]any{
				"path": map[string]any{"type": "string"},
			},
			Required: []string{"path"},
		},
	}

	resp, err := prov.Send(context.Background(), history, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify outbound tool schema translation.
	if len(decodedReq.Tools) != 1 {
		t.Fatalf("expected 1 serialized tool, got %d", len(decodedReq.Tools))
	}
	if decodedReq.Tools[0].Type != "function" || decodedReq.Tools[0].Function.Name != "read_file" {
		t.Errorf("unexpected serialized tool header: %+v", decodedReq.Tools[0])
	}
	if decodedReq.Tools[0].Function.Parameters.Type != "object" {
		t.Errorf("expected parameters type 'object', got %q", decodedReq.Tools[0].Function.Parameters.Type)
	}

	// Verify outbound multi-turn message translation (no system message since system was empty).
	if len(decodedReq.Messages) != 3 {
		t.Fatalf("expected 3 translated messages, got %d", len(decodedReq.Messages))
	}
	assistantTurn := decodedReq.Messages[1]
	if assistantTurn.Role != "assistant" || len(assistantTurn.ToolCalls) != 1 || assistantTurn.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected assistant tool_use translation: %+v", assistantTurn)
	}

	toolResultTurn := decodedReq.Messages[2]
	if toolResultTurn.Role != "tool" {
		t.Errorf("expected role 'tool' for BlockToolResult, got %q", toolResultTurn.Role)
	}
	if toolResultTurn.ToolName != "read_file" {
		t.Errorf("expected resolved ToolName 'read_file', got %q", toolResultTurn.ToolName)
	}
	if toolResultTurn.Content != "Error: file not found" {
		t.Errorf("expected error-prefixed content 'Error: file not found', got %q", toolResultTurn.Content)
	}

	// Verify inbound normalization: text block + 2 tool_use blocks + StopReason="tool_use".
	if resp.StopReason != "tool_use" {
		t.Errorf("expected StopReason 'tool_use', got %q", resp.StopReason)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("expected 3 normalized blocks (1 text + 2 tool_use), got %d", len(resp.Content))
	}
	if resp.Content[1].ToolUseID != "tc_explicit" || resp.Content[1].ToolName != "read_file" {
		t.Errorf("unexpected first tool_use block: %+v", resp.Content[1])
	}
	if !strings.Contains(resp.Content[1].ToolInput, `"path"`) {
		t.Errorf("expected preserved JSON arguments in ToolInput, got %q", resp.Content[1].ToolInput)
	}
	if resp.Content[2].ToolUseID != "ollama_call_2" || resp.Content[2].ToolInput != "{}" {
		t.Errorf("expected synthetic fallback ID 'ollama_call_2' and '{}' args, got %+v", resp.Content[2])
	}
}

// TestOllamaProvider_ErrorAndEdgeHandling covers nil receivers, HTTP error responses (JSON and plain text),
// HTTP 200 embedded error payloads, malformed JSON bodies, and cancelled contexts.
func TestOllamaProvider_ErrorAndEdgeHandling(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var nilProv *OllamaProvider
		_, err := nilProv.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("expected nil provider error, got %v", err)
		}
	})

	t.Run("HTTP 404 with JSON error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "model 'ghost' not found"}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "ghost", "")
		_, err := prov.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model 'ghost' not found") {
			t.Fatalf("expected formatted 404 JSON error, got %v", err)
		}
	})

	t.Run("HTTP 500 with plain text body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal server crash"))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "llama3.2", "")
		_, err := prov.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal server crash") {
			t.Fatalf("expected formatted 500 plain-text error, got %v", err)
		}
	})

	t.Run("HTTP 200 with embedded error field", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"error": "out of VRAM"}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "llama3.2", "")
		_, err := prov.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "out of VRAM") {
			t.Fatalf("expected embedded ollama error, got %v", err)
		}
	})

	t.Run("malformed JSON response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "llama3.2", "")
		_, err := prov.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "decode ollama response") {
			t.Fatalf("expected decode error, got %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"too late"},"done":true}`))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "llama3.2", "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := prov.Send(ctx, nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

// TestOllamaProvider_ListModels_WithHTTPTest verifies GET /api/tags catalog parsing,
// whitespace/empty entry filtering, empty lists, HTTP errors, and nil/context guards.
func TestOllamaProvider_ListModels_WithHTTPTest(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var nilProv *OllamaProvider
		if _, err := nilProv.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("expected nil provider error, got %v", err)
		}
	})

	t.Run("parses tags from GET /api/tags", func(t *testing.T) {
		var capturedMethod string
		var capturedPath string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedMethod = r.Method
			capturedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"models": [
					{"name": " llama3.2:latest "},
					{"name": ""},
					{"name": "qwen3-coder:latest"}
				]
			}`))
		}))
		defer srv.Close()

		models, err := NewOllamaProvider(srv.URL, "llama3.2", "").ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected ListModels error: %v", err)
		}
		if capturedMethod != http.MethodGet || capturedPath != "/api/tags" {
			t.Errorf("expected GET /api/tags, got %s %s", capturedMethod, capturedPath)
		}
		if len(models) != 2 || models[0] != "llama3.2:latest" || models[1] != "qwen3-coder:latest" {
			t.Fatalf("expected [llama3.2:latest qwen3-coder:latest], got %v", models)
		}
	})

	t.Run("empty tags catalog", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"models": []}`))
		}))
		defer srv.Close()

		models, err := NewOllamaProvider(srv.URL, "llama3.2", "").ListModels(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(models) != 0 {
			t.Fatalf("expected 0 models, got %v", models)
		}
	})

	t.Run("HTTP 500 JSON error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "daemon unavailable"}`))
		}))
		defer srv.Close()

		_, err := NewOllamaProvider(srv.URL, "llama3.2", "").ListModels(context.Background())
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "daemon unavailable") {
			t.Fatalf("expected HTTP 500 error, got %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"models": []}`))
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := NewOllamaProvider(srv.URL, "llama3.2", "").ListModels(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

// TestOllamaProvider_Stream_WithHTTPTest verifies NDJSON /api/chat streaming with stream=true:
// incremental text emission to onChunk, tool_calls consolidation with synthetic call IDs,
// nil-callback tolerance, embedded stream errors, HTTP errors, and nil/context guards.
func TestOllamaProvider_Stream_WithHTTPTest(t *testing.T) {
	t.Run("nil receiver and nil client", func(t *testing.T) {
		var nilProv *OllamaProvider
		if _, err := nilProv.Stream(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("expected nil provider error, got %v", err)
		}
		if _, err := (&OllamaProvider{baseURL: defaultOllamaBaseURL}).Stream(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "client is nil") {
			t.Fatalf("expected nil client error, got %v", err)
		}
	})

	t.Run("streams NDJSON text chunks and consolidates tool_calls", func(t *testing.T) {
		var capturedPath string
		var capturedStream bool

		ndjson := strings.Join([]string{
			`{"message":{"role":"assistant","content":"Local "},"done":false}`,
			`{"message":{"role":"assistant","content":"stream"},"done":false}`,
			`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"read_file","arguments":{"path":"go.mod"}}}]},"done":true,"done_reason":"stop"}`,
			``,
		}, "\n")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("failed reading request body: %v", err)
			}
			var req ollamaChatRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("failed decoding request JSON: %v", err)
			}
			capturedStream = req.Stream

			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(ndjson))
		}))
		defer srv.Close()

		prov := NewOllamaProvider(srv.URL, "llama3.2", "You are concise.")
		var chunks []string
		resp, err := prov.Stream(
			context.Background(),
			[]api.Message{{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "read go.mod"}}}},
			nil,
			func(chunk string) {
				chunks = append(chunks, chunk)
			},
		)
		if err != nil {
			t.Fatalf("unexpected Stream error: %v", err)
		}
		if capturedPath != "/api/chat" || !capturedStream {
			t.Errorf("expected POST /api/chat with stream=true, got path=%q stream=%v", capturedPath, capturedStream)
		}
		if len(chunks) != 2 || chunks[0] != "Local " || chunks[1] != "stream" {
			t.Fatalf("expected chunks [Local  stream], got %#v", chunks)
		}
		if resp.StopReason != "tool_use" {
			t.Errorf("expected StopReason 'tool_use', got %q", resp.StopReason)
		}
		if len(resp.Content) != 2 {
			t.Fatalf("expected 2 blocks (text + tool_use), got %+v", resp.Content)
		}
		if resp.Content[0].Type != api.BlockText || resp.Content[0].Text != "Local stream" {
			t.Errorf("expected consolidated text 'Local stream', got %+v", resp.Content[0])
		}
		toolBlock := resp.Content[1]
		if toolBlock.Type != api.BlockToolUse || toolBlock.ToolUseID != "ollama_call_1" || toolBlock.ToolName != "read_file" || toolBlock.ToolInput != `{"path":"go.mod"}` {
			t.Errorf("unexpected tool_use block: %+v", toolBlock)
		}

		// Nil callback safety
		nilResp, err := prov.Stream(context.Background(), nil, nil, nil)
		if err != nil {
			t.Fatalf("unexpected nil-callback Stream error: %v", err)
		}
		if len(nilResp.Content) != 2 || nilResp.Content[0].Text != "Local stream" {
			t.Errorf("unexpected response for nil callback: %+v", nilResp)
		}
	})

	t.Run("embedded stream error and HTTP 500", func(t *testing.T) {
		srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"partial\"},\"done\":false}\n{\"error\":\"model unloaded\"}\n"))
		}))
		defer srvErr.Close()

		_, err := NewOllamaProvider(srvErr.URL, "llama3.2", "").Stream(context.Background(), nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "model unloaded") {
			t.Fatalf("expected embedded stream error 'model unloaded', got %v", err)
		}

		srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"gpu error"}`))
		}))
		defer srvHTTP.Close()

		_, err = NewOllamaProvider(srvHTTP.URL, "llama3.2", "").Stream(context.Background(), nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "gpu error") {
			t.Fatalf("expected HTTP 500 formatted error, got %v", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{\"message\":{\"role\":\"assistant\",\"content\":\"too late\"},\"done\":true}\n"))
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := NewOllamaProvider(srv.URL, "llama3.2", "").Stream(ctx, nil, nil, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}

