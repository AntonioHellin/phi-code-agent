package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

// validate guards against nil receivers, uninitialized clients, or empty model configuration.
func (p *GeminiProvider) validate() error {
	if p == nil {
		return errors.New("gemini provider is nil")
	}
	if p.client == nil {
		return errors.New("gemini client is nil")
	}
	if p.model == "" {
		return errors.New("gemini model is empty")
	}
	return nil
}

// buildParams constructs shared Gemini generation settings (system, token cap, tools) for Send and Stream.
func (p *GeminiProvider) buildParams(tools []api.ToolDef) *genai.GenerateContentConfig {
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
	return cfg
}

// collectGeminiParts extracts non-thought text, FunctionCall parts, and normalized UsageMetadata from one SDK chunk.
// It forwards text deltas to onChunk while accumulating full domain blocks and surfacing blocks/errors.
func collectGeminiParts(resp *genai.GenerateContentResponse, textBuf *strings.Builder, toolBlocks *[]api.Block, callSeq *int, usage *api.Usage, onChunk func(string)) error {
	if resp == nil {
		return nil
	}
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		msg := strings.TrimSpace(resp.PromptFeedback.BlockReasonMessage)
		if msg != "" {
			return fmt.Errorf("gemini prompt blocked (%s): %s", resp.PromptFeedback.BlockReason, msg)
		}
		return fmt.Errorf("gemini prompt blocked (%s)", resp.PromptFeedback.BlockReason)
	}
	// Overwrite (never add) before the Candidates check: streams emit cumulative request totals
	// and may deliver final UsageMetadata on a trailing chunk with no Candidates.
	if usage != nil && resp.UsageMetadata != nil {
		inputTokens := int(resp.UsageMetadata.PromptTokenCount) + int(resp.UsageMetadata.ToolUsePromptTokenCount) - int(resp.UsageMetadata.CachedContentTokenCount)
		if inputTokens < 0 {
			inputTokens = 0
		}
		*usage = api.Usage{
			InputTokens: inputTokens,
			// Gemini bills reasoning ThoughtsTokenCount separately from visible CandidatesTokenCount.
			OutputTokens:    int(resp.UsageMetadata.CandidatesTokenCount) + int(resp.UsageMetadata.ThoughtsTokenCount),
			CacheReadTokens: int(resp.UsageMetadata.CachedContentTokenCount),
		}
	}
	if len(resp.Candidates) == 0 {
		return nil
	}

	cand := resp.Candidates[0]
	if cand == nil {
		return nil
	}

	if cand.Content != nil {
		for _, part := range cand.Content.Parts {
			// Skip internal model reasoning parts so final assistant text stays clean.
			if part == nil || part.Thought {
				continue
			}
			if part.Text != "" {
				textBuf.WriteString(part.Text)
				if onChunk != nil {
					onChunk(part.Text)
				}
			}
			if part.FunctionCall != nil {
				*callSeq++
				callID := part.FunctionCall.ID
				if callID == "" {
					// Synthesize a correlation ID when the model omits FunctionCall.ID.
					callID = fmt.Sprintf("gemini_call_%d", *callSeq)
				}

				argsJSON := "{}"
				if len(part.FunctionCall.Args) > 0 {
					encoded, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						return fmt.Errorf("marshal gemini function args: %w", err)
					}
					argsJSON = string(encoded)
				}

				*toolBlocks = append(*toolBlocks, api.Block{
					Type:             api.BlockToolUse,
					ToolUseID:        callID,
					ToolName:         part.FunctionCall.Name,
					ToolInput:        argsJSON,
					ThoughtSignature: slices.Clone(part.ThoughtSignature),
				})
			}
		}
	}

	switch cand.FinishReason {
	case "", genai.FinishReasonUnspecified, genai.FinishReasonStop, genai.FinishReasonMaxTokens, genai.FinishReasonContinuation:
		// Normal progress chunk or natural completion.
	default:
		msg := strings.TrimSpace(cand.FinishMessage)
		if msg != "" {
			return fmt.Errorf("gemini generation stopped (%s): %s", cand.FinishReason, msg)
		}
		return fmt.Errorf("gemini generation stopped (%s)", cand.FinishReason)
	}

	return nil
}

// buildGeminiResponse assembles consolidated text and tool-use blocks into domain api.Response.
func buildGeminiResponse(text string, toolBlocks []api.Block, usage api.Usage) api.Response {
	var outBlocks []api.Block
	if text != "" {
		outBlocks = append(outBlocks, api.Block{
			Type: api.BlockText,
			Text: text,
		})
	}
	outBlocks = append(outBlocks, toolBlocks...)

	stopReason := "end_turn"
	if len(toolBlocks) > 0 {
		stopReason = "tool_use"
	}

	return api.Response{
		Content:    outBlocks,
		StopReason: stopReason,
		Usage:      usage,
	}
}

// Send fulfills the Provider interface by translating domain history and tool definitions
// into Gemini GenerateContent calls and normalizing candidates back into api.Response.
func (p *GeminiProvider) Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error) {
	if err := p.validate(); err != nil {
		return api.Response{}, err
	}

	resp, err := p.client.Models.GenerateContent(ctx, p.Model(), toGeminiContents(messages), p.buildParams(tools))
	if err != nil {
		return api.Response{}, fmt.Errorf("gemini generate content: %w", err)
	}

	var textBuf strings.Builder
	var toolBlocks []api.Block
	var usage api.Usage
	callSeq := 0
	if err := collectGeminiParts(resp, &textBuf, &toolBlocks, &callSeq, &usage, nil); err != nil {
		return api.Response{}, err
	}

	return buildGeminiResponse(textBuf.String(), toolBlocks, usage), nil
}

// Stream implements provider.Streamer via Gemini's GenerateContentStream iterator.
// It emits non-thought text deltas to onChunk in real time while assembling the full Response.
func (p *GeminiProvider) Stream(ctx context.Context, messages []api.Message, tools []api.ToolDef, onChunk func(string)) (api.Response, error) {
	if err := p.validate(); err != nil {
		return api.Response{}, err
	}

	var textBuf strings.Builder
	var toolBlocks []api.Block
	var usage api.Usage
	callSeq := 0

	for resp, err := range p.client.Models.GenerateContentStream(ctx, p.Model(), toGeminiContents(messages), p.buildParams(tools)) {
		if err != nil {
			return api.Response{}, fmt.Errorf("gemini generate content stream: %w", err)
		}
		if err := collectGeminiParts(resp, &textBuf, &toolBlocks, &callSeq, &usage, onChunk); err != nil {
			return api.Response{}, err
		}
	}

	return buildGeminiResponse(textBuf.String(), toolBlocks, usage), nil
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
				if len(b.ThoughtSignature) > 0 {
					part.ThoughtSignature = slices.Clone(b.ThoughtSignature)
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
		props := t.InputSchema
		if props == nil {
			props = map[string]any{}
		}
		schema := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(t.Required) > 0 {
			schema["required"] = t.Required
		}
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: schema,
		})
	}

	return []*genai.Tool{{FunctionDeclarations: decls}}
}

// ListModels queries Gemini's model catalog on demand, returning generative model IDs
// that support "generateContent" with the leading "models/" resource prefix removed.
func (p *GeminiProvider) ListModels(ctx context.Context) ([]string, error) {
	if p == nil {
		return nil, errors.New("gemini provider is nil")
	}
	if p.client == nil {
		return nil, errors.New("gemini client is nil")
	}

	page, err := p.client.Models.List(ctx, &genai.ListModelsConfig{})
	if err != nil {
		return nil, fmt.Errorf("gemini list models: %w", err)
	}

	var models []string
	for {
		for _, m := range page.Items {
			// Exclude embedding, TTS, or image-only entries that cannot handle chat generation.
			if m == nil || !slices.Contains(m.SupportedActions, "generateContent") {
				continue
			}
			name := strings.TrimPrefix(m.Name, "models/")
			if name != "" {
				models = append(models, name)
			}
		}
		if page.NextPageToken == "" {
			break
		}
		page, err = page.Next(ctx)
		if errors.Is(err, genai.ErrPageDone) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gemini list next models page: %w", err)
		}
	}

	return models, nil
}

// Compile-time assertions ensuring GeminiProvider satisfies Provider, ModelLister, and Streamer.
var (
	_ Provider    = (*GeminiProvider)(nil)
	_ ModelLister = (*GeminiProvider)(nil)
	_ Streamer    = (*GeminiProvider)(nil)
)
