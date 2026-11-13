package tool

import (
	"context"
	"strings"
	"time"

	"phi-agent-code/internal/api"
)

// defaultTransientPatterns lists well-known lowercase substrings that indicate
// temporary environmental or I/O contention rather than logical LLM mistakes.
var defaultTransientPatterns = []string{
	"resource temporarily unavailable",
	"temporarily unavailable",
	"file is locked",
	"lock violation",
	"sharing violation",
	"connection reset",
	"timeout",
	"timed out",
	"exit status 124",
}

// RetryDecorator wraps a Tool to automatically retry transient failures with
// context-aware exponential backoff while passing logical errors straight through.
type RetryDecorator struct {
	Tool        Tool                                   // Underlying tool implementation being decorated
	MaxRetries  int                                    // Maximum retry attempts after the initial call
	BaseDelay   time.Duration                          // Initial wait duration before the first retry (defaults to 100ms)
	IsRetryable func(output string, isError bool) bool // Optional custom classifier; defaults to DefaultIsRetryable
}

// Definition delegates schema metadata directly to the wrapped tool so decoration
// remains completely transparent to the LLM.
func (r *RetryDecorator) Definition() api.ToolDef {
	if r == nil || r.Tool == nil {
		return api.ToolDef{}
	}
	return r.Tool.Definition()
}

// Execute runs the wrapped tool and retries only when the failure is classified
// as transient and retries remain, honoring context cancellation during backoff.
func (r *RetryDecorator) Execute(ctx context.Context, rawInput string) (string, bool) {
	if r == nil || r.Tool == nil {
		return "retry decorator: nil tool", true
	}
	// Normalize negative retry counts so the initial attempt always executes once.
	maxRetries := r.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, isError := r.Tool.Execute(ctx, rawInput)
		if !isError {
			return output, false
		}
		if attempt == maxRetries || !r.shouldRetry(output, isError) {
			return output, isError
		}

		// Wait with exponential backoff while staying responsive to context cancellation
		// (e.g., Ctrl+D/Ctrl+C or parent deadline expiration).
		delay := r.delayForAttempt(attempt)
		select {
		case <-ctx.Done():
			return "operation canceled: " + ctx.Err().Error(), true
		case <-time.After(delay):
		}
	}

	return "retries exhausted", true
}

const (
	defaultBaseDelay = 100 * time.Millisecond
	maxRetryDelay    = 10 * time.Second
)

// delayForAttempt computes capped exponential backoff (base * 2^attempt) with
// fail-safe guards against nil receivers, negative attempts, and integer overflow.
func (r *RetryDecorator) delayForAttempt(attempt int) time.Duration {
	base := defaultBaseDelay
	if r != nil && r.BaseDelay > 0 {
		base = r.BaseDelay
	}
	if attempt < 0 {
		attempt = 0
	}
	// Cap exponent before bit-shifting to prevent signed integer overflow (1<<63 becomes negative).
	if attempt > 30 {
		return maxRetryDelay
	}

	delay := base * time.Duration(1<<attempt)
	// Checking delay <= 0 catches signed int64 multiplication overflow when BaseDelay is huge.
	if delay <= 0 || delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

// shouldRetry delegates classification to a custom IsRetryable hook when provided,
// falling back to DefaultIsRetryable otherwise.
func (r *RetryDecorator) shouldRetry(output string, isError bool) bool {
	if !isError {
		return false
	}
	if r.IsRetryable != nil {
		return r.IsRetryable(output, isError)
	}
	return DefaultIsRetryable(output, isError)
}

// DefaultIsRetryable inspects error output against defaultTransientPatterns
// to distinguish temporary system or network contention from permanent LLM errors.
func DefaultIsRetryable(output string, isError bool) bool {
	if !isError {
		return false
	}
	lower := strings.ToLower(output)
	for _, pattern := range defaultTransientPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// Compile-time assertion ensuring RetryDecorator satisfies the Tool contract.
var _ Tool = (*RetryDecorator)(nil)
