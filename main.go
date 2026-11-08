package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

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

// scanner provides buffered standard input reading for interactive confirmation prompts.
var scanner = func() *bufio.Scanner {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}()

// Global application references shared across command handlers and delegation tools.
var (
	prov      provider.Provider
	rootAgent *agent.Agent
	compactor compact.CompactionStrategy = compact.NoCompaction{}
)

func main() {
	// =========================================================================
	// 1. Composition Root: Dependency Wiring & Initialization
	// =========================================================================
	ctx := context.Background()

	// Initialize concrete Anthropic LLM provider adapter
	anthropicClient := anthropic.NewClient()
	prov = provider.NewAnthropicProvider(
		&anthropicClient,
		string(anthropic.ModelClaudeHaiku5_5),
		8192,
		systemPrompt,
	)

	// Display CLI startup banner with active model and current working directory
	ui.PrintBanner(prov.Model())

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
		Confirm:   confirm,
	})

	// Initialize terminal raw-mode readline reader with history and multiline prompt support
	inputReader, err := ui.NewInputReader()
	if err != nil {
		fmt.Printf("failed to initialize input: %v\n", err)
		return
	}
	defer inputReader.Close()

	// =========================================================================
	// 2. Outer Loop: Interactive REPL (Read-Eval-Print Loop)
	// =========================================================================
	for {
		userInput, err := inputReader.ReadPrompt()
		if err != nil {
			return // Graceful exit on EOF (Ctrl+D) or terminal IO error
		}
		if userInput == "" {
			continue // Skip empty input
		}

		// Intercept and execute local slash commands (e.g. /help, /tools, /clear, /subagents)
		if handleCommand(userInput) {
			continue
		}

		// Dispatch user instruction to the root agent's inner ReAct execution loop
		_, err = rootAgent.Run(ctx, userInput)
		if err != nil {
			fmt.Printf("agent error: %v\n", err)
		}
	}
}

// confirm prompts the user for interactive terminal authorization before executing sensitive tools.
func confirm(prompt string) bool {
	fmt.Printf("%s %s[y/N]%s ", prompt, ui.ColorGray, ui.ColorReset)
	if !scanner.Scan() {
		return false
	}
	line := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return line == "y" || line == "yes"
}
