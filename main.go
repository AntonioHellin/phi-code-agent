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
var tools = []anthropic.ToolUnionParam{
	{OfTool: &anthropic.ToolParam{
		Name:        "bash",
		Description: anthropic.String("Run a shell command and return its combined stdout/stderr."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"command": map[string]any{"type": "string", "description": "The command to run."},
			},
			Required: []string{"command"},
		},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "read_file",
		Description: anthropic.String("Read the contents of a file at the given path."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"path": map[string]any{"type": "string", "description": "Filesystem path to read."},
			},
			Required: []string{"path"},
		},
	}},
	{OfTool: &anthropic.ToolParam{
		Name:        "write_file",
		Description: anthropic.String("Write content to a file (creating or overwriting it)."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"path":    map[string]any{"type": "string", "description": "Filesystem path to write."},
				"content": map[string]any{"type": "string", "description": "The bytes to write."},
			},
			Required: []string{"path", "content"},
		},
	}},
}

var client = anthropic.NewClient()
var scanner = func() *bufio.Scanner {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}()

func main() {
	ctx := context.Background()
	var messages []anthropic.MessageParam

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
		messages = append(messages, anthropic.NewUserMessage(anthropic.NewTextBlock(input)))
		messages = agentLoop(ctx, messages)
	}
}

// agentLoop is the inner agent loop.
// Calls the model, executes any requested tools, and loops until
// the model returns plain text (end_turn).
func agentLoop(ctx context.Context, messages []anthropic.MessageParam) []anthropic.MessageParam {
	for {
		log.Printf("[agent] calling client.Messages.New | len(messages)=%d", len(messages))
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeOpus4_7,
			MaxTokens: 8192,
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
			Messages:  messages,
			Tools:     tools,
		})
		if err != nil {
			fmt.Printf("api error: %v\n", err)
			return messages
		}

		log.Printf("[agent] received response | stop_reason=%s", resp.StopReason)

		// Record assistant response in conversation history
		messages = append(messages, resp.ToParam())

		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			switch v := block.AsAny().(type) {
			case anthropic.TextBlock:
				fmt.Println(v.Text)
			case anthropic.ToolUseBlock:
				result, isErr := executeTool(v.Name, v.JSON.Input.Raw())
				log.Printf("[tool] executed %s (is_error=%v)", v.Name, isErr)
				toolResults = append(toolResults, anthropic.NewToolResultBlock(v.ID, result, isErr))
			}
		}

		// Exit when model finishes turn without requesting tools
		if resp.StopReason != anthropic.StopReasonToolUse {
			return messages
		}

		// Append tool results and loop
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
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
