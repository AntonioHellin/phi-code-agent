// Package ui provides terminal styling, banner formatting, animated spinners,
// and the full Bubble Tea Terminal User Interface (TUI) for interactive agent workflows.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// modelState tracks the operational state of the interactive TUI state machine.
type modelState int

const (
	stateIdle modelState = iota
	stateRunning
	stateAwaitingApproval
	stateSelectingModel
)

const (
	defaultInputHeight      = 5
	maxVisibleSelectOptions = 5
	viewportMouseWheelStep  = 3
)

// AgentRunner defines the decoupled contract to execute user instructions or slash commands asynchronously.
type AgentRunner func(ctx context.Context, prompt string) error

// AppendMsg delivers asynchronous text lines captured from the stdout pipe into the scrollback viewport.
type AppendMsg string

// ApprovalRequest is dispatched by sensitive tools requiring interactive user confirmation.
type ApprovalRequest struct {
	Prompt string
	Reply  chan bool
}

// SelectResult carries the user's chosen item or cancellation from an interactive list picker.
type SelectResult struct {
	Selected string
	Canceled bool
}

// SelectRequest is dispatched by commands requiring bounded single-item selection inside the TUI.
type SelectRequest struct {
	Title   string
	Options []string
	Current string
	Reply   chan SelectResult
}

// modelUpdateMsg refreshes the banner when the active model changes dynamically.
type modelUpdateMsg string

// agentDoneMsg signals that a background agent execution turn has finished.
type agentDoneMsg struct {
	err error
}

// introTickMsg advances the optical lighting sweep animation across the logo at startup.
type introTickMsg int

func introTickCmd(step int) tea.Cmd {
	return tea.Tick(35*time.Millisecond, func(time.Time) tea.Msg {
		return introTickMsg(step)
	})
}

// Program manages the Bubble Tea execution lifecycle, stdout pipe redirection, and interactive confirmations.
type Program struct {
	teaProgram     *tea.Program
	originalStdout *os.File
	pipeReader     *os.File
	pipeWriter     *os.File
}

// harness encapsulates the complete Elm-architecture state for the agent TUI.
type harness struct {
	runner AgentRunner

	viewport viewport.Model
	input    textinput.Model
	spinner  spinner.Model

	state          modelState
	approvalPrompt string
	approvalReply  chan bool

	selectTitle   string
	selectOptions []string
	selectCurrent string
	selectCursor  int
	selectOffset  int
	selectReply   chan SelectResult

	termWidth    int
	termHeight   int
	output       *strings.Builder // Pointer to prevent value-copy panics during Bubble Tea model updates
	followBottom bool             // Auto-scrolls viewport to bottom unless user manually scrolled up

	modelName string
	introStep int // Current sweep column index (0..55); -1 when animation is complete
	bannerLen int // Byte length of the rendered banner at the start of the output buffer
}

// View renders the terminal frame by vertically joining the scrollback viewport and the dynamic input area.
func (h harness) View() string {
	return lipgloss.JoinVertical(
		lipgloss.Left,
		h.viewport.View(),
		h.inputArea(),
	)
}

// inputArea dynamically renders the bottom interface based on the active state machine status.
// Normal states maintain a fixed 5-line height budget; list selection expands cleanly with viewport compensation.
func (h harness) inputArea() string {
	// Scenario A: Agent paused, awaiting interactive user confirmation for a sensitive tool
	if h.state == stateAwaitingApproval {
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("220")). // Amber/yellow warning accent
			Padding(0, 1).
			Render(fmt.Sprintf("%s [y/N]", h.approvalPrompt))

		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("y: allow · n: deny · esc: cancel")
		return lipgloss.JoinVertical(lipgloss.Left, box, hint)
	}

	// Scenario B: Interactive bounded 5-row picker (e.g. /models discovery selector)
	if h.state == stateSelectingModel {
		return h.renderSelectBox()
	}

	// Scenario C: Standard interactive prompt or active execution
	statusLine := " "
	if h.state == stateRunning {
		statusLine = fmt.Sprintf("%s Thinking...", h.spinner.View())
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30E1C3")). // Mineral turquoise border matching brand
		Padding(0, 1).
		Render(h.input.View())

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("enter: send · wheel/pgup/pgdn: scroll · ctrl-c: clear · ctrl-d: exit")
	return lipgloss.JoinVertical(lipgloss.Left, statusLine, box, hint)
}

// renderSelectBox draws a scrolling 5-row viewport over available options with active-item tracking.
func (h harness) renderSelectBox() string {
	total := len(h.selectOptions)
	end := h.selectOffset + maxVisibleSelectOptions
	if end > total {
		end = total
	}

	var rows []string
	title := h.selectTitle
	if title == "" {
		title = "Select option"
	}
	header := fmt.Sprintf("%s (%d-%d of %d)", title, h.selectOffset+1, end, total)
	if total == 0 {
		header = title + " (0 of 0)"
	}
	rows = append(rows, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#30E1C3")).Render(header))

	cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#30E1C3")).Bold(true)
	currentBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(" (current)")
	for i := h.selectOffset; i < end; i++ {
		opt := h.selectOptions[i]
		prefix := "  "
		label := opt
		if i == h.selectCursor {
			prefix = cursorStyle.Render("> ")
			label = cursorStyle.Render(opt)
		}
		if opt == h.selectCurrent {
			label += currentBadge
		}
		rows = append(rows, prefix+label)
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#30E1C3")).
		Padding(0, 1).
		Render(strings.Join(rows, "\n"))

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("↑/↓ or wheel: navigate · enter: select · esc: cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, hint)
}

// Init starts the initial background timers for cursor blinking, spinner animation, and intro light sweep.
func (h harness) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		h.spinner.Tick,
	}
	if h.introStep >= 0 {
		cmds = append(cmds, introTickCmd(0))
	}
	return tea.Batch(cmds...)
}

// Update processes incoming terminal messages, keystrokes, and asynchronous worker events.
func (h harness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.termWidth = msg.Width
		h.termHeight = msg.Height
		h.viewport.Width = msg.Width
		h.syncViewportHeight()
		h.input.Width = msg.Width - 4

	case AppendMsg:
		// Append incoming stdout line to buffer and update scrollback viewport
		h.output.WriteString(string(msg))
		h.viewport.SetContent(h.output.String())
		if h.followBottom {
			h.viewport.GotoBottom()
		}

	case ApprovalRequest:
		// Switch to approval state and cache prompt and reply channel
		h.state = stateAwaitingApproval
		h.approvalPrompt = msg.Prompt
		h.approvalReply = msg.Reply

	case SelectRequest:
		if len(msg.Options) == 0 {
			if msg.Reply != nil {
				msg.Reply <- SelectResult{Canceled: true}
			}
			return h, nil
		}
		h.state = stateSelectingModel
		h.selectTitle = msg.Title
		h.selectOptions = msg.Options
		h.selectCurrent = msg.Current
		h.selectReply = msg.Reply
		h.selectCursor = 0
		h.selectOffset = 0
		for i, opt := range msg.Options {
			if opt == msg.Current {
				h.setSelectCursor(i)
				break
			}
		}
		h.syncViewportHeight()

	case modelUpdateMsg:
		h.modelName = string(msg)
		h.introStep = -1
		h.renderBannerAtStep(-1)

	case agentDoneMsg:
		// Return to idle state upon background agent completion
		h.state = stateIdle
		h.syncViewportHeight()
		if msg.err != nil {
			errNotice := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("\n[error: %v]\n", msg.err))
			h.output.WriteString(errNotice)
			h.viewport.SetContent(h.output.String())
			if h.followBottom {
				h.viewport.GotoBottom()
			}
		}

	case spinner.TickMsg:
		// Advance spinner frame only while agent is actively running
		if h.state == stateRunning {
			var cmd tea.Cmd
			h.spinner, cmd = h.spinner.Update(msg)
			return h, cmd
		}

	case introTickMsg:
		if h.introStep < 0 {
			return h, nil
		}
		h.introStep = int(msg)
		if h.introStep <= 6 {
			h.renderBannerAtStep(h.introStep)
			return h, introTickCmd(h.introStep + 1)
		}

		// Animation complete: render permanent static banner
		h.introStep = -1
		h.renderBannerAtStep(-1)
		return h, nil

	case tea.MouseMsg:
		if h.state == stateSelectingModel {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				h.moveSelect(-1)
			case tea.MouseButtonWheelDown:
				h.moveSelect(1)
			}
			return h, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			h.scrollViewportUp(viewportMouseWheelStep)
		case tea.MouseButtonWheelDown:
			h.scrollViewportDown(viewportMouseWheelStep)
		}

	case tea.KeyMsg:
		// If user types or navigates before intro wave finishes, immediately finalize banner
		if h.introStep >= 0 {
			h.introStep = -1
			h.renderBannerAtStep(-1)
		}
		switch h.state {
		case stateAwaitingApproval:
			switch msg.String() {
			case "y", "Y":
				h.approvalReply <- true
				h.approvalReply = nil
				h.state = stateRunning
			case "n", "N", "esc":
				h.approvalReply <- false
				h.approvalReply = nil
				h.state = stateRunning
			}

		case stateSelectingModel:
			switch msg.String() {
			case "up", "k":
				h.moveSelect(-1)
			case "down", "j":
				h.moveSelect(1)
			case "pgup":
				h.moveSelect(-maxVisibleSelectOptions)
			case "pgdown":
				h.moveSelect(maxVisibleSelectOptions)
			case "home":
				h.setSelectCursor(0)
			case "end":
				h.setSelectCursor(len(h.selectOptions) - 1)
			case "enter":
				chosen := h.selectOptions[h.selectCursor]
				h.finishSelection(SelectResult{Selected: chosen})
			case "esc", "ctrl+c", "q":
				h.finishSelection(SelectResult{Canceled: true})
			}

		case stateRunning:
			// While agent is executing, permit viewport scrollback navigation
			switch msg.String() {
			case "pgup":
				h.scrollViewportUp(h.viewport.Height / 2)
			case "pgdown":
				h.scrollViewportDown(h.viewport.Height / 2)
			}

		case stateIdle:
			switch msg.String() {
			case "ctrl+d":
				return h, tea.Quit

			case "ctrl+c":
				h.input.SetValue("")

			case "pgup":
				h.scrollViewportUp(h.viewport.Height / 2)

			case "pgdown":
				h.scrollViewportDown(h.viewport.Height / 2)

			case "enter":
				val := strings.TrimSpace(h.input.Value())
				if val == "" {
					return h, nil
				}
				h.input.SetValue("")

				// Echo user prompt into scrollback history
				h.output.WriteString("> ")
				h.output.WriteString(val)
				h.output.WriteString("\n")
				h.viewport.SetContent(h.output.String())
				h.followBottom = true
				h.viewport.GotoBottom()

				// Transition state and dispatch agent runner in background goroutine
				h.state = stateRunning
				return h, tea.Batch(
					h.spinner.Tick,
					func() tea.Msg {
						err := h.runner(context.Background(), val)
						return agentDoneMsg{err: err}
					},
				)
			}

			// Forward other typing events (characters, backspace, arrows) to textinput
			var cmd tea.Cmd
			h.input, cmd = h.input.Update(msg)
			return h, cmd
		}
	}

	return h, nil
}

func (h *harness) reservedHeight() int {
	if h.state == stateSelectingModel {
		visible := len(h.selectOptions)
		if visible > maxVisibleSelectOptions {
			visible = maxVisibleSelectOptions
		}
		if visible < 1 {
			visible = 1
		}
		return 4 + visible
	}
	return defaultInputHeight
}

func (h *harness) syncViewportHeight() {
	if h.termHeight <= 0 {
		return
	}
	h.viewport.Height = h.termHeight - h.reservedHeight()
	if h.viewport.Height < 1 {
		h.viewport.Height = 1
	}
	if h.followBottom {
		h.viewport.GotoBottom()
	}
}

func (h *harness) renderBannerAtStep(step int) {
	banner := RenderBanner(h.modelName, step)
	history := ""
	if h.output.Len() > h.bannerLen {
		history = h.output.String()[h.bannerLen:]
	}
	h.output.Reset()
	h.output.WriteString(banner)
	h.bannerLen = len(banner)
	h.output.WriteString(history)
	h.viewport.SetContent(h.output.String())
}

func (h *harness) scrollViewportUp(lines int) {
	if lines < 1 {
		lines = 1
	}
	h.followBottom = false
	h.viewport.ScrollUp(lines)
}

func (h *harness) scrollViewportDown(lines int) {
	if lines < 1 {
		lines = 1
	}
	h.viewport.ScrollDown(lines)
	if h.viewport.AtBottom() {
		h.followBottom = true
	}
}

func (h *harness) setSelectCursor(idx int) {
	if len(h.selectOptions) == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(h.selectOptions) {
		idx = len(h.selectOptions) - 1
	}
	h.selectCursor = idx

	if h.selectCursor < h.selectOffset {
		h.selectOffset = h.selectCursor
	}
	if h.selectCursor >= h.selectOffset+maxVisibleSelectOptions {
		h.selectOffset = h.selectCursor - maxVisibleSelectOptions + 1
	}
	maxOffset := len(h.selectOptions) - maxVisibleSelectOptions
	if maxOffset < 0 {
		maxOffset = 0
	}
	if h.selectOffset > maxOffset {
		h.selectOffset = maxOffset
	}
	if h.selectOffset < 0 {
		h.selectOffset = 0
	}
}

func (h *harness) moveSelect(delta int) {
	h.setSelectCursor(h.selectCursor + delta)
}

func (h *harness) finishSelection(res SelectResult) {
	if h.selectReply != nil {
		h.selectReply <- res
		h.selectReply = nil
	}
	h.selectTitle = ""
	h.selectOptions = nil
	h.selectCurrent = ""
	h.selectCursor = 0
	h.selectOffset = 0
	h.state = stateRunning
	h.syncViewportHeight()
}

// NewProgram initializes the full TUI with stdout pipe redirection and encapsulated event handling.
func NewProgram(runner AgentRunner, model string) (*Program, error) {
	ti := textinput.New()
	ti.Placeholder = "Type your prompt here..."
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#30E1C3"))
	ti.TextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0FAF5"))
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#30E1C3"))
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	initialBanner := RenderBanner(model, 0)
	out := &strings.Builder{}
	out.WriteString(initialBanner)

	m := harness{
		runner:       runner,
		input:        ti,
		spinner:      sp,
		output:       out,
		followBottom: true,
		modelName:    model,
		introStep:    0,
		bannerLen:    len(initialBanner),
	}

	// Intercept standard output via pipe so all agent and tool logs flow into viewport
	originalStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	os.Stdout = w

	// Initialize Bubble Tea rendering directly to physical terminal stdout
	p := tea.NewProgram(
		m,
		tea.WithOutput(originalStdout),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	// Background goroutine scanning the pipe and feeding lines into the TUI event loop
	go func() {
		scanner := bufio.NewScanner(r)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)
		for scanner.Scan() {
			p.Send(AppendMsg(scanner.Text() + "\n"))
		}
		if err := scanner.Err(); err != nil {
			p.Send(AppendMsg(fmt.Sprintf("[error reading stdout pipe: %v]\n", err)))
		}
	}()

	return &Program{
		teaProgram:     p,
		originalStdout: originalStdout,
		pipeReader:     r,
		pipeWriter:     w,
	}, nil
}

// Confirm satisfies the agent's interactive confirmation contract (rootAgent.Confirm).
// It dispatches an ApprovalRequest to the TUI and blocks the agent goroutine until user responds.
func (p *Program) Confirm(prompt string) bool {
	reply := make(chan bool, 1)
	p.teaProgram.Send(ApprovalRequest{Prompt: prompt, Reply: reply})
	return <-reply
}

// SelectModel opens a bounded interactive picker in the TUI and blocks until resolved.
func (p *Program) SelectModel(title string, options []string, current string) (string, bool) {
	if len(options) == 0 {
		return "", false
	}
	reply := make(chan SelectResult, 1)
	p.teaProgram.Send(SelectRequest{
		Title:   title,
		Options: options,
		Current: current,
		Reply:   reply,
	})
	res := <-reply
	if res.Canceled {
		return "", false
	}
	return res.Selected, true
}

// SetModel updates the active model name rendered in the TUI banner.
func (p *Program) SetModel(model string) {
	p.teaProgram.Send(modelUpdateMsg(model))
}

// Run executes the Bubble Tea program and ensures stdout and pipe descriptors are safely restored on exit.
func (p *Program) Run() error {
	defer func() {
		os.Stdout = p.originalStdout
		_ = p.pipeWriter.Close()
		_ = p.pipeReader.Close()
	}()
	_, err := p.teaProgram.Run()
	return err
}
