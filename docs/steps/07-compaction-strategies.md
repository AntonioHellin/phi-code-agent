# Step 07: Compaction Strategies

## 1. Architectural Overview & The Context Boundary

As an AI coding agent pairs with a developer across hours of reading files, writing diffs, and executing tests, the conversation transcript grows monotonically. Eventually, it encounters two hard technical boundaries:

1. **Context Window Limits**: Exceeding the maximum token limit of the model (e.g., 200k tokens for Claude) results in catastrophic API failure (`400 Bad Request: prompt is too long`).
2. **Cost & Latency Explosion**: LLM providers bill per input token on *every single request*. Sending 100,000 historical tokens for every minor keystroke or tool iteration causes prohibitive cost inflation and adds seconds of time-to-first-token (TTFT) latency.

**Compaction** solves this by strategically trimming or summarizing past conversation turns while strictly maintaining provider protocol invariants:

```mermaid
flowchart TD
    Raw["Raw History (N turns)"] --> Compactor{"Compaction Strategy"}
    Compactor -->|NoCompaction| PassThrough["Keep Everything (Default)"]
    Compactor -->|Sliding Window| Trimmed["Target: Keep Last K Turns"]
    Compactor -->|Summarization| Summarized["Summarize Old Turns + Keep Recent K"]
    Trimmed --> SafeSplit["SafeSplitPoint (Scan backward for clean human turn)"]
    Summarized --> SafeSplit
    SafeSplit --> Guarded["Compacted Message Slice"]
    Guarded --> Logger["WithLogging Decorator (Audit Trail)"]
    Logger --> Provider["LLM Provider (API Call)"]
```

---

## 2. Key Design Principles & Invariants

### The Safe Split Invariant
In Anthropic's wire protocol, tool execution outputs are not assigned an independent `"tool"` role; they are returned inside a `RoleUser` message block (`BlockToolResult`). 

Consequently, naive slicing (`messages[len(messages)-K:]`) can easily cut a conversation history right down the middle of a tool transaction:
- An assistant message requesting a tool (`tool_use`) is pruned.
- The next user message returning the tool output (`tool_result`) remains as the new head of the history.

The Anthropic API strictly enforces that **every `tool_result` must be preceded by a matching `tool_use`**. An orphaned tool result causes immediate rejection:
```text
HTTP 400 Bad Request: each tool_result block must correspond to a preceding tool_use block in the previous message
```

Furthermore, conversations must always begin with an authentic human prompt. Therefore, `SafeSplitPoint` walks backward from the desired cut index until it finds a turn satisfying:
$$\text{messages}[i].\text{Role} == \text{RoleUser} \quad \land \quad \neg\text{messages}[i].\text{HasToolResult}()$$

```mermaid
sequenceDiagram
    autonumber
    participant U as User
    participant A as Assistant (Model)
    participant H as SafeSplitPoint

    Note over U,A: History
    U->>A: Turn 1: "Read main.go" (Index 2 - Clean Human Turn)
    A->>U: ToolUse: read_file (Index 3)
    U->>A: ToolResult: "package main..." (Index 4 - RoleUser with ToolResult)
    A->>U: "Here is the code" (Index 5)
    
    Note over H: Naive cut at index 4 leaves orphaned ToolResult!
    Note over H: SafeSplitPoint walks back 4 -> 3 -> 2
    Note over H: Snaps slice to Index 2 (Clean Human Turn)
```

---

### Turn Boundary Compaction vs. Mid-Turn Compaction
An agent interaction turn is an **atomic transaction**:
1. User provides prompt.
2. Model requests tool.
3. Harness executes tool and returns result.
4. Model synthesizes final response (`end_turn`).

Compacting *inside* the inner agent loop between tool executions risks pruning the prompt that defined the active task before it finishes. Therefore, compaction executes at the **turn boundary** in [`main.go`](../../main.go) before delegating to `agentLoop`.

---

### Go Method Sets & Receiver Mechanics
In Go, an interface is satisfied only when a type's method set contains all required signatures:
- For value receiver `func (s SlidingWindow) Compact(...)`: both `SlidingWindow` and `*SlidingWindow` implement `CompactionStrategy`.
- For pointer receiver `func (s *SlidingWindow) Compact(...)`: only `*SlidingWindow` implements `CompactionStrategy`.

When instantiating `SlidingWindow`, pass the pointer (`&SlidingWindow{KeepLast: K}`) to satisfy the interface.

---

## 3. Implementation Details

### 1. Domain Helper on `Message` ([`provider.go`](../../provider.go))
Detects whether a message turn contains any tool execution outputs:
```go
func (m Message) HasToolResult() bool {
	for _, b := range m.Content {
		if b.Type == BlockToolResult {
			return true
		}
	}
	return false
}
```

### 2. Strategy Contract & Implementations ([`compact.go`](../../compact.go))
```go
type CompactionStrategy interface {
	Compact(ctx context.Context, messages []Message) ([]Message, error)
}

func SafeSplitPoint(messages []Message, desiredCutIndex int) int {
	if desiredCutIndex <= 0 {
		return 0
	}
	if desiredCutIndex >= len(messages) {
		return len(messages)
	}
	for index := desiredCutIndex; index > 0; index-- {
		targetMessage := messages[index]
		if targetMessage.Role == RoleUser && !targetMessage.HasToolResult() {
			return index
		}
	}
	return 0
}

type NoCompaction struct{}

func (NoCompaction) Compact(_ context.Context, messages []Message) ([]Message, error) {
	return messages, nil
}

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

	summaryBlock := Block{
		Type: BlockText,
		Text: "[Summary of previous conversation]:\n" + strings.TrimSpace(summaryText) + "\n\n",
	}

	compactedMessages := make([]Message, len(messagesToKeep))
	copy(compactedMessages, messagesToKeep)
	compactedMessages[0] = Message{
		Role:    compactedMessages[0].Role,
		Content: append([]Block{summaryBlock}, compactedMessages[0].Content...),
	}

	return compactedMessages, nil
}

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
```

### 3. Wiring at Turn Boundary ([`main.go`](../../main.go))
```go
var (
	provider  Provider
	messages  []Message
	compactor CompactionStrategy = NoCompaction{}
)

// Inside main() REPL loop:
messages = append(messages, Message{
	Role:    RoleUser,
	Content: []Block{{Type: BlockText, Text: input}},
})

compacted, err := compactor.Compact(ctx, messages)
if err != nil {
	fmt.Printf("compaction error: %v\n", err)
} else {
	messages = compacted
}

messages = agentLoop(ctx, provider, messages)
```

### 4. Interactive Slash Command ([`commands.go`](../../commands.go))
Allows inspecting and triggering compaction interactively:
```go
commands["compact"] = command{description: "compact conversation history", run: cmdCompact}

func cmdCompact(_ string) {
	if len(messages) == 0 {
		fmt.Println("no messages to compact")
		return
	}
	before := len(messages)
	compacted, err := compactor.Compact(context.Background(), messages)
	if err != nil {
		fmt.Printf("compact error: %v\n", err)
		return
	}
	messages = compacted
	fmt.Printf("compacted: %d -> %d messages\n", before, len(messages))
}
```

---

## 4. Verification & Exercises

### Exercise 1: Table-Driven Unit Testing (`compact_test.go`)
Run the test suite to verify boundary conditions, backward scanning, tool-use atomicity, decorator logging, and summarization:

```bash
go test -v ./...
```

**Verified Scenarios:**
- Desired index $\le 0$ returns `0`.
- Desired index beyond slice length returns `len(messages)`.
- Cut index falling on `BlockToolUse` or `BlockToolResult` automatically walks back to the preceding clean human message.
- `SlidingWindow` preserves tool call atomicity and ensures history never starts with an orphaned tool result.
- `WithLogging` decorator generates an audit trail of pruned and retained message turns in `compactions.log`.
- `Summarize` preserves recent turns while compressing older context into a clean human summary block.

### Exercise 2: Interactive Sliding Window Verification with Logging
1. Open [`main.go`](../../main.go) and wrap the compactor with logging:
   ```go
   var compactor CompactionStrategy = &WithLogging{
       Inner:   &SlidingWindow{KeepLast: 4},
       LogPath: "compactions.log",
   }
   ```
2. Run `go run .`
3. Execute 3 consecutive turns requiring tool use (e.g. read files or run commands).
4. Run `/compact`:
   ```text
   > /compact
   compacted: 8 -> 4 messages
   ```
5. Inspect `compactions.log` to view the exact diff of pruned and retained turns.
