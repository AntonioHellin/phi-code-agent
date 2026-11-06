package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const systemPrompt = `You are a coding assistant running in a terminal. You have three tools:
bash, read_file, write_file. Be concise.`

var scanner = func() *bufio.Scanner {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}()

var (
	provider  Provider
	messages  []Message
	compactor CompactionStrategy = NoCompaction{}
)

func main() {
	ctx := context.Background()
	client := anthropic.NewClient()
	provider = NewAnthropicProvider(
		&client,
		string(anthropic.ModelClaudeHaiku5_5),
		8192,
		systemPrompt,
	)

	printBanner(provider)

	// -------------------------------------------------------------------------
	// Compaction Strategy Configuration (Composition Root)
	// Uncomment the strategy you wish to evaluate:
	// -------------------------------------------------------------------------

	// Option 1: No compaction (Default pass-through: retains all history indefinitely)
	// compactor = NoCompaction{}

	// Option 2: Sliding Window without logging (retains last K turns, e.g. 6 messages)
	// compactor = &SlidingWindow{KeepLast: 6}

	// Option 3: Sliding Window WITH Logging (ACTIVE - logs audit trail to compactions.log)
	compactor = &WithLogging{
		Inner:   &SlidingWindow{KeepLast: 6},
		LogPath: "compactions.log",
	}

	// Option 4: LLM-driven Summarization with Logging (summarizes older turns when threshold reached)
	// compactor = &WithLogging{
	// 	Inner: &Summarize{
	// 		Provider:   provider,
	// 		Threshold:  8,
	// 		KeepRecent: 4,
	// 	},
	// 	LogPath: "compactions.log",
	// }

	inputReader, err := NewInputReader()
	if err != nil {
		fmt.Printf("failed to initialize input: %v\n", err)
		return
	}
	defer inputReader.Close()

	for {
		input, err := inputReader.ReadPrompt()
		if err != nil {
			return // EOF (Ctrl+D) o error de lectura
		}
		if input == "" {
			continue
		}
		if handleCommand(input) {
			continue
		}
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []Block{{Type: BlockText, Text: input}},
		})

		compacted, err := compactor.Compact(ctx, messages)
		if err != nil {
			fmt.Printf("compaction error: %v\n", err)
		} else {
			messages = compacted
		}

		messages = agentLoop(ctx, provider, messages)
	}
}

// agentLoop is the vendor-agnostic inner agent loop.
// It interacts with the LLM solely through the Provider interface abstraction (DIP).
// Calls the model, executes any requested tools, and loops until
// the model returns plain text (end_turn).
func agentLoop(ctx context.Context, provider Provider, messages []Message) []Message {
	for {
		sp := startSpinner("thinking...")
		resp, err := provider.Send(ctx, messages, defaultToolRegistry.Definitions())
		sp.Stop()
		if err != nil {
			fmt.Printf("api error: %v\n", err)
			return messages
		}

		// Record assistant response in conversation history
		messages = append(messages, Message{
			Role:    RoleAssistant,
			Content: resp.Content,
		})

		var toolResults []Block
		for _, block := range resp.Content {
			switch block.Type {
			case BlockText:
				fmt.Println(block.Text)
			case BlockToolUse:
				result, isErr := executeTool(block.ToolName, block.ToolInput)

				toolResults = append(toolResults, Block{
					Type:       BlockToolResult,
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
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: toolResults,
		})
	}
}

// executeTool dispatches requested tool calls.
// Errors are returned as tool results (string, isError=true) so the model
// can observe the failure and attempt self-correction.
func executeTool(name, rawInput string) (string, bool) {
	fmt.Printf("\n%s%sPermission Required:%s %s%s%s %s\n", colorBold, colorYellow, colorReset, colorBold, name, colorReset, rawInput)
	if !confirm(fmt.Sprintf("%sAllow execution?%s", colorBold, colorReset)) {
		fmt.Printf("%sDenied by user%s\n\n", colorRed, colorReset)
		return "Permission denied by user", true
	}
	fmt.Printf("%s▶ Executing %s...%s\n", colorCyan, name, colorReset)
	tool, exists := defaultToolRegistry.Get(name)
	if !exists {
    	return fmt.Sprintf("unknown tool: %s", name), true
	}
	return tool.Execute(context.Background(), rawInput)
}

func confirm(prompt string) bool {
	fmt.Printf("%s %s[y/N]%s ", prompt, colorGray, colorReset)
	if !scanner.Scan() {
		return false
	}
	line := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return line == "y" || line == "yes"
}
