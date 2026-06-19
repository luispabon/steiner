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

// defaultChildSystemPrompt stays empty so child agents use the shared system
// preamble unless a spec provides an explicit override.
const defaultChildSystemPrompt = ""

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
	CaveHuman            bool
	// Sandbox is the parent sandbox to inherit. Child sandbox permissions cannot
	// exceed parent permissions: the parent sandbox is passed as-is to the child
	// executor. A nil Sandbox means unsafe mode (no sandboxing).
	Sandbox tool.SandboxWrapper
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, builds child prompt and tool registries, and returns the assembled
// request together with the computed DelegationLimits.
func BuildChildRun(ctx context.Context, deps BootstrapDeps, spec DelegationSpec) (agent.RunRequest, DelegationLimits, error) {
	if err := ctx.Err(); err != nil {
		return agent.RunRequest{}, DelegationLimits{}, err
	}
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

	promptOpts := buildChildPrompt(spec, deps.WorkDir, deps.HomeDir, deps.ProjectContextConfig, deps.CaveHuman)

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, deps.SubAgentCfg.AllowedTools)
	req := buildChildRunRequest(deps.WorkDir, spec.AgentID, deps.Provider, visibleReg, execReg, agentLimits, deps.Events, promptOpts, deps.ResolvedModel, modelBudget, deps.MaxTokens, deps.StreamingPreferred, deps.CaveHuman, deps.Sandbox)
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
// Specified system prompts are forwarded as prompt overrides; otherwise the
// shared system preamble is left intact. Project context (AGENTS.md,
// configured extra files) is included so child agents inherit project
// conventions without the parent forwarding them.
func buildChildPrompt(spec DelegationSpec, workDir, homeDir string, pcc config.ProjectContextConfig, caveHuman bool) prompt.AssemblyOptions {
	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	opts := prompt.AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               workDir,
		ProjectContextExtraFiles:  pcc.ExtraFiles,
		ProjectContextIgnoreFiles: pcc.IgnoreFiles,
		ProjectContextBudgetBytes: pcc.MaxTokens,
		CaveHuman:                 caveHuman,
		Conversation: []provider.Message{
			{Role: provider.MessageRoleUser, Content: taskContent},
		},
	}
	if spec.SystemPrompt != "" {
		opts.PromptOverrides.System = spec.SystemPrompt
	}

	return opts
}

// buildChildToolRegistry creates a new tool registry from the parent registry,
// excluding the tool named delegateToolName.
func buildChildToolRegistry(parent *tool.Registry, delegateToolName string) *tool.Registry {
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
	excluded := []string{DelegateToolName, FollowUpToolName, "workflow_handoff"}
	visible := parent.Subset(allowedTools, excluded)
	exec := parent.Subset(allowedTools, excluded)
	return visible, exec
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Registries and prompt must be provided pre-built; the caller (typically
// BuildChildRun) is responsible for registry and prompt assembly.
// sandbox is the parent's SandboxWrapper; if non-nil it is applied to the child
// executor unchanged, enforcing child sandbox ≤ parent sandbox.
func buildChildRunRequest(workDir string, agentID string, prov provider.Provider, visibleReg *tool.Registry, execReg *tool.Registry, baseLimits agent.Limits, events output.EventSink, promptOpts prompt.AssemblyOptions, rm provider.ResolvedModel, modelBudget prompt.ModelTokenBudget, maxTokens *int, streamingPreferred bool, caveHuman bool, sandbox tool.SandboxWrapper) agent.RunRequest {
	childCfg := config.Config{}
	scopedEvents := withAgentScope(agentID, events)

	exec := tool.NewExecutor(execReg, childCfg, nil, workDir)
	if sandbox != nil {
		exec = exec.WithSandbox(sandbox)
	}

	req := agent.RunRequest{
		Provider:           prov,
		Executor:           exec,
		Tools:              visibleReg.ToProviderSpecs(),
		Limits:             baseLimits,
		Events:             scopedEvents,
		Prompt:             promptOpts,
		ResolvedModel:      rm,
		ModelBudget:        modelBudget,
		MaxTokens:          maxTokens,
		StreamingPreferred: streamingPreferred,
		CaveHuman:          caveHuman,
	}

	return req
}

// scopedEventSink tags emitted child-run events with the child agent scope.
type scopedEventSink struct {
	sink    output.EventSink
	agentID string
}

func (s scopedEventSink) Emit(event output.Event) {
	if s.sink == nil {
		return
	}
	s.sink.Emit(output.WithAgentScope(event, s.agentID))
}

func withAgentScope(agentID string, sink output.EventSink) output.EventSink {
	if sink == nil || agentID == "" {
		return sink
	}
	return scopedEventSink{sink: sink, agentID: agentID}
}
