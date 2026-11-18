package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"phi-agent-code/internal/api"
)

const (
	// defaultOllamaBaseURL targets a standard local Ollama daemon when no override is given.
	defaultOllamaBaseURL = "http://localhost:11434"
	// defaultOllamaDialTimeout bounds TCP connection establishment when the local daemon is down.
	defaultOllamaDialTimeout = 10 * time.Second
	// defaultOllamaHeaderTimeout bounds initial response headers (including cold model load)
	// without setting http.Client.Timeout, which would otherwise abort long-running streams mid-body.
	defaultOllamaHeaderTimeout = 2 * time.Minute
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
	Stream   bool            `json:"stream"` // Explicit false for single JSON Send; true for NDJSON Stream
}

// ollamaChatResponse models a non-streaming response payload returned by POST /api/chat.
type ollamaChatResponse struct {
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	DoneReason      string        `json:"done_reason,omitempty"`
	Error           string        `json:"error,omitempty"`
	PromptEvalCount int           `json:"prompt_eval_count,omitempty"`
	EvalCount       int           `json:"eval_count,omitempty"`
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

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: defaultOllamaDialTimeout}).DialContext,
		ResponseHeaderTimeout: defaultOllamaHeaderTimeout,
	}

	return &OllamaProvider{
		// Leave Client.Timeout=0 so active NDJSON streams can exceed header-wait duration cleanly;
		// caller ctx controls overall cancellation.
		client:  &http.Client{Transport: transport},
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

// validate guards against nil provider receivers or uninitialized HTTP clients.
func (p *OllamaProvider) validate() error {
	if p == nil {
		return errors.New("ollama provider is nil")
	}
	if p.client == nil {
		return errors.New("ollama client is nil")
	}
	return nil
}

// buildParams constructs the shared Ollama /api/chat request body for both Send and Stream.
func (p *OllamaProvider) buildParams(messages []api.Message, tools []api.ToolDef, stream bool) ollamaChatRequest {
	return ollamaChatRequest{
		Model:    p.model,
		Messages: toOllamaMessages(p.system, messages),
		Tools:    toOllamaTools(tools),
		Stream:   stream,
	}
}

// doChatRequest serializes the Ollama /api/chat request, executes the HTTP call,
// and validates non-2xx responses uniformly for Send and Stream.
func (p *OllamaProvider) doChatRequest(ctx context.Context, messages []api.Message, tools []api.ToolDef, stream bool) (*http.Response, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(p.buildParams(messages, tools, stream))
	if err != nil {
		return nil, fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("ollama HTTP %d (read error: %w)", resp.StatusCode, readErr)
		}
		var errResp ollamaChatResponse
		// Surface Ollama's structured JSON error message when available.
		if json.Unmarshal(bodyBytes, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	return resp, nil
}

// ollamaUsageFromChunk maps Ollama's prompt/generation evaluation counts into api.Usage.
func ollamaUsageFromChunk(r ollamaChatResponse) api.Usage {
	in := r.PromptEvalCount
	if in < 0 {
		in = 0
	}
	out := r.EvalCount
	if out < 0 {
		out = 0
	}
	return api.Usage{
		InputTokens:  in,
		OutputTokens: out,
	}
}

// buildOllamaResponse converts accumulated text, tool calls, and usage metadata into domain api.Response.
func buildOllamaResponse(text string, toolCalls []ollamaToolCall, usage api.Usage) api.Response {
	var outBlocks []api.Block
	if text != "" {
		outBlocks = append(outBlocks, api.Block{
			Type: api.BlockText,
			Text: text,
		})
	}

	for i, tc := range toolCalls {
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
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	return api.Response{
		Content:    outBlocks,
		StopReason: stopReason,
		Usage:      usage,
	}
}

// Send translates domain messages and tool definitions into an Ollama /api/chat request,
// executes the HTTP call with context cancellation, and normalizes the response back into api.Response.
func (p *OllamaProvider) Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error) {
	resp, err := p.doChatRequest(ctx, messages, tools, false)
	if err != nil {
		return api.Response{}, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return api.Response{}, fmt.Errorf("read ollama response: %w", err)
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return api.Response{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if chatResp.Error != "" {
		return api.Response{}, fmt.Errorf("ollama error: %s", chatResp.Error)
	}

	return buildOllamaResponse(chatResp.Message.Content, chatResp.Message.ToolCalls, ollamaUsageFromChunk(chatResp)), nil
}

// Stream implements provider.Streamer against Ollama's NDJSON /api/chat stream ("stream": true).
// It decodes each JSON line incrementally, forwards text tokens to onChunk, and gathers tool calls and final usage.
func (p *OllamaProvider) Stream(ctx context.Context, messages []api.Message, tools []api.ToolDef, onChunk func(string)) (api.Response, error) {
	resp, err := p.doChatRequest(ctx, messages, tools, true)
	if err != nil {
		return api.Response{}, err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var textBuf strings.Builder
	var toolCalls []ollamaToolCall
	var usage api.Usage

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var chunk ollamaChatResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			return api.Response{}, fmt.Errorf("decode ollama stream chunk: %w", err)
		}
		if chunk.Error != "" {
			return api.Response{}, fmt.Errorf("ollama error: %s", chunk.Error)
		}

		// Ollama emits prompt_eval_count and eval_count on the final done=true chunk; overwrite cleanly.
		if chunk.PromptEvalCount > 0 || chunk.EvalCount > 0 || chunk.Done {
			usage = ollamaUsageFromChunk(chunk)
		}

		if chunk.Message.Content != "" {
			textBuf.WriteString(chunk.Message.Content)
			if onChunk != nil {
				onChunk(chunk.Message.Content)
			}
		}
		if len(chunk.Message.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.Message.ToolCalls...)
		}
		if chunk.Done {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return api.Response{}, fmt.Errorf("read ollama stream: %w", err)
	}

	return buildOllamaResponse(textBuf.String(), toolCalls, usage), nil
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

// ollamaTagsResponse models the JSON payload returned by Ollama's GET /api/tags endpoint.
type ollamaTagsResponse struct {
	Models []ollamaModelTag `json:"models"`
	Error  string           `json:"error,omitempty"`
}

// ollamaModelTag represents a locally pulled model entry in /api/tags.
type ollamaModelTag struct {
	Name string `json:"name"`
}

// ListModels queries Ollama's local /api/tags catalog on demand and returns installed model names.
func (p *OllamaProvider) ListModels(ctx context.Context) ([]string, error) {
	if p == nil {
		return nil, errors.New("ollama provider is nil")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("create ollama tags request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama tags request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ollama tags response: %w", err)
	}

	var tagsResp ollamaTagsResponse
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		if json.Unmarshal(bodyBytes, &tagsResp) == nil && tagsResp.Error != "" {
			return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, tagsResp.Error)
		}
		return nil, fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if err := json.Unmarshal(bodyBytes, &tagsResp); err != nil {
		return nil, fmt.Errorf("decode ollama tags response: %w", err)
	}
	if tagsResp.Error != "" {
		return nil, fmt.Errorf("ollama error: %s", tagsResp.Error)
	}

	models := make([]string, 0, len(tagsResp.Models))
	for _, m := range tagsResp.Models {
		if name := strings.TrimSpace(m.Name); name != "" {
			models = append(models, name)
		}
	}
	return models, nil
}

// Compile-time assertions ensuring OllamaProvider satisfies Provider, ModelLister, and Streamer.
var (
	_ Provider    = (*OllamaProvider)(nil)
	_ ModelLister = (*OllamaProvider)(nil)
	_ Streamer    = (*OllamaProvider)(nil)
)
