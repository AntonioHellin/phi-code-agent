package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"phi-agent-code/internal/api"
)

// BashTool implements the Tool interface for executing shell commands on the host OS.
// It handles platform-specific shell resolution across Windows (sh/Git bash/cmd) and POSIX systems.
type BashTool struct{}

func init() {
	DefaultRegistry.RegisterWithRetry(&BashTool{}, 2)
}

// Definition returns the JSON Schema metadata describing the bash tool to the LLM provider.
func (BashTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "bash",
		Description: "Run a shell command and return its combined stdout/stderr.",
		InputSchema: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to run.",
			},
		},
		Required: []string{"command"},
	}
}

// Execute executes the specified shell command with context-aware process management.
// If the context is canceled or times out, the child process is terminated immediately.
// Returns combined stdout/stderr on success, or the output along with exit error details and isError=true on failure.
func (BashTool) Execute(ctx context.Context, rawInput string) (string, bool) {
	var in struct {
		Command string `json:"command"`
	}

	if err := json.Unmarshal([]byte(rawInput), &in); err != nil {
		return err.Error(), true
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if shPath, err := exec.LookPath("sh"); err == nil {
			cmd = exec.CommandContext(ctx, shPath, "-c", in.Command)
		} else if _, err := os.Stat(`C:\Program Files\Git\bin\sh.exe`); err == nil {
			cmd = exec.CommandContext(ctx, `C:\Program Files\Git\bin\sh.exe`, "-c", in.Command)
		} else {
			cmd = exec.CommandContext(ctx, "cmd", "/c", in.Command)
		}
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", in.Command)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s\n[exit error: %v]", out, err), true
	}
	return string(out), false
}
