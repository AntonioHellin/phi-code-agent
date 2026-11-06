package tool

import (
	"context"
	"encoding/json"
	"os"

	"phi-agent-code/internal/api"
)

// ReadFileTool implements the Tool interface for reading file contents from the local filesystem.
type ReadFileTool struct{}

func init() {
	DefaultRegistry.Register(&ReadFileTool{})
}

// Definition returns the JSON Schema declaration for the read_file tool.
func (ReadFileTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "read_file",
		Description: "Read the contents of a file at the given path.",
		InputSchema: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Filesystem path to read.",
			},
		},
		Required: []string{"path"},
	}
}

// Execute reads the specified file from disk.
// Returns the file content on success, or the error message with isError=true on failure.
func (ReadFileTool) Execute(_ context.Context, rawInput string) (string, bool) {
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
}

