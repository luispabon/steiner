package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/notify"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/quota"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/luispabon/steiner/internal/update"
)

type delegationCanceller struct{ s *delegation.Supervisor }

func (d delegationCanceller) CancelAgent(agentID string, discard bool) error {
	if d.s == nil {
		return errors.New("no active delegate cancellation available")
	}
	if agentID == "" {
		return errors.New("cancel delegate: agent id required")
	}
	switch d.s.CancelAgent(agentID, discard, delegation.CancelCauseUser) {
	case delegation.CancelAccepted:
		return nil
	case delegation.CancelAlreadyFinished:
		return fmt.Errorf("delegate %q already finished; worktree retained", agentID)
	default:
		return fmt.Errorf("no active delegate %q", agentID)
	}
}

func (d delegationCanceller) CancelAll() error {
	if d.s == nil {
		return errors.New("no active delegate cancellation available")
	}
	d.s.CancelAll(delegation.CancelCauseUser)
	return nil
}

func buildInteractiveSession(rt cliRuntime) (*interactive.Session, error) {
	sessionCfg := rt.cfg

	sessDeps := interactive.Dependencies{
		BaseEvents:          rt.events,
		SkillNames:          rt.skillNames,
		Config:              sessionCfg,
		HomeDir:             rt.homeDir,
		WorkDir:             rt.workDir,
		SessionStore:        rt.sessionStore,
		SkillLoader:         skill.Loader{RootDirs: prompt.SkillRoots(rt.homeDir, rt.projectRoot), BundledFS: rt.skillBundledFS},
		DelegateCanceller:   delegationCanceller{s: rt.delegationSupervisor},
		CompactionLogPath:   rt.compactionLogFile,
		RecordModelSwitch:   modelPopularityRecorder(rt.modelPopularity),
		ResolveModel:        rt.resolveModel,
		MaxTokensPerEpisode: sessionCfg.Limits.MaxTokens,
		OnEffectiveAssignmentsChanged: func(effective config.EffectiveModelAssignments) {
			if rt.visionCapabilities != nil {
				rt.visionCapabilities.SetSubAgentConfigured(effective.SubAgents["vision"] != "")
			}
		},
	}
	if rt.historyWriter != nil {
		sessDeps.HistoryWriter = rt.historyWriter
	}
	if rt.imageStore != nil {
		sessDeps.ImageStore = rt.imageStore
	}
	if rt.delegationSupervisor != nil {
		sessDeps.Background = rt.delegationSupervisor
		sessDeps.ClearFinishedSubAgents = rt.delegationSupervisor.ClearFinishedSubAgents
		sessDeps.SetCompletionSink = rt.delegationSupervisor.SetCompletionSink
		sessDeps.OpenGroupScope = rt.delegationSupervisor.OpenGroupScope
		sessDeps.SnapshotGroupLedger = rt.delegationSupervisor.SnapshotGroupLedger
	}
	sessDeps.StartupModel, sessDeps.StartupModelSource = startupModelSelection(rt)
	return interactive.NewSession(sessDeps)
}

func buildInteractiveRuntime(rt cliRuntime, sess *interactive.Session) cliRuntime {
	rt.events = sess.EventSink()
	// The runtime was built with the non-interactive sink, so re-point the
	// sandbox tmp warning at the live session sink.
	wireSandboxTmpWarning(rt.sandbox, rt.events)
	// The approver must be updated before the registry is built: the registry
	// copies ToolDef values (including their approval closures) by value, and
	// the interactive runner later snapshots the runtime by value too, so any
	// approver wiring done after this point is invisible downstream.
	if rt.mcpManager != nil {
		rt.mcpManager.UpdateApprover(sess.Approver(rt.events))
		rt.mcpManager.UpdatePlanMode(sess.Mode() == config.ExecutionModePlan)
	}
	// Keep the Manager's plan mode in sync with mid-session mode switches.
	sess.SetModeListener(func(m config.ExecutionMode) {
		if rt.mcpManager != nil {
			rt.mcpManager.UpdatePlanMode(m == config.ExecutionModePlan)
		}
	})
	registry := runtimeRegistryWithSinkAndMode(rt.cfg, rt.workDir, sess.DisplaySink(), true, sess.WorkflowHandoffResponder(sess.EventSink()), rt.sandbox, rt.mcpManager, rt.lspManager, withPendingSubAgents(rt.delegationSupervisor))
	rt.registry = registry
	rt.toolNames = registry.Names()
	rt.mcpInit = &mcpInitOnce{}
	return rt
}

func modelEntriesFromChoices(choices []modelcatalog.ModelChoice) []tui.ModelEntry {
	entries := make([]tui.ModelEntry, 0, len(choices))
	for _, choice := range choices {
		entries = append(entries, tui.ModelEntry{
			Ref:              choice.Ref,
			Display:          choice.Display,
			SupportedEfforts: append([]string(nil), choice.SupportedEfforts...),
			Current:          choice.Current,
		})
	}
	return entries
}

func modelPopularityRecorder(store *modelcatalog.Store) func(string, string) error {
	if store == nil {
		return nil
	}
	return store.Record
}

func resetSandboxTmp(events output.EventSink, sb *sandbox.Sandbox) {
	if sb == nil {
		return
	}
	if err := sb.ResetTmp(); err != nil && events != nil {
		emitCloseWarning(events, "sandbox tmp reset", err)
	}
}

func startModelCatalogRefresh(ctx context.Context, rt cliRuntime, sess *interactive.Session, updates chan<- []tui.ModelEntry) {
	if updates == nil {
		return
	}
	if rt.modelCatalog == nil || len(rt.modelCatalogEndpoints) == 0 {
		close(updates)
		return
	}
	go func() {
		defer close(updates)
		rt.modelCatalog.RefreshAll(ctx, rt.modelCatalogEndpoints, modelcatalog.RefreshOptions{
			Force: false,
			OnResult: func(alias string, err error) {
				if err != nil && rt.events != nil {
					rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
						Kind: "session_health", Severity: "warning",
						Notes: []string{fmt.Sprintf("model catalog refresh failed for %s: %v", alias, err)},
					}))
				}
				entries := modelEntriesFromChoices(rt.modelCatalog.Choices(catalogConfigCopy(&rt.cfg), sess.CurrentModelAlias()))
				select {
				case updates <- entries:
				case <-ctx.Done():
				}
			},
		})
		if rt.modelResolver != nil {
			rt.modelResolver.Invalidate()
		}
	}()
}

func startupTUIModelConfig(cfg config.Config) (tui.Config, config.ModelConfig) {
	activeModel := cfg.Models.Effective.ActiveOrchestratorModel
	if activeModel == "" {
		activeModel = cfg.Models.Effective.DefaultModel
	}
	return tui.Config{Model: activeModel, CurrentModelAlias: activeModel}, interactive.SelectedModelConfig(cfg)
}

// excludePatternDevLogWarnings formats the malformed paths.exclude_patterns
// entries for the TUI developer error log. Positions are one-based and reasons
// are the fixed strings from internal/tool, so no configured pattern text
// appears; reports are capped and the overflow becomes a count. The warnings go
// only to the error log: they are not emitted as events, so they never reach
// the conversation, stdout, or a tool result.
func excludePatternDevLogWarnings(patterns []string) []string {
	invalid, total := tool.InvalidWorkspaceExcludePatterns(patterns)
	if total == 0 {
		return nil
	}
	warnings := make([]string, 0, len(invalid)+1)
	for _, entry := range invalid {
		warnings = append(warnings, fmt.Sprintf("paths.exclude_patterns entry %d: %s; the entry is ignored", entry.Index+1, entry.Reason))
	}
	if remaining := total - len(invalid); remaining > 0 {
		warnings = append(warnings, fmt.Sprintf("paths.exclude_patterns: %d more invalid entries not listed", remaining))
	}
	return warnings
}

// primeQuota registers the configured quota accounts and loads the quota store.
// Load reads the store here, so store I/O stays out of the TUI's Update and
// View. quotaService may be nil.
func primeQuota(rt cliRuntime) {
	if rt.quotaService == nil {
		return
	}
	deps := provider.QuotaRegistryDeps{HTTPClient: rt.httpClient, ClaudePool: rt.claudeSubPool}
	for _, acct := range provider.BuildQuotaAccounts(rt.cfg.Providers, deps) {
		rt.quotaService.Register(acct)
	}
	rt.quotaService.Load()
}

// quotaTUIConfig returns the quota service and refresh interval for the TUI.
// A zero interval disables the refresh.
func quotaTUIConfig(rt cliRuntime) (*quota.Service, time.Duration) {
	return rt.quotaService, time.Duration(rt.cfg.TUI.QuotaRefreshInterval.Duration())
}

func buildInteractiveApp(cmd *cobra.Command, flags *cliFlags, rt cliRuntime, sess *interactive.Session) (*tui.App, error) {
	primeQuota(rt)
	startupCfg, selected := startupTUIModelConfig(rt.cfg)
	entries := []tui.ModelEntry(nil)
	updates := rt.modelEntriesUpdates
	if updates == nil {
		updates = make(chan []tui.ModelEntry, max(1, len(rt.modelCatalogEndpoints)))
	}
	if rt.modelCatalog != nil {
		entries = modelEntriesFromChoices(rt.modelCatalog.Choices(catalogConfigCopy(&rt.cfg), sess.CurrentModelAlias()))
	}
	selectedProviderBaseURL := ""
	selectedProviderName := ""
	if p, ok := rt.cfg.Providers[selected.Provider]; ok {
		selectedProviderBaseURL = p.BaseURL
		selectedProviderName = selected.Provider
	}
	projection := interactive.BuildModelProjection(rt.cfg)
	tuiCfg := tui.Config{
		Model:               startupCfg.Model,
		Entries:             entries,
		ModelEntriesUpdates: updates,
		ModelNames:          projection.AliasNames,
		ModelBackendAlias:   projection.BackendAliases,
		ModelContexts:       projection.ContextSizes,
		ModelBaseURLs:       projection.BaseURLs,
		ModelProviderNames:  projection.ProviderNames,
		CurrentModelAlias:   startupCfg.CurrentModelAlias,
		ProfileName:         rt.cfg.Models.Effective.ProfileName,
		ProfileNames:        interactive.SortedProfileNames(rt.cfg.Models.Profiles),
		InitialMode:         string(sess.Mode()),
		ProviderBaseURL:     selectedProviderBaseURL,
		ProviderName:        selectedProviderName,
		HomeDir:             rt.homeDir,
		WorkingDir:          rt.workDir,
		MaxTurns:            0,
		Version:             version,
		SkillNames:          rt.skillNames,
		SkillDescriptions:   rt.skillDescriptions,
		SkillSources:        rt.skillSources,
		Controller:          sess,
		SteerQueue:          sess.ActiveRunController().SteerQueue(),
		SandboxStatus:       rt.sandboxStatus,
		ConfigWarnings:      rt.configWarnings,
		DevLogWarnings:      excludePatternDevLogWarnings(rt.cfg.Paths.ExcludePatterns),
		WorktreeCleanup:     rt.worktreeCleanup,
		SubAgentsEnabled:    rt.cfg.SubAgent.Enabled,
		OrchestrationLevel:  string(sess.OrchestrationLevel()),
	}
	if rt.sessionStore != nil {
		tuiCfg.SessionStore = rt.sessionStore
	}
	tuiCfg.MCPEnabled, tuiCfg.MCPServers, tuiCfg.MCPToolOrigins = mcpTUIState(rt.cfg, rt.mcpManager, rt.registry)
	tuiCfg.LSPEnabled = rt.cfg.LSP.Enabled
	tuiCfg.PollLSPStatesFunc = func() []tui.LSPServerStatus {
		return lspTUIStates(rt.cfg, rt.lspManager)
	}
	tuiCfg.Recorder = rt.usageRecorder
	tuiCfg.Quota, tuiCfg.QuotaRefreshInterval = quotaTUIConfig(rt)
	if rt.diagnostics.Enabled(diagnostics.KindTUI) {
		tuiCfg.FrameStats = rt.diagnostics
	}
	if rt.diagnostics != nil {
		tuiCfg.Diagnostics = rt.diagnostics
	}
	tuiCfg.ImageStore = rt.imageStore
	tuiCfg.VisionCapabilities = rt.visionCapabilities
	tuiCfg.OneshotRunnerFactory = newOneshotRunnerFactoryBuilder(cmd, flags, rt.projectRoot, sess.EventSink(), sess.CurrentEffective, sess.OrchestrationLevel, sess.ActiveRunController().SteerQueue())
	tuiCfg.Notifier = notify.New(notify.Options{
		Enabled:  rt.cfg.DesktopNotifications.Enabled,
		Duration: time.Duration(rt.cfg.DesktopNotifications.Duration) * time.Second,
		AppName:  "steiner",
	})
	tuiCfg.SessionResetCleanup = func() {
		resetSandboxTmp(rt.events, rt.sandbox)
	}
	tuiCfg.ClearConversationHooks = func() {
		delegation.ResetForNewConversation(rt.delegationSessionStore, rt.delegationAdvisorBudgetStore)
	}
	tuiCfg.ResolveReasoningFunc = func() (map[string]provider.ReasoningCapabilities, map[string]string) {
		if len(rt.cfg.Models.Definitions) == 0 {
			return nil, nil
		}
		aliases := make([]string, 0, len(rt.cfg.Models.Definitions))
		for alias := range rt.cfg.Models.Definitions {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		caps := make(map[string]provider.ReasoningCapabilities)
		efforts := make(map[string]string)
		for _, alias := range aliases {
			rm, err := rt.resolveModel(alias)
			if err != nil {
				continue
			}
			caps[alias] = rm.Reasoning
			efforts[alias] = rm.ReasoningConfiguredEffort
		}
		return caps, efforts
	}
	tuiCfg.ResolveReasoningForAliasFunc = func(alias string) (provider.ReasoningCapabilities, string) {
		rm, err := rt.resolveModel(alias)
		if err != nil {
			return provider.ReasoningCapabilities{}, ""
		}
		return rm.Reasoning, rm.ReasoningEffectiveEffort
	}
	if rt.cfg.UpdateCheck.Enabled {
		tuiCfg.CheckUpdateFunc = func() (latestVersion string, needsUpdate bool) {
			interval := time.Duration(rt.cfg.UpdateCheck.IntervalHours) * time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			latest, needs, err := update.CheckCached(ctx, update.DefaultCachePath(), interval, version, "luispabon", "steiner", os.Getenv("STEINER_GITHUB_TOKEN"), channel)
			if err != nil {
				return "", false
			}
			return latest, needs
		}
	}
	app, err := tui.NewApp(tuiCfg)
	if err != nil {
		return nil, err
	}
	return app, nil
}

// newOneshotRunnerFactoryBuilder returns a builder that binds a oneshot phase
// runner factory to a specific run identity. The interactive TUI mints a fresh
// identity per launch or resume, so the factory must be constructed per run.
func newOneshotRunnerFactoryBuilder(cmd *cobra.Command, flags *cliFlags, projectRoot string, events output.EventSink, currentEffective func() config.EffectiveModelAssignments, orchestrationLevel func() config.OrchestrationLevel, steers *agent.SteerQueue) tui.OneshotRunnerFactoryBuilder {
	return func(identity oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
		return phaseRunnerFactory{
			cmd:                cmd,
			flags:              flags,
			rootDir:            projectRoot,
			identity:           identity,
			events:             events,
			sessionDate:        prompt.NewSessionDate(time.Now()),
			currentEffective:   currentEffective,
			orchestrationLevel: orchestrationLevel,
			baseline:           agent.NewCacheBaselineStore(),
			steers:             steers,
		}
	}
}

func wireInteractiveRunner(rt cliRuntime, sess *interactive.Session) {
	// The archive resolves the session ID on every call, so a fork or resume
	// that changes the session is reflected in later compactions.
	runner := cliRunner{
		runtime:                  rt,
		runMode:                  "interactive",
		streamingPreferred:       true,
		currentAlias:             sess.CurrentModelAlias,
		currentEffective:         sess.CurrentEffective,
		currentReasoningOverride: sess.CurrentReasoningOverride,
		promptCacheKeyFn:         sess.PromptCacheKey,
		sessionIDFn:              sess.SessionID,
		sessionDateFn:            sess.SessionDate,
		modeGetterFunc:           sess.Mode,
		orchestrationLevelFn:     sess.OrchestrationLevel,
		staticContext:            &prompt.StaticContextCache{},
		cacheBaseline:            agent.NewCacheBaselineStore(),
		compactionTargets:        delegation.NewCompactionFallbackTargets(),
		historyArchive:           newHistoryArchive(rt.sessionStore, sess.SessionID),
		recallArchive:            newRecallArchive(rt.sessionStore, sess.SessionID),
	}
	runner.approver = sess.Approver(rt.events)
	if rt.mcpState != nil {
		rt.mcpState.setListener(func() { emitMCPStateSnapshot(rt, sess.EventSink()) })
		rt.mcpState.setPreListener(func() { emitMCPServerStatesSnapshot(rt, sess.EventSink()) })
	}
	sess.SetRunner(sessionRunner{runner: runner, mcpInit: rt.mcpInit})
}
func resumeInteractiveSession(ctx context.Context, sess *interactive.Session, resumeID string, rt *cliRuntime) error {
	if resumeID == "" {
		return nil
	}
	if err := sess.LoadSessionByID(ctx, resumeID); err != nil {
		closeRuntime(rt)
		return err
	}
	return nil
}

// startupModelSelection returns the model reference and source captured before
// a resume restore: the reference active at process start and where it came
// from (--model, STEINER_MODEL, or the profile default).
func startupModelSelection(rt cliRuntime) (string, string) {
	ref := strings.TrimSpace(rt.cfg.Models.Effective.ActiveOrchestratorModel)
	if ref == "" {
		ref = strings.TrimSpace(rt.cfg.Models.Effective.DefaultModel)
	}
	source := rt.startupModelSource
	if source == "" {
		source = interactive.ModelSourceProfile
	}
	return ref, source
}

// resumedModelTUIState returns the model alias and provider details the TUI
// should seed after a session load, so the sidebar and status bar show the
// restored model before any event arrives.
func resumedModelTUIState(sess *interactive.Session) (alias, baseURL, providerName string) {
	if sess == nil {
		return "", "", ""
	}
	cfg := sess.Config()
	alias = strings.TrimSpace(sess.CurrentModelAlias())
	if alias == "" {
		alias = strings.TrimSpace(cfg.Models.Effective.DefaultModel)
	}
	model, _ := config.ResolveModelConfig(&cfg, alias)
	if provider, ok := cfg.Providers[model.Provider]; ok {
		baseURL = provider.BaseURL
		providerName = model.Provider
	}
	return alias, baseURL, providerName
}

// invalidExplicitModelWarnings reports --model or STEINER_MODEL values that
// were silently ignored because they are not a configured alias or
// provider/model reference.
func invalidExplicitModelWarnings(cfg config.Config, flags *cliFlags) []string {
	var warnings []string
	if flags != nil {
		if model := strings.TrimSpace(flags.model); model != "" && !config.IsValidModelReference(&cfg, model) {
			warnings = append(warnings, fmt.Sprintf("--model %q is not a configured model alias or provider/model reference; ignored", model))
		}
	}
	if model := strings.TrimSpace(os.Getenv("STEINER_MODEL")); model != "" && !config.IsValidModelReference(&cfg, model) {
		warnings = append(warnings, fmt.Sprintf("STEINER_MODEL=%q is not a configured model alias or provider/model reference; ignored", model))
	}
	return warnings
}

// mcpInitOnce runs the MCP wait and registry re-registration once. The tool
// registry is not thread-safe, so wait serializes its callers and the
// initialization completes before any agent turn or compaction touches the
// registry.
type mcpInitOnce struct {
	mu sync.Mutex
	// running is set while one caller executes the attempt. Waiters block on
	// changed instead of on mu, so a queued caller can abandon the wait when
	// its own context ends.
	running bool
	done    bool
	err     error
	changed chan struct{}
	// attempt replaces the MCP wait and registration in tests; production
	// leaves it nil. It runs at most once at a time, like run itself.
	attempt func(context.Context, cliRuntime) error
}

// wait runs the initialization unless it already succeeded. Only one caller
// executes an attempt at a time; the others wait for it to finish and retry
// only if it was cancelled. A cancelled attempt is not final, so a run or
// compaction cancelled while MCP servers are still connecting does not disable
// later requests. Any other error is final and is reported to every later
// caller, because the manager's server set has resolved. A queued caller that
// is cancelled returns its own context error promptly, without waiting for the
// running attempt.
func (i *mcpInitOnce) wait(ctx context.Context, rt cliRuntime) error {
	for {
		i.mu.Lock()
		if i.done {
			err := i.err
			i.mu.Unlock()
			return err
		}
		if !i.running {
			i.running = true
			changed := make(chan struct{})
			i.changed = changed
			i.mu.Unlock()

			err := i.run(ctx, rt)

			i.mu.Lock()
			i.running = false
			if err == nil {
				i.done = true
			} else if !isContextEnd(err) {
				i.done = true
				i.err = err
			}
			close(changed)
			i.mu.Unlock()
			return err
		}
		changed := i.changed
		i.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isContextEnd reports whether err is a cancelled or expired context rather
// than an initialization failure.
func isContextEnd(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// run executes WaitInit, registers the connected tool defs in place, and arms
// the MCP state producer. A failed or cancelled wait returns before registering
// or arming anything. Only the caller holding the running slot reaches it, so
// the registry is never mutated concurrently.
func (i *mcpInitOnce) run(ctx context.Context, rt cliRuntime) error {
	if i.attempt != nil {
		return i.attempt(ctx, rt)
	}
	if rt.mcpManager != nil {
		if err := rt.mcpManager.WaitInit(ctx); err != nil {
			return err
		}
		// Register in place: Registry.Register overwrites by name, so a
		// server that already connected before the interactive registry
		// was built is replaced and the rest are added, leaving one def
		// per tool. The replayed defs carry the session approver because
		// UpdateApprover ran before the first registry build.
		for _, def := range rt.mcpManager.ToolDefs() {
			rt.registry.Register(def)
		}
	}
	if rt.mcpState != nil {
		rt.mcpState.arm()
	}
	return nil
}

// sessionRunner adapts cliRunner to the runExecutor interface expected by
// the interactive session.
type sessionRunner struct {
	runner  cliRunner
	mcpInit *mcpInitOnce
}

// CompactWithLineage compacts against the run's lineage and last main request.
// It waits for MCP initialization before the runner rebuilds a missing parent
// tool registry, so a resumed session sends the tools a run would have sent.
func (r sessionRunner) CompactWithLineage(ctx context.Context, conversation []agent.Message, lineage agent.ConversationLineage, lastMainRequest agent.LastMainRequest, tools []provider.ToolSpec, steering string) ([]agent.Message, bool, bool, error) {
	if err := r.waitMCPInit(ctx); err != nil {
		return nil, false, false, err
	}
	return r.runner.CompactWithLineage(ctx, conversation, lineage, lastMainRequest, nil, tools, steering)
}

// waitMCPInit runs the MCP wait and registry re-registration, blocking until it
// finishes. The tool registry is not thread-safe, so every path that reads or
// rebuilds it calls this first. A cancellation of the caller's context abandons
// its attempt without blocking a later caller from retrying.
func (r sessionRunner) waitMCPInit(ctx context.Context) error {
	if r.mcpInit == nil {
		return nil
	}
	return r.mcpInit.wait(ctx, r.runner.runtime)
}

func (r sessionRunner) Run(ctx context.Context, in interactive.RunInput) (interactive.RunResult, error) {
	if err := r.waitMCPInit(ctx); err != nil {
		return interactive.RunResult{}, err
	}
	result, err := r.runner.RunWithHooks(ctx, in.Conversation, nil, runHooks{
		drainInbox:           in.DrainInbox,
		pendingSubAgents:     in.PendingSubAgents,
		delegationGroupScope: in.DelegationGroupScope,
		lineage:              in.Lineage,
		maxTokens:            in.MaxTokens,
		parentUsage:          in.ParentUsage,
		lastMainRequest:      in.LastMainRequest,
	})
	return interactive.RunResult{
		Conversation:    result.Conversation,
		WorkflowHandoff: result.WorkflowHandoff,
		Lineage:         result.Lineage,
		TokenCount:      result.TokenCount,
		StopReason:      result.StopReason,
		ParentUsage:     result.ParentUsage,
		LastMainRequest: result.LastMainRequest,
	}, err
}
