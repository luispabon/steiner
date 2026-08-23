package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
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
}

func (r cliRunner) prepareRun(conversation []agent.Message, skillNames []string) (runnerSetup, error) {
	alias := r.selectedAlias()
	rm, err := provider.ResolveWithDiscovery(r.runtime.cfg, alias, r.runtime.httpClient)
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
	emitFallbackWarnings(r.runtime.status, rm)
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
			rm.MetadataSource,
			rm.TransportOverrideReason,
		))
	}
}

func emitFallbackWarnings(stream *output.EventStream, rm provider.ResolvedModel) {
	if rm.MetadataSource != "fallback" || len(rm.Warnings) == 0 {
		return
	}
	key := rm.Alias + "\x00" + rm.BackendModelID
	if _, loaded := fallbackWarningModels.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	for _, warn := range rm.Warnings {
		if stream != nil {
			stream.Printf("%s\n", warn)
			continue
		}
		fmt.Fprintf(os.Stderr, "%s\n", warn)
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
	return r.runtime.cfg.Models.Default
}

func (r cliRunner) runtimeProvider(rm provider.ResolvedModel) (provider.Provider, error) {
	prov := r.runtime.provider
	if r.runtime.providerFactory != nil {
		var err error
		prov, err = r.runtime.providerFactory(rm)
		if err != nil {
			return nil, err
		}
	}
	if prov == nil {
		return nil, fmt.Errorf("provider is required")
	}
	return prov, nil
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
		AdvisorEnabled:            r.runtime.cfg.Advisor.Enabled,
		SandboxEnabled:            r.sandboxEnabled(),
		SandboxWritableMounts:     sandbox.WritableHostMounts(r.runtime.cfg.Sandbox),
		PhasePrompt:               r.phasePrompt,
		WorkflowMode:              r.workflowMode,
		Conversation:              toProviderConversation(conversation),
		CaveHuman:                 r.runtime.cfg.CaveHuman,
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
	events := output.NewMultiSink(
		base,
		output.SinkFunc(func(event output.Event) {
			if isRetainedDiagnosticEvent(event) {
				diagnostics = append(diagnostics, event)
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
	if r.modeGetterFunc != nil {
		executor = executor.WithModeGetter(r.modeGetterFunc)
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
			MaxTurns:  r.maxTurns,
			MaxTokens: r.runtime.cfg.Limits.MaxTokens,
		},
		CaveHuman:          r.runtime.cfg.CaveHuman,
		Events:             events,
		ContextManager:     agent.NewContextStateManager(r.runtime.cfg.ContextManagement),
		StreamingPreferred: r.streamingPreferred,
		CompactionLogPath:  r.runtime.compactionLogFile,
		DrainSteers:        drainSteers,
		PromptCacheKey:     r.promptCacheKey(),
		VisionCapabilities: r.runtime.visionCapabilities,
	}
	if r.runtime.cfg.SubAgent.Enabled {
		req.ParallelTool = delegation.IsDelegationTool
		req.MaxParallelTools = r.runtime.cfg.SubAgent.MaxParallel
	} else {
		// Leave parallel delegation fields unset when delegation is disabled.
		req.ParallelTool = nil
		req.MaxParallelTools = 0
	}
	if r.runtime.usageRecorder != nil {
		req.UsageRecorder = r.runtime.usageRecorder
	}
	return req
}
