# Phi Agent Code

A step-by-step journey building a production-grade coding agent harness in Go from scratch.

---

## What is an Agent Harness?

When working with LLMs, it is common to focus entirely on the model (Claude, GPT, Gemini). But in autonomous coding assistants—like **Claude Code**, **Aider**, or **OpenCode**—the model is only one component:

> **The Model is the engine. The Harness is the car.**

An engine sitting on a garage floor cannot drive anywhere. It needs:
* **The Steering Wheel & Pedals** (The Outer REPL): Capturing human input and routing instructions.
* **The Transmission & Fuel System** (The Inner Agent Loop & Provider Abstraction): Calling the model, passing tools, and looping until completion.
* **Hands & Feet** (The Tools): Letting the model interact with the physical world (`bash`, `read_file`, `write_file`).
* **The Brakes** (The Permission Gate): Preventing the car from driving off a cliff (stopping destructive commands before execution).
* **The Dashboard & Mirrors** (Conversation State & Context Compaction): Keeping track of fuel/tokens, pruning old history, and retaining critical memories without overflowing the context window.

```
┌────────────────────────────────────────────────────────────────────────┐
│                          THE AGENT HARNESS                             │
│                                                                        │
│   ┌──────────────┐         ┌──────────────────┐       ┌────────────┐   │
│   │  Human User  │ ◄─────► │   Control Gate   │ ◄───► │ Host OS    │   │
│   │  (Terminal)  │         │   (Permissions)  │       │ (Tools/FS) │   │
│   └──────────────┘         └──────────────────┘       └────────────┘   │
│          ▲                          ▲                       ▲          │
│          │                          │                       │          │
│   ┌──────┴──────────────────────────┴───────────────────────┴──────┐   │
│   │                      The Agent Loop (Go)                       │   │
│   └─────────────────────────────────┬──────────────────────────────┘   │
│                                     │                                  │
│                                     ▼                                  │
│                        ┌────────────────────────┐                      │
│                        │       LLM ENGINE       │                      │
│                        │   (Claude, GPT, etc.)  │                      │
│                        └────────────────────────┘                      │
└────────────────────────────────────────────────────────────────────────┘
```

If the harness is poorly engineered, even the smartest frontier model will crash, leak tokens, get stuck in infinite error loops, or overwrite your source files. If the harness is well-engineered, even a modest model feels reliable, safe, and productive.

This repository demystifies harness engineering by constructing one incrementally across **27 progressive milestones**.

---

## Roadmap & Step Guides

All milestones are unified into a single linear progression. Every milestone has a dedicated guide in `docs/steps/` containing:
- **Architecture & Theory**: Core design problems, diagrams, and trade-offs.
- **Implementation Guide**: Concrete requirements and components.
- **Verification Scenarios**: Step-by-step terminal prompts and expected model outputs.

| Step | Milestone | Key Concepts & Architecture | Guide | Snapshot Branch |
|:---:|---|---|:---:|:---:|
| **01** | **The Minimal Loop** | Outer REPL + inner agent loop + Anthropic API + `bash`, `read_file`, `write_file` tools. | [Read Guide](docs/steps/01-minimal-loop.md) | `step/01-minimal-loop` |
| **02** | **The Permission Gate** | Intercepting tool calls for user confirmation; errors-as-results contract. | [Read Guide](docs/steps/02-permission-gate.md) | `step/02-permission-gate` |
| **03** | **Permission Policies** | Pluggable policies (`AlwaysAsk`, `AllowList`, `AutoAccept`, path-scoped restrictions). | [Read Guide](docs/steps/03-permission-policies.md) | `step/03-permission-policies` |
| **04** | **Tool Error Retry** | Automatic retry handling for transient errors before returning to the model. | [Read Guide](docs/steps/04-tool-retry.md) | `step/04-tool-retry` |
| **05** | **Provider Abstraction** | Decoupling the engine from Anthropic with an agnostic `Provider` interface. | [Read Guide](docs/steps/05-provider-interface.md) | `step/05-provider-interface` |
| **06** | **Alternative Providers & Mocks** | Adding OpenAI, Ollama, and a deterministic `MockProvider` for testing. | [Read Guide](docs/steps/06-providers-and-mocks.md) | `step/06-providers-and-mocks` |
| **07** | **Streaming Responses** | Real-time token streaming from the model to the terminal output. | [Read Guide](docs/steps/07-streaming-responses.md) | `step/07-streaming-responses` |
| **08** | **Multimodal & Image Inputs** | Handling image files and binary attachments in user prompts. | [Read Guide](docs/steps/08-image-inputs.md) | `step/08-image-inputs` |
| **09** | **Terminal Polish & UX** | Dynamic terminal width detection, banner rendering, and live spinner. | [Read Guide](docs/steps/09-terminal-polish.md) | `step/09-terminal-polish` |
| **10** | **Slash Commands** | Interactive in-chat commands (`/help`, `/model`, `/clear`, `/exit`). | [Read Guide](docs/steps/10-slash-commands.md) | `step/10-slash-commands` |
| **11** | **Advanced Input Handling** | Multiline text inputs, history navigation, and readline capabilities. | [Read Guide](docs/steps/11-advanced-input.md) | `step/11-advanced-input` |
| **12** | **Conversation State** | Client-side tracking of stateless LLM dialog context. | [Read Guide](docs/steps/12-conversation-state.md) | `step/12-conversation-state` |
| **13** | **Transcript Renderer** | Exporting and formatting full conversation traces to Markdown and HTML. | [Read Guide](docs/steps/13-transcript-renderer.md) | `step/13-transcript-renderer` |
| **14** | **Compaction Strategies** | Managing long context windows: sliding window, summarization, and safe split points. | [Read Guide](docs/steps/14-compaction-strategies.md) | `step/14-compaction-strategies` |
| **15** | **Token Budgeting & Cost Tracking** | Budget-based message pruning, live token counting, and `/tokens` command. | [Read Guide](docs/steps/15-token-budget-and-cost.md) | `step/15-token-budget-and-cost` |
| **16** | **Prompt Caching** | Leveraging provider prompt cache breakpoints to reduce latency and API cost. | [Read Guide](docs/steps/16-prompt-caching.md) | `step/16-prompt-caching` |
| **17** | **Plug-and-Play Tool Registry** | Dynamic `Tool` interface with self-registration via `init()` without touching `main.go`. | [Read Guide](docs/steps/17-tool-registry.md) | `step/17-tool-registry` |
| **18** | **Custom Tools Suite** | Extending the harness with `git_diff`, `grep`, and `file_search` tools. | [Read Guide](docs/steps/18-custom-tools.md) | `step/18-custom-tools` |
| **19** | **Diff Approval for File Writes** | Generating unified git-style diff previews before `write_file` modifies disk. | [Read Guide](docs/steps/19-diff-approval.md) | `step/19-diff-approval` |
| **20** | **Model Context Protocol (MCP)** | Client implementation to consume external MCP tool servers dynamically. | [Read Guide](docs/steps/20-mcp-support.md) | `step/20-mcp-support` |
| **21** | **Internal Package Architecture** | Refactoring codebase into decoupled `internal/` packages with clean boundaries. | [Read Guide](docs/steps/21-internal-architecture.md) | `step/21-internal-architecture` |
| **22** | **Project Context via AGENTS.md** | Injecting repository-specific instructions into system prompts on boot. | [Read Guide](docs/steps/22-agents-md-context.md) | `step/22-agents-md-context` |
| **23** | **Subagents & Task Delegation** | Bounded worker agents, isolated sub-contexts, and the `delegate` tool. | [Read Guide](docs/steps/23-subagents-delegation.md) | `step/23-subagents-delegation` |
| **24** | **Markdown-Configured Subagents** | Defining specialized subagents declaratively from YAML/Markdown files. | [Read Guide](docs/steps/24-markdown-subagents.md) | `step/24-markdown-subagents` |
| **25** | **Persistent Agent Memory** | Pluggable KV memory store for retaining cross-session knowledge and preferences. | [Read Guide](docs/steps/25-persistent-memory.md) | `step/25-persistent-memory` |
| **26** | **Full Terminal UI (TUI)** | Production Bubble Tea terminal interface with viewport, scrollback, and modal dialogs. | [Read Guide](docs/steps/26-full-tui.md) | `step/26-full-tui` |
| **27** | **Harness End-to-End Testing** | Deterministic automated testing of the agent loop using `MockProvider`. | [Read Guide](docs/steps/27-harness-testing.md) | `step/27-harness-testing` |

---

## Milestone Workflow Automation

To enforce architectural discipline and eliminate manual Git friction, this repository includes an automated workflow manager:

```powershell
# Inspect roadmap progress
.\scripts\step.ps1 status

# Start a milestone (runs pre-flight checks, verifies main, creates branch)
.\scripts\step.ps1 start 2

# Complete a milestone (runs tests, merges into main, preserves branch snapshot)
.\scripts\step.ps1 finish
```

### Pre-flight Safety Guarantees

- **Clean Working Tree**: Blocks branch operations if there are uncommitted changes.
- **Sequential Integrity**: Guarantees that Step `N` cannot start until Step `N-1` is merged into `main`.
- **Code Health Verification**: Automatically runs `go vet` and `go build` before switching or merging branches.

---

## Quickstart

### Prerequisites

- Go 1.21+
- Anthropic API Key

### Running the Current Harness

```bash
# Set your API key
export ANTHROPIC_API_KEY="sk-ant-..."

# On Windows PowerShell:
# $env:ANTHROPIC_API_KEY="sk-ant-..."

# Run the agent
go run .
```

### Navigating Milestone Snapshots

To run or compare any milestone snapshot in history:

```bash
git checkout step/01-minimal-loop
# or
git checkout step/02-permission-gate
```

## License

This project is licensed under the [MIT License](LICENSE).
