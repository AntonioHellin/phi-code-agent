package provider

import (
	"context"
	"errors"
	"slices"

	"phi-agent-code/internal/api"
)

// MockProvider is an in-memory Provider that replays scripted responses in sequence,
// records cloned turn histories, and serves offline defaults when exhausted.
type MockProvider struct {
	Responses       []api.Response  // Pre-scripted responses popped sequentially by Send
	CallIndex       int             // Cursor tracking the next response to return
	History         [][]api.Message // Immutable per-turn snapshots of incoming message slices
	AvailableModels []string        // Optional deterministic catalog returned by ListModels
	model           string          // Active model name reported by Model(); defaults to "mock" when empty
}

// Model returns the configured mock model name, defaulting to "mock" when unset.
func (m *MockProvider) Model() string {
	if m == nil || m.model == "" {
		return "mock"
	}
	return m.model
}

// Send records a cloned snapshot of messages into History and returns the next scripted
// response, falling back to a default end_turn text message once Responses is exhausted.
func (m *MockProvider) Send(ctx context.Context, messages []api.Message, _ []api.ToolDef) (api.Response, error) {
	if m == nil {
		return api.Response{}, errors.New("mock provider is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return api.Response{}, err
		}
	}

	// Clone so subsequent agent slice appends do not mutate earlier recorded turns.
	m.History = append(m.History, slices.Clone(messages))

	// Guard against negative externally initialized cursors before indexing Responses.
	if m.CallIndex < 0 {
		m.CallIndex = 0
	}
	if m.CallIndex < len(m.Responses) {
		resp := m.Responses[m.CallIndex]
		m.CallIndex++
		return resp, nil
	}

	// Keep counting calls and return a safe offline fallback so interactive --provider mock runs stay alive.
	m.CallIndex++
	return api.Response{
		Content:    []api.Block{{Type: api.BlockText, Text: "mock default response"}},
		StopReason: "end_turn",
	}, nil
}

// SetModel updates the active model name reported by Model.
func (m *MockProvider) SetModel(name string) {
	if m != nil {
		m.model = name
	}
}

// ListModels returns a defensive copy of AvailableModels (or the active model when unset)
// so tests and --provider mock sessions can exercise ModelLister offline.
func (m *MockProvider) ListModels(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, errors.New("mock provider is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(m.AvailableModels) > 0 {
		return slices.Clone(m.AvailableModels), nil
	}
	return []string{m.Model()}, nil
}

// Compile-time assertions ensuring MockProvider satisfies Provider and ModelLister.
var (
	_ Provider    = (*MockProvider)(nil)
	_ ModelLister = (*MockProvider)(nil)
)
