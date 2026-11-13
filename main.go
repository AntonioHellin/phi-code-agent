package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"

	"phi-agent-code/internal/agent"
	"phi-agent-code/internal/compact"
	"phi-agent-code/internal/permission"
	"phi-agent-code/internal/provider"
	"phi-agent-code/internal/tool"
	"phi-agent-code/internal/ui"
)

// systemPrompt establishes the core behavioral persona and available toolset for the agent.
const systemPrompt = `You are a coding assistant running in a terminal. You have tools:
bash, read_file, write_file, delegate. Be concise.`

// Global application references shared across command handlers and delegation tools.
var (
	prov        provider.Provider
	rootAgent   *agent.Agent
	compactor   compact.CompactionStrategy = compact.NoCompaction{}
	selectModel func(title string, options []string, current string) (string, bool)
	onModelSet  func(model string)
)

// buildProvider acts as the Composition Root factory, selecting and wiring a concrete
// provider.Provider backend from CLI flags and environment configuration.
func buildProvider(name, modelOverride, system string) (provider.Provider, error) {
	model := strings.TrimSpace(modelOverride)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		if strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required when using --provider anthropic")
		}
		if model == "" {
			model = strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL"))
		}
		if model == "" {
			model = string(anthropic.ModelClaudeHaiku5_5)
		}
		anthropicClient := anthropic.NewClient()
		return provider.NewAnthropicProvider(&anthropicClient, model, 8192, system), nil

	case "ollama":
		if model == "" {
			model = strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
		}
		if model == "" {
			model = "llama3.2"
		}
		return provider.NewOllamaProvider(os.Getenv("OLLAMA_BASE_URL"), model, system), nil

	case "gemini":
		apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
		if apiKey == "" {
			apiKey = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
		}
		if apiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY is required when using --provider gemini")
		}
		if model == "" {
			model = strings.TrimSpace(os.Getenv("GEMINI_MODEL"))
		}
		if model == "" {
			model = "gemini-4-hydrogen"
		}
		client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return nil, fmt.Errorf("create gemini client: %w", err)
		}
		return provider.NewGeminiProvider(client, model, 0, system), nil

	case "mock":
		mock := &provider.MockProvider{
			AvailableModels: []string{"mock", "mock-fast", "mock-reasoning"},
		}
		if model != "" {
			mock.SetModel(model)
			found := false
			for _, candidate := range mock.AvailableModels {
				if candidate == model {
					found = true
					break
				}
			}
			if !found {
				mock.AvailableModels = append(mock.AvailableModels, model)
			}
		}
		return mock, nil

	default:
		return nil, fmt.Errorf("unknown provider %q (expected: anthropic, ollama, gemini, mock)", name)
	}
}

func main() {
	providerFlag := flag.String("provider", "gemini", "LLM provider backend: gemini (default), anthropic, ollama, mock")
	modelFlag := flag.String("model", "", "Override active model ID for the selected provider")
	flag.Parse()

	var err error
	prov, err = buildProvider(*providerFlag, *modelFlag, systemPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider initialization error: %v\n", err)
		os.Exit(1)
	}

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
		Policy:    permission.ReadOnlyPolicy{},
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

	// Connect interactive UI hooks and start the Bubble Tea event loop
	rootAgent.Confirm = app.Confirm
	selectModel = app.SelectModel
	onModelSet = app.SetModel
	if err := app.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
	}
}
