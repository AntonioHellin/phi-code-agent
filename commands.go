package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
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
	commands["exit"] = command{description: "exit the harness", run: cmdExit}
	commands["quit"] = command{description: "exit the harness (alias to /exit)", run: cmdExit}
	commands["compact"] = command{description: "compact conversation history", run: cmdCompact}
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

// knownModels lists model suggestions shown when /model is called with no arguments.
var knownModels = []string{
	"claude-opus-4-7",
	"claude-opus-4-6",
	"claude-sonnet-4-6",
	"claude-haiku-4-5",
	"claude-haiku-5-5",
}

// cmdModel displays the active model or dynamically updates it.
func cmdModel(args string) {
	if args == "" {
		fmt.Printf("current: %s\n", provider.Model())
		fmt.Println("suggestions:")
		for _, model := range knownModels {
			fmt.Printf("  %s\n", model)
		}
		return
	}
	provider.SetModel(args)
	fmt.Printf("model: %s\n", args)
}

// cmdClear resets the in-memory conversation history without reallocating backing memory.
func cmdClear(_ string) {
	messages = messages[:0]
	fmt.Println("conversation cleared")
}

// cmdTools lists all tools available to the model and their descriptions.
func cmdTools(_ string) {
	definitions := defaultToolRegistry.Definitions()
	fmt.Printf("Registered tools (%d):\n", len(definitions))
	for _, tool := range definitions {
		fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}
}

// cmdExit cleanly terminates the agent harness process.
func cmdExit(_ string) {
	os.Exit(0)
}

// cmdCompact triggers compaction manually and displays the before/after message count.
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
