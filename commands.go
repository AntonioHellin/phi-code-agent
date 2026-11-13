package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

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
	commands["model"] = command{description: "show active model or switch to <name>", run: cmdModel}
	commands["models"] = command{description: "browse and select a model interactively", run: cmdModels}
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

// discoverModels queries the active provider on demand when it implements provider.ModelLister.
func discoverModels(p provider.Provider) ([]string, bool, error) {
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil, false, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := lister.ListModels(ctx)
	return models, true, err
}

// staticSuggestions returns curated fallback models for the active provider when available.
func staticSuggestions(p provider.Provider) []string {
	if key := providerKey(p); key != "" {
		return knownModels[key]
	}
	return nil
}

// printStaticSuggestions displays curated fallback model suggestions scoped to the active provider.
func printStaticSuggestions(p provider.Provider) {
	if key := providerKey(p); key != "" {
		if models := knownModels[key]; len(models) > 0 {
			for _, model := range models {
				fmt.Printf("  %s\n", model)
			}
			return
		}
		fmt.Println("  (no fallback models registered for active provider)")
		return
	}
	for _, backend := range []string{"gemini", "anthropic", "ollama"} {
		fmt.Printf("  [%s]\n", backend)
		for _, model := range knownModels[backend] {
			fmt.Printf("    %s\n", model)
		}
	}
}

type modelCatalog struct {
	models    []string
	fromLive  bool
	supported bool
	err       error
}

// resolveModels returns the active provider's live generative models, falling back to curated suggestions.
func resolveModels(p provider.Provider) modelCatalog {
	models, supported, err := discoverModels(p)
	if supported && err == nil && len(models) > 0 {
		return modelCatalog{
			models:    models,
			fromLive:  true,
			supported: true,
		}
	}
	return modelCatalog{
		models:    staticSuggestions(p),
		fromLive:  false,
		supported: supported,
		err:       err,
	}
}

// ownerProvider checks curated backend catalogs to identify which provider owns candidate, if any.
func ownerProvider(candidate string) string {
	for backend, list := range knownModels {
		if slices.Contains(list, candidate) {
			return backend
		}
	}
	return ""
}

// validateModel verifies candidate against the active provider's live catalog (or curated fallback).
func validateModel(p provider.Provider, candidate string) error {
	if p == nil {
		return fmt.Errorf("error: no active provider configured")
	}
	cat := resolveModels(p)
	if slices.Contains(cat.models, candidate) {
		return nil
	}

	activeKey := providerKey(p)
	if activeKey == "" {
		activeKey = "unknown"
	}
	if owner := ownerProvider(candidate); owner != "" && owner != activeKey {
		return fmt.Errorf("error: model %q belongs to provider %q, not active provider %q (use /models to browse)", candidate, owner, activeKey)
	}
	if len(cat.models) == 0 {
		return fmt.Errorf("error: unknown model %q — not listed in any provider catalog (use /models to browse)", candidate)
	}
	if !cat.fromLive && cat.err != nil {
		return fmt.Errorf("error: unknown model %q for active provider %q (live lookup failed: %v; use /models to see curated models)", candidate, activeKey, cat.err)
	}
	return fmt.Errorf("error: unknown model %q for active provider %q (use /models to see available models)", candidate, activeKey)
}

// applyModel switches the active provider model, syncs the TUI banner, and confirms in stdout.
func applyModel(model string) {
	prov.SetModel(model)
	if onModelSet != nil {
		onModelSet(model)
	}
	fmt.Printf("model: %s\n", model)
}

// cmdModels queries live models supported by the active provider and opens the interactive 5-row picker.
func cmdModels(_ string) {
	cat := resolveModels(prov)
	if !cat.fromLive {
		switch {
		case !cat.supported:
			fmt.Println("active provider does not support live model discovery; curated suggestions:")
		case cat.err != nil:
			fmt.Printf("live model discovery failed: %v\ncurated suggestions:\n", cat.err)
		default:
			fmt.Println("no generative models returned by provider; curated suggestions:")
		}
		if len(cat.models) == 0 {
			printStaticSuggestions(prov)
			return
		}
	}

	if selectModel != nil {
		chosen, ok := selectModel("Select model", cat.models, prov.Model())
		if !ok {
			fmt.Println("model selection canceled")
			return
		}
		applyModel(chosen)
		return
	}

	fmt.Printf("available models (%d):\n", len(cat.models))
	for _, model := range cat.models {
		fmt.Printf("  %s\n", model)
	}
}

// cmdModel displays the active model when invoked without arguments, or validates and switches to the named model.
func cmdModel(args string) {
	name := strings.TrimSpace(args)
	if name == "" {
		fmt.Printf("current: %s\n", prov.Model())
		fmt.Println("use /models to browse or /model <name> to switch")
		return
	}
	if err := validateModel(prov, name); err != nil {
		fmt.Println(err)
		return
	}
	applyModel(name)
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
