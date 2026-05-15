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
	Provider           provider.Provider
	ParentReg          *tool.Registry
	SubAgentCfg        config.SubAgentConfig
	Events             output.EventSink
	WorkDir            string
	ExtraParams        map[string]any
	Thinking           config.ThinkingConfig
	ModelBudget        prompt.ModelTokenBudget
	Model              string
	MaxTokens          *int
	StreamingPreferred bool
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, builds child prompt and tool registries, and returns the assembled
// request together with the computed DelegationLimits.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, DelegationLimits, error) {
	_ = ctx
	limits := deriveChildLimits(deps.SubAgentCfg, spec.Limits)
	agentLimits := agent.Limits{
		MaxTurns:  limits.MaxTurns,
		MaxTokens: limits.OutputLimitTokens,
	}

	promptOpts := buildChildPrompt(spec)

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, deps.SubAgentCfg.AllowedTools)
	req := buildChildRunRequest(deps.WorkDir, spec, deps.Provider, visibleReg, execReg, agentLimits, deps.Events, promptOpts, deps.ExtraParams, deps.Thinking, deps.ModelBudget, deps.Model, deps.MaxTokens, deps.StreamingPreferred)
	return req, limits, nil
}

// deriveChildLimits combines SubAgentConfig defaults with overrides from the
// spec using tighten-only semantics. The returned Limits have all unset override
// fields filled from configuration defaults.
func deriveChildLimits(cfg config.SubAgentConfig, overrides DelegationLimits) DelegationLimits {
	base := DefaultLimits(cfg)
	return ApplyOverrides(base, overrides)
}

// buildChildPrompt assembles the prompt.AssemblyOptions for a child agent.
// The child system prompt is supplied as the preamble override instead of a
// conversation message so the assembled provider request has one system message.
func buildChildPrompt(spec DelegationSpec) prompt.AssemblyOptions {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a sub-agent. Complete the task given to you.\n\nUse the scratchpad tool to record your findings as you go. Update it after each significant discovery — do not wait until the end to synthesize. Your work may be interrupted at any time; only findings recorded in scratchpad are guaranteed to survive."
	}

	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	return prompt.AssemblyOptions{
		PromptOverrides: config.ModelPrompts{System: systemPrompt},
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: taskContent},
		},
	}
}

// buildChildRegistries produces both the visible tool registry (tools the model
// can request) and the execution registry (tools the child agent can actually
// execute) from the parent registry. The named tool (typically "delegate") is
// always excluded from both, even if it appears in allowedTools. If allowedTools
// is non-empty, only listed tools are included. If empty, no tools are included.
// Execution tools are set to auto-approval mode.
func buildChildRegistries(parent *tool.Registry, allowedTools []string) (*tool.Registry, *tool.Registry) {
	if parent == nil {
		empty := tool.NewRegistry()
		return empty, empty
	}
	visible := parent.Subset(allowedTools, []string{DelegateToolName}, "")
	exec := parent.Subset(allowedTools, []string{DelegateToolName}, config.ApprovalModeAuto)
	return visible, exec
}
