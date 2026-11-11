// Package agent provides reusable conversational agent execution loops.
// It supports isolated execution contexts for both root interactive agents and delegated subagents.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"phi-agent-code/internal/api"
	"phi-agent-code/internal/compact"
	"phi-agent-code/internal/permission"
	"phi-agent-code/internal/provider"
	"phi-agent-code/internal/tool"
)

// Agent coordinates conversational LLM inference and tool execution in an isolated loop.
// It encapsulates the core ReAct (Reason-Act-Observe) cycle, conversational memory,
// tool dispatching, and execution limits.
type Agent struct {
	Name      string                     // Human-readable identifier (e.g. "root", "researcher")
	Provider  provider.Provider          // LLM client abstraction for inference turns
	Tools     *tool.Registry             // Scoped catalog of tools available to this specific agent
	Compactor compact.CompactionStrategy // Memory compaction strategy to prevent context window overflow
	System    string                     // Optional customized system instructions
	MaxTurns  int                        // Maximum allowed conversation turns to prevent infinite loops
	Verbose   bool                       // When true, displays spinner and streams assistant text to terminal
	LogPrefix string                     // CLI log prefix displayed before tool execution notices
	Confirm   func(prompt string) bool   // Optional permission gate hook for sensitive operations
	Policy    permission.Policy
	messages  []api.Message // Private conversational memory for this agent instance
}

// New constructs an Agent instance with defensive defaults applied.
// This guarantees that nil tools, unconfigured compactors, or zero-valued turn limits
// will not cause runtime panics or runaway execution loops.
func New(initialConfig Agent) *Agent {
	configuredAgent := initialConfig
	if configuredAgent.Policy == nil {
		configuredAgent.Policy = permission.AlwaysAskPolicy{}
	}
	if configuredAgent.MaxTurns <= 0 {
		configuredAgent.MaxTurns = 15 // Default guard limit against runaway recursion
	}
	if configuredAgent.Tools == nil {
		configuredAgent.Tools = tool.DefaultRegistry
	}
	if configuredAgent.Compactor == nil {
		configuredAgent.Compactor = compact.NoCompaction{}
	}
	if configuredAgent.LogPrefix == "" && configuredAgent.Name != "" {
		configuredAgent.LogPrefix = fmt.Sprintf("[%s]", configuredAgent.Name)
	}

	return &configuredAgent
}

// Messages returns a copy of the current conversational message history.
func (agentInstance *Agent) Messages() []api.Message {
	return agentInstance.messages
}

// Clear resets the conversational memory slice without deallocating backing capacity.
func (agentInstance *Agent) Clear() {
	agentInstance.messages = agentInstance.messages[:0]
}

// Run executes an autonomous ReAct loop starting from a single user prompt.
// It maintains private conversation history across turns, evaluates tools requested by the model,
// and returns the final assistant text synthesis when inference completes.
func (agentInstance *Agent) Run(ctx context.Context, prompt string) (string, error) {
	// Append user prompt to this agent's private conversational history
	agentInstance.messages = append(agentInstance.messages, api.Message{
		Role:    api.RoleUser,
		Content: []api.Block{{Type: api.BlockText, Text: prompt}},
	})

	// Apply context compaction if history exceeds configured thresholds
	compactedMessages, compactionErr := agentInstance.Compactor.Compact(ctx, agentInstance.messages)
	if compactionErr == nil {
		agentInstance.messages = compactedMessages
	}

	turnCount := 0
	for {
		turnCount++
		if turnCount > agentInstance.MaxTurns {
			return "", fmt.Errorf("agent '%s' reached maximum turn limit (%d)", agentInstance.Name, agentInstance.MaxTurns)
		}

		providerResponse, err := agentInstance.Provider.Send(ctx, agentInstance.messages, agentInstance.Tools.Definitions())
		if err != nil {
			return "", fmt.Errorf("provider error: %w", err)
		}

		// Record assistant model turn into history
		agentInstance.messages = append(agentInstance.messages, api.Message{
			Role:    api.RoleAssistant,
			Content: providerResponse.Content,
		})

		var toolResults []api.Block
		for _, contentBlock := range providerResponse.Content {
			switch contentBlock.Type {
			case api.BlockText:
				if agentInstance.Verbose {
					fmt.Println(contentBlock.Text)
				}
			case api.BlockToolUse:
				executionOutput, isExecutionError := agentInstance.executeTool(ctx, contentBlock.ToolName, contentBlock.ToolInput)
				toolResults = append(toolResults, api.Block{
					Type:       api.BlockToolResult,
					ToolUseID:  contentBlock.ToolUseID,
					ToolResult: executionOutput,
					IsError:    isExecutionError,
				})
			}
		}

		// If the model finished without requesting more tools, execution turn is complete
		if providerResponse.StopReason != "tool_use" {
			if agentInstance.Name != "" && agentInstance.Name != "root" {
				fmt.Printf("[subagent] %s completed in %d turns.\n", agentInstance.Name, turnCount)
			}
			return agentInstance.lastAssistantText(), nil
		}

		// Feed tool execution results back into message stack for next reasoning iteration
		agentInstance.messages = append(agentInstance.messages, api.Message{
			Role:    api.RoleUser,
			Content: toolResults,
		})
	}
}

// executeTool resolves and runs requested tools within the agent's scoped registry.
func (agentInstance *Agent) executeTool(ctx context.Context, toolName, rawArguments string) (string, bool) {
	decision := agentInstance.Policy.Authorize(toolName, rawArguments)
	switch decision {
	case permission.Deny:
		return "Permission denied by policy", true
	case permission.AskUser:
		if agentInstance.Confirm != nil {
			confirmationPrompt := fmt.Sprintf("Allow execution of %s %s?", toolName, rawArguments)
			if !agentInstance.Confirm(confirmationPrompt) {
				return "Permission denied by user", true
			}
		}
	case permission.Allow:
		// Auto-approved
	}

	// Provide progress logging for subagents
	if agentInstance.Name != "" && agentInstance.Name != "root" {
		agentInstance.logToolCall(toolName, rawArguments)
	}

	targetTool, exists := agentInstance.Tools.Get(toolName)
	if !exists {
		return fmt.Sprintf("unknown tool: %s", toolName), true
	}

	return targetTool.Execute(ctx, rawArguments)
}

// logToolCall prints structured, human-friendly tool invocation notices to stdout.
func (agentInstance *Agent) logToolCall(toolName, rawArguments string) {
	logPrefix := agentInstance.LogPrefix
	if logPrefix == "" {
		logPrefix = fmt.Sprintf("[%s]", agentInstance.Name)
	}

	switch toolName {
	case "read_file":
		var pathArguments struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(rawArguments), &pathArguments); err == nil && pathArguments.Path != "" {
			fmt.Printf("%s reading %s\n", logPrefix, pathArguments.Path)
			return
		}
	case "write_file":
		var writeArguments struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(rawArguments), &writeArguments); err == nil && writeArguments.Path != "" {
			fmt.Printf("%s writing %s\n", logPrefix, writeArguments.Path)
			return
		}
	case "bash":
		var commandArguments struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(rawArguments), &commandArguments); err == nil && commandArguments.Command != "" {
			fmt.Printf("%s executing: %s\n", logPrefix, commandArguments.Command)
			return
		}
	}

	fmt.Printf("%s calling tool: %s\n", logPrefix, toolName)
}

// lastAssistantText extracts and concatenates the text content of the final assistant message.
func (agentInstance *Agent) lastAssistantText() string {
	for messageIndex := len(agentInstance.messages) - 1; messageIndex >= 0; messageIndex-- {
		if agentInstance.messages[messageIndex].Role == api.RoleAssistant {
			var textBuilder strings.Builder
			for _, contentBlock := range agentInstance.messages[messageIndex].Content {
				if contentBlock.Type == api.BlockText {
					textBuilder.WriteString(contentBlock.Text)
				}
			}
			return textBuilder.String()
		}
	}
	return ""
}
