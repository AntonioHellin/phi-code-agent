// Package provider defines the vendor-agnostic LLM engine contract and wire-format adapters.
// Higher-level orchestration modules interact solely through the Provider interface.
package provider

import (
	"context"

	"phi-agent-code/internal/api"
)

// Provider is the decoupled contract that any LLM engine must fulfill.
// High-level modules (e.g. agent.Agent, compact.Summarize) depend solely on this abstraction (Dependency Inversion).
type Provider interface {
	Send(ctx context.Context, messages []api.Message, tools []api.ToolDef) (api.Response, error)
	Model() string
	SetModel(name string)
}

// ModelLister is an optional capability interface (Interface Segregation Principle) implemented
// by providers that can enumerate available models from their backend catalog on demand.
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}
