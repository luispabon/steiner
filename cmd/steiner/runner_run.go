package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
)

var fallbackWarningModels sync.Map

type runnerSetup struct {
	resolvedModel provider.ResolvedModel
	// baseResolvedModel is the config-resolved parent model before any session-time
	// reasoning override was applied. It is passed to delegation so sub-agents
	// falling back to the parent's model inherit the model's configured reasoning
	// default rather than the orchestrator's runtime override (issue #543).
	baseResolvedModel provider.ResolvedModel
	provider          provider.Provider
	modelBudget       prompt.ModelTokenBudget
	assembly          prompt.AssemblyOptions
	runMode           string
	conversation      []agent.Message
}

func (r cliRunner) prepareRun(conversation []agent.Message, skillNames []string) (runnerSetup, error) {
	alias := r.selectedAlias()
	rm, err := r.runtime.resolveModel(alias)
	if err != nil {
		return runnerSetup{}, err
	}
	baseModel := rm
	if r.currentReasoningOverride != nil {
		rm, err = provider.ApplyReasoningOverride(rm, r.currentReasoningOverride())
		if err != nil {
			return runnerSetup{}, err
		}
	}
	if r.runtime.visionCapabilities != nil {
		r.runtime.visionCapabilities.SetDerived(alias, agent.VisionStateFromPtr(rm.Vision))
	}
	emitFallbackWarnings(r.runtime.events, rm)
	emitTransportDiagnostic(r.runtime.events, rm)

	prov, err := r.runtimeProvider(rm)
	if err != nil {
		return runnerSetup{}, err
	}
	modelBudget := prompt.ModelBudgetFromEffectiveLimits(rm.EffectiveLimits)

	return runnerSetup{
		resolvedModel:     rm,
		baseResolvedModel: baseModel,
		provider:          loggingProvider{inner: prov, sink: r.runtime.events},
		modelBudget:       modelBudget,
		assembly:          r.promptAssembly(conversation, skillNames, modelBudget, rm.Prompts),
		runMode:           r.normalizedRunMode(),
		conversation:      conversation,
	}, nil
}

func emitTransportDiagnostic(events output.EventSink, rm provider.ResolvedModel) {
	if rm.EffectiveTransport == provider.TransportConfigured {
		return
	}
	if events != nil {
		events.Emit(output.NewTransportDiagnosticEvent(
			rm.BackendModelID,
			string(rm.ProviderConfig.Type),
			string(rm.EffectiveProviderType),
			string(rm.Facts.Transport.Source),
			rm.TransportOverrideReason,
		))
	}
}

func emitFallbackWarnings(events output.EventSink, rm provider.ResolvedModel) {
	if len(rm.Warnings) == 0 {
		return
	}
	if events == nil {
		return
	}
	key := rm.Alias + "\x00" + rm.BackendModelID
	if _, loaded := fallbackWarningModels.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	for _, warn := range rm.Warnings {
		events.Emit(output.NewConfigWarningEvent(warn))
	}
}

func resetFallbackModelWarnings() {
	fallbackWarningModels.Range(func(key, _ any) bool {
		fallbackWarningModels.Delete(key)
		return true
	})
}

func (r cliRunner) selectedAlias() string {
	if r.currentAlias != nil {
		return r.currentAlias()
	}
	if r.currentModel != nil {
		// Reverse lookup: find the alias whose ModelConfig matches the one returned.
		selected := r.currentModel()
		for alias, mc := range r.runtime.cfg.Models.Definitions {
			if mc.ID == selected.ID && mc.Provider == selected.Provider {
				return alias
			}
		}
	}
	if alias := r.runtime.cfg.Models.Effective.ActiveOrchestratorModel; alias != "" {
		return alias
	}
	return r.runtime.cfg.Models.Effective.DefaultModel
}

func (r cliRunner) runtimeProvider(rm provider.ResolvedModel) (provider.Provider, error) {
	if r.runtime.providerFactory == nil {
		if r.runtime.provider == nil {
			return nil, fmt.Errorf("provider is required")
		}
		return r.runtime.provider, nil
	}
	if cache := r.runtime.codexWSProviderCache; cache != nil && isCodexWSDispatch(rm) {
		return r.cachedCodexWSProvider(cache, rm)
	}
	prov, err := r.runtime.providerFactory(rm, r.sessionID())
	if err != nil {
		return nil, err
	}
	if prov == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return prov, nil
}

// cachedCodexWSProvider returns a process-lifetime cached Codex WebSocket
// provider for rm's (alias, promptCacheKey), building one via
// providerFactory on a miss.
func (r cliRunner) cachedCodexWSProvider(cache *provider.CodexWSCache, rm provider.ResolvedModel) (provider.Provider, error) {
	key := rm.Alias + "|" + r.promptCacheKey()
	return provider.NewCachingCodexWS(cache, key, func() (provider.Provider, error) {
		built, err := r.runtime.providerFactory(rm, r.sessionID())
		if err != nil {
			return nil, err
		}
		if built == nil {
			return nil, fmt.Errorf("provider is required")
		}
		return built, nil
	})
}

func (r cliRunner) Compact(ctx context.Context, conversation []agent.Message, skillNames []string, tools []provider.ToolSpec, steering string) ([]agent.Message, error) {
	setup, err := r.prepareRun(nil, skillNames)
	if err != nil {
		return nil, err
	}
	req := agent.RunRequest{
		Provider:          setup.provider,
		Tools:             provider.CloneTools(tools),
		Prompt:            setup.assembly,
		ModelBudget:       setup.modelBudget,
		ResolvedModel:     setup.resolvedModel,
		Events:            r.runtime.events,
		CaveHuman:         r.runtime.cfg.CaveHuman,
		CompactionLogPath: r.runtime.compactionLogFile,
		PromptCacheKey:    r.promptCacheKey(),
		Diagnostics:       r.runtime.diagnostics,
	}
	return agent.NewRunner().Compact(ctx, req, conversation, steering)
}

func (r cliRunner) promptAssembly(conversation []agent.Message, skillNames []string, modelBudget prompt.ModelTokenBudget, prompts config.ModelPrompts) prompt.AssemblyOptions {
	return prompt.AssemblyOptions{
		HomeDir:                   r.runtime.homeDir,
		ProjectRoot:               r.runtime.projectRoot,
		SkillsRoots:               prompt.SkillRoots(r.runtime.homeDir, r.runtime.projectRoot),
		SkillNames:                append([]string(nil), skillNames...),
		SkillsBundledFS:           r.runtime.skillBundledFS,
		ModelBudget:               modelBudget,
		PromptOverrides:           prompts,
		ProjectContextBudgetBytes: r.runtime.cfg.ProjectContext.MaxBytes,
		ProjectAgentsPath:         r.projectAgentsPath,
		ProjectContextExtraFiles:  append([]string(nil), r.runtime.cfg.ProjectContext.ExtraFiles...),
		ProjectContextIgnoreFiles: append([]string(nil), r.runtime.cfg.ProjectContext.IgnoreFiles...),
		DelegationEnabled:         r.runtime.cfg.SubAgent.Enabled,
		OrchestrationLevel:        r.orchestrationLevel(),
		AdvisorEnabled:            r.runtime.cfg.Advisor.Enabled,
		LSPEnabled:                r.runtime.cfg.LSP.Enabled && len(r.runtime.cfg.LSP.Servers) > 0,
		SandboxEnabled:            r.sandboxEnabled(),
		SandboxWritableMounts:     sandbox.WritableHostMounts(r.runtime.cfg.Sandbox),
		PhasePrompt:               r.phasePrompt,
		WorkflowMode:              r.workflowMode,
		Conversation:              toProviderConversation(conversation),
		CaveHuman:                 r.runtime.cfg.CaveHuman,
		CachedStaticContext:       r.staticContext,
		StaticContextScope:        r.sessionID(),
		SessionDate:               r.sessionDate(),
	}
}

// sandboxEnabled reports whether the runtime sandbox is active.
func (r cliRunner) sandboxEnabled() bool {
	return r.runtime.sandbox != nil && r.runtime.sandbox.Enabled()
}

// sandboxWrapper returns the tool.SandboxWrapper for this runtime, explicitly
// tool.Unsandboxed{} when no sandbox is configured, so callers never need to
// nil-check the runtime sandbox before constructing an Executor.
func (r cliRunner) sandboxWrapper() tool.SandboxWrapper {
	if r.runtime.sandbox == nil {
		return tool.Unsandboxed{}
	}
	return r.runtime.sandbox
}

func (r cliRunner) normalizedRunMode() string {
	runMode := strings.TrimSpace(r.runMode)
	if runMode == "" {
		return "exec"
	}
	return runMode
}

func retainDiagnosticEvents(base output.EventSink) (output.EventSink, *[]output.Event) {
	diagnostics := make([]output.Event, 0, 4)
	// Parallel tool execution emits diagnostic events from multiple goroutines,
	// so append must be serialized. Readers only run after the producing run has
	// joined, which the run loop guarantees, so no lock is needed to read.
	var mu sync.Mutex
	events := output.NewMultiSink(
		base,
		output.SinkFunc(func(event output.Event) {
			if isRetainedDiagnosticEvent(event) {
				mu.Lock()
				diagnostics = append(diagnostics, event)
				mu.Unlock()
			}
		}),
	)
	return events, &diagnostics
}

func buildRunRequest(r cliRunner, setup runnerSetup, activeRegistry *tool.Registry, events output.EventSink, drainSteers func() []agent.SteerMessage) agent.RunRequest {
	maxTokens := setup.resolvedModel.EffectiveLimits.MaxOutputTokens
	sandboxTmpDir := ""
	if r.sandboxEnabled() {
		sandboxTmpDir = r.runtime.sandbox.TmpDir()
	}
	executor := tool.NewExecutor(activeRegistry, r.runtime.cfg, r.approver, r.runtime.workDir, sandboxTmpDir, r.sandboxWrapper())
	executor = executor.WithDiagnostics(r.runtime.diagnostics)
	executor = executor.WithDiagnosticsScope(diagnostics.SourceParent, "", "")
	if r.modeGetterFunc != nil {
		executor = executor.WithModeGetter(r.modeGetterFunc)
	}
	visionCapabilities := r.runtime.visionCapabilities
	if visionCapabilities != nil {
		visionCapabilities = visionCapabilities.SnapshotWithSubAgentConfigured(visionCapabilities.SubAgentConfigured())
	}
	req := agent.RunRequest{
		Provider:      setup.provider,
		Executor:      executor,
		Tools:         activeRegistry.ToProviderSpecs(),
		Prompt:        setup.assembly,
		ModelBudget:   setup.modelBudget,
		ResolvedModel: setup.resolvedModel,
		MaxTokens:     &maxTokens,
		Limits: agent.Limits{
			MaxTurns:         r.maxTurns,
			MaxTokens:        r.runtime.cfg.Limits.MaxTokens,
			ModelCallTimeout: time.Duration(r.runtime.cfg.Limits.ModelCallTimeout.Duration()),
		},
		CaveHuman:          r.runtime.cfg.CaveHuman,
		Diagnostics:        r.runtime.diagnostics,
		Events:             events,
		ContextManager:     agent.NewContextStateManager(r.runtime.cfg.ContextManagement),
		StreamingPreferred: r.streamingPreferred,
		CompactionLogPath:  r.runtime.compactionLogFile,
		DrainSteers:        drainSteers,
		PromptCacheKey:     r.promptCacheKey(),
		CacheBaseline:      r.cacheBaseline,
		VisionCapabilities: visionCapabilities,
		ImageStore:         r.runtime.imageStore,
		SourceConversation: setup.conversation,
	}
	// delegation.IsDelegationTool matching a name has no effect when
	// delegation is disabled: BuildDelegateRegistry never registers
	// delegation tools into activeRegistry in that case, so the model can
	// never call a name the predicate would match. No branching is needed.
	req.ParallelClassOf = func(name string) agent.ParallelClass {
		if delegation.IsDelegationTool(name) {
			return agent.ParallelClassDelegation
		}
		if activeRegistry.IsParallelSafe(name) {
			return agent.ParallelClassTool
		}
		return agent.ParallelClassNone
	}
	req.MaxParallelTools = r.runtime.cfg.Limits.MaxParallelTools
	req.MaxParallelDelegations = r.runtime.cfg.SubAgent.MaxParallel
	if r.runtime.usageRecorder != nil {
		req.UsageRecorder = r.runtime.usageRecorder
	}
	return req
}
