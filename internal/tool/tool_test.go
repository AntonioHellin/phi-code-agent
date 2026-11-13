package tool

import (
	"context"
	"testing"

	"phi-agent-code/internal/api"
)

// mockTool implements the Tool interface for isolated unit testing.
type mockTool struct {
	name        string
	description string
	output      string
	isError     bool
}

func (m *mockTool) Definition() api.ToolDef {
	return api.ToolDef{
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

// TestRegistryRegisterAndGet verifies registering a tool by name, retrieving its metadata,
// and returning false when looking up an unregistered tool.
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

// TestRegistryDefinitions verifies that Definitions returns schemas for all registered tools.
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

// TestDefaultRegistryContainsNativeTools verifies that bash, read_file, and write_file
// self-register into DefaultRegistry during package initialization.
func TestDefaultRegistryContainsNativeTools(t *testing.T) {
	expectedTools := []string{"bash", "read_file", "write_file"}

	for _, toolName := range expectedTools {
		tInstance, exists := DefaultRegistry.Get(toolName)
		if !exists {
			t.Errorf("expected DefaultRegistry to contain '%s' via init() self-registration", toolName)
			continue
		}

		definition := tInstance.Definition()
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

// TestRegistryRegisterWithRetry verifies that RegisterWithRetry ignores nil tools
// and wraps valid tools in a RetryDecorator with the configured retry limit.
func TestRegistryRegisterWithRetry(t *testing.T) {
	registry := NewToolRegistry()

	// Defensive guard: registering a nil tool must not panic or populate the registry.
	registry.RegisterWithRetry(nil, 2)
	if len(registry.Definitions()) != 0 {
		t.Fatalf("expected empty registry after registering nil tool, got %d entries", len(registry.Definitions()))
	}

	mock := &mockTool{
		name:        "flaky_tool",
		description: "Tool wrapped with retry support",
	}
	registry.RegisterWithRetry(mock, 2)

	retrieved, exists := registry.Get("flaky_tool")
	if !exists {
		t.Fatalf("expected 'flaky_tool' to be registered")
	}

	decorator, ok := retrieved.(*RetryDecorator)
	if !ok {
		t.Fatalf("expected registered tool to be wrapped in *RetryDecorator, got %T", retrieved)
	}
	if decorator.Tool != mock {
		t.Errorf("expected decorator to wrap the original mock tool")
	}
	if decorator.MaxRetries != 2 {
		t.Errorf("expected MaxRetries=2, got %d", decorator.MaxRetries)
	}
	if retrieved.Definition().Name != "flaky_tool" {
		t.Errorf("expected preserved definition name 'flaky_tool', got %q", retrieved.Definition().Name)
	}
}
