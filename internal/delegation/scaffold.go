package delegation

import (
	"context"

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
