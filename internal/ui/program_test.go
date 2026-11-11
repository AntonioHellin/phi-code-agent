package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// newTestHarness initializes a clean harness model for unit testing.
func newTestHarness() harness {
	ti := textinput.New()
	sp := spinner.New()

	return harness{
		runner:       func(ctx context.Context, prompt string) error { return nil },
		viewport:     viewport.New(80, 20),
		input:        ti,
		spinner:      sp,
		state:        stateIdle,
		output:       &strings.Builder{},
		followBottom: true,
		modelName:    "claude-haiku-5-5",
		introStep:    -1, // Unit tests default to completed intro animation
	}
}

// TestHarnessInit verifies that Init() returns initial background animation commands.
func TestHarnessInit(t *testing.T) {
	h := newTestHarness()
	cmd := h.Init()
	if cmd == nil {
		t.Fatal("expected Init() to return a non-nil tea.Cmd batch for cursor and spinner")
	}
}

// TestHarnessWindowSizeMsg verifies that WindowSizeMsg budgets the layout correctly.
func TestHarnessWindowSizeMsg(t *testing.T) {
	h := newTestHarness()

	// 1. Standard terminal dimension: 100 columns by 40 rows
	updated, _ := h.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m, ok := updated.(harness)
	if !ok {
		t.Fatalf("expected updated model to be harness, got %T", updated)
	}

	if m.viewport.Width != 100 {
		t.Errorf("expected viewport width 100, got %d", m.viewport.Width)
	}
	// Viewport height should be total height (40) minus inputArea reservation (5) = 35
	if m.viewport.Height != 35 {
		t.Errorf("expected viewport height 35, got %d", m.viewport.Height)
	}
	if m.input.Width != 96 {
		t.Errorf("expected input width 96 (100 - 4), got %d", m.input.Width)
	}

	// 2. Edge case: very small terminal height (under 5 rows)
	updatedSmall, _ := h.Update(tea.WindowSizeMsg{Width: 80, Height: 4})
	mSmall := updatedSmall.(harness)
	if mSmall.viewport.Height < 1 {
		t.Errorf("expected viewport height to be clamped to at least 1, got %d", mSmall.viewport.Height)
	}
}

// TestHarnessAppendMsg verifies that incoming stdout lines are appended to the output buffer.
func TestHarnessAppendMsg(t *testing.T) {
	h := newTestHarness()

	updated1, _ := h.Update(AppendMsg("line 1\n"))
	m1 := updated1.(harness)

	updated2, _ := m1.Update(AppendMsg("line 2\n"))
	m2 := updated2.(harness)

	expected := "line 1\nline 2\n"
	if m2.output.String() != expected {
		t.Errorf("expected output buffer %q, got %q", expected, m2.output.String())
	}
}

// TestHarnessApprovalFlow verifies the asynchronous tool permission gate via channels.
func TestHarnessApprovalFlow(t *testing.T) {
	t.Run("user approves with y", func(t *testing.T) {
		h := newTestHarness()
		replyCh := make(chan bool, 1)

		// 1. Tool dispatches ApprovalRequest
		updated, _ := h.Update(ApprovalRequest{
			Prompt: "execute bash rm -rf",
			Reply:  replyCh,
		})
		m := updated.(harness)

		if m.state != stateAwaitingApproval {
			t.Errorf("expected state %d (stateAwaitingApproval), got %d", stateAwaitingApproval, m.state)
		}
		if m.approvalPrompt != "execute bash rm -rf" {
			t.Errorf("expected prompt %q, got %q", "execute bash rm -rf", m.approvalPrompt)
		}

		// 2. User presses 'y' to allow
		updatedAfterKey, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		mAfterKey := updatedAfterKey.(harness)

		if mAfterKey.state != stateRunning {
			t.Errorf("expected state to return to %d (stateRunning), got %d", stateRunning, mAfterKey.state)
		}

		select {
		case approved := <-replyCh:
			if !approved {
				t.Errorf("expected approval channel to receive true, got false")
			}
		default:
			t.Fatal("expected approval channel to receive response, but channel was empty")
		}
	})

	t.Run("user denies with n", func(t *testing.T) {
		h := newTestHarness()
		replyCh := make(chan bool, 1)

		updated, _ := h.Update(ApprovalRequest{
			Prompt: "execute bash rm -rf",
			Reply:  replyCh,
		})
		m := updated.(harness)

		// User presses 'n' to deny
		updatedAfterKey, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
		mAfterKey := updatedAfterKey.(harness)

		if mAfterKey.state != stateRunning {
			t.Errorf("expected state to return to %d (stateRunning), got %d", stateRunning, mAfterKey.state)
		}

		select {
		case approved := <-replyCh:
			if approved {
				t.Errorf("expected approval channel to receive false, got true")
			}
		default:
			t.Fatal("expected approval channel to receive response, but channel was empty")
		}
	})
}

// TestHarnessViewRendering verifies that View() renders appropriately for different states.
func TestHarnessViewRendering(t *testing.T) {
	h := newTestHarness()

	// 1. Idle state rendering should include hints
	idleView := h.View()
	if !strings.Contains(idleView, "enter: send") {
		t.Errorf("expected idle view to contain 'enter: send', got:\n%s", idleView)
	}

	// 2. Awaiting approval state rendering should include warning prompt and [y/N]
	h.state = stateAwaitingApproval
	h.approvalPrompt = "execute bash find ."
	approvalView := h.View()

	if !strings.Contains(approvalView, "execute bash find . [y/N]") {
		t.Errorf("expected approval view to contain prompt and [y/N], got:\n%s", approvalView)
	}
	if !strings.Contains(approvalView, "y: allow · n: deny") {
		t.Errorf("expected approval view to contain allow/deny hints, got:\n%s", approvalView)
	}
}

// TestHarnessAgentDoneMsgError verifies that agentDoneMsg surfaces errors in the output buffer.
func TestHarnessAgentDoneMsgError(t *testing.T) {
	h := newTestHarness()
	h.state = stateRunning

	updated, _ := h.Update(agentDoneMsg{err: fmt.Errorf("connection refused")})
	m := updated.(harness)

	if m.state != stateIdle {
		t.Errorf("expected state to return to stateIdle (%d), got %d", stateIdle, m.state)
	}

	if !strings.Contains(m.output.String(), "connection refused") {
		t.Errorf("expected output buffer to contain error message, got: %q", m.output.String())
	}
}

// TestHarnessPromptSubmission verifies prompt submission on enter, input reset, and runner dispatch.
func TestHarnessPromptSubmission(t *testing.T) {
	h := newTestHarness()
	h.runner = func(ctx context.Context, prompt string) error {
		return nil
	}

	// 1. Enter with empty input should be ignored
	updatedEmpty, cmdEmpty := h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mEmpty := updatedEmpty.(harness)
	if mEmpty.state != stateIdle {
		t.Errorf("expected state to stay idle for empty enter, got %d", mEmpty.state)
	}
	if cmdEmpty != nil {
		t.Errorf("expected nil cmd for empty enter, got non-nil")
	}

	// 2. Enter with prompt value
	h.input.SetValue("check status")
	updated, cmd := h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(harness)

	if m.state != stateRunning {
		t.Errorf("expected state to transition to stateRunning (%d), got %d", stateRunning, m.state)
	}
	if m.input.Value() != "" {
		t.Errorf("expected input to be cleared, got %q", m.input.Value())
	}
	if !strings.Contains(m.output.String(), "> check status\n") {
		t.Errorf("expected output buffer to contain prompt echo, got %q", m.output.String())
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd returning batch")
	}
}

// TestHarnessKeyShortcuts verifies ctrl+c and ctrl+d shortcuts in idle mode.
func TestHarnessKeyShortcuts(t *testing.T) {
	h := newTestHarness()
	h.input.SetValue("half-typed command")

	// ctrl+c clears input
	updatedCtrlC, _ := h.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	mCtrlC := updatedCtrlC.(harness)
	if mCtrlC.input.Value() != "" {
		t.Errorf("expected ctrl+c to clear input, got %q", mCtrlC.input.Value())
	}

	// ctrl+d returns tea.Quit
	_, cmdCtrlD := h.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmdCtrlD == nil {
		t.Fatal("expected ctrl+d to return tea.Quit command")
	}
}

// TestHarnessScrollback verifies pgup/pgdown viewport navigation and followBottom tracking.
func TestHarnessScrollback(t *testing.T) {
	h := newTestHarness()
	h.followBottom = true

	// pgup unsets followBottom
	updatedPgUp, _ := h.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	mPgUp := updatedPgUp.(harness)
	if mPgUp.followBottom {
		t.Errorf("expected pgup to set followBottom to false")
	}

	// pgdown at bottom restores followBottom
	updatedPgDn, _ := mPgUp.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	mPgDn := updatedPgDn.(harness)
	if !mPgDn.followBottom {
		t.Errorf("expected pgdown at bottom to restore followBottom to true")
	}
}

// TestHarnessSpinnerTick verifies that spinner ticks only when running.
func TestHarnessSpinnerTick(t *testing.T) {
	h := newTestHarness()

	// In stateIdle, TickMsg is a no-op
	updatedIdle, cmdIdle := h.Update(spinner.TickMsg{})
	mIdle := updatedIdle.(harness)
	if cmdIdle != nil {
		t.Errorf("expected nil cmd for spinner tick in idle state")
	}
	if mIdle.state != stateIdle {
		t.Errorf("expected state to remain idle")
	}

	// In stateRunning, TickMsg updates spinner and returns next tick cmd
	h.state = stateRunning
	updatedRunning, cmdRunning := h.Update(spinner.TickMsg{})
	mRunning := updatedRunning.(harness)
	if cmdRunning == nil {
		t.Errorf("expected non-nil cmd for spinner tick in running state")
	}
	if mRunning.state != stateRunning {
		t.Errorf("expected state to remain running")
	}
}

// TestHarnessIntroLightSweep verifies the intro light wave animation across the logo and keypress finalization.
func TestHarnessIntroLightSweep(t *testing.T) {
	ti := textinput.New()
	sp := spinner.New()
	initialBanner := RenderBanner("test-model", 0)
	out := &strings.Builder{}
	out.WriteString(initialBanner)

	h := harness{
		runner:       func(ctx context.Context, prompt string) error { return nil },
		viewport:     viewport.New(80, 20),
		input:        ti,
		spinner:      sp,
		output:       out,
		followBottom: true,
		modelName:    "test-model",
		introStep:    0,
		bannerLen:    len(initialBanner),
	}

	// 1. Init should schedule the first intro tick
	cmd := h.Init()
	if cmd == nil {
		t.Fatal("expected Init() to return batch including intro tick cmd")
	}

	// 2. An intermediate tick advances sweep column and returns next command
	updated, nextCmd := h.Update(introTickMsg(3))
	m := updated.(harness)
	if m.introStep != 3 {
		t.Errorf("expected introStep=3, got %d", m.introStep)
	}
	if nextCmd == nil {
		t.Error("expected next tick command during active sweep")
	}

	// 3. User keypress before completion immediately finalizes the banner
	updatedKey, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	mKey := updatedKey.(harness)
	if mKey.introStep != -1 {
		t.Errorf("expected keypress to complete intro animation (introStep=-1), got %d", mKey.introStep)
	}
}

// TestRenderBannerVisual verifies banner rendering and prints it to test logs.
func TestRenderBannerVisual(t *testing.T) {
	banner := RenderBanner("test-model", -1)
	if !strings.Contains(banner, "test-model") {
		t.Errorf("expected banner to contain model name")
	}
	t.Logf("\n%s", banner)
}

