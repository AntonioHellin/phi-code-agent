package tool

import (
	"context"
	"encoding/json"
	"os"

	"phi-agent-code/internal/api"
)

// WriteFileTool implements the Tool interface for writing content to files on the local filesystem.
type WriteFileTool struct{}

func init() {
	DefaultRegistry.RegisterWithRetry(&WriteFileTool{}, 2)
}

// Definition returns the JSON Schema declaration for the write_file tool.
func (WriteFileTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "write_file",
		Description: "Write content to a file (creating or overwriting it).",
		InputSchema: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Filesystem path to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The bytes to write.",
			},
		},
		Required: []string{"path", "content"},
	}
}

// Execute writes the given content to the target path with 0644 permissions.
// Returns a confirmation message on success, or the error with isError=true on failure.
func (WriteFileTool) Execute(_ context.Context, rawInput string) (string, bool) {
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
}
