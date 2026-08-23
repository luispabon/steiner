package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/notify"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

func buildInteractiveSession(rt cliRuntime) (*interactive.Session, error) {
	sessDeps := interactive.Dependencies{
		BaseEvents:        rt.events,
		SkillNames:        rt.skillNames,
		Config:            rt.cfg,
		HTTPClient:        rt.httpClient,
		HomeDir:           rt.homeDir,
		WorkDir:           rt.workDir,
		SessionStore:      rt.sessionStore,
		CompactionLogPath: rt.compactionLogFile,
	}
	if rt.historyWriter != nil {
		sessDeps.HistoryWriter = rt.historyWriter
	}
	return interactive.NewSession(sessDeps)
}

func buildInteractiveRuntime(rt cliRuntime, sess *interactive.Session) cliRuntime {
	rt.events = sess.EventSink()
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
	registry := runtimeRegistryWithSinkAndMode(rt.cfg, rt.workDir, sess.DisplaySink(), true, sess.WorkflowHandoffResponder(sess.EventSink()), rt.sandbox, rt.mcpManager)
	rt.registry = registry
	rt.toolNames = registry.Names()
	rt.mcpInit = &mcpInitOnce{}
	return rt
}

func buildInteractiveApp(cmd *cobra.Command, flags *cliFlags, rt cliRuntime, sess *interactive.Session) *tui.App {
	selected := selectedModelConfig(rt.cfg)
	selectedProviderBaseURL := ""
	selectedProviderName := ""
	if p, ok := rt.cfg.Providers[selected.Provider]; ok {
		selectedProviderBaseURL = p.BaseURL
		selectedProviderName = selected.Provider
	}
	tuiCfg := tui.Config{
		Model:              selected.ID,
		ModelNames:         modelAliasNames(rt.cfg),
		ModelBackendAlias:  modelBackendAliases(rt.cfg),
		ModelContexts:      modelContextSizes(rt.cfg),
		ModelBaseURLs:      modelBaseURLs(rt.cfg),
		ModelProviderNames: modelProviderNames(rt.cfg),
		CurrentModelAlias:  rt.cfg.Models.Default,
		InitialMode:        string(sess.Mode()),
		ProviderBaseURL:    selectedProviderBaseURL,
		ProviderName:       selectedProviderName,
		HomeDir:            rt.homeDir,
		WorkingDir:         rt.workDir,
		MaxTurns:           0,
		Version:            version,
		SkillNames:         rt.skillNames,
		SkillDescriptions:  rt.skillDescriptions,
		SkillSources:       rt.skillSources,
		Controller:         sess,
		SandboxStatus:      rt.sandboxStatus,
		ConfigWarnings:     rt.configWarnings,
		WorktreeCleanup:    rt.worktreeCleanup,
	}
	if rt.sessionStore != nil {
		tuiCfg.SessionStore = rt.sessionStore
	}
	tuiCfg.MCPEnabled, tuiCfg.MCPServers, tuiCfg.MCPToolOrigins = mcpTUIState(rt.cfg, rt.mcpManager, rt.registry)
	tuiCfg.Recorder = rt.usageRecorder
	tuiCfg.ImageStore = rt.imageStore
	tuiCfg.VisionCapabilities = rt.visionCapabilities
	tuiCfg.OneshotRunnerFactory = newOneshotRunnerFactoryBuilder(cmd, flags, rt.projectRoot, sess.EventSink())
	tuiCfg.Notifier = notify.New(notify.Options{
		Enabled:  rt.cfg.DesktopNotifications.Enabled,
		Duration: time.Duration(rt.cfg.DesktopNotifications.Duration) * time.Second,
		AppName:  "steiner",
	})
	if rt.sandbox != nil {
		sb := rt.sandbox
		tuiCfg.SessionResetCleanup = func() {
			_ = sb.ResetTmp()
		}
	}
	tuiCfg.ResolveReasoningFunc = func() (map[string]provider.ReasoningCapabilities, map[string]string) {
		return provider.ResolveReasoningBatch(rt.cfg, rt.httpClient)
	}
	tuiCfg.ResolveReasoningForAliasFunc = func(alias string) (provider.ReasoningCapabilities, string) {
		rm, err := provider.ResolveWithDiscovery(rt.cfg, alias, rt.httpClient)
		if err != nil {
			return provider.ReasoningCapabilities{}, ""
		}
		return rm.Reasoning, rm.ReasoningEffectiveEffort
	}
	return tui.NewApp(tuiCfg)
}

// mcpTUIState converts MCP manager state and registry tool provenance into
// the TUI's display-only snapshot: whether MCP is enabled, the per-server
// states, and the tool-name-to-origin map.
func mcpTUIState(cfg config.Config, mgr *mcp.Manager, registry *tool.Registry) (bool, []tui.MCPServerStatus, map[string]tui.MCPToolOrigin) {
	enabled := cfg.MCP.Enabled

	states := mgr.ServerStates() // nil-safe: nil when mgr is nil (MCP off, no Manager was constructed)
	if !enabled {
		states = mcp.DeclaredStates(cfg.MCP) // MCP off: no Manager exists
	}

	servers := make([]tui.MCPServerStatus, 0, len(states))
	for _, s := range states {
		tools := make([]tui.MCPToolStatus, 0, len(s.AdvertisedTools))
		for _, t := range s.AdvertisedTools {
			tools = append(tools, tui.MCPToolStatus{Name: t.Name, Outcome: mcpOutcomeLabel(t.Outcome)})
		}
		servers = append(servers, tui.MCPServerStatus{
			Name:      s.Name,
			State:     string(s.Status),
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Err,
		})
	}

	var origins map[string]tui.MCPToolOrigin
	for _, def := range registry.Definitions() {
		if def.MCP.Server == "" {
			continue
		}
		if origins == nil {
			origins = make(map[string]tui.MCPToolOrigin)
		}
		origins[def.Name] = tui.MCPToolOrigin{Server: def.MCP.Server, Tool: def.MCP.ToolName}
	}

	return enabled, servers, origins
}

// mcpOutcomeLabel maps a manager ToolOutcome to the display label the TUI and
// status snapshots carry: "registered", "filtered" or "denied".
func mcpOutcomeLabel(o mcp.ToolOutcome) string {
	switch o {
	case mcp.ToolFiltered:
		return "filtered"
	case mcp.ToolDenied:
		return "denied"
	default:
		return "registered"
	}
}

// serverStatesSnapshot converts MCP manager server states into the output format.
// It applies mcpOutcomeLabel to tool outcomes and builds the servers map.
func serverStatesSnapshot(servers []tui.MCPServerStatus) map[string]output.MCPServerState {
	states := make(map[string]output.MCPServerState, len(servers))
	for _, s := range servers {
		tools := make([]output.MCPAdvertisedTool, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, output.MCPAdvertisedTool{Name: t.Name, Outcome: t.Outcome})
		}
		states[s.Name] = output.MCPServerState{
			State:     s.State,
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Error,
		}
	}
	return states
}

// emitMCPServerStatesSnapshot emits a snapshot of the current MCP server states
// without registry origins. This is used for pre-arm notifications while servers
// are connecting, before tool defs are registered.
func emitMCPServerStatesSnapshot(rt cliRuntime, sink output.EventSink) {
	if rt.mcpManager == nil {
		return
	}
	enabled := rt.cfg.MCP.Enabled
	states := rt.mcpManager.ServerStates()
	if !enabled {
		states = mcp.DeclaredStates(rt.cfg.MCP)
	}

	servers := make([]tui.MCPServerStatus, 0, len(states))
	for _, s := range states {
		tools := make([]tui.MCPToolStatus, 0, len(s.AdvertisedTools))
		for _, t := range s.AdvertisedTools {
			tools = append(tools, tui.MCPToolStatus{Name: t.Name, Outcome: mcpOutcomeLabel(t.Outcome)})
		}
		servers = append(servers, tui.MCPServerStatus{
			Name:      s.Name,
			State:     string(s.Status),
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Err,
		})
	}

	statesMap := serverStatesSnapshot(servers)
	sink.Emit(output.NewMCPStatusEvent(enabled, statesMap, nil))
}

// emitMCPStateSnapshot computes the current MCP display state and emits it as
// one immutable snapshot event on the given sink. The snapshot carries every
// server's live state plus the registry's MCP tool origins, so the TUI can
// rebuild its MCP surface from a single event without touching the manager or
// registry.
func emitMCPStateSnapshot(rt cliRuntime, sink output.EventSink) {
	if rt.mcpManager == nil {
		return
	}
	enabled, servers, origins := mcpTUIState(rt.cfg, rt.mcpManager, rt.registry)
	statesMap := serverStatesSnapshot(servers)
	toolOrigins := make(map[string]output.MCPToolOrigin, len(origins))
	for name, o := range origins {
		toolOrigins[name] = output.MCPToolOrigin{Server: o.Server, Tool: o.Tool}
	}
	sink.Emit(output.NewMCPStatusEvent(enabled, statesMap, toolOrigins))
}

// mcpStateProducer forwards MCP state-change notifications to different
// listeners depending on the armed status. Before arming, state changes invoke
// the preListener with states-only snapshots; after arming, they invoke the
// listener with full snapshots including registry origins. This allows the TUI
// to show connection progress before tool defs are registered, while ensuring
// it never observes a half-connected server set with incomplete tool origins.
type mcpStateProducer struct {
	mu          sync.Mutex
	armed       bool
	listener    func()
	preListener func()
}

// stateChanged is the onStateChange callback passed to mcp.Connect. It forwards
// to preListener when not armed, listener when armed.
func (p *mcpStateProducer) stateChanged() {
	p.mu.Lock()
	armed, listener, preListener := p.armed, p.listener, p.preListener
	p.mu.Unlock()
	if armed {
		if listener != nil {
			listener()
		}
	} else {
		if preListener != nil {
			preListener()
		}
	}
}

// setListener installs the full snapshot/transition listener. Safe to call
// before arm.
func (p *mcpStateProducer) setListener(listener func()) {
	p.mu.Lock()
	p.listener = listener
	p.mu.Unlock()
}

// setPreListener installs the states-only snapshot listener for pre-arm
// notifications. Safe to call before arm.
func (p *mcpStateProducer) setPreListener(listener func()) {
	p.mu.Lock()
	p.preListener = listener
	p.mu.Unlock()
}

// arm marks the producer armed and emits one consolidated snapshot of the
// current state. After arm, every manager state change forwards to the
// listener. Idempotent: arming twice emits one snapshot total.
func (p *mcpStateProducer) arm() {
	p.mu.Lock()
	if p.armed {
		p.mu.Unlock()
		return
	}
	p.armed = true
	listener := p.listener
	p.mu.Unlock()
	if listener != nil {
		listener()
	}
}

func selectedModelConfig(cfg config.Config) config.ModelConfig {
	return cfg.Models.Definitions[cfg.Models.Default]
}

func modelAliasNames(cfg config.Config) []string {
	if len(cfg.Models.Definitions) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Models.Definitions))
	for k := range cfg.Models.Definitions {
		names = append(names, k)
	}
	return names
}

func modelBackendAliases(cfg config.Config) map[string]string {
	if len(cfg.Models.Definitions) == 0 {
		return nil
	}
	aliases := make(map[string]string, len(cfg.Models.Definitions))
	for alias, model := range cfg.Models.Definitions {
		aliases[model.ID] = alias
	}
	return aliases
}

func modelContextSizes(cfg config.Config) map[string]int {
	if len(cfg.Models.Definitions) == 0 {
		return nil
	}
	sizes := make(map[string]int, len(cfg.Models.Definitions))
	for name, model := range cfg.Models.Definitions {
		if model.Advanced.Limits.ContextWindow > 0 {
			sizes[name] = model.Advanced.Limits.ContextWindow
		}
	}
	return sizes
}

func modelBaseURLs(cfg config.Config) map[string]string {
	if len(cfg.Models.Definitions) == 0 {
		return nil
	}
	urls := make(map[string]string, len(cfg.Models.Definitions))
	for name, model := range cfg.Models.Definitions {
		if p, ok := cfg.Providers[model.Provider]; ok {
			urls[name] = p.BaseURL
		}
	}
	return urls
}

func modelProviderNames(cfg config.Config) map[string]string {
	if len(cfg.Models.Definitions) == 0 {
		return nil
	}
	names := make(map[string]string, len(cfg.Models.Definitions))
	for name, model := range cfg.Models.Definitions {
		if _, ok := cfg.Providers[model.Provider]; ok {
			names[name] = model.Provider
		}
	}
	return names
}

// newOneshotRunnerFactoryBuilder returns a builder that binds a oneshot phase
// runner factory to a specific run identity. The interactive TUI mints a fresh
// identity per launch or resume, so the factory must be constructed per run.
func newOneshotRunnerFactoryBuilder(cmd *cobra.Command, flags *cliFlags, projectRoot string, events output.EventSink) tui.OneshotRunnerFactoryBuilder {
	return func(identity oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
		return phaseRunnerFactory{
			cmd:      cmd,
			flags:    flags,
			rootDir:  projectRoot,
			identity: identity,
			events:   events,
		}
	}
}

func wireInteractiveRunner(rt cliRuntime, sess *interactive.Session) {
	runner := cliRunner{
		runtime:                  rt,
		runMode:                  "interactive",
		streamingPreferred:       true,
		currentAlias:             sess.CurrentModelAlias,
		currentReasoningOverride: sess.CurrentReasoningOverride,
		promptCacheKeyFn:         sess.PromptCacheKey,
		modeGetterFunc:           sess.Mode,
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

func runInteractiveSession(cmd *cobra.Command, sess *interactive.Session, p *tea.Program, rt *cliRuntime) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	wait := startInteractiveProgram(p, rt.events, stop)
	if rt.mcpInit != nil {
		go rt.mcpInit.once.Do(func() { rt.mcpInit.run(ctx, *rt) })
	}
	err := sess.Run(ctx)
	stopInteractiveProgram(p)
	wait()
	pruneWorktreesOnExit(cmd, sess, rt)
	clearTerminalScreen(cmd.OutOrStdout())
	if err == nil && sess.SessionTitle() != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nResume this session:\n  steiner --resume %s\n\n", sess.SessionID())
	}
	closeRuntime(rt)
	return err
}

var worktreeCleanupJoinTimeout = 5 * time.Second

func pruneWorktreesOnExit(cmd *cobra.Command, sess *interactive.Session, rt *cliRuntime) {
	if rt == nil || rt.worktreeCleanup == nil || !rt.worktreeCleanup.ShouldPrune() {
		return
	}

	if sess != nil {
		joinCtx, cancel := context.WithTimeout(context.Background(), worktreeCleanupJoinTimeout)
		finished := sess.WaitRuns(joinCtx)
		cancel()
		if !finished {
			warning := errors.New("skipped because an active run was still finishing")
			if rt.events != nil {
				emitCloseWarning(rt.events, "worktree cleanup", warning)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: worktree cleanup: %v.\n", warning)
			}
			return
		}
	}
	pruneCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	n, err := rt.worktreeCleanup.Prune(pruneCtx)
	if err != nil {
		if rt.events != nil {
			emitCloseWarning(rt.events, "worktree cleanup", err)
		}
		return
	}
	if n > 0 {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nCleaned up %d worktree(s).\n\n", n)
	}
}

func startInteractiveProgram(p *tea.Program, events output.EventSink, stop context.CancelFunc) func() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := runTeaProgram(p); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			emitInteractiveProgramWarning(events, err)
		}
		stop()
	}()
	return wg.Wait
}

func stopInteractiveProgram(p *tea.Program) {
	quitTeaProgram(p)
}

func emitInteractiveProgramWarning(events output.EventSink, err error) {
	events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "session_health",
		Severity: "warning",
		Notes:    []string{fmt.Sprintf("tui runtime failed: %v", err)},
	}))
}

// mcpInitOnce runs the one-shot MCP wait and registry re-registration exactly
// once in the background, protected by sync.Once. The tool registry is not
// thread-safe, so this initialization must complete before any agent turn
// accesses the registry. A concurrent turn submission that calls once.Do while
// the background goroutine is running blocks until the init completes, then
// observes the error.
type mcpInitOnce struct {
	once sync.Once
	err  error
}

// run executes WaitInit, registers the connected tool defs in place, and arms
// the MCP state producer. If WaitInit returns an error, run returns early
// without arming (preserving the states-only snapshot mode indefinitely).
func (i *mcpInitOnce) run(ctx context.Context, rt cliRuntime) {
	if rt.mcpManager != nil {
		i.err = rt.mcpManager.WaitInit(ctx)
		if i.err != nil {
			return
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
}

// sessionRunner adapts cliRunner to the runExecutor interface expected by
// the interactive session.
type sessionRunner struct {
	runner  cliRunner
	mcpInit *mcpInitOnce
}

func (r sessionRunner) Compact(ctx context.Context, conversation []agent.Message, skillNames []string, tools []provider.ToolSpec, steering string) ([]agent.Message, error) {
	return r.runner.Compact(ctx, conversation, skillNames, tools, steering)
}

func (r sessionRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (interactive.RunResult, error) {
	if r.mcpInit != nil {
		r.mcpInit.once.Do(func() { r.mcpInit.run(ctx, r.runner.runtime) })
		if r.mcpInit.err != nil {
			return interactive.RunResult{}, r.mcpInit.err
		}
	}
	result, err := r.runner.Run(ctx, conversation, skillNames, drainSteers)
	return interactive.RunResult{
		Conversation:    result.Conversation,
		WorkflowHandoff: result.WorkflowHandoff,
	}, err
}
