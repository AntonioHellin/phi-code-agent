package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"phi-agent-code/internal/api"
)

// WriteFileTool implements the Tool interface for writing content to files on the local filesystem.
type WriteFileTool struct{}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

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
	var parsedInput writeFileInput

	if err := json.Unmarshal([]byte(rawInput), &parsedInput); err != nil {
		return err.Error(), true
	}

	if err := os.WriteFile(parsedInput.Path, []byte(parsedInput.Content), 0644); err != nil {
		return err.Error(), true
	}

	return "wrote " + parsedInput.Path, false
}

// Preview builds a colored unified diff preview against disk state before user confirmation.
func (WriteFileTool) Preview(rawInput string) (string, string, bool) {
	var parsedInput writeFileInput
	if err := json.Unmarshal([]byte(rawInput), &parsedInput); err != nil {
		return "", "", false
	}
	if strings.TrimSpace(parsedInput.Path) == "" {
		return "", "", false
	}

	existingBytes, readErr := os.ReadFile(parsedInput.Path)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			plainDiff := UnifiedDiff("/dev/null", parsedInput.Path+" (new file)", "", parsedInput.Content)
			previewText := fmt.Sprintf("[write_file diff] %s (new file)", parsedInput.Path)
			if plainDiff != "" {
				previewText += "\n" + ColorizeDiff(plainDiff)
			}
			return previewText, fmt.Sprintf("Create %s?", parsedInput.Path), true
		}
		// Other I/O errors fall back to normal prompt so Execute surfaces the real filesystem error.
		return "", "", false
	}

	plainDiff := UnifiedDiff(parsedInput.Path+" (existing)", parsedInput.Path+" (proposed)", string(existingBytes), parsedInput.Content)
	previewText := fmt.Sprintf("[write_file diff] %s (no changes)", parsedInput.Path)
	if plainDiff != "" {
		previewText = fmt.Sprintf("[write_file diff] %s\n%s", parsedInput.Path, ColorizeDiff(plainDiff))
	}
	return previewText, fmt.Sprintf("Apply changes to %s?", parsedInput.Path), true
}
