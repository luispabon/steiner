package delegation

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// BootstrapDeps holds the dependencies needed to assemble a child agent run request.
type BootstrapDeps struct {
	Provider    provider.Provider
	ParentReg   *tool.Registry
	SubAgentCfg config.SubAgentConfig
	Events      output.EventSink
	WorkDir     string
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, builds child prompt and tool registries, and returns the assembled
// request together with the computed DelegationLimits.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, DelegationLimits, error) {
	limits := deriveChildLimits(deps.SubAgentCfg, spec.Limits)
	agentLimits := agent.Limits{
		MaxTurns:  limits.MaxTurns,
		MaxTokens: limits.OutputLimitTokens,
	}

	promptOpts, err := buildChildPrompt(spec)
	if err != nil {
		return agent.RunRequest{}, DelegationLimits{}, fmt.Errorf("build child prompt: %w", err)
	}

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, delegateToolName)
	req := buildChildRunRequest(deps.WorkDir, spec, deps.Provider, visibleReg, execReg, agentLimits, deps.Events, promptOpts)
	return req, limits, nil
}

// deriveChildLimits combines SubAgentConfig defaults with overrides from the
// spec using tighten-only semantics. The returned Limits have all unset override
// fields filled from configuration defaults.
func deriveChildLimits(cfg config.SubAgentConfig, overrides DelegationLimits) DelegationLimits {
	base := DefaultLimits(cfg)
	return ApplyOverrides(base, overrides)
}

// buildChildPrompt assembles the prompt.AssemblyOptions for a child agent,
// including system prompt (from spec or default) and task with optional context.
func buildChildPrompt(spec DelegationSpec) (prompt.AssemblyOptions, error) {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a sub-agent. Complete the task given to you."
	}

	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	conversation := []provider.Message{
		{Role: provider.MessageRoleUser, Content: taskContent},
	}

	if systemPrompt != "" {
		conversation = append(
			[]provider.Message{{Role: provider.MessageRoleSystem, Content: systemPrompt}},
			conversation...,
		)
	}

	return prompt.AssemblyOptions{
		Conversation: conversation,
	}, nil
}

// buildChildRegistries produces both the visible tool registry (tools the model
// can request) and the execution registry (tools the child agent can actually
// execute) from the parent registry. The named tool (typically "delegate") is
// excluded from both. Execution tools are set to auto-approval mode.
func buildChildRegistries(parent *tool.Registry, excludeTool string) (*tool.Registry, *tool.Registry) {
	if parent == nil {
		empty := tool.NewRegistry()
		return empty, empty
	}

	defs := parent.Definitions()
	visibleDefs := make([]tool.ToolDef, 0, len(defs))
	execDefs := make([]tool.ToolDef, 0, len(defs))

	for _, def := range defs {
		if def.Name == excludeTool {
			continue
		}
		execDef := def
		execDef.Approval = config.ApprovalModeAuto
		visibleDefs = append(visibleDefs, def)
		execDefs = append(execDefs, execDef)
	}

	return tool.NewRegistry(visibleDefs...), tool.NewRegistry(execDefs...)
}
