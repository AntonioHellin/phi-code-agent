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

	// Definimos el runner que la TUI llamará cuando el usuario pulse Enter
	runner := func(ctx context.Context, input string) error {
		if handleCommand(input) {
			return nil
		}
		_, err := rootAgent.Run(ctx, input)
		return err
	}

	// Inicializamos la TUI (pipe y escáner quedan encapsulados dentro)
	app, err := ui.NewProgram(runner)
	if err != nil {
		fmt.Printf("failed to initialize TUI: %v\n", err)
		return
	}

	// Display CLI startup banner with active model and current working directory
	ui.PrintBanner(prov.Model())

	// Conectamos la confirmación interactiva y arrancamos
	rootAgent.Confirm = app.Confirm
	if err := app.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
	}
}
