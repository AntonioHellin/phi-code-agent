package main

import (
	"context"
	"encoding/json"
	"fmt"

	"phi-agent-code/internal/agent"
	"phi-agent-code/internal/api"
	"phi-agent-code/internal/tool"
)

// DelegateTool enables the primary LLM agent to spawn isolated subagents for specialized subtasks.
// It enforces the Context Isolation pattern: child agent tool invocations and intermediate reasoning
// stay confined within the child's execution thread, returning only a synthesized summary back to the parent.
type DelegateTool struct{}

func init() {
	tool.DefaultRegistry.Register(&DelegateTool{})
}

// Definition declares the JSON Schema specification for the "delegate" tool to the model.
func (delegateTool *DelegateTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "delegate",
		Description: "Delegate a subtask to an isolated subagent (e.g. 'researcher'). Returns the subagent's summary.",
		InputSchema: map[string]any{
			"agent": map[string]any{
				"type":        "string",
				"description": "The subagent profile to invoke (e.g. 'researcher').",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The specific task description or investigative question for the subagent.",
			},
		},
		Required: []string{"agent", "prompt"},
	}
}

// Execute runs the delegated subagent loop to completion and returns its final synthesis.
func (delegateTool *DelegateTool) Execute(ctx context.Context, rawInput string) (string, bool) {
	// Unmarshal structured delegation parameters from the model
	var delegationRequest struct {
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(rawInput), &delegationRequest); err != nil {
		return fmt.Sprintf("invalid delegate arguments: %v", err), true
	}

	// Validate requested subagent profile against available roles
	if delegationRequest.Agent != "researcher" {
		return fmt.Sprintf("unknown subagent profile: %s (available: researcher)", delegationRequest.Agent), true
	}

	fmt.Printf("[subagent] spawning %s subagent...\n", delegationRequest.Agent)

	// Construct a dedicated, isolated tool registry containing only read-only capabilities.
	// The researcher subagent is strictly forbidden from mutating files or executing arbitrary shell commands.
	researcherTools := tool.NewToolRegistry()
	if readFileTool, exists := tool.DefaultRegistry.Get("read_file"); exists {
		researcherTools.Register(readFileTool)
	}

	// Instantiate a fresh Agent struct. Its conversation history (messages) begins completely empty,
	// ensuring no memory cross-contamination with the parent agent's session.
	researcherSubagent := agent.New(agent.Agent{
		Name:      delegationRequest.Agent,
		Provider:  prov,            // Shared LLM provider connection
		Tools:     researcherTools, // Scoped read-only tool catalog
		MaxTurns:  10,              // Guard against runaway child loops
		Verbose:   false,           // Suppress raw token streaming to terminal to maintain clean UX
		LogPrefix: fmt.Sprintf("[%s]", delegationRequest.Agent),
	})

	// Run child agent loop to completion
	summaryResult, err := researcherSubagent.Run(ctx, delegationRequest.Prompt)
	if err != nil {
		return fmt.Sprintf("subagent execution error: %v", err), true
	}

	// Return synthesized summary back to parent agent as standard tool_result
	return summaryResult, false
}
