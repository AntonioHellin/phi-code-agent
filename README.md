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

All milestones are organized into two clear phases: **Phase 1: The Core Arc** (evolving from a single file to a modular Bubble Tea TUI) and **Phase 2: Extensions & How-Tos** (modular capabilities built on top of the consolidated internal/ architecture). Every milestone has a dedicated guide in docs/steps/:

### Phase 1: The Core Arc (Foundations & Architecture)

| Step | Milestone | Key Concepts & Architecture | Guide | Snapshot Branch |
|:---:|---|---|:---:|:---:|
| **01** | **The Minimal Loop** | Outer REPL + inner agent loop + Anthropic API + ash, ead_file, write_file tools. | [Read Guide](docs/steps/01-minimal-loop.md) | step/01-minimal-loop |
| **02** | **The Permission Gate** | Intercepting tool calls for user confirmation; errors-as-results contract. | [Read Guide](docs/steps/02-permission-gate.md) | step/02-permission-gate |
| **03** | **Provider Abstraction** | Decoupling the engine from Anthropic with an agnostic Provider interface. | [Read Guide](docs/steps/03-provider-interface.md) | step/03-provider-interface |
| **04** | **Terminal Polish & UX** | Dynamic terminal width detection, banner rendering, and live spinner. | [Read Guide](docs/steps/04-terminal-polish.md) | step/04-terminal-polish |
| **05** | **Slash Commands** | Interactive in-chat commands (/help, /model, /clear, /exit). | [Read Guide](docs/steps/05-slash-commands.md) | step/05-slash-commands |
| **06** | **Conversation State** | Client-side tracking of stateless LLM dialog context. | [Read Guide](docs/steps/06-conversation-state.md) | step/06-conversation-state |
| **07** | **Compaction Strategies** | Managing long context windows: sliding window, summarization, and safe split points. | [Read Guide](docs/steps/07-compaction-strategies.md) | step/07-compaction-strategies |
| **08** | **Advanced Input Handling** | Multiline text inputs, history navigation, and readline capabilities. | [Read Guide](docs/steps/08-advanced-input.md) | step/08-advanced-input |
| **09** | **Plug-and-Play Tool Registry** | Dynamic Tool interface with self-registration via init() without touching main.go. | [Read Guide](docs/steps/09-tool-registry.md) | step/09-tool-registry |
| **10** | **Internal Package Architecture** | Refactoring codebase into decoupled internal/ packages with clean boundaries. | [Read Guide](docs/steps/10-internal-architecture.md) | step/10-internal-architecture |
| **11** | **Subagents & Task Delegation** | Bounded worker agents, isolated sub-contexts, and the delegate tool. | [Read Guide](docs/steps/11-subagents-delegation.md) | step/11-subagents-delegation |
| **12** | **Full Terminal UI (TUI)** | Production Bubble Tea terminal interface with viewport, scrollback, and modal dialogs. | [Read Guide](docs/steps/12-full-tui.md) | step/12-full-tui |
| **13** | **Harness End-to-End Testing** | Deterministic automated testing of the agent loop using MockProvider. | [Read Guide](docs/steps/13-harness-testing.md) | step/13-harness-testing |

### Phase 2: Extensions & How-Tos (Modular Capabilities)

| Step | Milestone | Key Concepts & Architecture | Guide | Snapshot Branch |
|:---:|---|---|:---:|:---:|
| **14** | **Permission Policies** | Pluggable policies (AlwaysAsk, AllowList, AutoAccept, path-scoped restrictions) in internal/policy. | [Read Guide](docs/steps/14-permission-policies.md) | step/14-permission-policies |
| **15** | **Tool Error Retry** | Automatic retry handling for transient errors before returning to the model in internal/tool. | [Read Guide](docs/steps/15-tool-retry.md) | step/15-tool-retry |
| **16** | **Alternative Providers & Mocks** | Adding OpenAI, Ollama, and a deterministic MockProvider for testing in internal/provider. | [Read Guide](docs/steps/16-providers-and-mocks.md) | step/16-providers-and-mocks |
| **17** | **Streaming Responses** | Real-time token streaming from the model to the terminal output. | [Read Guide](docs/steps/17-streaming-responses.md) | step/17-streaming-responses |
| **18** | **Token Budgeting & Cost Tracking** | Budget-based message pruning, live token counting, and /tokens command. | [Read Guide](docs/steps/18-token-budget-and-cost.md) | step/18-token-budget-and-cost |
| **19** | **Prompt Caching** | Leveraging provider prompt cache breakpoints to reduce latency and API cost. | [Read Guide](docs/steps/19-prompt-caching.md) | step/19-prompt-caching |
| **20** | **Diff Approval for File Writes** | Generating unified git-style diff previews before write_file modifies disk. | [Read Guide](docs/steps/20-diff-approval.md) | step/20-diff-approval |
| **21** | **Model Context Protocol (MCP)** | Client implementation to consume external MCP tool servers dynamically in internal/mcp. | [Read Guide](docs/steps/21-mcp-support.md) | step/21-mcp-support |
| **22** | **Project Context via AGENTS.md** | Injecting repository-specific instructions into system prompts on boot. | [Read Guide](docs/steps/22-agents-md-context.md) | step/22-agents-md-context |
| **23** | **Persistent Agent Memory** | Pluggable KV memory store for retaining cross-session knowledge and preferences in internal/memory. | [Read Guide](docs/steps/23-persistent-memory.md) | step/23-persistent-memory |
| **24** | **Multimodal & Image Inputs** | Handling image files and binary attachments in user prompts in internal/api. | [Read Guide](docs/steps/24-image-inputs.md) | step/24-image-inputs |
| **25** | **Custom Tools Suite** | Extending the harness with git_diff, grep, and ile_search tools in internal/tool. | [Read Guide](docs/steps/25-custom-tools.md) | step/25-custom-tools |
| **26** | **Transcript Renderer** | Exporting and formatting full conversation traces to Markdown and HTML in internal/ui. | [Read Guide](docs/steps/26-transcript-renderer.md) | step/26-transcript-renderer |
| **27** | **Markdown-Configured Subagents** | Defining specialized subagents declaratively from YAML/Markdown files in internal/subagent. | [Read Guide](docs/steps/27-markdown-subagents.md) | step/27-markdown-subagents |

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
