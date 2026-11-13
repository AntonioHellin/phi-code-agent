package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"phi-agent-code/internal/api"
)

// mockResult represents a single pre-programmed response for mockRetryTool.
type mockResult struct {
	output  string
	isError bool
}

// mockRetryTool simulates a tool with deterministic per-attempt outputs for testing retries.
type mockRetryTool struct {
	name    string
	results []mockResult
	calls   int
}

// Definition returns a minimal ToolDef schema for the mock tool.
func (m *mockRetryTool) Definition() api.ToolDef {
	name := m.name
	if name == "" {
		name = "mock_retry"
	}
	return api.ToolDef{
		Name:        name,
		Description: "Deterministic mock tool for retry tests",
	}
}

// Execute returns the pre-programmed result for the current call index and increments calls.
func (m *mockRetryTool) Execute(_ context.Context, _ string) (string, bool) {
	idx := m.calls
	m.calls++
	if idx >= len(m.results) {
		return "unexpected extra call", true
	}
	res := m.results[idx]
	return res.output, res.isError
}

// TestRetryDecorator_SuccessFirstAttempt verifies that a succeeding tool runs exactly once
// without triggering any retry or backoff overhead.
func TestRetryDecorator_SuccessFirstAttempt(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "clean output", isError: false},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 2,
		BaseDelay:  1 * time.Millisecond,
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if isError {
		t.Fatalf("expected success on first attempt, got error: %q", out)
	}
	if out != "clean output" {
		t.Errorf("expected 'clean output', got %q", out)
	}
	if mock.calls != 1 {
		t.Errorf("expected exactly 1 call, got %d", mock.calls)
	}
}

// TestRetryDecorator_TransientFailureRecovery verifies that transient errors are retried
// up to MaxRetries and return the eventual successful output transparently.
func TestRetryDecorator_TransientFailureRecovery(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "file is locked by another process", isError: true},
			{output: "resource temporarily unavailable", isError: true},
			{output: "recovered output", isError: false},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 2,
		BaseDelay:  1 * time.Millisecond,
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if isError {
		t.Fatalf("expected recovery after transient failures, got error: %q", out)
	}
	if out != "recovered output" {
		t.Errorf("expected 'recovered output', got %q", out)
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 total calls (1 initial + 2 retries), got %d", mock.calls)
	}
}

// TestRetryDecorator_LogicalErrorNoRetry verifies that non-transient logical errors
// bypass retries immediately so the LLM can observe the error and self-correct without delay.
func TestRetryDecorator_LogicalErrorNoRetry(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "open missing.txt: no such file or directory", isError: true},
			{output: "should never reach attempt 2", isError: false},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 3,
		BaseDelay:  1 * time.Millisecond,
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if !isError {
		t.Fatalf("expected isError=true for logical error")
	}
	if !strings.Contains(out, "no such file or directory") {
		t.Errorf("expected original error output, got %q", out)
	}
	if mock.calls != 1 {
		t.Errorf("expected exactly 1 call (no retries for logical error), got %d", mock.calls)
	}
}

// TestRetryDecorator_ExhaustedRetries verifies that repeated transient failures stop
// after 1 initial attempt + MaxRetries and surface the last observed error to the LLM.
func TestRetryDecorator_ExhaustedRetries(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "connection reset by peer (attempt 1)", isError: true},
			{output: "connection reset by peer (attempt 2)", isError: true},
			{output: "connection reset by peer (final)", isError: true},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 2,
		BaseDelay:  1 * time.Millisecond,
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if !isError {
		t.Fatalf("expected isError=true when retries are exhausted")
	}
	if out != "connection reset by peer (final)" {
		t.Errorf("expected last error output to preserve failure context, got %q", out)
	}
	if mock.calls != 3 {
		t.Errorf("expected 3 total calls (1 initial + 2 retries), got %d", mock.calls)
	}
}

// TestRetryDecorator_NegativeMaxRetries verifies that negative MaxRetries values
// are normalized to 0 so the initial attempt still runs safely once.
func TestRetryDecorator_NegativeMaxRetries(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "timeout on initial run", isError: true},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: -3,
		BaseDelay:  1 * time.Millisecond,
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if !isError {
		t.Fatalf("expected isError=true")
	}
	if out != "timeout on initial run" {
		t.Errorf("expected initial attempt output, got %q", out)
	}
	if mock.calls != 1 {
		t.Errorf("expected exactly 1 call when MaxRetries < 0, got %d", mock.calls)
	}
}

// TestRetryDecorator_NilToolSafety verifies that nil RetryDecorator receivers and
// unset inner Tool fields never panic, returning empty metadata and an error tool result.
func TestRetryDecorator_NilToolSafety(t *testing.T) {
	var nilDecorator *RetryDecorator
	if def := nilDecorator.Definition(); def.Name != "" {
		t.Errorf("expected empty ToolDef from nil decorator, got %q", def.Name)
	}
	out, isError := nilDecorator.Execute(context.Background(), `{}`)
	if !isError || !strings.Contains(out, "nil tool") {
		t.Errorf("expected nil-tool error from nil decorator, got (%q, %v)", out, isError)
	}

	emptyDecorator := &RetryDecorator{MaxRetries: 2}
	if def := emptyDecorator.Definition(); def.Name != "" {
		t.Errorf("expected empty ToolDef when inner Tool is nil, got %q", def.Name)
	}
	out, isError = emptyDecorator.Execute(context.Background(), `{}`)
	if !isError || !strings.Contains(out, "nil tool") {
		t.Errorf("expected nil-tool error when inner Tool is nil, got (%q, %v)", out, isError)
	}
}

// TestRetryDecorator_DelayForAttemptCapping verifies exponential backoff progression
// and ensures negative attempts, large bit shifts, and huge BaseDelay values never overflow.
func TestRetryDecorator_DelayForAttemptCapping(t *testing.T) {
	decorator := &RetryDecorator{BaseDelay: 100 * time.Millisecond}

	if got := decorator.delayForAttempt(0); got != 100*time.Millisecond {
		t.Errorf("attempt 0: expected 100ms, got %v", got)
	}
	if got := decorator.delayForAttempt(1); got != 200*time.Millisecond {
		t.Errorf("attempt 1: expected 200ms, got %v", got)
	}
	if got := decorator.delayForAttempt(-2); got != 100*time.Millisecond {
		t.Errorf("negative attempt: expected 100ms, got %v", got)
	}
	// attempt > 30 would overflow 1<<attempt in signed int64 if not guarded.
	if got := decorator.delayForAttempt(64); got != maxRetryDelay {
		t.Errorf("huge attempt: expected capped %v, got %v", maxRetryDelay, got)
	}

	// Huge BaseDelay multiplied by 2^attempt could wrap int64 without the delay <= 0 guard.
	hugeBase := &RetryDecorator{BaseDelay: (1 << 61) * time.Nanosecond}
	if got := hugeBase.delayForAttempt(5); got != maxRetryDelay {
		t.Errorf("huge BaseDelay overflow: expected capped %v, got %v", maxRetryDelay, got)
	}
}

// TestRetryDecorator_ContextCanceledDuringBackoff verifies that canceling the context
// while waiting for the next retry aborts immediately instead of blocking for BaseDelay.
func TestRetryDecorator_ContextCanceledDuringBackoff(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "resource temporarily unavailable", isError: true},
			{output: "should not execute after cancel", isError: false},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 2,
		BaseDelay:  2 * time.Second, // Long enough that test would stall if ctx.Done() were ignored
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after attempt 0 enters the backoff select.
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	out, isError := decorator.Execute(ctx, `{}`)
	elapsed := time.Since(start)

	if !isError {
		t.Fatalf("expected isError=true when context is canceled")
	}
	if !strings.Contains(out, "operation canceled") {
		t.Errorf("expected 'operation canceled' message, got %q", out)
	}
	if mock.calls != 1 {
		t.Errorf("expected only 1 call before cancellation, got %d", mock.calls)
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("expected immediate abort on cancel, took %v", elapsed)
	}
}

// TestRetryDecorator_CustomIsRetryable verifies that a caller-supplied IsRetryable hook
// overrides DefaultIsRetryable for tool-specific transient conditions (e.g., HTTP 429).
func TestRetryDecorator_CustomIsRetryable(t *testing.T) {
	mock := &mockRetryTool{
		results: []mockResult{
			{output: "HTTP 429 Too Many Requests", isError: true},
			{output: "HTTP 200 OK", isError: false},
		},
	}
	decorator := &RetryDecorator{
		Tool:       mock,
		MaxRetries: 1,
		BaseDelay:  1 * time.Millisecond,
		IsRetryable: func(output string, isError bool) bool {
			return isError && strings.Contains(output, "429")
		},
	}

	out, isError := decorator.Execute(context.Background(), `{}`)
	if isError || out != "HTTP 200 OK" {
		t.Fatalf("expected custom retry recovery to 'HTTP 200 OK', got (%q, %v)", out, isError)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 calls via custom classifier, got %d", mock.calls)
	}
}

// TestDefaultIsRetryable verifies transient pattern matching, case insensitivity,
// and ensures non-error outputs never trigger retries even if they contain keywords like "timeout".
func TestDefaultIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		isError  bool
		expected bool
	}{
		{
			name:     "successful output mentioning timeout must not retry",
			output:   "configured timeout=30s successfully",
			isError:  false,
			expected: false,
		},
		{
			name:     "file locked error",
			output:   "Error: File Is Locked by another process",
			isError:  true,
			expected: true,
		},
		{
			name:     "exit status 124 timeout",
			output:   "[exit error: exit status 124]",
			isError:  true,
			expected: true,
		},
		{
			name:     "sharing violation on Windows",
			output:   "CreateFile: sharing violation",
			isError:  true,
			expected: true,
		},
		{
			name:     "logical syntax or missing file error",
			output:   "unknown flag: --invalid-option",
			isError:  true,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultIsRetryable(tc.output, tc.isError); got != tc.expected {
				t.Errorf("DefaultIsRetryable(%q, %v) = %v, want %v", tc.output, tc.isError, got, tc.expected)
			}
		})
	}
}
