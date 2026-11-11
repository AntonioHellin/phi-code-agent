package permission

import (
	"testing"
)

// TestDecisionDefaultZeroValue verifies that the uninitialized zero-value evaluates to AskUser for safety.
func TestDecisionDefaultZeroValue(t *testing.T) {
	var uninitialized Decision
	if uninitialized != AskUser {
		t.Errorf("expected zero-value to be AskUser (0), got %v (%d)", uninitialized, uninitialized)
	}
}

// TestDecisionString verifies the human-readable string representation of each Decision value.
func TestDecisionString(t *testing.T) {
	tests := []struct {
		decision Decision
		expected string
	}{
		{decision: AskUser, expected: "AskUser"},
		{decision: Allow, expected: "Allow"},
		{decision: Deny, expected: "Deny"},
		{decision: Decision(99), expected: "Unknown"},
	}

	for _, tc := range tests {
		if tc.decision.String() != tc.expected {
			t.Errorf("expected String() for %d to be %q, got %q", tc.decision, tc.expected, tc.decision.String())
		}
	}
}

// TestReadOnlyPolicyAuthorize verifies that ReadOnlyPolicy permits read_file and prompts for others.
func TestReadOnlyPolicyAuthorize(t *testing.T) {
	p := ReadOnlyPolicy{}

	tests := []struct {
		toolName string
		expected Decision
	}{
		{toolName: "read_file", expected: Allow},
		{toolName: "write_file", expected: AskUser},
		{toolName: "bash", expected: AskUser},
		{toolName: "unknown_tool", expected: AskUser},
	}

	for _, tc := range tests {
		decision := p.Authorize(tc.toolName, "{}")
		if decision != tc.expected {
			t.Errorf("expected Authorize(%q) to be %v, got %v", tc.toolName, tc.expected, decision)
		}
	}
}

// TestAlwaysAskPolicyAuthorize verifies that AlwaysAskPolicy requires user confirmation for all tools.
func TestAlwaysAskPolicyAuthorize(t *testing.T) {
	p := AlwaysAskPolicy{}

	tools := []string{"read_file", "write_file", "bash", "custom"}
	for _, toolName := range tools {
		decision := p.Authorize(toolName, "{}")
		if decision != AskUser {
			t.Errorf("expected Authorize(%q) to be AskUser, got %v", toolName, decision)
		}
	}
}
