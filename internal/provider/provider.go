// Package provider abstracts LLM engines and implements wire-format adapters.
// Higher-level orchestration modules interact solely through the Provider interface.
package provider

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"

	"phi-agent-code/internal/api"
)

// Provider is the decoupled contract that any LLM engine must fulfill.
// High-level modules (e.g. agentLoop) depend solely on this abstraction (Dependency Inversion).
type Provider interface {
	Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error)
	Model() string
	SetModel(name string)
}

// AnthropicProvider implements the Provider interface by adapting domain
// types into the concrete wire format required by the Anthropic SDK.
type AnthropicProvider struct {
	client    *anthropic.Client
	model     string
	maxTokens int64
	system    string
}

// NewAnthropicProvider initializes the Anthropic adapter with default execution parameters.
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

