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
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/spf13/cobra"
)

const terminalClearSequence = "\x1b[2J\x1b[H"

type interactiveMode struct {
	session      *interactive.Session
	rt           cliRuntime
	ctx          context.Context
	stop         context.CancelFunc
	teaProgram   *tea.Program
	runner       cliRunner
	clearSession chan struct{}
	exitRequests chan struct{}
	wg           sync.WaitGroup
}

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
	mode, err := newInteractiveMode(cmd, flags)
	if err != nil {
		return err
	}
	defer mode.close(cmd)
	return mode.run()
}

func newInteractiveMode(cmd *cobra.Command, flags *cliFlags) (*interactiveMode, error) {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return nil, err
	}
	sess := interactive.NewSession(interactive.Dependencies{
		BaseEvents:      rt.events,
		Runner:          sessionRunner{runner: cliRunner{runtime: rt, runMode: "interactive", streamingPreferred: true}},
		HistoryWriter:   rt.historyWriter,
		SkillNames:      rt.skillNames,
		Config:          rt.cfg,
		Provider:        rt.provider,
		ProviderFactory: rt.providerFactory,
		HomeDir:         rt.homeDir,
		WorkDir:         rt.workDir,
	})

	mode := &interactiveMode{
		session:      sess,
		rt:           rt,
		runner:       cliRunner{runtime: rt, runMode: "interactive", streamingPreferred: true},
		clearSession: make(chan struct{}, 1),
		exitRequests: make(chan struct{}, 1),
	}

	// Rebuild the registry with interactive mode and the forward sink wired in.
	interactiveRegistry, err := runtimeRegistryWithSink(rt.cfg, rt.workDir, sess.DisplaySink(), true)
	if err != nil {
		return nil, fmt.Errorf("build interactive registry: %w", err)
	}
	rt.registry = interactiveRegistry
	mode.runner.runtime.registry = interactiveRegistry
	rt.toolNames = interactiveRegistry.Names()
	mode.runner.runtime.toolNames = append([]string(nil), rt.toolNames...)

	selected, err := selectedModelConfig(rt.cfg)
	if err != nil {
		return nil, err
	}

	tuiApp := tui.NewApp(tui.Config{
		Model:           rt.cfg.Model.Model,
		ModelNames:      modelAliasNames(rt.cfg),
		ModelContexts:   modelContextSizes(rt.cfg),
		ModelBaseURLs:   modelBaseURLs(rt.cfg),
		ProviderBaseURL: selected.BaseURL,
		HomeDir:         rt.homeDir,
		WorkingDir:      rt.workDir,
		MaxTurns:        0,
		SkillNames:      rt.skillNames,
		Controller:      sess,
	})
	// Attach TUI sink to the session event bus. The session already owns the
	// base events, display forward sink, and snapshot capture.
	rt.events = output.NewMultiSink(sess.EventSink(), tuiApp.EventSink())
	mode.rt.events = rt.events
	mode.runner.runtime.events = rt.events

	// Wire the TUI sink into the display_file forward sink so the tool can emit
	// display events once the TUI is running.
	sess.DisplaySink().Set(tuiApp.EventSink())

	mode.teaProgram = tuiApp.NewProgram()
	mode.runner.approver = sess.Approver(rt.events)

	mode.ctx, mode.stop = signal.NotifyContext(cmd.Context(), os.Interrupt)
	mode.wg.Add(1)
	go func() {
		defer mode.wg.Done()
		if _, err := runTeaProgram(mode.teaProgram); err != nil {
			if !errors.Is(err, tea.ErrProgramKilled) {
				mode.rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
					Kind:     "session_health",
					Severity: "warning",
					Notes:    []string{fmt.Sprintf("tui runtime failed: %v", err)},
				}))
			}
		}
		mode.stop()
	}()
	return mode, nil
}

func (m *interactiveMode) close(cmd *cobra.Command) {
	quitTeaProgram(m.teaProgram)
	m.wg.Wait()
	clearTerminalScreen(cmd.OutOrStdout())
	closeRuntime(&m.rt)
}

func (m *interactiveMode) run() error {
	if m.rt.historyWriter != nil {
		prompts, err := m.rt.historyWriter.Load()
		if err != nil {
			m.rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to load history: %v", err)},
			}))
		}
		m.rt.events.Emit(output.NewHistoryLoadedEvent(prompts))
	}
	for {
		select {
		case <-m.ctx.Done():
			return nil
		case <-m.exitRequests:
			return nil
		case <-m.clearSession:
			m.session.SetConversation(nil)
		}
	}
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
