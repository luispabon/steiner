package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
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

func buildInteractiveRuntime(rt cliRuntime, sess *interactive.Session) (cliRuntime, error) {
	registry, err := runtimeRegistryWithSink(rt.cfg, rt.workDir, sess.DisplaySink(), true)
	if err != nil {
		return cliRuntime{}, fmt.Errorf("build interactive registry: %w", err)
	}
	rt.registry = registry
	rt.toolNames = registry.Names()
	rt.events = sess.EventSink()
	return rt, nil
}

func buildInteractiveApp(rt cliRuntime, sess *interactive.Session) *tui.App {
	selected := selectedModelConfig(rt.cfg)
	selectedProviderBaseURL := ""
	if p, ok := rt.cfg.Providers[selected.Provider]; ok {
		selectedProviderBaseURL = p.BaseURL
	}
	tuiCfg := tui.Config{
		Model:             selected.ID,
		ModelNames:        modelAliasNames(rt.cfg),
		ModelContexts:     modelContextSizes(rt.cfg),
		ModelBaseURLs:     modelBaseURLs(rt.cfg),
		ProviderBaseURL:   selectedProviderBaseURL,
		HomeDir:           rt.homeDir,
		WorkingDir:        rt.workDir,
		MaxTurns:          0,
		Version:           version,
		SkillNames:        rt.skillNames,
		SkillDescriptions: rt.skillDescriptions,
		SkillSources:      rt.skillSources,
		Controller:        sess,
	}
	if rt.sessionStore != nil {
		tuiCfg.SessionStore = rt.sessionStore
	}
	return tui.NewApp(tuiCfg)
}

func wireInteractiveRunner(rt cliRuntime, sess *interactive.Session) {
	runner := cliRunner{
		runtime:            rt,
		runMode:            "interactive",
		streamingPreferred: true,
		currentAlias:       sess.CurrentModelAlias,
		cavemanMode:        sess.CavemanMode,
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

func (r sessionRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string) ([]agent.Message, error) {
	result, err := r.runner.Run(ctx, conversation, skillNames)
	if err != nil {
		return nil, err
	}
	return result.Conversation, nil
}
