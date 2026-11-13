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
	if !strings.Contains(banner, "Ctrl+D to exit") {
		t.Errorf("expected banner to instruct Ctrl+D to exit, got %q", banner)
	}
	t.Logf("\n%s", banner)
}

// TestHarnessMouseWheelScroll verifies mouse wheel scrollback in idle and running states.
func TestHarnessMouseWheelScroll(t *testing.T) {
	h := newTestHarness()
	h.viewport = viewport.New(80, 5)
	for i := 0; i < 30; i++ {
		h.output.WriteString(fmt.Sprintf("line %d\n", i))
	}
	h.viewport.SetContent(h.output.String())
	h.viewport.GotoBottom()
	h.followBottom = true

	// Mouse wheel up moves off bottom and disables followBottom
	updatedUp, _ := h.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	mUp := updatedUp.(harness)
	if mUp.followBottom {
		t.Errorf("expected mouse wheel up to set followBottom to false")
	}
	if mUp.viewport.AtBottom() {
		t.Errorf("expected mouse wheel up to scroll above bottom")
	}

	// Mouse wheel down returns to bottom and re-enables followBottom
	updatedDown, _ := mUp.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	mDown := updatedDown.(harness)
	if !mDown.viewport.AtBottom() {
		t.Errorf("expected mouse wheel down to scroll back to bottom")
	}
	if !mDown.followBottom {
		t.Errorf("expected reaching bottom via wheel down to restore followBottom")
	}
}

// TestHarnessSelectFlow verifies the bounded 5-row picker navigation, viewport sizing, confirm, and cancel.
func TestHarnessSelectFlow(t *testing.T) {
	options := []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7"}

	t.Run("windowed rendering, wheel and arrow navigation, enter confirms", func(t *testing.T) {
		h := newTestHarness()
		sized, _ := h.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		h = sized.(harness)

		replyCh := make(chan SelectResult, 1)
		updated, _ := h.Update(SelectRequest{
			Title:   "Select model",
			Options: options,
			Current: "m6",
			Reply:   replyCh,
		})
		m := updated.(harness)

		if m.state != stateSelectingModel {
			t.Fatalf("expected stateSelectingModel (%d), got %d", stateSelectingModel, m.state)
		}
		if m.selectCursor != 6 || m.selectOffset != 2 {
			t.Fatalf("expected cursor=6 offset=2 for Current=m6, got cursor=%d offset=%d", m.selectCursor, m.selectOffset)
		}
		// Layout compensation: 40 - (4 + 5) = 31
		if m.viewport.Height != 31 {
			t.Errorf("expected compensated viewport height 31 while picker open, got %d", m.viewport.Height)
		}

		view := m.View()
		if !strings.Contains(view, "Select model (3-7 of 8)") {
			t.Errorf("expected windowed counter (3-7 of 8), got:\n%s", view)
		}
		if !strings.Contains(view, "m6 (current)") {
			t.Errorf("expected active model badge in view, got:\n%s", view)
		}
		if strings.Contains(view, "  m0") || strings.Contains(view, "  m7") {
			t.Errorf("expected non-windowed items m0/m7 to be hidden outside 5-row window, got:\n%s", view)
		}

		// Arrow down shifts cursor and window to bottom (m7, 4-8 of 8)
		afterDown, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = afterDown.(harness)
		if m.selectCursor != 7 || m.selectOffset != 3 {
			t.Errorf("expected cursor=7 offset=3 after KeyDown, got cursor=%d offset=%d", m.selectCursor, m.selectOffset)
		}

		// Mouse wheel up moves selection back to m6
		afterWheelUp, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
		m = afterWheelUp.(harness)
		if m.selectCursor != 6 {
			t.Errorf("expected cursor=6 after MouseButtonWheelUp, got %d", m.selectCursor)
		}

		// Confirm with enter
		afterEnter, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		mDone := afterEnter.(harness)
		if mDone.state != stateRunning {
			t.Errorf("expected stateRunning after enter, got %d", mDone.state)
		}
		if mDone.viewport.Height != 35 {
			t.Errorf("expected viewport height restored to 35 after closing picker, got %d", mDone.viewport.Height)
		}

		select {
		case res := <-replyCh:
			if res.Canceled || res.Selected != "m6" {
				t.Errorf("expected Selected='m6' Canceled=false, got %+v", res)
			}
		default:
			t.Fatal("expected SelectResult on reply channel")
		}
	})

	t.Run("esc cancels selection", func(t *testing.T) {
		h := newTestHarness()
		replyCh := make(chan SelectResult, 1)

		updated, _ := h.Update(SelectRequest{
			Title:   "Select model",
			Options: options,
			Current: "m0",
			Reply:   replyCh,
		})
		m := updated.(harness)

		afterEsc, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		mDone := afterEsc.(harness)
		if mDone.state != stateRunning {
			t.Errorf("expected stateRunning after esc, got %d", mDone.state)
		}

		select {
		case res := <-replyCh:
			if !res.Canceled || res.Selected != "" {
				t.Errorf("expected Canceled=true Selected='', got %+v", res)
			}
		default:
			t.Fatal("expected SelectResult on reply channel after esc")
		}
	})

	t.Run("empty options reply immediately without entering picker", func(t *testing.T) {
		h := newTestHarness()
		replyCh := make(chan SelectResult, 1)

		updated, _ := h.Update(SelectRequest{
			Title:   "Select model",
			Options: nil,
			Reply:   replyCh,
		})
		m := updated.(harness)
		if m.state != stateIdle {
			t.Errorf("expected stateIdle for empty options, got %d", m.state)
		}
		res := <-replyCh
		if !res.Canceled {
			t.Errorf("expected empty options to reply Canceled=true, got %+v", res)
		}
	})
}

// TestHarnessModelUpdate verifies runtime banner updates preserve scrollback history.
func TestHarnessModelUpdate(t *testing.T) {
	initialBanner := RenderBanner("old-model", -1)
	out := &strings.Builder{}
	out.WriteString(initialBanner)
	out.WriteString("history line\n")

	h := newTestHarness()
	h.modelName = "old-model"
	h.bannerLen = len(initialBanner)
	h.output = out

	updated, _ := h.Update(modelUpdateMsg("new-model"))
	m := updated.(harness)

	if m.modelName != "new-model" {
		t.Errorf("expected modelName='new-model', got %q", m.modelName)
	}
	if !strings.Contains(m.output.String(), "new-model") {
		t.Errorf("expected refreshed banner to include 'new-model'")
	}
	if !strings.Contains(m.output.String(), "history line\n") {
		t.Errorf("expected history line to be preserved across banner refresh")
	}
}

