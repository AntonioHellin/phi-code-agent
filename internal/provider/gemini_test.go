package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"

	"phi-agent-code/internal/api"
)

// TestGeminiProvider_ModelAccessors verifies caller-supplied model initialization,
// dynamic updates via SetModel, and nil-receiver safety.
func TestGeminiProvider_ModelAccessors(t *testing.T) {
	var nilProv *GeminiProvider
	if nilProv.Model() != "" {
		t.Fatalf("expected empty model for nil receiver, got %q", nilProv.Model())
	}

	prov := NewGeminiProvider(nil, "  gemini-2.5-flash  ", 4096, "test-prompt")
	if prov.Model() != "gemini-2.5-flash" {
		t.Fatalf("expected trimmed model 'gemini-2.5-flash', got %q", prov.Model())
	}

	prov.SetModel("gemini-2.5-pro")
	if prov.Model() != "gemini-2.5-pro" {
		t.Fatalf("expected updated model 'gemini-2.5-pro', got %q", prov.Model())
	}
}

// TestToGeminiContents_AndTools verifies domain-to-Gemini SDK translation for roles ("user"/"model"),
// functionCall blocks (omitting synthetic gemini_call_ IDs), functionResponse "output"/"error" objects,
// and single-envelope FunctionDeclarations tool schemas.
func TestToGeminiContents_AndTools(t *testing.T) {
	history := []api.Message{
		{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: "Inspect go.mod and missing.txt"}},
		},
		{
			Role: api.RoleAssistant,
			Content: []api.Block{
				{
					Type:      api.BlockToolUse,
					ToolUseID: "fc_real_1",
					ToolName:  "read_file",
					ToolInput: `{"path":"go.mod"}`,
				},
				{
					Type:      api.BlockToolUse,
					ToolUseID: "gemini_call_2",
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
					ToolUseID:  "fc_real_1",
					ToolResult: "module phi-agent-code",
					IsError:    false,
				},
				{
					Type:       api.BlockToolResult,
					ToolUseID:  "gemini_call_2",
					ToolResult: "file not found",
					IsError:    true,
				},
			},
		},
	}

	contents := toGeminiContents(history)
	if len(contents) != 3 {
		t.Fatalf("expected 3 Gemini contents, got %d", len(contents))
	}

	if contents[0].Role != genai.RoleUser || contents[0].Parts[0].Text != "Inspect go.mod and missing.txt" {
		t.Errorf("unexpected first user content: %+v", contents[0])
	}

	modelContent := contents[1]
	if modelContent.Role != genai.RoleModel || len(modelContent.Parts) != 2 {
		t.Fatalf("expected role 'model' with 2 functionCall parts, got role=%q parts=%d", modelContent.Role, len(modelContent.Parts))
	}
	if modelContent.Parts[0].FunctionCall.ID != "fc_real_1" {
		t.Errorf("expected preserved real call ID 'fc_real_1', got %q", modelContent.Parts[0].FunctionCall.ID)
	}
	if modelContent.Parts[1].FunctionCall.ID != "" {
		t.Errorf("expected synthetic gemini_call_ ID to be omitted from wire FunctionCall.ID, got %q", modelContent.Parts[1].FunctionCall.ID)
	}

	toolRespContent := contents[2]
	if toolRespContent.Role != genai.RoleUser || len(toolRespContent.Parts) != 2 {
		t.Fatalf("expected role 'user' with 2 functionResponse parts, got role=%q parts=%d", toolRespContent.Role, len(toolRespContent.Parts))
	}

	okResp := toolRespContent.Parts[0].FunctionResponse
	if okResp.Name != "read_file" || okResp.ID != "fc_real_1" || okResp.Response["output"] != "module phi-agent-code" {
		t.Errorf("unexpected success FunctionResponse: %+v", okResp)
	}

	errResp := toolRespContent.Parts[1].FunctionResponse
	if errResp.Name != "read_file" || errResp.ID != "" || errResp.Response["error"] != "file not found" {
		t.Errorf("unexpected error FunctionResponse: %+v", errResp)
	}

	if toGeminiTools(nil) != nil {
		t.Errorf("expected nil tools when input slice is empty")
	}

	tools := toGeminiTools([]api.ToolDef{
		{
			Name:        "read_file",
			Description: "Reads a file",
			InputSchema: map[string]any{"path": map[string]any{"type": "string"}},
			Required:    []string{"path"},
		},
	})
	if len(tools) != 1 || len(tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected single Tool wrapping 1 FunctionDeclaration, got %+v", tools)
	}
	decl := tools[0].FunctionDeclarations[0]
	if decl.Name != "read_file" || decl.ParametersJsonSchema == nil {
		t.Errorf("unexpected FunctionDeclaration: %+v", decl)
	}
}

// TestGeminiProvider_SendText_WithHTTPTest verifies end-to-end SDK serialization against an
// httptest.Server (path, x-goog-api-key header, systemInstruction, thought filtering, and end_turn).
func TestGeminiProvider_SendText_WithHTTPTest(t *testing.T) {
	var capturedPath string
	var capturedAPIKey string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAPIKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("failed decoding SDK request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		// Include a Thought part to confirm internal reasoning is excluded from api.Response.
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"content": {
						"role": "model",
						"parts": [
							{"text": "hidden reasoning", "thought": true},
							{"text": "Hello from Gemini"}
						]
					},
					"finishReason": "STOP"
				}
			]
		}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test-gemini-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  srv.Client(),
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatalf("failed creating genai client: %v", err)
	}

	prov := NewGeminiProvider(client, "gemini-2.5-flash", 2048, "Be ultra concise.")
	resp, err := prov.Send(ctx, []api.Message{
		{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: "Say hi"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected Send error: %v", err)
	}

	if capturedPath != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Errorf("expected path '/v1beta/models/gemini-2.5-flash:generateContent', got %q", capturedPath)
	}
	if capturedAPIKey != "test-gemini-key" {
		t.Errorf("expected x-goog-api-key 'test-gemini-key', got %q", capturedAPIKey)
	}
	if _, hasSys := capturedBody["systemInstruction"]; !hasSys {
		t.Errorf("expected systemInstruction in request payload, got keys: %v", capturedBody)
	}

	if resp.StopReason != "end_turn" {
		t.Errorf("expected StopReason 'end_turn', got %q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != api.BlockText || resp.Content[0].Text != "Hello from Gemini" {
		t.Fatalf("expected only non-thought text block 'Hello from Gemini', got %+v", resp.Content)
	}
}

// TestGeminiProvider_SendFunctionCall_WithHTTPTest verifies functionCall normalization,
// synthetic call ID generation when omitted, and StopReason="tool_use".
func TestGeminiProvider_SendFunctionCall_WithHTTPTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [
				{
					"content": {
						"role": "model",
						"parts": [
							{
								"functionCall": {
									"id": "call_srv_1",
									"name": "read_file",
									"args": {"path": "main.go"}
								}
							},
							{
								"functionCall": {
									"name": "list_dir",
									"args": {}
								}
							}
						]
					},
					"finishReason": "STOP"
				}
			]
		}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test-gemini-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  srv.Client(),
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatalf("failed creating genai client: %v", err)
	}

	prov := NewGeminiProvider(client, "gemini-2.5-flash", 0, "")
	resp, err := prov.Send(ctx, []api.Message{
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Read main.go"}}},
	}, []api.ToolDef{{Name: "read_file", Description: "Read file"}})
	if err != nil {
		t.Fatalf("unexpected Send error: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Fatalf("expected StopReason 'tool_use', got %q", resp.StopReason)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("expected 2 tool_use blocks, got %d", len(resp.Content))
	}
	if resp.Content[0].ToolUseID != "call_srv_1" || resp.Content[0].ToolName != "read_file" {
		t.Errorf("unexpected first tool_use block: %+v", resp.Content[0])
	}
	if !strings.Contains(resp.Content[0].ToolInput, `"main.go"`) {
		t.Errorf("expected serialized JSON args for read_file, got %q", resp.Content[0].ToolInput)
	}
	if resp.Content[1].ToolUseID != "gemini_call_2" || resp.Content[1].ToolInput != "{}" {
		t.Errorf("expected synthetic fallback ID 'gemini_call_2' and '{}' args, got %+v", resp.Content[1])
	}
}

// TestGeminiProvider_GuardsAndErrors covers nil receiver, nil client, empty model,
// and server-side HTTP errors surfaced through the SDK.
func TestGeminiProvider_GuardsAndErrors(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var nilProv *GeminiProvider
		_, err := nilProv.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("expected nil provider error, got %v", err)
		}
	})

	t.Run("nil client", func(t *testing.T) {
		prov := NewGeminiProvider(nil, "gemini-2.5-flash", 0, "")
		_, err := prov.Send(context.Background(), nil, nil)
		if err == nil || !strings.Contains(err.Error(), "client is nil") {
			t.Fatalf("expected nil client error, got %v", err)
		}
	})

	t.Run("empty model", func(t *testing.T) {
		ctx := context.Background()
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  "test-key",
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			t.Fatalf("failed creating genai client: %v", err)
		}
		prov := NewGeminiProvider(client, "   ", 0, "")
		_, err = prov.Send(ctx, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "model is empty") {
			t.Fatalf("expected empty model error, got %v", err)
		}
	})

	t.Run("HTTP 400 API error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error": {"code": 400, "message": "Invalid model name", "status": "INVALID_ARGUMENT"}}`))
		}))
		defer srv.Close()

		ctx := context.Background()
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:      "test-key",
			Backend:     genai.BackendGeminiAPI,
			HTTPClient:  srv.Client(),
			HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
		})
		if err != nil {
			t.Fatalf("failed creating genai client: %v", err)
		}
		prov := NewGeminiProvider(client, "bad-model", 0, "")
		_, err = prov.Send(ctx, []api.Message{
			{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Hi"}}},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "Invalid model name") {
			t.Fatalf("expected Gemini API error message, got %v", err)
		}
	})
}

// TestGeminiProvider_ListModels_WithHTTPTest verifies paginated /v1beta/models queries,
// filtering to models supporting "generateContent", "models/" prefix stripping, and nil guards.
func TestGeminiProvider_ListModels_WithHTTPTest(t *testing.T) {
	var nilProv *GeminiProvider
	if _, err := nilProv.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("expected nil provider error, got %v", err)
	}
	if _, err := NewGeminiProvider(nil, "gemini-4-hydrogen", 0, "").ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "client is nil") {
		t.Fatalf("expected nil client error, got %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1beta/models" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "page2" {
			_, _ = w.Write([]byte(`{
				"models": [
					{
						"name": "models/gemini-3.8-flash",
						"supportedGenerationMethods": ["generateContent", "countTokens"]
					}
				]
			}`))
			return
		}

		_, _ = w.Write([]byte(`{
			"models": [
				{
					"name": "models/gemini-4-hydrogen",
					"supportedGenerationMethods": ["generateContent"]
				},
				{
					"name": "models/text-embedding-004",
					"supportedGenerationMethods": ["embedContent"]
				}
			],
			"nextPageToken": "page2"
		}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:      "test-key",
		Backend:     genai.BackendGeminiAPI,
		HTTPClient:  srv.Client(),
		HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatalf("failed creating genai client: %v", err)
	}

	models, err := NewGeminiProvider(client, "gemini-4-hydrogen", 0, "").ListModels(ctx)
	if err != nil {
		t.Fatalf("unexpected ListModels error: %v", err)
	}

	if len(models) != 2 || models[0] != "gemini-4-hydrogen" || models[1] != "gemini-3.8-flash" {
		t.Fatalf("expected [gemini-4-hydrogen gemini-3.8-flash], got %v", models)
	}
}

