package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// ChildContext contains the system prompt and project context for a child agent.
type ChildContext struct {
	// SystemPrompt is the system message for the child agent.
	SystemPrompt string

	// ProjectContext is optional additional project context.
	ProjectContext string
}

// ScaffoldChildContext builds the system and project context for a delegated child agent.
// No parent transcript is included. System prompt comes from spec.SystemPrompt or a default.
func ScaffoldChildContext(ctx context.Context, spec DelegationSpec) (ChildContext, error) {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a sub-agent. Complete the task given to you."
	}

	return ChildContext{
		SystemPrompt:   systemPrompt,
		ProjectContext: spec.Context,
	}, nil
}

// BuildChildToolRegistry creates a new tool registry from the parent registry,
// excluding the tool named delegateToolName.
func BuildChildToolRegistry(parent *tool.Registry, delegateToolName string) *tool.Registry {
	if parent == nil {
		return tool.NewRegistry()
	}

	parentDefs := parent.Definitions()
	childDefs := make([]tool.ToolDef, 0, len(parentDefs))

	for _, def := range parentDefs {
		if def.Name != delegateToolName {
			childDefs = append(childDefs, def)
		}
	}

	return tool.NewRegistry(childDefs...)
}

// noopExecutor is a ToolExecutor that returns an error for every tool call.
// Used in child agents that have no external tools in the scaffold stage.
type noopExecutor struct{ reg *tool.Registry }

func (n *noopExecutor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	return nil, fmt.Errorf("tool %q is not available in sub-agent context", toolName)
}

// BuildChildRunRequest assembles the agent.RunRequest for a child delegation.
func BuildChildRunRequest(spec DelegationSpec, prov provider.Provider, childReg *tool.Registry, baseLimits agent.Limits, events output.EventSink) agent.RunRequest {
	childCtx, _ := ScaffoldChildContext(context.Background(), spec)

	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	conversation := []provider.Message{
		{Role: provider.MessageRoleUser, Content: taskContent},
	}

	assemblyOpts := prompt.AssemblyOptions{
		Conversation: conversation,
	}

	// Set the system prompt as a preamble via a pre-populated conversation message
	// Since AssemblyOptions has no Preamble field, inject system prompt as first user
	// message prefix. Instead, we prepend it to the conversation as a system message.
	if childCtx.SystemPrompt != "" {
		assemblyOpts.Conversation = append(
			[]provider.Message{{Role: provider.MessageRoleUser, Content: childCtx.SystemPrompt}},
			conversation...,
		)
		// Actually, put system prompt as its own system-role message first
		assemblyOpts.Conversation = append(
			[]provider.Message{{Role: provider.MessageRoleSystem, Content: childCtx.SystemPrompt}},
			conversation...,
		)
	}

	req := agent.RunRequest{
		Provider: prov,
		Executor: &noopExecutor{reg: childReg},
		Tools:    nil,
		Model:    spec.Model,
		Limits:   baseLimits,
		Events:   events,
		Prompt:   assemblyOpts,
	}

	return req
}
