package agent

import (
	"context"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
	"phi-agent-code/internal/tool"
)

// mockProvider simulates LLM responses for deterministic, isolated unit testing.
type mockProvider struct {
	responses []api.Response
	callIndex int
}

func (m *mockProvider) Send(_ context.Context, _ []api.Message, _ []api.ToolDef) (api.Response, error) {
	if m.callIndex >= len(m.responses) {
		return api.Response{
			Content:    []api.Block{{Type: api.BlockText, Text: "default response"}},
			StopReason: "end_turn",
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockProvider) Model() string   { return "mock" }
func (m *mockProvider) SetModel(_ string) {}

// mockEchoTool simulates a simple executable tool.
type mockEchoTool struct{}

func (mockEchoTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "echo",
		Description: "Echoes input back",
	}
}

func (mockEchoTool) Execute(_ context.Context, rawInput string) (string, bool) {
	return "echoed: " + rawInput, false
}

func TestAgentRunSingleTurn(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			{
				Content:    []api.Block{{Type: api.BlockText, Text: "Hello from agent"}},
				StopReason: "end_turn",
			},
		},
	}

	a := New(Agent{
		Name:     "test-agent",
		Provider: mock,
		Verbose:  false,
	})

	output, err := a.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output != "Hello from agent" {
		t.Errorf("expected 'Hello from agent', got '%s'", output)
	}

	if len(a.Messages()) != 2 {
		t.Fatalf("expected 2 messages (user + assistant), got %d", len(a.Messages()))
	}
}

func TestAgentRunToolExecution(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			// Turn 1: Model requests tool execution
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_1",
						ToolName:  "echo",
						ToolInput: "ping",
					},
				},
				StopReason: "tool_use",
			},
			// Turn 2: Model receives tool result and provides final text
			{
				Content: []api.Block{
					{
						Type: api.BlockText,
						Text: "Tool finished successfully",
					},
				},
				StopReason: "end_turn",
			},
		},
	}

	registry := tool.NewToolRegistry()
	registry.Register(mockEchoTool{})

	a := New(Agent{
		Name:     "test-agent",
		Provider: mock,
		Tools:    registry,
		Verbose:  false,
	})

	output, err := a.Run(context.Background(), "Run echo tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output != "Tool finished successfully" {
		t.Errorf("expected 'Tool finished successfully', got '%s'", output)
	}

	// 1 (user) + 1 (assistant tool use) + 1 (user tool result) + 1 (assistant final) = 4
	if len(a.Messages()) != 4 {
		t.Fatalf("expected 4 messages in conversation history, got %d", len(a.Messages()))
	}
}

// mockPerpetualToolProvider perpetually returns tool use requests to test runaway loop guards.
type mockPerpetualToolProvider struct{}

func (mockPerpetualToolProvider) Send(_ context.Context, _ []api.Message, _ []api.ToolDef) (api.Response, error) {
	return api.Response{
		Content: []api.Block{
			{
				Type:      api.BlockToolUse,
				ToolUseID: "call_perpetual",
				ToolName:  "echo",
				ToolInput: "looping",
			},
		},
		StopReason: "tool_use",
	}, nil
}

func (mockPerpetualToolProvider) Model() string   { return "mock" }
func (mockPerpetualToolProvider) SetModel(_ string) {}

func TestAgentMaxTurnsLimit(t *testing.T) {
	registry := tool.NewToolRegistry()
	registry.Register(mockEchoTool{})

	a := New(Agent{
		Name:     "test-agent",
		Provider: mockPerpetualToolProvider{},
		Tools:    registry,
		MaxTurns: 3,
		Verbose:  false,
	})

	_, err := a.Run(context.Background(), "Loop forever")
	if err == nil {
		t.Fatalf("expected error when exceeding MaxTurns, got nil")
	}

	if !strings.Contains(err.Error(), "reached maximum turn limit") {
		t.Errorf("expected error to mention maximum turn limit, got '%v'", err)
	}
}

func TestAgentPermissionDenied(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_denied",
						ToolName:  "echo",
						ToolInput: "secret",
					},
				},
				StopReason: "tool_use",
			},
			{
				Content: []api.Block{
					{
						Type: api.BlockText,
						Text: "Understood, permission was denied.",
					},
				},
				StopReason: "end_turn",
			},
		},
	}

	registry := tool.NewToolRegistry()
	registry.Register(mockEchoTool{})

	a := New(Agent{
		Name:     "test-agent",
		Provider: mock,
		Tools:    registry,
		Verbose:  false,
		Confirm: func(_ string) bool {
			return false // Deny permission
		},
	})

	output, err := a.Run(context.Background(), "Run forbidden tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if output != "Understood, permission was denied." {
		t.Errorf("expected denial acknowledgement, got '%s'", output)
	}

	// Verify that the tool result recorded error and denial message
	messages := a.Messages()
	if len(messages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(messages))
	}

	toolResultBlock := messages[2].Content[0]
	if !toolResultBlock.IsError {
		t.Errorf("expected tool result IsError to be true")
	}
	if toolResultBlock.ToolResult != "Permission denied by user" {
		t.Errorf("expected 'Permission denied by user', got '%s'", toolResultBlock.ToolResult)
	}
}
