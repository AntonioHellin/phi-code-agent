package compact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
)

func TestSafeSplitPoint(t *testing.T) {
	// Sample message builders for readability
	userMsg := func(text string) api.Message {
		return api.Message{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: text}},
		}
	}

	assistantToolUseMsg := func(id, name string) api.Message {
		return api.Message{
			Role:    api.RoleAssistant,
			Content: []api.Block{{Type: api.BlockToolUse, ToolUseID: id, ToolName: name}},
		}
	}

	toolResultMsg := func(id, result string) api.Message {
		return api.Message{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockToolResult, ToolUseID: id, ToolResult: result}},
		}
	}

	assistantTextMsg := func(text string) api.Message {
		return api.Message{
			Role:    api.RoleAssistant,
			Content: []api.Block{{Type: api.BlockText, Text: text}},
		}
	}

	// Conversation scenario:
	// 0: User: "Hello"
	// 1: Assistant: "Hi"
	// 2: User: "Read a file"
	// 3: Assistant: tool_use (read_file)
	// 4: User: tool_result (file contents)
	// 5: Assistant: "Here is the content"
	// 6: User: "Run tests"
	// 7: Assistant: "Running tests..."
	msgs := []api.Message{
		userMsg("Hello"),                           // 0
		assistantTextMsg("Hi"),                     // 1
		userMsg("Read a file"),                     // 2
		assistantToolUseMsg("call_1", "read_file"), // 3
		toolResultMsg("call_1", "package main"),    // 4 (user role, but tool result!)
		assistantTextMsg("Here is the content"),    // 5
		userMsg("Run tests"),                       // 6
		assistantTextMsg("Running tests..."),       // 7
	}

	tests := []struct {
		name     string
		desired  int
		expected int
	}{
		{
			name:     "desired zero or negative",
			desired:  0,
			expected: 0,
		},
		{
			name:     "desired beyond length",
			desired:  10,
			expected: len(msgs),
		},
		{
			name:     "desired is exact clean user message (index 2)",
			desired:  2,
			expected: 2,
		},
		{
			name:     "desired falls on tool_use (index 3) - walks back to clean user prompt (index 2)",
			desired:  3,
			expected: 2,
		},
		{
			name:     "desired falls on tool_result (index 4) - MUST NOT split here, walks back to index 2",
			desired:  4,
			expected: 2,
		},
		{
			name:     "desired falls on assistant text after tool (index 5) - walks back to index 2",
			desired:  5,
			expected: 2,
		},
		{
			name:     "desired falls on clean user message (index 6)",
			desired:  6,
			expected: 6,
		},
		{
			name:     "desired falls on final assistant text (index 7) - walks back to index 6",
			desired:  7,
			expected: 6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeSplitPoint(msgs, tc.desired)
			if got != tc.expected {
				t.Fatalf("SafeSplitPoint(msgs, %d) = %d, expected %d", tc.desired, got, tc.expected)
			}
		})
	}
}

func TestNoCompaction(t *testing.T) {
	strategy := NoCompaction{}
	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "one"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "two"}}},
	}

	res, err := strategy.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(res))
	}
}

func TestSlidingWindow(t *testing.T) {
	strategy := &SlidingWindow{KeepLast: 4}

	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 1"}}},           // 0
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 1"}}},     // 1
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 2: run tool"}}}, // 2
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockToolUse, ToolUseID: "t1"}}},  // 3
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockToolResult, ToolUseID: "t1"}}},   // 4
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Tool finished"}}}, // 5
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 3"}}},           // 6
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 3"}}},     // 7
	}

	// len(msgs) = 8, KeepLast = 4 -> desired cut = 8 - 4 = 4.
	// Index 4 is a tool_result! Naive cut would leave index 4 orphaned without index 3.
	// SafeSplitPoint must step back to index 2!
	compacted, err := strategy.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// If it stepped back to index 2, compacted starts at index 2, so len is 8 - 2 = 6
	if len(compacted) != 6 {
		t.Fatalf("expected 6 messages to preserve tool call atomicity, got %d", len(compacted))
	}

	if compacted[0].Content[0].Text != "Turn 2: run tool" {
		t.Fatalf("expected first message to be 'Turn 2: run tool', got %v", compacted[0].Content[0].Text)
	}

	// Ensure the first message is NOT a tool result
	if compacted[0].HasToolResult() {
		t.Fatalf("compacted conversation starts with a tool result, which is invalid")
	}
}

func TestWithLogging(t *testing.T) {
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "compactions.log")

	inner := &SlidingWindow{KeepLast: 2}
	decorator := &WithLogging{
		Inner:   inner,
		LogPath: logPath,
	}

	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "User 1"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Assistant 1"}}},
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "User 2"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Assistant 2"}}},
	}

	res, err := decorator.Compact(context.Background(), msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(res))
	}

	// Verify log file was created and written
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read compaction log file: %v", err)
	}
	logContent := string(data)

	if !strings.Contains(logContent, "Compacted: 4 -> 2 messages") {
		t.Fatalf("expected log to contain summary line, got: %s", logContent)
	}
	if !strings.Contains(logContent, "User 1") {
		t.Fatalf("expected log to mention pruned message 'User 1'")
	}
	if !strings.Contains(logContent, "User 2") {
		t.Fatalf("expected log to mention kept message 'User 2'")
	}
}

// mockSummaryProvider implements provider.Provider for unit testing Summarize without real API calls.
type mockSummaryProvider struct {
	summaryText string
	called      bool
}

func (m *mockSummaryProvider) Send(_ context.Context, _ []api.Message, _ []api.ToolDef) (api.Response, error) {
	m.called = true
	return api.Response{
		Content:    []api.Block{{Type: api.BlockText, Text: m.summaryText}},
		StopReason: "end_turn",
	}, nil
}

func (m *mockSummaryProvider) Model() string   { return "mock-model" }
func (m *mockSummaryProvider) SetModel(_ string) {}

func TestSummarize(t *testing.T) {
	mock := &mockSummaryProvider{summaryText: "User asked for file operations and tests were executed."}
	strategy := &Summarize{
		Provider:   mock,
		Threshold:  6,
		KeepRecent: 2,
	}

	msgs := []api.Message{
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 1"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 1"}}},
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 2"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 2"}}},
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 3"}}},
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 3"}}},
	}

	t.Run("below threshold does not compact", func(t *testing.T) {
		mock.called = false
		shortMsgs := msgs[:4]
		res, err := strategy.Compact(context.Background(), shortMsgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != len(shortMsgs) {
			t.Fatalf("expected %d messages, got %d", len(shortMsgs), len(res))
		}
		if mock.called {
			t.Fatalf("provider should not have been called when below threshold")
		}
	})

	t.Run("at or above threshold compacts with summary", func(t *testing.T) {
		mock.called = false
		res, err := strategy.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !mock.called {
			t.Fatalf("expected provider to be called for summarization")
		}

		// Desired cut = 6 - 2 = 4 (index 4 is Turn 3, clean user message)
		// It keeps index 4 and 5 (len = 2)
		if len(res) != 2 {
			t.Fatalf("expected 2 kept messages, got %d", len(res))
		}

		// First message must have summary block prepended to its content
		if res[0].Role != api.RoleUser {
			t.Fatalf("expected first message role to be user, got %s", res[0].Role)
		}
		if len(res[0].Content) < 2 {
			t.Fatalf("expected at least 2 content blocks (summary + original text), got %d", len(res[0].Content))
		}

		firstBlockText := res[0].Content[0].Text
		if !strings.Contains(firstBlockText, "[Summary of previous conversation]") {
			t.Fatalf("expected summary header in block, got: %s", firstBlockText)
		}
		if !strings.Contains(firstBlockText, mock.summaryText) {
			t.Fatalf("expected summary text in block, got: %s", firstBlockText)
		}

		originalBlockText := res[0].Content[1].Text
		if originalBlockText != "Turn 3" {
			t.Fatalf("expected original prompt 'Turn 3' preserved, got: %s", originalBlockText)
		}
	})
}

