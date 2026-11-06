package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockTool implements the Tool interface for isolated unit testing.
type mockTool struct {
	name        string
	description string
	output      string
	isError     bool
}

func (m *mockTool) Definition() ToolDef {
	return ToolDef{
		Name:        m.name,
		Description: m.description,
		InputSchema: map[string]any{
			"type": "object",
		},
	}
}

func (m *mockTool) Execute(_ context.Context, _ string) (string, bool) {
	return m.output, m.isError
}

func TestRegistryRegisterAndGet(t *testing.T) {
	registry := NewToolRegistry()

	mock := &mockTool{
		name:        "test_tool",
		description: "A test tool for registry testing",
		output:      "success",
		isError:     false,
	}

	registry.Register(mock)

	// Verify existing tool lookup
	retrievedTool, exists := registry.Get("test_tool")
	if !exists {
		t.Fatalf("expected tool 'test_tool' to be found in registry")
	}

	definition := retrievedTool.Definition()
	if definition.Name != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got '%s'", definition.Name)
	}
	if definition.Description != "A test tool for registry testing" {
		t.Errorf("expected tool description 'A test tool for registry testing', got '%s'", definition.Description)
	}

	// Verify non-existent tool lookup
	_, missingExists := registry.Get("non_existent_tool")
	if missingExists {
		t.Fatalf("expected non_existent_tool to not exist in registry")
	}
}

func TestRegistryDefinitions(t *testing.T) {
	registry := NewToolRegistry()

	toolA := &mockTool{name: "tool_alpha", description: "Alpha tool"}
	toolB := &mockTool{name: "tool_beta", description: "Beta tool"}

	registry.Register(toolA)
	registry.Register(toolB)

	definitions := registry.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("expected 2 tool definitions, got %d", len(definitions))
	}

	foundNames := make(map[string]bool)
	for _, def := range definitions {
		foundNames[def.Name] = true
	}

	if !foundNames["tool_alpha"] || !foundNames["tool_beta"] {
		t.Errorf("expected definitions to contain 'tool_alpha' and 'tool_beta', got %v", foundNames)
	}
}

func TestDefaultRegistryContainsNativeTools(t *testing.T) {
	expectedTools := []string{"bash", "read_file", "write_file"}

	for _, toolName := range expectedTools {
		tool, exists := defaultToolRegistry.Get(toolName)
		if !exists {
			t.Errorf("expected defaultToolRegistry to contain '%s' via init() self-registration", toolName)
			continue
		}

		definition := tool.Definition()
		if definition.Name != toolName {
			t.Errorf("expected tool name '%s', got '%s'", toolName, definition.Name)
		}
		if definition.Description == "" {
			t.Errorf("expected tool '%s' to have non-empty description", toolName)
		}
		if definition.InputSchema == nil {
			t.Errorf("expected tool '%s' to have non-empty InputSchema", toolName)
		}
	}
}

func TestReadFileToolExecution(t *testing.T) {
	tool, exists := defaultToolRegistry.Get("read_file")
	if !exists {
		t.Fatalf("read_file tool not registered in defaultToolRegistry")
	}

	ctx := context.Background()

	// 1. Test reading non-existent file returns error flag without panicking
	nonExistentPath := filepath.Join(t.TempDir(), "missing.txt")
	result, isError := tool.Execute(ctx, `{"path":"`+strings.ReplaceAll(nonExistentPath, `\`, `\\`)+`"}`)
	if !isError {
		t.Fatalf("expected isError=true for non-existent file, got false")
	}
	if result == "" {
		t.Fatalf("expected non-empty error message for non-existent file")
	}

	// 2. Test reading an existing file returns its content
	testDir := t.TempDir()
	filePath := filepath.Join(testDir, "test.txt")
	expectedContent := "hello tool registry"
	if err := os.WriteFile(filePath, []byte(expectedContent), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	validResult, validErr := tool.Execute(ctx, `{"path":"`+strings.ReplaceAll(filePath, `\`, `\\`)+`"}`)
	if validErr {
		t.Fatalf("expected valid file read to succeed, got error: %s", validResult)
	}
	if validResult != expectedContent {
		t.Errorf("expected file content '%s', got '%s'", expectedContent, validResult)
	}
}

func TestWriteFileToolExecution(t *testing.T) {
	tool, exists := defaultToolRegistry.Get("write_file")
	if !exists {
		t.Fatalf("write_file tool not registered in defaultToolRegistry")
	}

	ctx := context.Background()
	testDir := t.TempDir()
	filePath := filepath.Join(testDir, "output.txt")
	contentToWrite := "writing from tool test"

	escapedPath := strings.ReplaceAll(filePath, `\`, `\\`)
	payload := `{"path":"` + escapedPath + `","content":"` + contentToWrite + `"}`

	result, isError := tool.Execute(ctx, payload)
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
}

