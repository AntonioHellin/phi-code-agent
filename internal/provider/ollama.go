package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"phi-agent-code/internal/api"
)

const (
	// defaultOllamaBaseURL targets a standard local Ollama daemon when no override is given.
	defaultOllamaBaseURL = "http://localhost:11434"
	// defaultOllamaTimeout prevents local inference or hung sockets from blocking the agent loop indefinitely.
	defaultOllamaTimeout = 2 * time.Minute
)

// OllamaProvider implements the Provider interface against Ollama's /api/chat REST endpoint
// using only Go's standard library (zero external SDK dependencies).
type OllamaProvider struct {
	client  *http.Client // HTTP client reused across inference turns
	baseURL string       // Base daemon URL without trailing slash (e.g. "http://localhost:11434")
	model   string       // Active Ollama model tag (e.g. "llama3.2", "qwen2.5-coder")
	system  string       // Optional system prompt injected as the leading system message
}

// ollamaChatRequest models the JSON body expected by Ollama's POST /api/chat endpoint.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"` // Explicit false disables NDJSON streaming so Send parses a single JSON payload
}

// ollamaChatResponse models a non-streaming response payload returned by POST /api/chat.
type ollamaChatResponse struct {
	Message    ollamaMessage `json:"message"`
	Done       bool          `json:"done"`
	DoneReason string        `json:"done_reason,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// ollamaMessage represents a single turn in Ollama's OpenAI-style chat history.
type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"` // Identifies which tool produced a role="tool" result message
}

// ollamaToolCall captures a model-requested function invocation in an assistant turn.
type ollamaToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Function ollamaToolCallFunction `json:"function"`
}

// ollamaToolCallFunction carries the target tool name and raw JSON argument object.
type ollamaToolCallFunction struct {
	Name string `json:"name"`
	// RawMessage preserves the JSON object directly without extra marshal/unmarshal overhead.
	Arguments json.RawMessage `json:"arguments"`
}

// ollamaTool declares an executable tool schema in Ollama's function-calling format.
type ollamaTool struct {
	Type     string             `json:"type"` // Always "function" in Ollama's schema
	Function ollamaToolFunction `json:"function"`
}

// ollamaToolFunction describes the tool identifier, purpose, and JSON Schema parameters.
type ollamaToolFunction struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Parameters  ollamaParameters `json:"parameters"`
}

// ollamaParameters wraps tool input properties in a standard JSON Schema object envelope.
type ollamaParameters struct {
	Type       string         `json:"type"` // Always "object"
	Properties map[string]any `json:"properties,omitempty"`
	Required   []string       `json:"required,omitempty"`
}

// NewOllamaProvider initializes an Ollama adapter, falling back to http://localhost:11434
// when baseURL is empty and stripping trailing slashes to build clean endpoint URLs.
func NewOllamaProvider(baseURL, model, system string) *OllamaProvider {
	cleanedURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanedURL == "" {
		cleanedURL = defaultOllamaBaseURL
	}

	return &OllamaProvider{
		client:  &http.Client{Timeout: defaultOllamaTimeout},
		baseURL: cleanedURL,
		model:   model,
		system:  system,
	}
}

// Model returns the active Ollama model tag.
func (p *OllamaProvider) Model() string {
	return p.model
}

// SetModel dynamically switches the target Ollama model tag.
func (p *OllamaProvider) SetModel(name string) {
	p.model = name
}

// Send translates domain messages and tool definitions into an Ollama /api/chat request,
// executes the HTTP call with context cancellation, and normalizes the response back into api.Response.
func (p *OllamaProvider) Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error) {
	if p == nil {
		return api.Response{}, errors.New("ollama provider is nil")
	}

	reqBody := ollamaChatRequest{
		Model:    p.model,
		Messages: toOllamaMessages(p.system, messages),
		Tools:    toOllamaTools(tools),
		Stream:   false,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return api.Response{}, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return api.Response{}, fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return api.Response{}, fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.Response{}, fmt.Errorf("read ollama response: %w", err)
	}

	var chatResp ollamaChatResponse
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Surface Ollama's structured JSON error message when available.
		if json.Unmarshal(bodyBytes, &chatResp) == nil && chatResp.Error != "" {
			return api.Response{}, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, chatResp.Error)
		}
		return api.Response{}, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return api.Response{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if chatResp.Error != "" {
		return api.Response{}, fmt.Errorf("ollama error: %s", chatResp.Error)
	}

	var outBlocks []api.Block
	if chatResp.Message.Content != "" {
		outBlocks = append(outBlocks, api.Block{
			Type: api.BlockText,
			Text: chatResp.Message.Content,
		})
	}

	for i, tc := range chatResp.Message.ToolCalls {
		callID := tc.ID
		if callID == "" {
			// Some Ollama models omit tool call IDs; synthesize one so Agent can pair results cleanly.
			callID = fmt.Sprintf("ollama_call_%d", i+1)
		}

		rawArgs := strings.TrimSpace(string(tc.Function.Arguments))
		if rawArgs == "" {
			rawArgs = "{}"
		}

		outBlocks = append(outBlocks, api.Block{
			Type:      api.BlockToolUse,
			ToolUseID: callID,
			ToolName:  tc.Function.Name,
			ToolInput: rawArgs,
		})
	}

	// Normalize Ollama's completion reason into the Agent loop's expected StopReason contract.
	stopReason := "end_turn"
	if len(chatResp.Message.ToolCalls) > 0 {
		stopReason = "tool_use"
	}

	return api.Response{
		Content:    outBlocks,
		StopReason: stopReason,
	}, nil
}

// toOllamaMessages converts domain conversation history into Ollama's message array,
// prepending the system prompt and splitting tool results into dedicated role="tool" entries.
func toOllamaMessages(system string, messages []api.Message) []ollamaMessage {
	var out []ollamaMessage
	if strings.TrimSpace(system) != "" {
		out = append(out, ollamaMessage{
			Role:    "system",
			Content: system,
		})
	}

	// Track tool_use ID -> name so subsequent BlockToolResult blocks can populate ToolName.
	toolNamesByID := make(map[string]string)

	for _, msg := range messages {
		if msg.Role == api.RoleAssistant {
			var textParts []string
			var toolCalls []ollamaToolCall

			for _, b := range msg.Content {
				switch b.Type {
				case api.BlockText:
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				case api.BlockToolUse:
					if b.ToolUseID != "" && b.ToolName != "" {
						toolNamesByID[b.ToolUseID] = b.ToolName
					}
					rawArgs := json.RawMessage(strings.TrimSpace(b.ToolInput))
					if len(rawArgs) == 0 {
						rawArgs = json.RawMessage("{}")
					}
					toolCalls = append(toolCalls, ollamaToolCall{
						ID: b.ToolUseID,
						Function: ollamaToolCallFunction{
							Name:      b.ToolName,
							Arguments: rawArgs,
						},
					})
				}
			}

			out = append(out, ollamaMessage{
				Role:      "assistant",
				Content:   strings.Join(textParts, "\n"),
				ToolCalls: toolCalls,
			})
			continue
		}

		// User turns in domain history can carry plain text and/or wrapped BlockToolResult items.
		var userTexts []string
		for _, b := range msg.Content {
			switch b.Type {
			case api.BlockText:
				if b.Text != "" {
					userTexts = append(userTexts, b.Text)
				}
			case api.BlockToolResult:
				content := b.ToolResult
				if b.IsError && content != "" {
					content = "Error: " + content
				}
				out = append(out, ollamaMessage{
					Role:     "tool",
					ToolName: toolNamesByID[b.ToolUseID],
					Content:  content,
				})
			}
		}
		if len(userTexts) > 0 {
			out = append(out, ollamaMessage{
				Role:    "user",
				Content: strings.Join(userTexts, "\n"),
			})
		}
	}

	return out
}

// toOllamaTools maps domain ToolDefs into Ollama's OpenAI-compatible function tool schema.
func toOllamaTools(tools []api.ToolDef) []ollamaTool {
	if len(tools) == 0 {
		return nil
	}

	out := make([]ollamaTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, ollamaTool{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters: ollamaParameters{
					Type:       "object",
					Properties: t.InputSchema,
					Required:   t.Required,
				},
			},
		})
	}
	return out
}

// Compile-time assertion ensuring OllamaProvider satisfies the Provider interface.
var _ Provider = (*OllamaProvider)(nil)

