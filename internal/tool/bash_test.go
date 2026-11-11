package tool

import (
	"context"
	"strings"
	"testing"
)

// TestBashToolDefinition verifies the tool schema contract and required fields.
func TestBashToolDefinition(t *testing.T) {
	b := BashTool{}
	def := b.Definition()

	if def.Name != "bash" {
		t.Errorf("expected name 'bash', got %q", def.Name)
	}
	if def.Description == "" {
		t.Errorf("expected non-empty description")
	}
	if len(def.Required) != 1 || def.Required[0] != "command" {
		t.Errorf("expected required field 'command', got %v", def.Required)
	}
}

// TestBashToolExecuteSuccess verifies normal command execution and combined stdout output.
func TestBashToolExecuteSuccess(t *testing.T) {
	b := BashTool{}
	out, isError := b.Execute(context.Background(), `{"command":"echo test-output"}`)

	if isError {
		t.Fatalf("expected successful execution, got error: %s", out)
	}
	if !strings.Contains(out, "test-output") {
		t.Errorf("expected output to contain 'test-output', got %q", out)
	}
}

// TestBashToolExecuteExitError verifies that non-zero exit codes return isError=true with exit details.
func TestBashToolExecuteExitError(t *testing.T) {
	b := BashTool{}
	out, isError := b.Execute(context.Background(), `{"command":"exit 42"}`)

	if !isError {
		t.Fatalf("expected isError=true for non-zero exit code, got false")
	}
	if !strings.Contains(out, "[exit error:") {
		t.Errorf("expected output to contain '[exit error:', got %q", out)
	}
}

// TestBashToolExecuteMalformedJSON verifies that invalid JSON returns an error flag without panicking.
func TestBashToolExecuteMalformedJSON(t *testing.T) {
	b := BashTool{}
	out, isError := b.Execute(context.Background(), `{"command":`)

	if !isError {
		t.Fatalf("expected isError=true for malformed JSON, got false")
	}
	if out == "" {
		t.Errorf("expected non-empty error message for malformed JSON")
	}
}

// TestBashToolExecuteCanceledContext verifies that an already-canceled context aborts command execution.
func TestBashToolExecuteCanceledContext(t *testing.T) {
	b := BashTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, isError := b.Execute(ctx, `{"command":"echo test"}`)

	if !isError {
		t.Fatalf("expected isError=true for canceled context, got false")
	}
	if out == "" {
		t.Errorf("expected non-empty error output for canceled execution")
	}
}
