// Package ui provides terminal styling, banner formatting, animated spinners,
// and the full Bubble Tea Terminal User Interface (TUI) for interactive agent workflows.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

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

// agentDoneMsg signals that a background agent execution turn has finished.
type agentDoneMsg struct {
	err error
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

	output       *strings.Builder // Pointer to prevent value-copy panics during Bubble Tea model updates
	followBottom bool             // Auto-scrolls viewport to bottom unless user manually scrolled up
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
// It consistently maintains a fixed 5-line height budget to avoid layout jitter during state transitions.
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

	// Scenario B: Standard interactive prompt or active execution
	statusLine := " "
	if h.state == stateRunning {
		statusLine = fmt.Sprintf("%s Thinking...", h.spinner.View())
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")). // Classic Bubble Tea purple border
		Padding(0, 1).
		Render(h.input.View())

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("enter: send · pgup/pgdn: scroll · ctrl-c: clear · ctrl-d: exit")
	return lipgloss.JoinVertical(lipgloss.Left, statusLine, box, hint)
}

// Init starts the initial background timers for cursor blinking and spinner animation.
func (h harness) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		h.spinner.Tick,
	)
}

// Update processes incoming terminal messages, keystrokes, and asynchronous worker events.
func (h harness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Budget layout: reserve exactly 5 vertical lines for the input area
		h.viewport.Width = msg.Width
		h.viewport.Height = msg.Height - 5
		if h.viewport.Height < 1 {
			h.viewport.Height = 1
		}
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

	case agentDoneMsg:
		// Return to idle state upon background agent completion
		h.state = stateIdle
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

	case tea.KeyMsg:
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

		case stateRunning:
			// While agent is executing, permit viewport scrollback navigation
			switch msg.String() {
			case "pgup":
				h.followBottom = false
				h.viewport.ScrollUp(h.viewport.Height / 2)
			case "pgdown":
				h.viewport.ScrollDown(h.viewport.Height / 2)
				if h.viewport.AtBottom() {
					h.followBottom = true
				}
			}

		case stateIdle:
			switch msg.String() {
			case "ctrl+d":
				return h, tea.Quit

			case "ctrl+c":
				h.input.SetValue("")

			case "pgup":
				h.followBottom = false
				h.viewport.ScrollUp(h.viewport.Height / 2)

			case "pgdown":
				h.viewport.ScrollDown(h.viewport.Height / 2)
				if h.viewport.AtBottom() {
					h.followBottom = true
				}

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

// NewProgram initializes the full TUI with stdout pipe redirection and encapsulated event handling.
func NewProgram(runner AgentRunner) (*Program, error) {
	ti := textinput.New()
	ti.Placeholder = "Type your prompt here..."
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := harness{
		runner:       runner,
		input:        ti,
		spinner:      sp,
		output:       &strings.Builder{},
		followBottom: true,
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
