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

// ChildBootstrapOverrides holds the values BuildChildRun needs beyond
// SubAgentHandlerDeps: the model resolved for this specific delegation
// (which may differ from anything in deps — deps.Provider/deps.ResolvedModel
// are never read by BuildChildRun; use Provider/ResolvedModel here instead),
// which agent type is being bootstrapped, and which tools it may use.
type ChildBootstrapOverrides struct {
	AgentType     AgentType
	AllowedTools  []string
	Provider      provider.Provider
	ResolvedModel provider.ResolvedModel

	// ProjectRoot is the parent's actual project directory, used only for
	// placing host-side artifacts (e.g. tool-call trace files) that must
	// never land inside a code agent's isolated git worktree. It is distinct
	// from deps.WorkDir, which specializedBootstrapDeps overrides to the
	// worktree path for AgentTypeCode before calling BuildChildRun. Callers
	// that never override WorkDir (vision, non-code specialized agents) may
	// leave this unset; BuildChildRun falls back to deps.WorkDir.
	ProjectRoot string
}

// childContextSkips reports which context sections a delegated child of the
// given agent type should skip. Explore, research, and sanity-check children
// are lean and don't need project-level conventions; vision children can't
// read the repo at all, so they skip both.
func childContextSkips(agentType AgentType) (skipProjectContext, skipAgents bool) {
	skipProjectContext = agentType != AgentTypeCode && agentType != AgentTypeReview && agentType != AgentTypeEvaluate
	skipAgents = agentType == AgentTypeVision
	return
}

// BuildChildRun assembles a complete agent.RunRequest for a delegated child agent.
// It derives final limits by combining SubAgentConfig defaults with spec-level
// overrides, builds child prompt and tool registries, and returns the assembled
// request together with the computed Limits.
func BuildChildRun(ctx context.Context, deps SubAgentHandlerDeps, override ChildBootstrapOverrides, spec Spec) (agent.RunRequest, Limits, error) {
	if err := ctx.Err(); err != nil {
		return agent.RunRequest{}, Limits{}, err
	}
	limits := deriveChildLimits(deps.SubAgentCfg, spec.Limits)
	skipProjectContext, skipAgents := childContextSkips(override.AgentType)
	agentLimits := agent.Limits{
		MaxTurns:    limits.MaxTurns,
		MaxTokens:   limits.OutputLimitTokens,
		TurnTimeout: limits.Timeout,
	}

	modelBudget := prompt.ModelTokenBudget{
		ContextSize:               override.ResolvedModel.EffectiveLimits.ContextWindow,
		MaxCompletionTokens:       override.ResolvedModel.EffectiveLimits.MaxOutputTokens,
		SafetyMarginTokens:        override.ResolvedModel.EffectiveLimits.EstimatorPadTokens,
		SummaryMaxTokens:          override.ResolvedModel.EffectiveLimits.NormalSummaryMaxTokens,
		NormalSummaryMaxTokens:    override.ResolvedModel.EffectiveLimits.NormalSummaryMaxTokens,
		EmergencySummaryMaxTokens: override.ResolvedModel.EffectiveLimits.EmergencySummaryMaxTokens,
	}

	promptOpts := buildChildPrompt(childPromptParams{
		spec:               spec,
		workDir:            deps.WorkDir,
		homeDir:            deps.HomeDir,
		projectContextCfg:  deps.ProjectContextConfig,
		caveHuman:          deps.CaveHuman,
		skipProjectContext: skipProjectContext,
		skipAgents:         skipAgents,
		sandboxEnabled:     deps.SandboxEnabled,
		writableMounts:     deps.SandboxWritableMounts,
	})

	visibleReg, execReg := buildChildRegistries(deps.ParentReg, override.AllowedTools)
	readOnlyBash := deps.SandboxEnabled && override.AgentType == AgentTypeExplore
	traceRoot := override.ProjectRoot
	if traceRoot == "" {
		traceRoot = deps.WorkDir
	}
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:            deps.WorkDir,
		TraceRoot:          traceRoot,
		AgentID:            spec.AgentID,
		SessionID:          deps.SessionID,
		Provider:           override.Provider,
		VisibleReg:         visibleReg,
		ExecReg:            execReg,
		BaseLimits:         agentLimits,
		Events:             deps.Events,
		PromptOpts:         promptOpts,
		ResolvedModel:      override.ResolvedModel,
		ModelBudget:        modelBudget,
		MaxTokens:          deps.MaxTokens,
		StreamingPreferred: deps.StreamingPreferred,
		UsageRecorder:      deps.UsageRecorder,
		ModeGetter:         deps.ModeGetter,
		AgentType:          override.AgentType,
		CacheKeyStore:      deps.CacheKeyStore,
		SandboxTmpDir:      deps.SandboxTmpDir,
		Sandbox:            deps.Sandbox,
		ReadOnlyBash:       readOnlyBash,
		MaxParallelTools:   deps.MaxParallelTools,
		Limits:             deps.Limits,
		Paths:              deps.Paths,
		ContextManagement:  deps.ContextManagement,
	})
	return req, limits, nil
}

// deriveChildLimits combines SubAgentConfig defaults with overrides from the
// spec using tighten-only semantics. The returned Limits have all unset override
// fields filled from configuration defaults.
func deriveChildLimits(cfg config.SubAgentConfig, overrides Limits) Limits {
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
	if p.spec.SystemSuffix != "" {
		opts.PromptOverrides.SystemSuffix = p.spec.SystemSuffix
	}

	return opts
}

// childPromptParams holds the arguments for buildChildPrompt. It exists to
// avoid a long positional parameter list where adjacent same-typed values
// (e.g. multiple bools) could be transposed without compiler protection.
type childPromptParams struct {
	spec               Spec
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
	WorkDir string
	// TraceRoot is where host-side tool-call trace files are written. It
	// equals WorkDir except for AgentTypeCode, where WorkDir is the isolated
	// worktree path and TraceRoot stays anchored to the parent's actual
	// project directory so trace files never appear as untracked changes
	// inside the child's git checkout.
	TraceRoot          string
	AgentID            string
	SessionID          string
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
	SandboxTmpDir      string
	Sandbox            tool.SandboxWrapper
	ReadOnlyBash       bool
	MaxParallelTools   int
	// Limits and Paths configure the child's tool executor (output byte cap
	// and path policy) the same way the parent's own executor is configured;
	// previously the child executor was built from a zero-value config.Config
	// and silently ignored both.
	Limits config.LimitsConfig
	Paths  config.PathsConfig
	// ContextManagement configures the child's ContextStateManager the same
	// way the parent's is configured; previously no ContextManager was set on
	// the child request, so it always ran with library defaults regardless of
	// configured context_management settings.
	ContextManagement config.ContextManagementConfig
}

// buildChildRunRequest assembles the agent.RunRequest for a child delegation.
// Registries and prompt must be provided pre-built; the caller (typically
// BuildChildRun) is responsible for registry and prompt assembly.
// p.ModeGetter, when non-nil, is wired into the child executor so it inherits
// the parent's execution mode.
func buildChildRunRequest(p childRunRequestParams) agent.RunRequest {
	childCfg := config.Config{Limits: p.Limits, Paths: p.Paths}
	scopedEvents := withAgentScope(p.AgentID, p.AgentType, p.Events)
	traceWriter := newToolCallTraceWriter(p.TraceRoot, p.AgentID, p.SessionID)
	registerToolCallTraceWriter(p.AgentID, traceWriter)
	scopedEvents = withToolCallTrace(scopedEvents, traceWriter)

	// p.Sandbox must not be nil: the composition root (cmd/steiner) always
	// passes an explicit wrapper (tool.Unsandboxed{} when sandboxing is off).
	// No silent fallback here — an omitted Sandbox is a caller bug, not a
	// default to paper over.
	exec := tool.NewExecutor(p.ExecReg, childCfg, nil, p.WorkDir, p.SandboxTmpDir, p.Sandbox)
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
	childCacheKey := cacheKeyOrMint(p.CacheKeyStore, p.AgentType)

	req := agent.RunRequest{
		Provider:           p.Provider,
		Executor:           scopedToolExecutor{inner: exec, agentID: p.AgentID, readOnlyBash: p.ReadOnlyBash},
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
		ContextManager:     agent.NewContextStateManager(p.ContextManagement),
	}
	if p.UsageRecorder != nil {
		req.UsageRecorder = p.UsageRecorder
	}
	req.ParallelTool = func(name string) bool {
		return p.ExecReg.IsParallelSafe(name)
	}
	req.MaxParallelTools = p.MaxParallelTools

	return req
}

// scopedEventSink tags emitted child-run events with the child agent scope.
type scopedEventSink struct {
	sink      output.EventSink
	agentID   string
	agentType string
}

func (s scopedEventSink) Emit(event output.Event) {
	if s.sink == nil {
		return
	}
	event = output.WithAgentScope(event, s.agentID)
	event = output.WithAgentTypeScope(event, s.agentType)
	s.sink.Emit(event)
}

func withAgentScope(agentID string, agentType AgentType, sink output.EventSink) output.EventSink {
	if sink == nil || agentID == "" {
		return sink
	}
	return scopedEventSink{sink: sink, agentID: agentID, agentType: string(agentType)}
}

// scopedToolExecutor injects the child agent scope into the tool execution
// context so approval events emitted by tool handlers (e.g. MCP tools) during
// a delegated child run carry the child's agent ID.
type scopedToolExecutor struct {
	inner        agent.ToolExecutor
	agentID      string
	readOnlyBash bool
}

func (e scopedToolExecutor) Execute(ctx context.Context, toolName, callID string, input map[string]any) (any, error) {
	if e.readOnlyBash {
		ctx = tool.WithReadOnlyProjectBash(ctx, true)
	}
	return e.inner.Execute(tool.WithApprovalAgentScope(ctx, e.agentID), toolName, callID, input)
}
