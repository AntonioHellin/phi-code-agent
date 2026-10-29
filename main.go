package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

const systemPrompt = `You are a coding assistant running in a terminal. You have three tools:
bash, read_file, write_file. Be concise.`

// tools defines the JSON schema catalog of available tools sent to the model.
var tools = []ToolDef{
	{
		Name:        "bash",
		Description: "Run a shell command and return its combined stdout/stderr.",
		InputSchema: map[string]any{
			"command": map[string]any{"type": "string", "description": "The command to run."},
		},
		Required: []string{"command"},
	},
	{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: map[string]any{
			"path": map[string]any{"type": "string", "description": "Filesystem path to read."},
		},
		Required: []string{"path"},
	},
	{
		Name:        "write_file",
		Description: "Write content to a file (creating or overwriting it).",
		InputSchema: map[string]any{
			"path":    map[string]any{"type": "string", "description": "Filesystem path to write."},
			"content": map[string]any{"type": "string", "description": "The bytes to write."},
		},
		Required: []string{"path", "content"},
	},
}

var scanner = func() *bufio.Scanner {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}()

func main() {
	ctx := context.Background()
	client := anthropic.NewClient()
	provider := NewAnthropicProvider(
		&client,
		string(anthropic.ModelClaudeHaiku5_5),
		8192,
		systemPrompt,
	)

	var messages []Message

	fmt.Println("Phi Agent Code")
	fmt.Println("Type your prompt or press Ctrl+C to exit.")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			return // EOF or Ctrl+D
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		messages = append(messages, Message{
			Role:    RoleUser,
			Content: []Block{{Type: BlockText, Text: input}},
		})
		messages = agentLoop(ctx, provider, messages)
	}
}

// agentLoop is the vendor-agnostic inner agent loop.
// It interacts with the LLM solely through the Provider interface abstraction (DIP).
// Calls the model, executes any requested tools, and loops until
// the model returns plain text (end_turn).
func agentLoop(ctx context.Context, provider Provider, messages []Message) []Message {
	for {
		log.Printf("[agent] calling provider.Send | len(messages)=%d", len(messages))
		resp, err := provider.Send(ctx, messages, tools)
		if err != nil {
			fmt.Printf("api error: %v\n", err)
			return messages
		}

		log.Printf("[agent] received response | stop_reason=%s", resp.StopReason)

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
				log.Printf("[tool] executed %s (is_error=%v)", block.ToolName, isErr)
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
	fmt.Printf("[permission required] %s %s\n", name, rawInput)
	if !confirm("Allow execution?") {
		return "Permission denied by user", true
	}
	fmt.Printf("[tool execute] %s\n", name)
	switch name {
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(rawInput), &in); err != nil {
			return err.Error(), true
		}

		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			if shPath, err := exec.LookPath("sh"); err == nil {
				cmd = exec.Command(shPath, "-c", in.Command)
			} else if _, err := os.Stat(`C:\Program Files\Git\bin\sh.exe`); err == nil {
				cmd = exec.Command(`C:\Program Files\Git\bin\sh.exe`, "-c", in.Command)
			} else {
				cmd = exec.Command("cmd", "/c", in.Command)
			}
		} else {
			cmd = exec.Command("sh", "-c", in.Command)
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("%s\n[exit error: %v]", out, err), true
		}
		return string(out), false

	case "read_file":
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(rawInput), &in); err != nil {
			return err.Error(), true
		}
		data, err := os.ReadFile(in.Path)
		if err != nil {
			return err.Error(), true
		}
		return string(data), false

	case "write_file":
		var in struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(rawInput), &in); err != nil {
			return err.Error(), true
		}
		if err := os.WriteFile(in.Path, []byte(in.Content), 0644); err != nil {
			return err.Error(), true
		}
		return "wrote " + in.Path, false

	default:
		return fmt.Sprintf("unknown tool: %s", name), true
	}
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	if !scanner.Scan() {
		return false
	}
	line := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return line == "y" || line == "yes"
}
