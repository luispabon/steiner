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
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/spf13/cobra"
)

const terminalClearSequence = "\x1b[2J\x1b[H"

var runTeaProgram = func(p *tea.Program) (tea.Model, error) {
	return p.Run()
}

var quitTeaProgram = func(p *tea.Program) {
	p.Quit()
}

func clearTerminalScreen(w io.Writer) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, terminalClearSequence)
}

func runInteractiveMode(cmd *cobra.Command, flags *cliFlags) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}

	// Create session without runner first so we can use its display sink
	// for building the interactive registry.
	//
	// Only set HistoryWriter when non-nil to avoid wrapping a nil
	// *history.Writer in the historyWriter interface, which would
	// pass a != nil check but panic on method call.
	sessDeps := interactive.Dependencies{
		BaseEvents:      rt.events,
		SkillNames:      rt.skillNames,
		Config:          rt.cfg,
		Provider:        rt.provider,
		ProviderFactory: rt.providerFactory,
		HomeDir:         rt.homeDir,
		WorkDir:         rt.workDir,
		SessionStore:    rt.sessionStore,
	}
	if rt.historyWriter != nil {
		sessDeps.HistoryWriter = rt.historyWriter
	}
	sess, err := interactive.NewSession(sessDeps)
	if err != nil {
		return err
	}

	// Build interactive registry using session's display sink.
	interactiveRegistry, err := runtimeRegistryWithSink(rt.cfg, rt.workDir, sess.DisplaySink(), true)
	if err != nil {
		return fmt.Errorf("build interactive registry: %w", err)
	}
	rt.registry = interactiveRegistry
	rt.toolNames = interactiveRegistry.Names()

	selected, err := selectedModelConfig(rt.cfg)
	if err != nil {
		return err
	}

	tuiApp := tui.NewApp(tui.Config{
		Model:                         rt.cfg.Model.Model,
		ModelNames:                    modelAliasNames(rt.cfg),
		ModelContexts:                 modelContextSizes(rt.cfg),
		ModelBaseURLs:                 modelBaseURLs(rt.cfg),
		ProviderBaseURL:               selected.BaseURL,
		HomeDir:                       rt.homeDir,
		WorkingDir:                    rt.workDir,
		MaxTurns:                      0,
		SkillNames:                    rt.skillNames,
		ShowInternalScaffoldInference: rt.cfg.Debug.ShowInternalScaffoldInference,
		Controller:                    sess,
	})

	// Attach TUI sink to session event bus before creating the runner,
	// so the runner's copy of the runtime picks up the multi-sink events.
	rt.events = sess.EventSink()

	// Create runner with updated registry and approver, then wire into session.
	runner := cliRunner{
		runtime:            rt,
		runMode:            "interactive",
		streamingPreferred: true,
		currentModel:       func() config.ModelConfig { return sess.CurrentModelConfig() },
	}
	runner.approver = sess.Approver(rt.events)
	sess.SetRunner(sessionRunner{runner: runner})

	// Wire display_file forward target to TUI.
	sess.DisplaySink().Set(tuiApp.EventSink())

	p := tuiApp.NewProgram()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := runTeaProgram(p); err != nil {
			if !errors.Is(err, tea.ErrProgramKilled) {
				rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
					Kind:     "session_health",
					Severity: "warning",
					Notes:    []string{fmt.Sprintf("tui runtime failed: %v", err)},
				}))
			}
		}
		stop()
	}()

	if flags.resume != "" {
		if err := sess.LoadSessionByID(cmd.Context(), flags.resume); err != nil {
			quitTeaProgram(p)
			wg.Wait()
			clearTerminalScreen(cmd.OutOrStdout())
			closeRuntime(&rt)
			return err
		}
	}

	err = sess.Run(ctx)

	quitTeaProgram(p)
	wg.Wait()
	clearTerminalScreen(cmd.OutOrStdout())

	if err == nil {
		sessionID := sess.SessionID()
		if sessionID != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Resume this session: steiner --resume %s\n", sessionID)
		}
	}

	closeRuntime(&rt)
	return err
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
