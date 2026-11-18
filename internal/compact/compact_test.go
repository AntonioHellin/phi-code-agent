package compact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
	"phi-agent-code/internal/provider"
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
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 1"}}},             // 0
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 1"}}},       // 1
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 2: run tool"}}},   // 2
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockToolUse, ToolUseID: "t1"}}},    // 3
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockToolResult, ToolUseID: "t1"}}},      // 4
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Tool finished"}}}, // 5
		{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockText, Text: "Turn 3"}}},             // 6
		{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockText, Text: "Reply 3"}}},       // 7
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

func TestSummarize(t *testing.T) {
	const summaryText = "User asked for file operations and tests were executed."
	mock := &provider.MockProvider{
		Responses: []api.Response{
			{
				Content:    []api.Block{{Type: api.BlockText, Text: summaryText}},
				StopReason: "end_turn",
			},
		},
	}
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
		shortMsgs := msgs[:4]
		res, err := strategy.Compact(context.Background(), shortMsgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != len(shortMsgs) {
			t.Fatalf("expected %d messages, got %d", len(shortMsgs), len(res))
		}
		if len(mock.History) != 0 {
			t.Fatalf("provider should not have been called when below threshold")
		}
	})

	t.Run("at or above threshold compacts with summary", func(t *testing.T) {
		res, err := strategy.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(mock.History) != 1 {
			t.Fatalf("expected provider to be called once for summarization, got %d calls", len(mock.History))
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
		if !strings.Contains(firstBlockText, summaryText) {
			t.Fatalf("expected summary text in block, got: %s", firstBlockText)
		}

		originalBlockText := res[0].Content[1].Text
		if originalBlockText != "Turn 3" {
			t.Fatalf("expected original prompt 'Turn 3' preserved, got: %s", originalBlockText)
		}
	})
}

func TestEstimateTokens(t *testing.T) {
	emptyMsg := api.Message{Role: api.RoleUser}
	if got := EstimateMessageTokens(emptyMsg); got != 4 {
		t.Fatalf("expected 4 framing tokens for empty message, got %d", got)
	}

	toolMsg := api.Message{
		Role: api.RoleAssistant,
		Content: []api.Block{
			{Type: api.BlockText, Text: strings.Repeat("a", 40)},                                // 40 chars
			{Type: api.BlockToolUse, ToolUseID: "id12", ToolName: "bash", ToolInput: `{"a":1}`}, // 4+4+7 = 15 chars
		},
	}
	// Total 55 chars -> 4 + (55+3)/4 = 4 + 14 = 18 tokens
	if got := EstimateMessageTokens(toolMsg); got != 18 {
		t.Fatalf("expected 18 estimated tokens, got %d", got)
	}

	if total := EstimateMessagesTokens([]api.Message{emptyMsg, toolMsg}); total != 22 {
		t.Fatalf("expected sum 22 estimated tokens, got %d", total)
	}
}

func TestTokenBudget(t *testing.T) {
	msg := func(role api.Role, text string) api.Message {
		return api.Message{Role: role, Content: []api.Block{{Type: api.BlockText, Text: text}}}
	}

	t.Run("disabled or under budget keeps all messages", func(t *testing.T) {
		msgs := []api.Message{msg(api.RoleUser, "hi"), msg(api.RoleAssistant, "hello")}
		if res, err := (&TokenBudget{MaxTokens: 0}).Compact(context.Background(), msgs); err != nil || len(res) != 2 {
			t.Fatalf("expected MaxTokens<=0 no-op, got len=%d err=%v", len(res), err)
		}
		var nilBudget *TokenBudget
		if res, err := nilBudget.Compact(context.Background(), msgs); err != nil || len(res) != 2 {
			t.Fatalf("expected nil receiver no-op, got len=%d err=%v", len(res), err)
		}
		if res, err := (&TokenBudget{MaxTokens: 100}).Compact(context.Background(), msgs); err != nil || len(res) != 2 {
			t.Fatalf("expected under-budget no-op, got len=%d err=%v", len(res), err)
		}
	})

	t.Run("prunes oldest clean turns to fit within MaxTokens", func(t *testing.T) {
		// Each 40-char message estimates to 4 + 40/4 = 14 tokens. Total = 6 * 14 = 84 tokens.
		payload := strings.Repeat("x", 40)
		msgs := []api.Message{
			msg(api.RoleUser, payload),      // suffix = 84
			msg(api.RoleAssistant, payload), // suffix = 70 (not clean)
			msg(api.RoleUser, payload),      // suffix = 56
			msg(api.RoleAssistant, payload), // suffix = 42 (not clean)
			msg(api.RoleUser, payload),      // suffix = 28
			msg(api.RoleAssistant, payload), // suffix = 14 (not clean)
		}

		res, err := (&TokenBudget{MaxTokens: 40}).Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("expected 2 kept messages (28 <= 40), got %d", len(res))
		}
		if EstimateMessagesTokens(res) > 40 {
			t.Errorf("expected kept messages <= 40 tokens, got %d", EstimateMessagesTokens(res))
		}
	})

	t.Run("preserves tool_use/tool_result atomicity and drops large finished tool turn", func(t *testing.T) {
		hugeOutput := strings.Repeat("log line\n", 80) // ~720 chars (~184 tokens)
		msgs := []api.Message{
			msg(api.RoleUser, "old turn"),
			msg(api.RoleAssistant, "old reply"),
			msg(api.RoleUser, "run huge bash"),
			{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockToolUse, ToolUseID: "c1", ToolName: "bash", ToolInput: "{}"}}},
			{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockToolResult, ToolUseID: "c1", ToolResult: hugeOutput}}},
			msg(api.RoleAssistant, "finished"),
			msg(api.RoleUser, "latest clean turn"),
		}

		// Budget allows only the newest clean turn ("latest clean turn" ~ 9 tokens), pruning finished tool chain cleanly.
		res, err := (&TokenBudget{MaxTokens: 30}).Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 1 || res[0].Content[0].Text != "latest clean turn" || res[0].HasToolResult() {
			t.Fatalf("expected clean latest turn only without orphaned tool_result, got %+v", res)
		}
	})

	t.Run("retains active oversized tool turn rather than wiping history", func(t *testing.T) {
		hugeOutput := strings.Repeat("z", 400) // ~104 tokens
		msgs := []api.Message{
			msg(api.RoleUser, "turn 1"),
			msg(api.RoleAssistant, "reply 1"),
			msg(api.RoleUser, "turn 2 tool"),
			{Role: api.RoleAssistant, Content: []api.Block{{Type: api.BlockToolUse, ToolUseID: "c2", ToolName: "read_file", ToolInput: "{}"}}},
			{Role: api.RoleUser, Content: []api.Block{{Type: api.BlockToolResult, ToolUseID: "c2", ToolResult: hugeOutput}}},
		}

		// Budget (20) is smaller than active turn 2 (~130), so turn 1 is pruned and active turn 2 stays atomic.
		res, err := (&TokenBudget{MaxTokens: 20}).Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 3 || res[0].Content[0].Text != "turn 2 tool" || res[0].HasToolResult() {
			t.Fatalf("expected active turn 2 retained intact from 'turn 2 tool', got %+v", res)
		}
	})

	t.Run("works with WithLogging decorator", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "budget.log")
		decorator := &WithLogging{
			Inner:   &TokenBudget{MaxTokens: 20},
			LogPath: logPath,
		}
		msgs := []api.Message{
			msg(api.RoleUser, strings.Repeat("a", 40)),
			msg(api.RoleAssistant, strings.Repeat("b", 40)),
			msg(api.RoleUser, "keep"),
		}
		res, err := decorator.Compact(context.Background(), msgs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res) != 1 {
			t.Fatalf("expected 1 kept message, got %d", len(res))
		}
		data, err := os.ReadFile(logPath)
		if err != nil || !strings.Contains(string(data), "Compacted: 3 -> 1 messages") {
			t.Fatalf("expected compaction log entry, got err=%v content=%q", err, string(data))
		}
	})
}
