package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFileToolExecution verifies writing file contents to disk and rejecting malformed JSON.
func TestWriteFileToolExecution(t *testing.T) {
	tInstance, exists := DefaultRegistry.Get("write_file")
	if !exists {
		t.Fatalf("write_file tool not registered in DefaultRegistry")
	}

	ctx := context.Background()
	testDir := t.TempDir()
	filePath := filepath.Join(testDir, "output.txt")
	contentToWrite := "writing from tool test"

	escapedPath := strings.ReplaceAll(filePath, `\`, `\\`)
	payload := `{"path":"` + escapedPath + `","content":"` + contentToWrite + `"}`

	// Test successful file write
	result, isError := tInstance.Execute(ctx, payload)
	if isError {
		t.Fatalf("expected write_file to succeed, got error: %s", result)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(data) != contentToWrite {
		t.Errorf("expected written content '%s', got '%s'", contentToWrite, string(data))
	}

	// Test malformed JSON payload returns error gracefully
	malformedResult, malformedErr := tInstance.Execute(ctx, `{"path":`)
	if !malformedErr {
		t.Fatalf("expected malformed JSON to return error flag, got false")
	}
	if malformedResult == "" {
		t.Errorf("expected non-empty error message for malformed JSON")
	}
}
