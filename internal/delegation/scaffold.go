package delegation

import (
	"context"
	"fmt"
	"os"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
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

func childProviderTools(reg *tool.Registry) []provider.ToolSpec {
	if reg == nil {
		return nil
	}

	defs := reg.Definitions()
	tools := make([]provider.ToolSpec, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, provider.ToolSpec{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.ParameterSchema,
			},
		})
	}
	return tools
}

// BuildChildRunRequest assembles the agent.RunRequest for a child delegation.
func BuildChildRunRequest(spec DelegationSpec, prov provider.Provider, childReg *tool.Registry, baseLimits agent.Limits, events output.EventSink) agent.RunRequest {
	childCtx, _ := ScaffoldChildContext(context.Background(), spec)
	visibleReg := BuildChildToolRegistry(childReg, DelegateToolName)
	executionReg := buildChildExecutionRegistry(visibleReg)

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

	// Set the system prompt as a preamble via a pre-populated conversation message.
	if childCtx.SystemPrompt != "" {
		assemblyOpts.Conversation = append(
			[]provider.Message{{Role: provider.MessageRoleSystem, Content: childCtx.SystemPrompt}},
			conversation...,
		)
	}

	workDir, _ := os.Getwd()
	childCfg := config.Config{Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto}}

	req := agent.RunRequest{
		Provider: prov,
		Executor: tool.NewExecutor(executionReg, childCfg, nil, workDir),
		Tools:    childProviderTools(visibleReg),
		Model:    spec.Model,
		Limits:   baseLimits,
		Events:   events,
		Prompt:   assemblyOpts,
	}

	return req
}
