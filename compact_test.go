package main

import (
	"context"
	"testing"
)

func TestSafeSplitPoint(t *testing.T) {
	// Sample message builders for readability
	userMsg := func(text string) Message {
		return Message{
			Role:    RoleUser,
			Content: []Block{{Type: BlockText, Text: text}},
		}
	}

	assistantToolUseMsg := func(id, name string) Message {
		return Message{
			Role:    RoleAssistant,
			Content: []Block{{Type: BlockToolUse, ToolUseID: id, ToolName: name}},
		}
	}

	toolResultMsg := func(id, result string) Message {
		return Message{
			Role:    RoleUser,
			Content: []Block{{Type: BlockToolResult, ToolUseID: id, ToolResult: result}},
		}
	}

	assistantTextMsg := func(text string) Message {
		return Message{
			Role:    RoleAssistant,
			Content: []Block{{Type: BlockText, Text: text}},
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
	msgs := []Message{
		userMsg("Hello"),                        // 0
		assistantTextMsg("Hi"),                  // 1
		userMsg("Read a file"),                  // 2
		assistantToolUseMsg("call_1", "read_file"), // 3
		toolResultMsg("call_1", "package main"), // 4 (user role, but tool result!)
		assistantTextMsg("Here is the content"), // 5
		userMsg("Run tests"),                    // 6
		assistantTextMsg("Running tests..."),    // 7
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
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockText, Text: "one"}}},
		{Role: RoleAssistant, Content: []Block{{Type: BlockText, Text: "two"}}},
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

	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockText, Text: "Turn 1"}}},                     // 0
		{Role: RoleAssistant, Content: []Block{{Type: BlockText, Text: "Reply 1"}}},               // 1
		{Role: RoleUser, Content: []Block{{Type: BlockText, Text: "Turn 2: run tool"}}},           // 2
		{Role: RoleAssistant, Content: []Block{{Type: BlockToolUse, ToolUseID: "t1"}}},            // 3
		{Role: RoleUser, Content: []Block{{Type: BlockToolResult, ToolUseID: "t1"}}},             // 4
		{Role: RoleAssistant, Content: []Block{{Type: BlockText, Text: "Tool finished"}}},        // 5
		{Role: RoleUser, Content: []Block{{Type: BlockText, Text: "Turn 3"}}},                     // 6
		{Role: RoleAssistant, Content: []Block{{Type: BlockText, Text: "Reply 3"}}},               // 7
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

