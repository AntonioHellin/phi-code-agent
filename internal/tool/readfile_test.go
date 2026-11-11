package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileToolExecution verifies reading existing files, handling missing files, and rejecting malformed JSON.
func TestReadFileToolExecution(t *testing.T) {
	tInstance, exists := DefaultRegistry.Get("read_file")
	if !exists {
		t.Fatalf("read_file tool not registered in DefaultRegistry")
	}

	ctx := context.Background()

	// Test reading non-existent file returns error flag without panicking
	nonExistentPath := filepath.Join(t.TempDir(), "missing.txt")
	result, isError := tInstance.Execute(ctx, `{"path":"`+strings.ReplaceAll(nonExistentPath, `\`, `\\`)+`"}`)
	if !isError {
		t.Fatalf("expected isError=true for non-existent file, got false")
	}
	if result == "" {
		t.Fatalf("expected non-empty error message for non-existent file")
	}

	// Test reading an existing file returns its content
	testDir := t.TempDir()
	filePath := filepath.Join(testDir, "test.txt")
	expectedContent := "hello tool registry"
	if err := os.WriteFile(filePath, []byte(expectedContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	validResult, validErr := tInstance.Execute(ctx, `{"path":"`+strings.ReplaceAll(filePath, `\`, `\\`)+`"}`)
	if validErr {
		t.Fatalf("expected valid file read to succeed, got error: %s", validResult)
	}
	if validResult != expectedContent {
		t.Errorf("expected file content '%s', got '%s'", expectedContent, validResult)
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
