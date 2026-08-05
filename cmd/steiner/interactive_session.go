package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		Provider:          rt.provider,
		ProviderFactory:   rt.providerFactory,
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
	}
	registry := runtimeRegistryWithSinkAndMode(rt.cfg, rt.workDir, sess.DisplaySink(), true, sess.WorkflowHandoffResponder(sess.EventSink()), rt.sandbox, sess, rt.mcpManager)
	rt.registry = registry
	rt.toolNames = registry.Names()
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
		SandboxStatus:      rt.cfg.Sandbox.Status,
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
		servers = append(servers, tui.MCPServerStatus{
			Name:      s.Name,
			State:     string(s.Status),
			Transport: s.Transport,
			Tools:     s.Tools,
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
		promptCacheKey:           sess.SessionID(),
		modeGetterFunc:           sess.Mode,
	}
	runner.approver = sess.Approver(rt.events)
	sess.SetRunner(sessionRunner{runner: runner})
}
func resumeInteractiveSession(ctx context.Context, sess *interactive.Session, resumeID string, p *tea.Program, out io.Writer, rt *cliRuntime) error {
	if resumeID == "" {
		return nil
	}
	if err := sess.LoadSessionByID(ctx, resumeID); err != nil {
		stopInteractiveProgram(p)
		clearTerminalScreen(out)
		closeRuntime(rt)
		return err
	}
	return nil
}

func runInteractiveSession(cmd *cobra.Command, sess *interactive.Session, p *tea.Program, rt *cliRuntime) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	wait := startInteractiveProgram(p, rt.events, stop)
	err := sess.Run(ctx)
	stopInteractiveProgram(p)
	wait()
	clearTerminalScreen(cmd.OutOrStdout())
	if err == nil && sess.SessionTitle() != "" {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nResume this session:\n  steiner --resume %s\n\n", sess.SessionID())
	}
	closeRuntime(rt)
	return err
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

// sessionRunner adapts cliRunner to the runExecutor interface expected by
// the interactive session.
type sessionRunner struct {
	runner cliRunner
}

func (r sessionRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (interactive.RunResult, error) {
	result, err := r.runner.Run(ctx, conversation, skillNames, drainSteers)
	return interactive.RunResult{
		Conversation:    result.Conversation,
		WorkflowHandoff: result.WorkflowHandoff,
	}, err
}
