package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"phi-agent-code/internal/api"
)

// GeminiProvider implements the Provider interface by adapting domain types into
// Google's official google.golang.org/genai SDK wire structures.
type GeminiProvider struct {
	client    *genai.Client // Official Google GenAI client configured with API key and HTTP options
	model     string        // Active Gemini model ID injected by the caller (e.g. "gemini-2.5-flash")
	maxTokens int32         // Optional completion token cap (0 defers to model default)
	system    string        // System instruction applied via GenerateContentConfig
}

// NewGeminiProvider initializes the Gemini adapter with required SDK client and caller-supplied model.
func NewGeminiProvider(client *genai.Client, model string, maxTokens int32, system string) *GeminiProvider {
	return &GeminiProvider{
		client:    client,
		model:     strings.TrimSpace(model),
		maxTokens: maxTokens,
		system:    system,
	}
}

// Model returns the active Gemini model identifier.
func (p *GeminiProvider) Model() string {
	if p == nil {
		return ""
	}
	return p.model
}

// SetModel dynamically updates the active Gemini model identifier.
func (p *GeminiProvider) SetModel(name string) {
	if p != nil {
		p.model = strings.TrimSpace(name)
	}
}

// Send fulfills the Provider interface by translating domain history and tool definitions
// into Gemini GenerateContent calls and normalizing candidates back into api.Response.
func (p *GeminiProvider) Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error) {
	if p == nil {
		return api.Response{}, errors.New("gemini provider is nil")
	}
	if p.client == nil {
		return api.Response{}, errors.New("gemini client is nil")
	}
	if p.model == "" {
		return api.Response{}, errors.New("gemini model is empty")
	}

	cfg := &genai.GenerateContentConfig{}
	if strings.TrimSpace(p.system) != "" {
		cfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{genai.NewPartFromText(p.system)},
		}
	}
	if p.maxTokens > 0 {
		cfg.MaxOutputTokens = p.maxTokens
	}
	if len(tools) > 0 {
		cfg.Tools = toGeminiTools(tools)
	}

	resp, err := p.client.Models.GenerateContent(ctx, p.Model(), toGeminiContents(messages), cfg)
	if err != nil {
		return api.Response{}, fmt.Errorf("gemini generate content: %w", err)
	}

	var outBlocks []api.Block
	hasToolUse := false
	callSeq := 0

	if resp != nil && len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			// Skip internal model reasoning parts so final assistant text stays clean.
			if part == nil || part.Thought {
				continue
			}
			if part.Text != "" {
				outBlocks = append(outBlocks, api.Block{
					Type: api.BlockText,
					Text: part.Text,
				})
			}
			if part.FunctionCall != nil {
				hasToolUse = true
				callSeq++
				callID := part.FunctionCall.ID
				if callID == "" {
					// Synthesize a correlation ID when the model omits FunctionCall.ID.
					callID = fmt.Sprintf("gemini_call_%d", callSeq)
				}

				argsJSON := "{}"
				if len(part.FunctionCall.Args) > 0 {
					encoded, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						return api.Response{}, fmt.Errorf("marshal gemini function args: %w", err)
					}
					argsJSON = string(encoded)
				}

				outBlocks = append(outBlocks, api.Block{
					Type:      api.BlockToolUse,
					ToolUseID: callID,
					ToolName:  part.FunctionCall.Name,
					ToolInput: argsJSON,
				})
			}
		}
	}

	stopReason := "end_turn"
	if hasToolUse {
		stopReason = "tool_use"
	}

	return api.Response{
		Content:    outBlocks,
		StopReason: stopReason,
	}, nil
}

// toGeminiContents converts domain messages into Gemini SDK Contents using "user" and "model" roles.
func toGeminiContents(messages []api.Message) []*genai.Content {
	var contents []*genai.Content
	// Track tool_use ID -> tool name so subsequent BlockToolResult turns populate FunctionResponse.Name.
	toolNamesByID := make(map[string]string)

	for _, msg := range messages {
		role := genai.RoleUser
		if msg.Role == api.RoleAssistant {
			role = genai.RoleModel
		}

		var parts []*genai.Part
		for _, b := range msg.Content {
			switch b.Type {
			case api.BlockText:
				if b.Text != "" {
					parts = append(parts, genai.NewPartFromText(b.Text))
				}
			case api.BlockToolUse:
				if b.ToolUseID != "" && b.ToolName != "" {
					toolNamesByID[b.ToolUseID] = b.ToolName
				}
				args := map[string]any{}
				if strings.TrimSpace(b.ToolInput) != "" {
					_ = json.Unmarshal([]byte(b.ToolInput), &args)
				}
				part := genai.NewPartFromFunctionCall(b.ToolName, args)
				if b.ToolUseID != "" && !strings.HasPrefix(b.ToolUseID, "gemini_call_") {
					part.FunctionCall.ID = b.ToolUseID
				}
				parts = append(parts, part)
			case api.BlockToolResult:
				// Gemini FunctionResponse uses "output" and "error" keys inside a structured JSON object.
				payload := map[string]any{"output": b.ToolResult}
				if b.IsError {
					payload = map[string]any{"error": b.ToolResult}
				}
				toolName := toolNamesByID[b.ToolUseID]
				if toolName == "" {
					toolName = b.ToolUseID
				}
				part := genai.NewPartFromFunctionResponse(toolName, payload)
				if b.ToolUseID != "" && !strings.HasPrefix(b.ToolUseID, "gemini_call_") {
					part.FunctionResponse.ID = b.ToolUseID
				}
				parts = append(parts, part)
			}
		}

		if len(parts) > 0 {
			contents = append(contents, &genai.Content{
				Role:  role,
				Parts: parts,
			})
		}
	}

	return contents
}

// toGeminiTools wraps domain ToolDefs into Gemini's single Tool.FunctionDeclarations envelope.
func toGeminiTools(tools []api.ToolDef) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, &genai.FunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			ParametersJsonSchema: map[string]any{
				"type":       "object",
				"properties": t.InputSchema,
				"required":   t.Required,
			},
		})
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// Compile-time assertion ensuring GeminiProvider satisfies the Provider interface.
var _ Provider = (*GeminiProvider)(nil)

