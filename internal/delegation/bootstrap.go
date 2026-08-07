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
	"github.com/luispabon/steiner/internal/usagestats"
)

// defaultChildSystemPrompt stays empty so child agents use the shared system
// preamble unless a spec provides an explicit override.
const defaultChildSystemPrompt = ""

// BootstrapDeps holds the dependencies needed to assemble a child agent run request.
type BootstrapDeps struct {
	Provider             provider.Provider
	ParentReg            *tool.Registry
	SubAgentCfg          config.SubAgentConfig
	AllowedTools         []string
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
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
	// ModeGetter returns the current execution mode. When non-nil, child executors
	// receive this getter via WithModeGetter so they inherit the parent's execution mode.
	ModeGetter func() config.ExecutionMode

	// SkipProjectContext skips AGENTS.md and project context files in the child
	// prompt. Used for lean sub-agents (explore, research, sanity_check, vision)
	// that don't need project-level conventions.
	SkipProjectContext bool
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

	promptOpts := buildChildPrompt(spec, deps.WorkDir, deps.HomeDir, deps.ProjectContextConfig, deps.CaveHuman, deps.SkipProjectContext)

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, deps.AllowedTools)
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:            deps.WorkDir,
		AgentID:            spec.AgentID,
		Provider:           deps.Provider,
		VisibleReg:         visibleReg,
		ExecReg:            execReg,
		BaseLimits:         agentLimits,
		Events:             deps.Events,
		PromptOpts:         promptOpts,
		ResolvedModel:      deps.ResolvedModel,
		ModelBudget:        modelBudget,
		MaxTokens:          deps.MaxTokens,
		StreamingPreferred: deps.StreamingPreferred,
		Sandbox:            deps.Sandbox,
		UsageRecorder:      deps.UsageRecorder,
		ModeGetter:         deps.ModeGetter,
	})
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
func buildChildPrompt(spec DelegationSpec, workDir, homeDir string, pcc config.ProjectContextConfig, caveHuman bool, skipProjectContext bool) prompt.AssemblyOptions {
	taskContent := spec.Task
	if spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", spec.Task, spec.Context)
	}

	msg := provider.Message{Role: provider.MessageRoleUser, Content: taskContent}
	if len(spec.Images) > 0 {
		msg.Images = spec.Images
	}

	opts := prompt.AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               workDir,
		ProjectContextExtraFiles:  pcc.ExtraFiles,
		ProjectContextIgnoreFiles: pcc.IgnoreFiles,
		ProjectContextBudgetBytes: pcc.MaxTokens,
		SkipProjectContext:        skipProjectContext,
		CaveHuman:                 caveHuman,
		WorkflowMode:              prompt.DelegatedChildWorkflowMode(),
		Conversation: []provider.Message{
			msg,
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
	excluded := []string{FollowUpToolName, "workflow_handoff"}
	visible := parent.Subset(allowedTools, excluded)
	exec := parent.Subset(allowedTools, excluded)
	return visible, exec
}

// childRunRequestParams holds the arguments needed to assemble a child
// agent.RunRequest. It exists to avoid a long positional parameter list where
// adjacent same-typed values (e.g. two bools, two *tool.Registry) could be
// transposed without compiler protection.
type childRunRequestParams struct {
	WorkDir            string
	AgentID            string
	Provider           provider.Provider
	VisibleReg         *tool.Registry
	ExecReg            *tool.Registry
	BaseLimits         agent.Limits
	Events             output.EventSink
	PromptOpts         prompt.AssemblyOptions
	ResolvedModel      provider.ResolvedModel
	ModelBudget        prompt.ModelTokenBudget
	MaxTokens          *int
	StreamingPreferred bool
	Sandbox            tool.SandboxWrapper
	UsageRecorder      *usagestats.Recorder
	ModeGetter         func() config.ExecutionMode
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Registries and prompt must be provided pre-built; the caller (typically
// BuildChildRun) is responsible for registry and prompt assembly.
// p.Sandbox is the parent's SandboxWrapper; if non-nil it is applied to the child
// executor unchanged, enforcing child sandbox ≤ parent sandbox.
// p.ModeGetter, when non-nil, is wired into the child executor so it inherits
// the parent's execution mode.
func buildChildRunRequest(p childRunRequestParams) agent.RunRequest {
	childCfg := config.Config{}
	scopedEvents := withAgentScope(p.AgentID, p.Events)

	sandboxTmpDir := ""
	if p.Sandbox != nil && p.Sandbox.Enabled() {
		sandboxTmpDir = p.Sandbox.TmpDir()
	}
	exec := tool.NewExecutor(p.ExecReg, childCfg, nil, p.WorkDir, sandboxTmpDir)
	if p.Sandbox != nil {
		exec = exec.WithSandbox(p.Sandbox)
	}
	if p.ModeGetter != nil {
		exec = exec.WithModeGetter(p.ModeGetter)
	}

	// A fresh per-run cache key, distinct from the parent's, keeps sub-agent
	// traffic off the parent conversation's cache shard. An entropy error leaves
	// it empty, which simply disables provider-side caching for this child run
	// rather than failing bootstrap.
	childCacheKey, _ := provider.NewPromptCacheKey()

	req := agent.RunRequest{
		Provider:           p.Provider,
		Executor:           scopedToolExecutor{inner: exec, agentID: p.AgentID},
		Tools:              p.VisibleReg.ToProviderSpecs(),
		Limits:             p.BaseLimits,
		Events:             scopedEvents,
		Prompt:             p.PromptOpts,
		ResolvedModel:      p.ResolvedModel,
		ModelBudget:        p.ModelBudget,
		MaxTokens:          p.MaxTokens,
		StreamingPreferred: p.StreamingPreferred,
		CaveHuman:          p.PromptOpts.CaveHuman,
		PromptCacheKey:     childCacheKey,
	}
	if p.UsageRecorder != nil {
		req.UsageRecorder = p.UsageRecorder
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

// scopedToolExecutor injects the child agent scope into the tool execution
// context so approval events emitted by tool handlers (e.g. MCP tools) during
// a delegated child run carry the child's agent ID.
type scopedToolExecutor struct {
	inner   agent.ToolExecutor
	agentID string
}

func (e scopedToolExecutor) Execute(ctx context.Context, toolName, callID string, input map[string]any) (any, error) {
	return e.inner.Execute(tool.WithApprovalAgentScope(ctx, e.agentID), toolName, callID, input)
}
