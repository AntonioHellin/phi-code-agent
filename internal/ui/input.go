package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
)

// InputReader encapsulates the readline controller to provide advanced terminal features:
// terminal raw mode, persistent history navigation, and delimited multiline accumulation.
type InputReader struct {
	readlineInstance *readline.Instance
}

// NewInputReader constructs and configures the terminal readline instance.
func NewInputReader() (*InputReader, error) {
	// 1. Locate the user's home directory to persist command history across agent restarts (~/.phi_agent_history)
	userHomeDirectory, err := os.UserHomeDir()
	historyFilePath := ".phi_agent_history"
	if err == nil {
		historyFilePath = filepath.Join(userHomeDirectory, historyFilePath)
	}

	// 2. Configure readline parameters: single-line prompt, history destination, and signal labels
	readlineConfig := &readline.Config{
		Prompt:          "> ",
		HistoryFile:     historyFilePath,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	}

	// 3. Initialize readline (switches terminal from canonical line mode to raw character mode)
	instance, err := readline.NewEx(readlineConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize terminal readline: %w", err)
	}

	return &InputReader{readlineInstance: instance}, nil
}

// Close restores the terminal device from raw character mode back to standard canonical line mode.
func (reader *InputReader) Close() error {
	return reader.readlineInstance.Close()
}

// ReadPrompt reads a user instruction from the interactive terminal.
// It supports:
// - Single-line prompts with Up/Down arrow history navigation.
// - Delimited multiline prompts triggered by triple quotes (""").
// - Graceful Ctrl+C interruption (discards the active buffer without terminating the agent).
// - Clean Ctrl+D / EOF session exit.
func (reader *InputReader) ReadPrompt() (string, error) {
	rawLine, readErr := reader.readlineInstance.Readline()
	if readErr != nil {
		// Intercept Ctrl+C: cancel the current input line gracefully instead of killing the process
		if readErr == readline.ErrInterrupt {
			return "", nil
		}
		// Propagate EOF (Ctrl+D) or underlying terminal IO errors
		return "", readErr
	}

	trimmedLine := strings.TrimSpace(rawLine)

	// Check if the user initiated a multiline block using triple quotes
	if strings.HasPrefix(trimmedLine, `"""`) {
		// Scenario A: Single-line self-closing block: """my prompt here"""
		if len(trimmedLine) > 3 && strings.HasSuffix(trimmedLine, `"""`) {
			extractedContent := strings.TrimSuffix(strings.TrimPrefix(trimmedLine, `"""`), `"""`)
			return strings.TrimSpace(extractedContent), nil
		}

		// Scenario B: Multiline prompt accumulator
		var multilineBuilder strings.Builder
		firstLineWithoutDelimiter := strings.TrimPrefix(rawLine, `"""`)
		if firstLineWithoutDelimiter != "" {
			multilineBuilder.WriteString(firstLineWithoutDelimiter)
			multilineBuilder.WriteString("\n")
		}

		// Temporarily change the terminal prompt to indicate multiline continuation
		reader.readlineInstance.SetPrompt("... ")
		// Guaranteed prompt restoration to primary cursor once multiline reading finishes
		defer reader.readlineInstance.SetPrompt("> ")

		for {
			nextLine, nextLineErr := reader.readlineInstance.Readline()
			if nextLineErr != nil {
				if nextLineErr == readline.ErrInterrupt {
					// Ctrl+C discards the entire in-flight multiline draft
					return "", nil
				}
				return "", nextLineErr
			}

			// Check for closing delimiter (""")
			if strings.Contains(nextLine, `"""`) {
				closingLineContent := strings.TrimSuffix(nextLine, `"""`)
				multilineBuilder.WriteString(closingLineContent)
				break
			}

			multilineBuilder.WriteString(nextLine)
			multilineBuilder.WriteString("\n")
		}

		return strings.TrimSpace(multilineBuilder.String()), nil
	}

	return trimmedLine, nil
}

