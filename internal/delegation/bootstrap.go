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

	childReg := BuildChildToolRegistry(deps.ParentReg, delegateToolName)
	req := buildChildRunRequest(spec, deps.Provider, childReg, agentLimits, deps.Events, promptOpts)
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
	childCtx, err := scaffoldChildContext(context.Background(), spec)
	if err != nil {
		return prompt.AssemblyOptions{}, fmt.Errorf("scaffold child context: %w", err)
	}

	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	conversation := []provider.Message{
		{Role: provider.MessageRoleUser, Content: taskContent},
	}

	if childCtx.SystemPrompt != "" {
		conversation = append(
			[]provider.Message{{Role: provider.MessageRoleSystem, Content: childCtx.SystemPrompt}},
			conversation...,
		)
	}

	return prompt.AssemblyOptions{
		Conversation: conversation,
	}, nil
}
