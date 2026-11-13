// Package tool defines the agent Tool interface, the central Registry,
// and native self-registering tool implementations.
package tool

import (
	"context"

	"phi-agent-code/internal/api"
)

// Tool defines the contract that any agent tool must implement.
// It decouples schema definition (sent to the LLM) from execution.
type Tool interface {
	// Definition returns the JSON Schema metadata describing the tool to the LLM.
	Definition() api.ToolDef

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

// DefaultRegistry is the global singleton registry populated during init().
var DefaultRegistry = NewToolRegistry()

// Register adds a tool to the registry using its definition name as the lookup key.
func (r *Registry) Register(t Tool) {
	r.tools[t.Definition().Name] = t
}

// RegisterWithRetry wraps the tool in a RetryDecorator with default backoff
// before registering it by its definition name. Nil tools are ignored safely.
func (r *Registry) RegisterWithRetry(t Tool, maxRetries int) {
	if t == nil {
		return
	}
	r.Register(&RetryDecorator{
		Tool:       t,
		MaxRetries: maxRetries,
	})
}

// Get retrieves a tool by its registered name.
// Returns the tool and true if found, or nil and false if not registered.
func (r *Registry) Get(name string) (Tool, bool) {
	t, exists := r.tools[name]
	return t, exists
}

// Definitions returns a slice of ToolDef schemas for all currently registered tools.
// This slice is passed directly to the LLM provider to declare available capabilities.
func (r *Registry) Definitions() []api.ToolDef {
	definitions := make([]api.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		definitions = append(definitions, t.Definition())
	}
	return definitions
}
