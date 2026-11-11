package agent

import (
	"context"
	"fmt"
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

// mockEchoTool simulates a simple executable tool for unit tests.
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

// mockFailingTool simulates a tool that fails on specific arguments to exercise self-correction.
type mockFailingTool struct{}

func (mockFailingTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "file_reader",
		Description: "Reads files or returns error if missing",
	}
}

func (mockFailingTool) Execute(_ context.Context, rawInput string) (string, bool) {
	if strings.Contains(rawInput, "missing.txt") {
		return "file not found: missing.txt", true
	}
	return "contents of valid.txt", false
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

// mockFailingProvider simulates network or API provider failures.
type mockFailingProvider struct {
	err error
}

func (m mockFailingProvider) Send(_ context.Context, _ []api.Message, _ []api.ToolDef) (api.Response, error) {
	return api.Response{}, m.err
}

func (mockFailingProvider) Model() string   { return "failing-mock" }
func (mockFailingProvider) SetModel(_ string) {}

// mockTrackingCompactor verifies that Agent.Run invokes CompactionStrategy and updates history.
type mockTrackingCompactor struct {
	callCount int
}

func (m *mockTrackingCompactor) Compact(_ context.Context, messages []api.Message) ([]api.Message, error) {
	m.callCount++
	if len(messages) > 2 {
		return messages[1:], nil
	}
	return messages, nil
}

// TestAgentLoop_SimpleResponse verifies a direct text response completed in a single reasoning turn.
func TestAgentLoop_SimpleResponse(t *testing.T) {
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

// TestAgentLoop_ToolExecution verifies tool execution and synthesis across two reasoning turns.
func TestAgentLoop_ToolExecution(t *testing.T) {
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

// TestAgentLoop_SelfCorrection verifies that tool execution errors trigger an autonomous self-correction turn.
func TestAgentLoop_SelfCorrection(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			// Turn 1: Model requests read of non-existent file
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_err_1",
						ToolName:  "file_reader",
						ToolInput: `{"path":"missing.txt"}`,
					},
				},
				StopReason: "tool_use",
			},
			// Turn 2: Model observes error in tool result and self-corrects with valid path
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_fix_2",
						ToolName:  "file_reader",
						ToolInput: `{"path":"valid.txt"}`,
					},
				},
				StopReason: "tool_use",
			},
			// Turn 3: Model receives valid file content and synthesizes final answer
			{
				Content: []api.Block{
					{
						Type: api.BlockText,
						Text: "File recovered successfully: contents of valid.txt",
					},
				},
				StopReason: "end_turn",
			},
		},
	}

	registry := tool.NewToolRegistry()
	registry.Register(mockFailingTool{})

	a := New(Agent{
		Name:     "self-correct-agent",
		Provider: mock,
		Tools:    registry,
		Verbose:  false,
	})

	output, err := a.Run(context.Background(), "Read configuration file")
	if err != nil {
		t.Fatalf("unexpected error during self-correction loop: %v", err)
	}

	if output != "File recovered successfully: contents of valid.txt" {
		t.Errorf("expected synthesized recovery output, got %q", output)
	}

	messages := a.Messages()
	// Turn sequence: 1 (user) + 1 (assistant fail) + 1 (tool err) + 1 (assistant fix) + 1 (tool ok) + 1 (assistant final) = 6
	if len(messages) != 6 {
		t.Fatalf("expected 6 messages in history, got %d", len(messages))
	}

	// Verify Turn 1 error payload
	if !messages[2].Content[0].IsError {
		t.Errorf("expected turn 1 tool result IsError to be true")
	}
	if !strings.Contains(messages[2].Content[0].ToolResult, "file not found") {
		t.Errorf("expected error message in turn 1 tool result, got %q", messages[2].Content[0].ToolResult)
	}

	// Verify Turn 2 corrected payload
	if messages[4].Content[0].IsError {
		t.Errorf("expected turn 2 tool result IsError to be false")
	}
	if !strings.Contains(messages[4].Content[0].ToolResult, "contents of valid.txt") {
		t.Errorf("expected success message in turn 2 tool result, got %q", messages[4].Content[0].ToolResult)
	}
}

// TestPermissionGate_Rejection verifies graceful agent recovery when user denies tool execution permission.
func TestPermissionGate_Rejection(t *testing.T) {
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

// TestAgentLoop_CompactionTrigger verifies that context compaction triggers and trims history when thresholds are met.
func TestAgentLoop_CompactionTrigger(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			{
				Content:    []api.Block{{Type: api.BlockText, Text: "Answer 1"}},
				StopReason: "end_turn",
			},
			{
				Content:    []api.Block{{Type: api.BlockText, Text: "Answer 2"}},
				StopReason: "end_turn",
			},
		},
	}

	compactor := &mockTrackingCompactor{}

	a := New(Agent{
		Name:      "compact-agent",
		Provider:  mock,
		Compactor: compactor,
		Verbose:   false,
	})

	// Run 1: Adds User 1 + Assistant 1 (total 2 messages)
	_, err := a.Run(context.Background(), "Question 1")
	if err != nil {
		t.Fatalf("unexpected error in run 1: %v", err)
	}
	if len(a.Messages()) != 2 {
		t.Fatalf("expected 2 messages after run 1, got %d", len(a.Messages()))
	}
	if compactor.callCount != 1 {
		t.Errorf("expected compactor to be called once, got %d", compactor.callCount)
	}

	// Run 2: appends Question 2 (3 messages), compactor drops oldest message, then assistant responds
	_, err = a.Run(context.Background(), "Question 2")
	if err != nil {
		t.Fatalf("unexpected error in run 2: %v", err)
	}

	if compactor.callCount != 2 {
		t.Errorf("expected compactor to be called twice, got %d", compactor.callCount)
	}

	// Uncompacted history would be 4 messages. With compaction dropping 1 message, length is 3.
	if len(a.Messages()) != 3 {
		t.Errorf("expected messages to be compacted to 3, got %d", len(a.Messages()))
	}
}

// TestAgentMaxTurnsLimit verifies that the agent terminates safely when reaching the maximum turn guard limit.
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

// TestAgentLoop_ParallelToolExecution verifies that multiple tool calls in a single assistant turn are executed in sequence and bundled into one tool result message.
func TestAgentLoop_ParallelToolExecution(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			// Turn 1: Model requests two tool calls in parallel
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_parallel_1",
						ToolName:  "echo",
						ToolInput: "first",
					},
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_parallel_2",
						ToolName:  "echo",
						ToolInput: "second",
					},
				},
				StopReason: "tool_use",
			},
			// Turn 2: Model receives both tool results and provides final text
			{
				Content: []api.Block{
					{
						Type: api.BlockText,
						Text: "Executed both tools in parallel",
					},
				},
				StopReason: "end_turn",
			},
		},
	}

	registry := tool.NewToolRegistry()
	registry.Register(mockEchoTool{})

	a := New(Agent{
		Name:     "parallel-agent",
		Provider: mock,
		Tools:    registry,
		Verbose:  false,
	})

	output, err := a.Run(context.Background(), "Run two echoes")
	if err != nil {
		t.Fatalf("unexpected error during parallel tool execution: %v", err)
	}

	if output != "Executed both tools in parallel" {
		t.Errorf("expected synthesized output, got %q", output)
	}

	messages := a.Messages()
	// Turn sequence: 1 (user) + 1 (assistant 2 tool uses) + 1 (user 2 tool results) + 1 (assistant final) = 4 messages
	if len(messages) != 4 {
		t.Fatalf("expected 4 messages in history, got %d", len(messages))
	}

	toolResultMsg := messages[2]
	if toolResultMsg.Role != api.RoleUser {
		t.Errorf("expected tool results message role to be %s, got %s", api.RoleUser, toolResultMsg.Role)
	}
	if len(toolResultMsg.Content) != 2 {
		t.Fatalf("expected 2 tool result blocks, got %d", len(toolResultMsg.Content))
	}

	res1 := toolResultMsg.Content[0]
	if res1.ToolUseID != "call_parallel_1" || res1.ToolResult != "echoed: first" || res1.IsError {
		t.Errorf("unexpected tool result 1: %+v", res1)
	}

	res2 := toolResultMsg.Content[1]
	if res2.ToolUseID != "call_parallel_2" || res2.ToolResult != "echoed: second" || res2.IsError {
		t.Errorf("unexpected tool result 2: %+v", res2)
	}
}

// TestAgentLoop_UnknownTool verifies that requesting an unregistered tool returns an error result instead of panicking.
func TestAgentLoop_UnknownTool(t *testing.T) {
	mock := &mockProvider{
		responses: []api.Response{
			// Turn 1: Model requests unknown tool
			{
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_unknown",
						ToolName:  "non_existent_tool",
						ToolInput: "{}",
					},
				},
				StopReason: "tool_use",
			},
			// Turn 2: Model receives tool error and acknowledges
			{
				Content: []api.Block{
					{
						Type: api.BlockText,
						Text: "Tool was not found, falling back to built-in reasoning.",
					},
				},
				StopReason: "end_turn",
			},
		},
	}

	registry := tool.NewToolRegistry() // Empty registry

	a := New(Agent{
		Name:     "unknown-tool-agent",
		Provider: mock,
		Tools:    registry,
		Verbose:  false,
	})

	output, err := a.Run(context.Background(), "Run non-existent tool")
	if err != nil {
		t.Fatalf("unexpected error when handling unknown tool: %v", err)
	}

	if output != "Tool was not found, falling back to built-in reasoning." {
		t.Errorf("expected fallback response, got %q", output)
	}

	messages := a.Messages()
	if len(messages) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(messages))
	}

	resultBlock := messages[2].Content[0]
	if !resultBlock.IsError {
		t.Errorf("expected IsError to be true for unknown tool")
	}
	if !strings.Contains(resultBlock.ToolResult, "unknown tool: non_existent_tool") {
		t.Errorf("expected error message to mention unknown tool name, got %q", resultBlock.ToolResult)
	}
}

// TestAgentLoop_ProviderError verifies that underlying provider errors halt execution immediately and propagate cleanly.
func TestAgentLoop_ProviderError(t *testing.T) {
	simulatedErr := fmt.Errorf("connection refused: 503 Service Unavailable")
	a := New(Agent{
		Name:     "failing-provider-agent",
		Provider: mockFailingProvider{err: simulatedErr},
		Verbose:  false,
	})

	_, err := a.Run(context.Background(), "Trigger failure")
	if err == nil {
		t.Fatal("expected provider error to propagate, got nil")
	}

	if !strings.Contains(err.Error(), "provider error: connection refused: 503") {
		t.Errorf("expected wrapped provider error, got %q", err.Error())
	}
}
