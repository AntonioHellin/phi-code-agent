package provider

import (
	"testing"

	"phi-agent-code/internal/api"
)

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

