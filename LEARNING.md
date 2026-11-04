# Engineering Journal: AI Agent Harness Architecture

This document tracks architectural decisions, LLM protocol invariants, and design patterns applied specifically to this Go AI coding agent harness (`phi-agent-code`).

---

## Milestone 07: Conversation Compaction & History Lifecycle

### 1. The Core LLM Protocol Invariant: Tool Use Atomicity
- **The Problem**: In Anthropic's wire protocol, tool results do not have a dedicated `"tool"` role; they are packaged inside a `RoleUser` message (`BlockToolResult`). Naively slicing message history (`messages[len(messages)-K:]`) can sever an assistant's `tool_use` request from its corresponding user `tool_result`.
- **The Failure Mode**: The Anthropic API strictly rejects orphaned tool outputs with an immediate `HTTP 400 Bad Request: each tool_result block must correspond to a preceding tool_use block`. Additionally, conversations must always start with an authentic human turn.
- **Architectural Solution (`SafeSplitPoint`)**: Rather than slicing at an arbitrary index, the harness scans backward to ensure cuts occur exclusively on genuine human turns satisfying:
  $$\text{messages}[i].\text{Role} == \text{RoleUser} \quad \land \quad \neg\text{messages}[i].\text{HasToolResult}()$$

---

### 2. Turn Boundary Compaction vs. Mid-Turn Compaction
- **The In-Flight Transaction Problem**: An agent turn (`agentLoop`) is an atomic task execution loop (user prompt $\rightarrow$ model tool request $\rightarrow$ local execution $\rightarrow$ model synthesis). If compaction runs *inside* `agentLoop` between tool steps, it risks pruning the initial human prompt that defines the active objective.
- **The Solution**: Compaction is strictly executed at the **Turn Boundary** in `main.go` when the system is in a quiescent state (between user prompts). This guarantees:
  1. The active user prompt is never pruned while in execution.
  2. The LLM starts each major turn with maximum freed context budget.

---

### 3. Go Design Patterns Applied to Agent Architecture

#### Strategy Pattern (`CompactionStrategy` Interface)
- **Why**: Decouples the CLI and agent loop from the concrete compaction mechanism. Allows plugging `NoCompaction`, `SlidingWindow`, or `Summarize` without touching `main.go` orchestration.
- **Contract**:
  ```go
  type CompactionStrategy interface {
      Compact(ctx context.Context, messages []Message) ([]Message, error)
  }
  ```

#### Decorator Pattern (`WithLogging`)
- **Why**: Observability and audit trails are cross-cutting concerns. Embedding disk I/O (`compactions.log`) inside `SlidingWindow` or `Summarize` violates the Single Responsibility Principle (SRP) and couples algorithms to filesystem operations.
- **Implementation**: `WithLogging` wraps any inner `CompactionStrategy`, records before/after diffs when pruning occurs, and implements `CompactionStrategy` itself.

#### Defensive Immutability on Backing Arrays
- **Why**: In Go, a slice expression (`messages[safeIndex:]`) produces a slice header pointing to the *same underlying backing array*. Modifying retained turns directly would introduce unintended side-effects into the caller's slice.
- **Implementation in `Summarize`**: We explicitly allocate and clone via `make([]Message, len(toKeep))` and `copy()`, ensuring the caller's historical state remains immutable before prepending the summary text block.

---

### 4. Compaction Tradeoffs: Sliding Window vs. Summarization

| Strategy | Advantages | Tradeoffs & Failure Modes |
| :--- | :--- | :--- |
| **`SlidingWindow`** | - Zero latency ($O(1)$ pointer math)<br>- Zero API token cost<br>- Deterministic message boundaries | **Contextual Amnesia**: Dropping older turns severs pronoun and file references (e.g., *"now read the last three lines of the file"* loses track of which file was read). |
| **`Summarize`** | - Preserves semantic intent and file paths<br>- Retains long-term continuity | - Adds network latency (extra LLM call)<br>- Incurs additional input/output token cost<br>- Potential loss of fine-grained code diff details |

---

### 5. Deterministic Verification via Mock Providers
- **Why**: Unit tests for agent compaction must execute rapidly in CI/CD without network dependencies or paid API keys.
- **Implementation (`compact_test.go`)**: We implement `mockSummaryProvider` satisfying the `Provider` interface. This enables simulating LLM summary outputs and asserting that summary blocks are prepended into `compactedMessages[0]` without calling Anthropic.

