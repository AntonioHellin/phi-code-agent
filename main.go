package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"phi-agent-code/internal/api"
	"phi-agent-code/internal/compact"
	"phi-agent-code/internal/provider"
	"phi-agent-code/internal/tool"
	"phi-agent-code/internal/ui"
)

const systemPrompt = `You are a coding assistant running in a terminal. You have three tools:
bash, read_file, write_file. Be concise.`

var scanner = func() *bufio.Scanner {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}()

var (
	prov      provider.Provider
	messages  []api.Message
	compactor compact.CompactionStrategy = compact.NoCompaction{}
)

func main() {
	ctx := context.Background()
	client := anthropic.NewClient()
	prov = provider.NewAnthropicProvider(
		&client,
		string(anthropic.ModelClaudeHaiku5_5),
		8192,
		systemPrompt,
	)

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

	inputReader, err := ui.NewInputReader()
	if err != nil {
		fmt.Printf("failed to initialize input: %v\n", err)
		return
	}
	defer inputReader.Close()

	for {
		input, err := inputReader.ReadPrompt()
		if err != nil {
			return // EOF (Ctrl+D) or read error
		}
		if input == "" {
			continue
		}
		if handleCommand(input) {
			continue
		}
		messages = append(messages, api.Message{
			Role:    api.RoleUser,
			Content: []api.Block{{Type: api.BlockText, Text: input}},
		})

		compacted, err := compactor.Compact(ctx, messages)
		if err != nil {
			fmt.Printf("compaction error: %v\n", err)
		} else {
			messages = compacted
		}

		messages = agentLoop(ctx, prov, messages)
	}
}

// agentLoop is the vendor-agnostic inner agent loop.
// It interacts with the LLM solely through the Provider interface abstraction (DIP).
// Calls the model, executes any requested tools, and loops until
// the model returns plain text (end_turn).
func agentLoop(ctx context.Context, p provider.Provider, messages []api.Message) []api.Message {
	for {
		sp := ui.StartSpinner("thinking...")
		resp, err := p.Send(ctx, messages, tool.DefaultRegistry.Definitions())
		sp.Stop()
		if err != nil {
			fmt.Printf("api error: %v\n", err)
			return messages
		}

		// Record assistant response in conversation history
		messages = append(messages, api.Message{
			Role:    api.RoleAssistant,
			Content: resp.Content,
		})

		var toolResults []api.Block
		for _, block := range resp.Content {
			switch block.Type {
			case api.BlockText:
				fmt.Println(block.Text)
			case api.BlockToolUse:
				result, isErr := executeTool(block.ToolName, block.ToolInput)

				toolResults = append(toolResults, api.Block{
					Type:       api.BlockToolResult,
					ToolUseID:  block.ToolUseID,
					ToolResult: result,
					IsError:    isErr,
				})
			}
		}

		// Exit when model finishes turn without requesting tools
		if resp.StopReason != "tool_use" {
			return messages
		}

		// Append tool results and loop
		messages = append(messages, api.Message{
			Role:    api.RoleUser,
			Content: toolResults,
		})
	}
}

// executeTool dispatches requested tool calls.
// Errors are returned as tool results (string, isError=true) so the model
// can observe the failure and attempt self-correction.
func executeTool(name, rawInput string) (string, bool) {
	fmt.Printf("\n%s%sPermission Required:%s %s%s%s %s\n", ui.ColorBold, ui.ColorYellow, ui.ColorReset, ui.ColorBold, name, ui.ColorReset, rawInput)
	if !confirm(fmt.Sprintf("%sAllow execution?%s", ui.ColorBold, ui.ColorReset)) {
		fmt.Printf("%sDenied by user%s\n\n", ui.ColorRed, ui.ColorReset)
		return "Permission denied by user", true
	}
	fmt.Printf("%s▶ Executing %s...%s\n", ui.ColorCyan, name, ui.ColorReset)
	t, exists := tool.DefaultRegistry.Get(name)
	if !exists {
		return fmt.Sprintf("unknown tool: %s", name), true
	}
	return t.Execute(context.Background(), rawInput)
}

func confirm(prompt string) bool {
	fmt.Printf("%s %s[y/N]%s ", prompt, ui.ColorGray, ui.ColorReset)
	if !scanner.Scan() {
		return false
	}
	line := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return line == "y" || line == "yes"
}
