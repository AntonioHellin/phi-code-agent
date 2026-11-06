package main

import (
	"context"
)

// Tool defines the contract that any agent tool must implement.
// It decouples schema definition (sent to the LLM) from tool execution.
type Tool interface {
	// Definition returns the JSON Schema metadata describing the tool to the LLM.
	Definition() ToolDef

	// Execute runs the tool logic with the provided context and raw JSON arguments.
	// It returns the output string and a boolean indicating whether execution failed.
	// In accordance with our core conventions, errors travel back as tool results
	// (isError=true) so the LLM can observe the failure and attempt self-correction.
	Execute(ctx context.Context, rawInput string) (string, bool)
}

// Registry maintains an in-memory catalog of registered tools indexed by name.
// It enables the Open/Closed Principle (OCP) by allowing dynamic tool registration
// without modifying main loop orchestration code.
type Registry struct {
	tools map[string]Tool
}

// NewToolRegistry constructs an initialized tool registry with an allocated map.
func NewToolRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// defaultToolRegistry is the global singleton registry populated during init().
var defaultToolRegistry = NewToolRegistry()

// Register adds a tool to the registry using its definition name as the lookup key.
func (r *Registry) Register(tool Tool) {
	r.tools[tool.Definition().Name] = tool
}

// Get retrieves a tool by its registered name.
// Returns the tool and true if found, or nil and false if not registered.
func (r *Registry) Get(name string) (Tool, bool) {
	tool, exists := r.tools[name]
	return tool, exists
}

// Definitions returns a slice of ToolDef schemas for all currently registered tools.
// This slice is passed directly to the LLM provider to declare available capabilities.
func (r *Registry) Definitions() []ToolDef {
	definitions := make([]ToolDef, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition())
	}
	return definitions
}
