package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// CompactionStrategy abstracts how conversation history is trimmed or summarized.
type CompactionStrategy interface {
	Compact(ctx context.Context, messages []Message) ([]Message, error)
}

// SafeSplitPoint finds the nearest valid split index at or before desiredCutIndex.
// It ensures cuts happen on an authentic user turn and never between a
// tool_use and its corresponding tool_result.
func SafeSplitPoint(messages []Message, desiredCutIndex int) int {
	if desiredCutIndex <= 0 {
		return 0
	}
	if desiredCutIndex >= len(messages) {
		return len(messages)
	}

	// Walk backward to locate a genuine user turn that is NOT a tool result
	for index := desiredCutIndex; index > 0; index-- {
		targetMessage := messages[index]
		if targetMessage.Role == RoleUser && !targetMessage.HasToolResult() {
			return index
		}
	}
	return 0
}

// NoCompaction is the default — never modifies messages.
type NoCompaction struct{}

func (NoCompaction) Compact(_ context.Context, messages []Message) ([]Message, error) {
	return messages, nil
}

// SlidingWindow keeps the last KeepLast messages and drops everything older.
// It snaps to a safe boundary so it never separates a tool_use from its tool_result.
type SlidingWindow struct {
	KeepLast int
}

func (window *SlidingWindow) Compact(_ context.Context, messages []Message) ([]Message, error) {
	if len(messages) <= window.KeepLast {
		return messages, nil
	}

	desiredCutIndex := len(messages) - window.KeepLast
	safeIndex := SafeSplitPoint(messages, desiredCutIndex)
	return messages[safeIndex:], nil
}

// Summarize uses the LLM provider to summarize old conversation turns
// once history length reaches Threshold, while retaining KeepRecent turns.
type Summarize struct {
	Provider   Provider
	Threshold  int
	KeepRecent int
}

func (strategy *Summarize) Compact(ctx context.Context, messages []Message) ([]Message, error) {
	if strategy.Threshold <= 0 || len(messages) < strategy.Threshold {
		return messages, nil
	}

	keepRecentCount := strategy.KeepRecent
	if keepRecentCount <= 0 {
		keepRecentCount = 4
	}

	desiredCutIndex := len(messages) - keepRecentCount
	safeSplitIndex := SafeSplitPoint(messages, desiredCutIndex)
	if safeSplitIndex <= 0 {
		return messages, nil
	}

	messagesToSummarize := messages[:safeSplitIndex]
	messagesToKeep := messages[safeSplitIndex:]

	summaryPrompt := []Message{
		{
			Role: RoleUser,
			Content: []Block{
				{
					Type: BlockText,
					Text: "Provide a concise summary of the following conversation history, preserving key decisions, user goals, and file paths:\n\n" + renderTranscriptText(messagesToSummarize),
				},
			},
		},
	}

	summaryResponse, err := strategy.Provider.Send(ctx, summaryPrompt, nil)
	if err != nil {
		return messages, fmt.Errorf("summarize failed: %w", err)
	}

	var summaryText string
	for _, block := range summaryResponse.Content {
		if block.Type == BlockText {
			summaryText += block.Text
		}
	}

	// Prepend summary into the first retained user message (safe split guarantees it is RoleUser).
	// This preserves strict alternating user/assistant turns without creating duplicate user messages in a row.
	summaryBlock := Block{
		Type: BlockText,
		Text: "[Summary of previous conversation]:\n" + strings.TrimSpace(summaryText) + "\n\n",
	}

	// Make a shallow copy of messagesToKeep slice to avoid mutating input backing array directly
	compactedMessages := make([]Message, len(messagesToKeep))
	copy(compactedMessages, messagesToKeep)
	compactedMessages[0] = Message{
		Role:    compactedMessages[0].Role,
		Content: append([]Block{summaryBlock}, compactedMessages[0].Content...),
	}

	return compactedMessages, nil
}

// WithLogging wraps a CompactionStrategy and records every compaction event to a log file.
// It implements the Decorator pattern.
type WithLogging struct {
	Inner   CompactionStrategy
	LogPath string
}

func (decorator *WithLogging) Compact(ctx context.Context, messages []Message) ([]Message, error) {
	compactedMessages, err := decorator.Inner.Compact(ctx, messages)
	if err != nil {
		return compactedMessages, err
	}
	if len(compactedMessages) < len(messages) {
		decorator.writeLog(messages, compactedMessages)
	}
	return compactedMessages, nil
}

func (decorator *WithLogging) writeLog(originalMessages, compactedMessages []Message) {
	if decorator.LogPath == "" {
		return
	}

	logFile, err := os.OpenFile(decorator.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer logFile.Close()

	prunedCount := len(originalMessages) - len(compactedMessages)
	fmt.Fprintf(logFile, "[%s] Compacted: %d -> %d messages (%d pruned)\n",
		time.Now().Format("2006-01-02 15:04:05"), len(originalMessages), len(compactedMessages), prunedCount)

	fmt.Fprintf(logFile, "--- Pruned (%d turns) ---\n", prunedCount)
	for index := 0; index < prunedCount && index < len(originalMessages); index++ {
		fmt.Fprintf(logFile, "  [%d] %-9s %s\n", index, originalMessages[index].Role, summarizeMessage(originalMessages[index]))
	}

	fmt.Fprintf(logFile, "--- Kept (%d turns) ---\n", len(compactedMessages))
	for index, message := range compactedMessages {
		fmt.Fprintf(logFile, "  [%d] %-9s %s\n", index, message.Role, summarizeMessage(message))
	}
	fmt.Fprintln(logFile, strings.Repeat("-", 50))
}

func summarizeMessage(message Message) string {
	if len(message.Content) == 0 {
		return "(empty)"
	}
	firstBlock := message.Content[0]
	switch firstBlock.Type {
	case BlockText:
		text := strings.TrimSpace(firstBlock.Text)
		if len(text) > 60 {
			return text[:57] + "..."
		}
		return text
	case BlockToolUse:
		return fmt.Sprintf("tool_use: %s", firstBlock.ToolName)
	case BlockToolResult:
		return fmt.Sprintf("tool_result: %s", firstBlock.ToolUseID)
	default:
		return string(firstBlock.Type)
	}
}

func renderTranscriptText(messages []Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(string(message.Role))
		builder.WriteString(":\n")
		for _, block := range message.Content {
			switch block.Type {
			case BlockText:
				builder.WriteString(block.Text)
				builder.WriteString("\n")
			case BlockToolUse:
				builder.WriteString(fmt.Sprintf("[tool_use: %s]\n", block.ToolName))
			case BlockToolResult:
				builder.WriteString(fmt.Sprintf("[tool_result: %s]\n", block.ToolResult))
			}
		}
		builder.WriteString("\n")
	}
	return builder.String()
}
