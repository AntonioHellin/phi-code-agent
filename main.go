package main

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"

	"phi-agent-code/internal/agent"
	"phi-agent-code/internal/compact"
	"phi-agent-code/internal/provider"
	"phi-agent-code/internal/tool"
	"phi-agent-code/internal/ui"
)

// systemPrompt establishes the core behavioral persona and available toolset for the agent.
const systemPrompt = `You are a coding assistant running in a terminal. You have tools:
bash, read_file, write_file, delegate. Be concise.`

// Global application references shared across command handlers and delegation tools.
var (
	prov      provider.Provider
	rootAgent *agent.Agent
	compactor compact.CompactionStrategy = compact.NoCompaction{}
)

func main() {
	// Initialize concrete Anthropic LLM provider adapter
	anthropicClient := anthropic.NewClient()
	prov = provider.NewAnthropicProvider(
		&anthropicClient,
		string(anthropic.ModelClaudeHaiku5_5),
		8192,
		systemPrompt,
	)

	// -------------------------------------------------------------------------
	// Compaction Strategy Configuration (Composition Root)
	// Uncomment the strategy you wish to evaluate:
	// -------------------------------------------------------------------------

	// Option 1: No compaction (Default pass-through: retains all history indefinitely)
	// compactor = compact.NoCompaction{}

	// Option 2: Sliding Window without logging (retains last K turns, e.g. 6 messages)
	// compactor = &compact.SlidingWindow{KeepLast: 6}

	// Option 3: Sliding Window WITH Logging (ACTIVE - logs audit trail to compactions.log)
	compactor = &compact.WithLogging{
		Inner:   &compact.SlidingWindow{KeepLast: 6},
		LogPath: "compactions.log",
	}

	// Option 4: LLM-driven Summarization with Logging (summarizes older turns when threshold reached)
	// compactor = &compact.WithLogging{
	// 	Inner: &compact.Summarize{
	// 		Provider:   prov,
	// 		Threshold:  8,
	// 		KeepRecent: 4,
	// 	},
	// 	LogPath: "compactions.log",
	// }

	// Instantiate the root interactive agent with complete tool registry and interactive confirmation
	rootAgent = agent.New(agent.Agent{
		Name:      "root",
		Provider:  prov,
		Tools:     tool.DefaultRegistry,
		Compactor: compactor,
		MaxTurns:  20,
		Verbose:   true,
	})

	// Define the execution runner invoked by the TUI when the user submits a prompt
	runner := func(ctx context.Context, input string) error {
		if handleCommand(input) {
			return nil
		}
		_, err := rootAgent.Run(ctx, input)
		return err
	}

	// Initialize the full TUI with integrated banner and startup light sweep animation
	app, err := ui.NewProgram(runner, prov.Model())
	if err != nil {
		fmt.Printf("failed to initialize TUI: %v\n", err)
		return
	}

	// Connect interactive tool approval hook and start the Bubble Tea event loop
	rootAgent.Confirm = app.Confirm
	if err := app.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
	}
}
