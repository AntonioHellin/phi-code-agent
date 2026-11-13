package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
)

// TestMockProvider_SequentialAndFallback verifies that Send pops scripted responses
// in order, advances CallIndex, and falls back to a safe end_turn message once exhausted.
func TestMockProvider_SequentialAndFallback(t *testing.T) {
	mock := &MockProvider{
		Responses: []api.Response{
			{
				StopReason: "tool_use",
				Content: []api.Block{
					{
						Type:      api.BlockToolUse,
						ToolUseID: "call_1",
						ToolName:  "bash",
						ToolInput: `{"command":"echo hello"}`,
					},
				},
			},
			{
				StopReason: "end_turn",
				Content: []api.Block{
					{Type: api.BlockText, Text: "Command finished."},
				},
			},
		},
	}

	ctx := context.Background()

	// Call 1: expect scripted tool_use response
	resp1, err := mock.Send(ctx, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error on call 1: %v", err)
	}
	if resp1.StopReason != "tool_use" || len(resp1.Content) != 1 || resp1.Content[0].ToolName != "bash" {
		t.Errorf("unexpected response 1: %+v", resp1)
	}
	if mock.CallIndex != 1 {
		t.Errorf("expected CallIndex=1 after first call, got %d", mock.CallIndex)
	}

	// Call 2: expect scripted final text response
	resp2, err := mock.Send(ctx, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error on call 2: %v", err)
	}
	if resp2.StopReason != "end_turn" || len(resp2.Content) != 1 || resp2.Content[0].Text != "Command finished." {
		t.Errorf("unexpected response 2: %+v", resp2)
	}
	if mock.CallIndex != 2 {
		t.Errorf("expected CallIndex=2 after second call, got %d", mock.CallIndex)
	}

	// Call 3: queue exhausted; expect default offline fallback so interactive CLI stays usable
	resp3, err := mock.Send(ctx, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error on fallback call: %v", err)
	}
	if resp3.StopReason != "end_turn" || len(resp3.Content) != 1 || resp3.Content[0].Text != "mock default response" {
		t.Errorf("unexpected fallback response: %+v", resp3)
	}
	if mock.CallIndex != 3 {
		t.Errorf("expected CallIndex=3 after fallback call, got %d", mock.CallIndex)
	}
}

// TestMockProvider_ImmutableHistorySnapshots verifies that Send clones incoming message
// slices so subsequent caller mutations or slice appends do not corrupt earlier History entries.
func TestMockProvider_ImmutableHistorySnapshots(t *testing.T) {
	mock := &MockProvider{}
	ctx := context.Background()

	// Allocate spare capacity so append() below reuses the caller's backing array.
	msgs := make([]api.Message, 1, 4)
	msgs[0] = api.Message{
		Role:    api.RoleUser,
		Content: []api.Block{{Type: api.BlockText, Text: "turn 1"}},
	}

	if _, err := mock.Send(ctx, msgs, nil); err != nil {
		t.Fatalf("unexpected error on turn 1: %v", err)
	}

	// Mutate element 0 and append a second message in place before turn 2.
	msgs[0] = api.Message{
		Role:    api.RoleUser,
		Content: []api.Block{{Type: api.BlockText, Text: "mutated"}},
	}
	msgs = append(msgs, api.Message{
		Role:    api.RoleAssistant,
		Content: []api.Block{{Type: api.BlockText, Text: "turn 2"}},
	})

	if _, err := mock.Send(ctx, msgs, nil); err != nil {
		t.Fatalf("unexpected error on turn 2: %v", err)
	}

	if len(mock.History) != 2 {
		t.Fatalf("expected 2 recorded history snapshots, got %d", len(mock.History))
	}
	if len(mock.History[0]) != 1 || mock.History[0][0].Content[0].Text != "turn 1" {
		t.Errorf("expected turn 1 snapshot to stay immutable as 'turn 1', got %+v", mock.History[0])
	}
	if len(mock.History[1]) != 2 || mock.History[1][1].Content[0].Text != "turn 2" {
		t.Errorf("expected turn 2 snapshot with 2 messages, got %+v", mock.History[1])
	}
}

// TestMockProvider_ModelAccessors verifies default model reporting, dynamic updates via
// SetModel, and nil-receiver safety on both Model and SetModel.
func TestMockProvider_ModelAccessors(t *testing.T) {
	var nilMock *MockProvider
	if got := nilMock.Model(); got != "mock" {
		t.Errorf("expected nil receiver Model() to default to 'mock', got %q", got)
	}
	nilMock.SetModel("ignored") // Must not panic

	mock := &MockProvider{}
	if got := mock.Model(); got != "mock" {
		t.Errorf("expected unset Model() to default to 'mock', got %q", got)
	}

	mock.SetModel("mock-fast")
	if got := mock.Model(); got != "mock-fast" {
		t.Errorf("expected updated model 'mock-fast', got %q", got)
	}
}

// TestMockProvider_GuardsAndContextCancellation verifies nil-receiver errors, immediate
// abort on canceled contexts without mutating state, and normalization of negative CallIndex.
func TestMockProvider_GuardsAndContextCancellation(t *testing.T) {
	var nilMock *MockProvider
	if _, err := nilMock.Send(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("expected nil receiver Send error, got %v", err)
	}

	mock := &MockProvider{
		CallIndex: -2,
		Responses: []api.Response{
			{StopReason: "end_turn", Content: []api.Block{{Type: api.BlockText, Text: "first"}}},
		},
	}

	// Canceled context must abort cleanly before recording History or advancing CallIndex.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := mock.Send(canceledCtx, []api.Message{{Role: api.RoleUser}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}
	if len(mock.History) != 0 {
		t.Errorf("expected canceled Send to leave History empty, got %d entries", len(mock.History))
	}
	if mock.CallIndex != -2 {
		t.Errorf("expected canceled Send to preserve CallIndex=-2, got %d", mock.CallIndex)
	}

	// Valid context should normalize CallIndex=-2 to 0 and return the first scripted response.
	resp, err := mock.Send(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error after negative CallIndex recovery: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "first" {
		t.Errorf("expected first scripted response after CallIndex normalization, got %+v", resp)
	}
	if mock.CallIndex != 1 {
		t.Errorf("expected CallIndex=1 after dispatching index 0, got %d", mock.CallIndex)
	}
}

