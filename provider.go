package main

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// =============================================================================
// 1. Domain Types (Vendor-Agnostic)
// =============================================================================

// BlockType identifies the semantic purpose of a content block.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block represents a single discrete payload within a message (text, tool request, or tool output).
// Uses the tagged struct pattern to avoid vendor-specific union abstractions.
type Block struct {
	Type       BlockType
	Text       string // Populated for BlockText
	ToolUseID  string // Tool call identifier (used for BlockToolUse and BlockToolResult)
	ToolName   string // Populated for BlockToolUse
	ToolInput  string // Populated for BlockToolUse — raw JSON string arguments
	ToolResult string // Populated for BlockToolResult — tool execution output
	IsError    bool   // Populated for BlockToolResult — indicates execution failure
}

// Role identifies the sender of a conversation turn.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message models a single conversational turn between the user, assistant, or tool runner.
type Message struct {
	Role    Role
	Content []Block
}

// ToolDef provides the generic schema contract an LLM needs to invoke a tool.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	Required    []string
}

// Response normalizes the output returned from any LLM provider turn.
type Response struct {
	Content    []Block
	StopReason string // e.g. "end_turn" or "tool_use"
}

// =============================================================================
// 2. Agnostic Provider Interface
// =============================================================================

// Provider is the decoupled contract that any LLM engine must fulfill.
// High-level modules (e.g. agentLoop) depend solely on this abstraction (Dependency Inversion).
type Provider interface {
	Send(ctx context.Context, messages []Message, tools []ToolDef) (Response, error)
	Model() string
	SetModel(name string)
}

// =============================================================================
// 3. Concrete Adapter: AnthropicProvider
// =============================================================================

// AnthropicProvider implements the Provider interface by adapting our generic domain
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
func (p *AnthropicProvider) Send(ctx context.Context, messages []Message, tools []ToolDef) (Response, error) {
	resp, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: p.maxTokens,
		System:    []anthropic.TextBlockParam{{Text: p.system}},
		Messages:  toAnthropicMessages(messages),
		Tools:     toAnthropicTools(tools),
	})
	if err != nil {
		return Response{}, err
	}

	var outBlocks []Block
	for _, block := range resp.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			outBlocks = append(outBlocks, Block{
				Type: BlockText,
				Text: v.Text,
			})
		case anthropic.ToolUseBlock:
			outBlocks = append(outBlocks, Block{
				Type:      BlockToolUse,
				ToolUseID: v.ID,
				ToolName:  v.Name,
				ToolInput: v.JSON.Input.Raw(),
			})
		}
	}
	return Response{
		Content:    outBlocks,
		StopReason: string(resp.StopReason),
	}, nil
}

// =============================================================================
// 4. Translation Helpers (Wire Format Adapters)
// =============================================================================

// toAnthropicMessages translates vendor-agnostic domain Messages into Anthropic SDK MessageParams.
// Note: Anthropic's wire API only accepts "user" and "assistant" roles; tool execution results
// must be encapsulated inside a RoleUser turn via anthropic.NewToolResultBlock.
func toAnthropicMessages(messages []Message) []anthropic.MessageParam {
	var params []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion
		for _, b := range msg.Content {
			switch b.Type {
			case BlockText:
				blocks = append(blocks, anthropic.NewTextBlock(b.Text))
			case BlockToolUse:
				blocks = append(blocks, anthropic.NewToolUseBlock(b.ToolUseID, json.RawMessage(b.ToolInput), b.ToolName))
			case BlockToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(b.ToolUseID, b.ToolResult, b.IsError))
			}
		}
		if msg.Role == RoleUser {
			params = append(params, anthropic.NewUserMessage(blocks...))
		} else {
			params = append(params, anthropic.NewAssistantMessage(blocks...))
		}
	}
	return params
}

// toAnthropicTools converts domain ToolDefs into Anthropic SDK ToolUnionParam structs.
func toAnthropicTools(tools []ToolDef) []anthropic.ToolUnionParam {
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
