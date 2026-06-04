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

const defaultChildSystemPrompt = "You are a sub-agent. Complete the task given to you."

// BootstrapDeps holds the dependencies needed to assemble a child agent run request.
type BootstrapDeps struct {
	Provider             provider.Provider
	ParentReg            *tool.Registry
	SubAgentCfg          config.SubAgentConfig
	Events               output.EventSink
	WorkDir              string
	HomeDir              string
	ProjectContextConfig config.ProjectContextConfig
	ResolvedModel        provider.ResolvedModel
	MaxTokens            *int
	StreamingPreferred   bool
	CavemanMode          bool
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, builds child prompt and tool registries, and returns the assembled
// request together with the computed DelegationLimits.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, DelegationLimits, error) {
	_ = ctx
	limits := deriveChildLimits(deps.SubAgentCfg, spec.Limits)
	agentLimits := agent.Limits{
		MaxTurns:    limits.MaxTurns,
		MaxTokens:   limits.OutputLimitTokens,
		TurnTimeout: limits.Timeout,
	}

	modelBudget := prompt.ModelTokenBudget{
		ContextSize:               deps.ResolvedModel.EffectiveLimits.ContextWindow,
		MaxCompletionTokens:       deps.ResolvedModel.EffectiveLimits.MaxOutputTokens,
		SafetyMarginTokens:        deps.ResolvedModel.EffectiveLimits.EstimatorPadTokens,
		SummaryMaxTokens:          deps.ResolvedModel.EffectiveLimits.NormalSummaryMaxTokens,
		NormalSummaryMaxTokens:    deps.ResolvedModel.EffectiveLimits.NormalSummaryMaxTokens,
		EmergencySummaryMaxTokens: deps.ResolvedModel.EffectiveLimits.EmergencySummaryMaxTokens,
	}

	promptOpts := buildChildPrompt(spec, deps.WorkDir, deps.HomeDir, deps.ProjectContextConfig, deps.CavemanMode)

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, deps.SubAgentCfg.AllowedTools)
	req := buildChildRunRequest(deps.WorkDir, spec, deps.Provider, visibleReg, execReg, agentLimits, deps.Events, promptOpts, deps.ResolvedModel, modelBudget, deps.MaxTokens, deps.StreamingPreferred, deps.CavemanMode)
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
// Project context (AGENTS.md, configured extra files) is included so child
// agents inherit project conventions without the parent forwarding them.
func buildChildPrompt(spec DelegationSpec, workDir, homeDir string, pcc config.ProjectContextConfig, cavemanMode bool) prompt.AssemblyOptions {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = defaultChildSystemPrompt
	}

	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	return prompt.AssemblyOptions{
		PromptOverrides:           config.ModelPrompts{System: systemPrompt},
		HomeDir:                   homeDir,
		ProjectRoot:               workDir,
		ProjectContextExtraFiles:  pcc.ExtraFiles,
		ProjectContextIgnoreFiles: pcc.IgnoreFiles,
		ProjectContextBudgetBytes: pcc.MaxTokens,
		CavemanMode:               cavemanMode,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: taskContent},
		},
	}
}

// buildChildRegistries produces both the visible tool registry (tools the model
// can request) and the execution registry (tools the child agent can actually
// execute) from the parent registry. Delegation control tools are always
// excluded from both, even if they appear in allowedTools. If allowedTools is
// non-empty, only listed tools are included. If empty, no tools are included.
// Execution tools are set to auto-approval mode.
func buildChildRegistries(parent *tool.Registry, allowedTools []string) (*tool.Registry, *tool.Registry) {
	if parent == nil {
		empty := tool.NewRegistry()
		return empty, empty
	}
	excluded := []string{DelegateToolName, FollowUpToolName}
	visible := parent.Subset(allowedTools, excluded)
	exec := parent.Subset(allowedTools, excluded)
	return visible, exec
}
