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
	// SandboxEnabled reports whether the parent sandbox is active. Forwarded as
	// a plain value into the child prompt so its system preamble renders the
	// same sandbox section as the parent when the sandbox is active.
	SandboxEnabled bool
	// SandboxWritableMounts lists host paths mounted writable in the sandbox,
	// rendered into the child system preamble when the sandbox is enabled.
	// Derived from the parent's config at the composition root; paths are
	// already home-expanded at config load.
	SandboxWritableMounts []string
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
	// AgentType identifies the agent type being bootstrapped, used to key
	// CacheKeyStore lookups for prompt-cache key reuse.
	AgentType AgentType
	// CacheKeyStore, when non-nil, is consulted to reuse a prompt-cache key
	// across delegations of the same AgentType. Nil means always mint a fresh
	// key, preserving pre-Fix-3 behavior; this matters for tests and any
	// caller that doesn't wire a store.
	CacheKeyStore *CacheKeyStore
	// ModeGetter returns the current execution mode. When non-nil, child executors
	// receive this getter via WithModeGetter so they inherit the parent's execution mode.
	ModeGetter func() config.ExecutionMode

	// SkipProjectContext skips only the project-context extra files in the child
	// prompt. AGENTS.md delivery is controlled separately by SkipAgents.
	// Used for lean sub-agents (explore, research, sanity_check, vision)
	// that don't need project-level conventions.
	SkipProjectContext bool
	// SkipAgents skips AGENTS.md delivery in the child prompt. Used for
	// sub-agents that cannot read the repo (vision).
	SkipAgents bool
}

// handlerBootstrapDeps maps the SubAgentHandlerDeps fields shared by all
// specialized handlers into a BootstrapDeps, keeping per-type divergent values
// as explicit parameters so field additions to the shared set touch one place.
func handlerBootstrapDeps(agentType AgentType, deps SubAgentHandlerDeps, resolvedProvider provider.Provider, resolvedModel provider.ResolvedModel, allowedTools []string, skipProjectContext, skipAgents bool) BootstrapDeps {
	return BootstrapDeps{
		Provider:              resolvedProvider,
		ParentReg:             deps.ParentReg,
		SubAgentCfg:           deps.SubAgentCfg,
		AllowedTools:          allowedTools,
		Events:                deps.Events,
		WorkDir:               deps.WorkDir,
		HomeDir:               deps.HomeDir,
		ProjectContextConfig:  deps.ProjectContextConfig,
		ResolvedModel:         resolvedModel,
		MaxTokens:             deps.MaxTokens,
		StreamingPreferred:    deps.StreamingPreferred,
		CaveHuman:             deps.CaveHuman,
		SandboxEnabled:        deps.SandboxEnabled,
		SandboxWritableMounts: deps.SandboxWritableMounts,
		UsageRecorder:         deps.UsageRecorder,
		SkipProjectContext:    skipProjectContext,
		SkipAgents:            skipAgents,
		AgentType:             agentType,
		CacheKeyStore:         deps.CacheKeyStore,
	}
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

	promptOpts := buildChildPrompt(childPromptParams{
		spec:               spec,
		workDir:            deps.WorkDir,
		homeDir:            deps.HomeDir,
		projectContextCfg:  deps.ProjectContextConfig,
		caveHuman:          deps.CaveHuman,
		skipProjectContext: deps.SkipProjectContext,
		skipAgents:         deps.SkipAgents,
		sandboxEnabled:     deps.SandboxEnabled,
		writableMounts:     deps.SandboxWritableMounts,
	})

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
		UsageRecorder:      deps.UsageRecorder,
		ModeGetter:         deps.ModeGetter,
		AgentType:          deps.AgentType,
		CacheKeyStore:      deps.CacheKeyStore,
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
func buildChildPrompt(p childPromptParams) prompt.AssemblyOptions {
	taskContent := p.spec.Task
	if p.spec.Context != "" {
		taskContent = fmt.Sprintf("%s\n\nAdditional context:\n%s", p.spec.Task, p.spec.Context)
	}

	msg := provider.Message{Role: provider.MessageRoleUser, Content: taskContent}
	if len(p.spec.Images) > 0 {
		msg.Images = p.spec.Images
	}

	opts := prompt.AssemblyOptions{
		HomeDir:                   p.homeDir,
		ProjectRoot:               p.workDir,
		ProjectContextExtraFiles:  p.projectContextCfg.ExtraFiles,
		ProjectContextIgnoreFiles: p.projectContextCfg.IgnoreFiles,
		ProjectContextBudgetBytes: p.projectContextCfg.MaxBytes,
		SkipProjectContext:        p.skipProjectContext,
		SkipAgents:                p.skipAgents,
		CaveHuman:                 p.caveHuman,
		SandboxEnabled:            p.sandboxEnabled,
		SandboxWritableMounts:     append([]string(nil), p.writableMounts...),
		WorkflowMode:              prompt.DelegatedChildWorkflowMode(),
		Conversation: []provider.Message{
			msg,
		},
	}
	if p.spec.SystemPrompt != "" {
		opts.PromptOverrides.System = p.spec.SystemPrompt
	}

	return opts
}

// childPromptParams holds the arguments for buildChildPrompt. It exists to
// avoid a long positional parameter list where adjacent same-typed values
// (e.g. multiple bools) could be transposed without compiler protection.
type childPromptParams struct {
	spec               DelegationSpec
	workDir            string
	homeDir            string
	projectContextCfg  config.ProjectContextConfig
	caveHuman          bool
	skipProjectContext bool
	skipAgents         bool
	sandboxEnabled     bool
	writableMounts     []string
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
	UsageRecorder      *usagestats.Recorder
	ModeGetter         func() config.ExecutionMode
	AgentType          AgentType
	CacheKeyStore      *CacheKeyStore
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Registries and prompt must be provided pre-built; the caller (typically
// BuildChildRun) is responsible for registry and prompt assembly.
// p.ModeGetter, when non-nil, is wired into the child executor so it inherits
// the parent's execution mode.
func buildChildRunRequest(p childRunRequestParams) agent.RunRequest {
	childCfg := config.Config{}
	scopedEvents := withAgentScope(p.AgentID, p.Events)

	exec := tool.NewExecutor(p.ExecReg, childCfg, nil, p.WorkDir, "")
	if p.ModeGetter != nil {
		exec = exec.WithModeGetter(p.ModeGetter)
	}

	// Same-type sub-agent delegations reuse one cache key per AgentType within
	// the process lifetime when p.CacheKeyStore is provided, still isolated
	// from the parent's own key and from other agent types' keys. When no
	// store is provided, or the store fails to produce a usable key, a fresh
	// per-run key is minted instead; an entropy error leaves it empty, which
	// simply disables provider-side caching for this child run rather than
	// failing bootstrap.
	var childCacheKey string
	if p.CacheKeyStore != nil {
		childCacheKey, _ = p.CacheKeyStore.KeyFor(p.AgentType, provider.NewPromptCacheKey)
	}
	if childCacheKey == "" {
		childCacheKey, _ = provider.NewPromptCacheKey()
	}

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
		UsageSource:        usagestats.SourceSubAgent,
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
