package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"

	"phi-agent-code/internal/api"
)

// AnthropicProvider implements the Provider interface by adapting domain
// types into the concrete wire format required by the Anthropic SDK.
type AnthropicProvider struct {
	client    *anthropic.Client // Official Anthropic API client configured with auth/transport
	model     string            // Active Claude model ID sent in Messages requests
	maxTokens int64             // Maximum completion tokens permitted per response
	system    string            // System prompt prepended to every conversation turn
}

// NewAnthropicProvider initializes the Anthropic adapter with required client and defaults.
func NewAnthropicProvider(client *anthropic.Client, model string, maxTokens int64, system string) *AnthropicProvider {
	return &AnthropicProvider{
		client:    client,
		model:     model,
		maxTokens: maxTokens,
		system:    system,
	}
}

// Model returns the active model identifier.
func (p *AnthropicProvider) Model() string {
	return p.model
}

// SetModel dynamically updates the active model identifier.
func (p *AnthropicProvider) SetModel(name string) {
	p.model = name
}

// Send fulfills the Provider interface: it maps agnostic domain messages and tool schemas
// into the Anthropic wire format, executes the API call, and normalizes the response.
func (p *AnthropicProvider) Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		System:    []anthropic.TextBlockParam{{Text: p.system}},
		Messages:  toAnthropicMessages(messages),
		Tools:     toAnthropicTools(tools),
	})
	if err != nil {
		return api.Response{}, err
	}

	var outBlocks []api.Block
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			outBlocks = append(outBlocks, api.Block{
				Type: api.BlockText,
				Text: v.Text,
			})
		case anthropic.ToolUseBlock:
			outBlocks = append(outBlocks, api.Block{
				Type:      api.BlockToolUse,
				ToolUseID: v.ID,
				ToolName:  v.Name,
				ToolInput: v.JSON.Input.Raw(),
			})
		}
	}
	return api.Response{
		Content:    outBlocks,
		StopReason: string(resp.StopReason),
	}, nil
}

// toAnthropicMessages translates vendor-agnostic domain Messages into Anthropic SDK MessageParams.
// Anthropic wire API requires tool execution results to be wrapped inside a RoleUser turn.
func toAnthropicMessages(messages []api.Message) []anthropic.MessageParam {
	var params []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion
		for _, b := range msg.Content {
			switch b.Type {
			case api.BlockText:
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case api.BlockToolUse:
				blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolUseID, json.RawMessage(b.ToolInput), b.ToolName))
			case api.BlockToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolUseID, b.ToolResult, b.IsError))
			}
		}
		if msg.Role == api.RoleUser {
			params = append(params, anthropic.NewUserMessage(blocks...))
		} else {
			params = append(params, anthropic.NewAssistantMessage(blocks...))
		}
	}
	return params
}

// toAnthropicTools converts domain ToolDefs into Anthropic SDK ToolUnionParam structs.
func toAnthropicTools(tools []api.ToolDef) []anthropic.ToolUnionParam {
	var params []anthropic.ToolUnionParam
	for _, t := range tools {
		params = append(params, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: t.InputSchema,
					Required:   t.Required,
				},
			},
		})
	}
	return params
}

// ListModels queries Anthropic's Models API on demand, returning non-retired model IDs.
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]string, error) {
	if p == nil {
		return nil, errors.New("anthropic provider is nil")
	}
	if p.client == nil {
		return nil, errors.New("anthropic client is nil")
	}

	page, err := p.client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		return nil, fmt.Errorf("anthropic list models: %w", err)
	}

	var models []string
	for page != nil {
		for _, m := range page.Data {
			// Skip retired models whose inference endpoints reject requests.
			if m.ID == "" || m.Lifecycle == anthropic.ModelInfoLifecycleRetired {
				continue
			}
			models = append(models, m.ID)
		}
		if !page.HasMore {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, fmt.Errorf("anthropic list next models page: %w", err)
		}
	}

	return models, nil
}

// Compile-time assertions ensuring AnthropicProvider satisfies Provider and ModelLister.
var (
	_ Provider    = (*AnthropicProvider)(nil)
	_ ModelLister = (*AnthropicProvider)(nil)
)

