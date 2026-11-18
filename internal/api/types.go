// Package api defines the vendor-agnostic core domain types for the agent harness.
// It sits at the innermost architectural layer and has zero internal dependencies.
package api

// BlockType identifies the semantic purpose of a content block within a conversation turn.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

// Block represents a single discrete payload within a message (text, tool invocation, or tool output).
// It uses a tagged struct pattern to remain vendor-agnostic and avoid vendor SDK union types in domain logic.
type Block struct {
	Type             BlockType
	Text             string // Populated for BlockText
	ToolUseID        string // Populated for BlockToolUse and BlockToolResult
	ToolName         string // Populated for BlockToolUse
	ToolInput        string // Populated for BlockToolUse (raw JSON arguments string)
	ThoughtSignature []byte // Optional opaque reasoning signature preserved across tool turns
	ToolResult       string // Populated for BlockToolResult (raw output string)
	IsError          bool   // Populated for BlockToolResult (indicates execution failure)
}

// Role identifies the sender of a conversation turn.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message models a single conversational turn between the user, assistant, or tool runner.
type Message struct {
	Role    Role
	Content []Block
}

// HasToolResult reports whether the message contains any tool_result blocks.
// Used by compaction strategies to identify clean conversation boundaries.
func (m Message) HasToolResult() bool {
	for _, b := range m.Content {
		if b.Type == BlockToolResult {
			return true
		}
	}
	return false
}

// ToolDef provides the generic schema contract an LLM needs to invoke a tool.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Required    []string       `json:"required,omitempty"`
}

// Response normalizes the output returned from any LLM provider turn.
type Response struct {
	Content    []Block
	StopReason string
	Usage      Usage
}

// Usage normalizes per-turn token accounting across LLM providers.
// InputTokens counts uncached input tokens only (excluding CacheCreationTokens and CacheReadTokens),
// and OutputTokens reflects total billed generation tokens including reasoning/thinking output.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}

// Add accumulates per-turn token counts into this Usage summary safely.
func (u *Usage) Add(other Usage) {
	if u == nil {
		return
	}
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheCreationTokens += other.CacheCreationTokens
	u.CacheReadTokens += other.CacheReadTokens
}

// Total returns the combined token count across input, cache creation, cache read, and output.
func (u Usage) Total() int {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
}
