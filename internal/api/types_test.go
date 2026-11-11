package api

import (
	"testing"
)

func TestMessageHasToolResult(t *testing.T) {
	tests := []struct {
		name     string
		message  Message
		expected bool
	}{
		{
			name: "empty message content returns false",
			message: Message{
				Role:    RoleUser,
				Content: []Block{},
			},
			expected: false,
		},
		{
			name: "text only blocks return false",
			message: Message{
				Role: RoleAssistant,
				Content: []Block{
					{Type: BlockText, Text: "Here is your answer"},
				},
			},
			expected: false,
		},
		{
			name: "tool use only blocks return false",
			message: Message{
				Role: RoleAssistant,
				Content: []Block{
					{Type: BlockToolUse, ToolName: "read_file", ToolInput: `{"path":"main.go"}`},
				},
			},
			expected: false,
		},
		{
			name: "message with tool result returns true",
			message: Message{
				Role: RoleUser,
				Content: []Block{
					{Type: BlockToolResult, ToolResult: "file content", IsError: false},
				},
			},
			expected: true,
		},
		{
			name: "message with mixed text and tool result returns true",
			message: Message{
				Role: RoleUser,
				Content: []Block{
					{Type: BlockText, Text: "tool execution output:"},
					{Type: BlockToolResult, ToolResult: "exit code 0", IsError: false},
				},
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.message.HasToolResult()
			if actual != tc.expected {
				t.Errorf("expected HasToolResult() = %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestConstantsIntegrity(t *testing.T) {
	if BlockText != "text" {
		t.Errorf("expected BlockText to be 'text', got '%s'", BlockText)
	}
	if BlockToolUse != "tool_use" {
		t.Errorf("expected BlockToolUse to be 'tool_use', got '%s'", BlockToolUse)
	}
	if BlockToolResult != "tool_result" {
		t.Errorf("expected BlockToolResult to be 'tool_result', got '%s'", BlockToolResult)
	}

	if RoleUser != "user" {
		t.Errorf("expected RoleUser to be 'user', got '%s'", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Errorf("expected RoleAssistant to be 'assistant', got '%s'", RoleAssistant)
	}
	if RoleTool != "tool" {
		t.Errorf("expected RoleTool to be 'tool', got '%s'", RoleTool)
	}
}

