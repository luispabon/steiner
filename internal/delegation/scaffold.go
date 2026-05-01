package delegation

import (
	"context"
	"os"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// childContext contains the system prompt and project context for a child agent.
type childContext struct {
	// SystemPrompt is the system message for the child agent.
	SystemPrompt string

	// ProjectContext is optional additional project context.
	ProjectContext string
}

// scaffoldChildContext builds the system and project context for a delegated child agent.
// No parent transcript is included. System prompt comes from spec.SystemPrompt or a default.
func scaffoldChildContext(ctx context.Context, spec DelegationSpec) (childContext, error) {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a sub-agent. Complete the task given to you."
	}

	return childContext{
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

func buildChildExecutionRegistry(parent *tool.Registry) *tool.Registry {
	if parent == nil {
		return tool.NewRegistry()
	}

	defs := parent.Definitions()
	for i := range defs {
		defs[i].Approval = config.ApprovalModeAuto
	}
	return tool.NewRegistry(defs...)
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Prompt options must be provided pre-built; the caller (typically BuildChildRun)
// is responsible for prompt assembly.
func buildChildRunRequest(spec DelegationSpec, prov provider.Provider, childReg *tool.Registry, baseLimits agent.Limits, events output.EventSink, promptOpts prompt.AssemblyOptions) agent.RunRequest {
	visibleReg := BuildChildToolRegistry(childReg, delegateToolName)
	executionReg := buildChildExecutionRegistry(visibleReg)

	workDir, _ := os.Getwd()
	childCfg := config.Config{Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto}}

	req := agent.RunRequest{
		Provider: prov,
		Executor: tool.NewExecutor(executionReg, childCfg, nil, workDir),
		Tools:    visibleReg.ToProviderSpecs(),
		Model:    spec.Model,
		Limits:   baseLimits,
		Events:   events,
		Prompt:   promptOpts,
	}

	return req
}
