package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"phi-agent-code/internal/provider"
	"phi-agent-code/internal/tool"
)

// command defines the metadata and execution handler for a terminal slash command.
type command struct {
	description string
	usage       string
	run         func(args string)
}

// commands maps command keywords to their definitions.
var commands = map[string]command{}

func init() {
	commands["help"] = command{description: "show available commands", run: cmdHelp}
	commands["model"] = command{description: "show or change the model", run: cmdModel}
	commands["clear"] = command{description: "clear conversation history", run: cmdClear}
	commands["tools"] = command{description: "list available tools", run: cmdTools}
	commands["subagents"] = command{description: "list available subagent profiles", run: cmdSubagents}
	commands["compact"] = command{description: "compact conversation history", run: cmdCompact}
	commands["exit"] = command{description: "exit the harness", run: cmdExit}
	commands["quit"] = command{description: "exit the harness (alias to /exit)", run: cmdExit}
}

// handleCommand intercepts input starting with '/'.
// Returns true if the line was handled locally, bypassing the LLM agent loop.
func handleCommand(line string) bool {
	if !strings.HasPrefix(line, "/") {
		return false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, "/"))
	if raw == "" {
		return true
	}
	parts := strings.SplitN(raw, " ", 2)
	name := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	cmd, ok := commands[name] // Idiomatic Go comma-ok idiom
	if !ok {
		fmt.Printf("unknown command: /%s (try /help)\n", name)
		return true
	}
	cmd.run(args)
	return true
}

// cmdHelp lists all registered slash commands in alphabetical order with aligned columns.
func cmdHelp(_ string) {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd := commands[name]
		display := "/" + name
		if cmd.usage != "" {
			display = cmd.usage
		}
		fmt.Printf(" %-22s %s\n", display, cmd.description)
	}
}

// knownModels maps each backend key to current recommended model suggestions shown by /model.
var knownModels = map[string][]string{
	"anthropic": {
		string(anthropic.ModelClaudeOpus5_5),
		string(anthropic.ModelClaudeSonnet5_5),
		string(anthropic.ModelClaudeHaiku5_5),
	},
	"gemini": {
		"gemini-4-hydrogen",
		"gemini-4-carbon",
		"gemini-4-argon",
		"gemini-3.8-flash",
	},
	"ollama": {
		"qwen3-coder",
		"llama3.3",
		"llama3.2",
		"deepseek-r1",
	},
}

// providerKey identifies the active concrete adapter so /model suggests only compatible models.
func providerKey(p provider.Provider) string {
	switch p.(type) {
	case *provider.AnthropicProvider:
		return "anthropic"
	case *provider.GeminiProvider:
		return "gemini"
	case *provider.OllamaProvider:
		return "ollama"
	case *provider.MockProvider:
		return "mock"
	default:
		return ""
	}
}

// cmdModel displays the active model plus backend-relevant suggestions, or dynamically updates it.
func cmdModel(args string) {
	if args == "" {
		fmt.Printf("current: %s\n", prov.Model())
		fmt.Println("suggestions:")
		if key := providerKey(prov); key != "" {
			if models := knownModels[key]; len(models) > 0 {
				for _, model := range models {
					fmt.Printf("  %s\n", model)
				}
				return
			}
			fmt.Println("  (any custom identifier for mock)")
			return
		}
		for _, backend := range []string{"gemini", "anthropic", "ollama"} {
			fmt.Printf("  [%s]\n", backend)
			for _, model := range knownModels[backend] {
				fmt.Printf("    %s\n", model)
			}
		}
		return
	}
	prov.SetModel(args)
	fmt.Printf("model: %s\n", args)
}

// cmdClear resets the in-memory conversation history without reallocating backing memory.
func cmdClear(_ string) {
	if rootAgent != nil {
		rootAgent.Clear()
	}
	fmt.Println("conversation cleared")
}

// cmdTools lists all tools available to the model and their descriptions.
func cmdTools(_ string) {
	definitions := tool.DefaultRegistry.Definitions()
	fmt.Printf("Registered tools (%d):\n", len(definitions))
	for _, t := range definitions {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}
}

// cmdSubagents lists all available subagent profiles and their capabilities.
func cmdSubagents(_ string) {
	fmt.Println("Available subagent profiles (1):")
	fmt.Println("  - researcher: Read-only codebase explorer. Inspects files without write/bash access.")
}

// cmdExit cleanly terminates the agent harness process.
func cmdExit(_ string) {
	os.Exit(0)
}

// cmdCompact triggers compaction manually and displays the before/after message count.
func cmdCompact(_ string) {
	if rootAgent == nil || len(rootAgent.Messages()) == 0 {
		fmt.Println("no messages to compact")
		return
	}
	currentMessages := rootAgent.Messages()
	before := len(currentMessages)
	compacted, err := compactor.Compact(context.Background(), currentMessages)
	if err != nil {
		fmt.Printf("compact error: %v\n", err)
		return
	}
	fmt.Printf("compacted: %d -> %d messages\n", before, len(compacted))
}
