package main

import (
	"context"
)

// Strategy
type CompactionStrategy interface {
	Compact(ctx context.Context, messages []Message) ([]Message, error)
}

func SafeSplitPoint(messages []Message, desired int) int {
	if desired <= 0 {
		return 0
	}
	if desired >= len(messages) {
		return len(messages)
	}
	for i := desired; i > 0; i-- {
		if messages[i].Role == RoleUser && !messages[i].HasToolResult() {
			return i
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
// It snaps to a safe boundary so it never separates a tool_use from its
// tool_result.
type SlidingWindow struct {
	KeepLast int
}

func (s *SlidingWindow) Compact(_ context.Context, messages []Message) ([]Message, error) {
	if len(messages) <= s.KeepLast {
		return messages, nil
	}
	split := SafeSplitPoint(messages, len(messages)-s.KeepLast)
	return messages[split:], nil
}
