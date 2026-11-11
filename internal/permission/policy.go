// Package permission decides whether a tool call should execute automatically, prompt the user,
// or be rejected immediately. The agent loop consults a Policy before every tool invocation.
package permission

// Decision represents the security verdict for a requested tool execution.
type Decision int

const (
	AskUser Decision = iota // Default fail-safe zero-value: prompts user for confirmation
	Allow                   // Automatically allows safe execution without prompting
	Deny                    // Immediately rejects execution without prompting
)

// String implements fmt.Stringer for human-readable logging and diagnostics.
func (d Decision) String() string {
	switch d {
	case Allow:
		return "Allow"
	case Deny:
		return "Deny"
	case AskUser:
		return "AskUser"
	default:
		return "Unknown"
	}
}

// Policy evaluates tool execution requests and produces an authorization decision.
type Policy interface {
	Authorize(toolName string, rawInput string) Decision
}

// ReadOnlyPolicy automatically permits non-destructive read operations while requiring approval for others.
type ReadOnlyPolicy struct{}

// Authorize permits read_file unconditionally and prompts for any side-effecting operations.
func (p ReadOnlyPolicy) Authorize(toolName string, _ string) Decision {
	switch toolName {
	case "read_file":
		return Allow
	default:
		return AskUser
	}
}

// AlwaysAskPolicy requires explicit user confirmation for every tool invocation.
type AlwaysAskPolicy struct{}

// Authorize always returns AskUser to guarantee human oversight across all operations.
func (p AlwaysAskPolicy) Authorize(_, _ string) Decision {
	return AskUser
}

// Compile-time assertions verifying that policy types satisfy the Policy interface.
var (
	_ Policy = ReadOnlyPolicy{}
	_ Policy = AlwaysAskPolicy{}
)
