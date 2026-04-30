package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type activeRunController struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

const terminalClearSequence = "\x1b[2J\x1b[H"

type interactiveMode struct {
	rt                  cliRuntime
	ctx                 context.Context
	stop                context.CancelFunc
	teaProgram          *tea.Program
	runController       *activeRunController
	runner              cliRunner
	enabledSkills       *interactiveSkills
	requestSnapshots    *requestSnapshotStore
	approvalCoordinator *tuiApprovalCoordinator
	submissions         chan string
	contextInspect      chan struct{}
	configInspect       chan struct{}
	clearSession        chan struct{}
	triggerCompact      chan struct{}
	exitRequests        chan struct{}
	wg                  sync.WaitGroup
	conversation        []agent.Message
}

var runTeaProgram = func(p *tea.Program) (tea.Model, error) {
	return p.Run()
}

var quitTeaProgram = func(p *tea.Program) {
	p.Quit()
}

func (c *activeRunController) Set(cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancel = cancel
}

func (c *activeRunController) Clear() {
	c.mu.Lock()
	c.cancel = nil
	c.mu.Unlock()
}

func (c *activeRunController) Interrupt() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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
	mode := &interactiveMode{
		rt:                  rt,
		runController:       &activeRunController{},
		runner:              cliRunner{runtime: rt, runMode: "interactive", streamingPreferred: true},
		enabledSkills:       newInteractiveSkills(rt.skillNames),
		requestSnapshots:    &requestSnapshotStore{},
		approvalCoordinator: &tuiApprovalCoordinator{},
		submissions:         make(chan string, 1),
		contextInspect:      make(chan struct{}, 1),
		configInspect:       make(chan struct{}, 1),
		clearSession:        make(chan struct{}, 1),
		triggerCompact:      make(chan struct{}, 1),
		exitRequests:        make(chan struct{}, 1),
	}

	// Build a ForwardSink so the display_file tool can emit events even though
	// the TUI event sink is assembled after the registry. We update it below
	// once the TUI sink is ready.
	displaySink := output.NewForwardSink()

	// Rebuild the registry with interactive mode and the forward sink wired in.
	interactiveRegistry, err := runtimeRegistryWithSink(rt.cfg, rt.workDir, displaySink, true)
	if err != nil {
		return nil, fmt.Errorf("build interactive registry: %w", err)
	}
	rt.registry = interactiveRegistry

	selected, err := selectedModelConfig(rt.cfg)
	if err != nil {
		return nil, err
	}

	tuiApp := tui.NewApp(tui.Config{
		Model:           rt.cfg.Model.Model,
		ModelNames:      modelAliasNames(rt.cfg),
		ModelContexts:   modelContextSizes(rt.cfg),
		ProviderBaseURL: selected.BaseURL,
		HomeDir:         rt.homeDir,
		WorkingDir:      rt.workDir,
		MaxTurns:        0,
		SkillNames:      rt.skillNames,
		OnSubmit: func(text string) {
			select {
			case mode.submissions <- text:
			default:
			}
		},
		OnContextInspect: func() {
			select {
			case mode.contextInspect <- struct{}{}:
			default:
			}
		},
		OnConfigInspect: func() {
			select {
			case mode.configInspect <- struct{}{}:
			default:
			}
		},
		OnApproval: func(submission tui.ApprovalSubmission) {
			mode.approvalCoordinator.submit(submission)
		},
		OnInterrupt: func() {
			mode.runController.Interrupt()
		},
		OnExitRequested: func() {
			select {
			case mode.exitRequests <- struct{}{}:
			default:
			}
		},
		OnSkillToggle: func(name string, enabled bool) {
			mode.enabledSkills.Set(name, enabled)
		},
		OnModelSwitch: func(name string) (string, bool) {
			selected, err := switchModelConfigByAlias(&mode.runner.runtime.cfg, name)
			if err != nil {
				mode.rt.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Model switch failed: %v", err)))
				return "", false
			}
			return selected.BaseURL, true
		},
		OnClear: func() {
			select {
			case mode.clearSession <- struct{}{}:
			default:
			}
		},
		OnCompact: func() {
			select {
			case mode.triggerCompact <- struct{}{}:
			default:
			}
		},
	})
	rt.events = output.NewMultiSink(
		rt.events,
		tuiApp.EventSink(),
		output.SinkFunc(func(event output.Event) {
			if payload, ok := event.Payload.(output.APIRequestEvent); ok {
				mode.requestSnapshots.Store(output.RequestContextSnapshot{
					Model:       payload.Model,
					Messages:    payload.Messages,
					Tools:       payload.Tools,
					MaxTokens:   payload.MaxTokens,
					Blocks:      payload.Blocks,
					ModelBudget: payload.ModelBudget,
				})
			}
		}),
	)
	mode.rt.events = rt.events
	mode.runner.runtime.events = rt.events

	// Wire the TUI sink into the display_file forward sink so the tool can emit
	// display events once the TUI is running.
	displaySink.Set(tuiApp.EventSink())

	mode.teaProgram = tuiApp.NewProgram()
	mode.runner.approver = agent.NewEventingApprover(rt.events, newTUIApprovalResponder(mode.approvalCoordinator))

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
		case <-m.contextInspect:
			m.emitContextReport()
			continue
		case <-m.configInspect:
			m.emitConfigReport()
			continue
		case <-m.clearSession:
			m.conversation = nil
		case <-m.triggerCompact:
			m.conversation = m.handleManualCompaction(m.conversation)
		case text := <-m.submissions:
			m.handleSubmission(text)
		}
	}
}

func (m *interactiveMode) emitContextReport() {
	if snapshot, ok := m.requestSnapshots.Snapshot(); ok {
		report, err := output.BuildContextReport(m.ctx, snapshot)
		if err != nil {
			m.rt.events.Emit(output.NewContextReportEvent("Context report unavailable.\n\n" + err.Error()))
			return
		}
		m.rt.events.Emit(output.NewContextReportEvent(report))
		return
	}
	m.rt.events.Emit(output.NewContextReportEvent("No request recorded yet in this interactive session."))
}

func (m *interactiveMode) emitConfigReport() {
	report, err := buildConfigOverlayReport(m.runner.runtime.cfg)
	if err != nil {
		m.rt.events.Emit(output.NewConfigReportEvent("Resolved config unavailable.\n\n" + err.Error()))
		return
	}
	m.rt.events.Emit(output.NewConfigReportEvent(report))
}

func (m *interactiveMode) handleManualCompaction(conversation []agent.Message) []agent.Message {
	if len(conversation) == 0 {
		m.rt.events.Emit(output.NewContextReportEvent("No conversation to compact."))
		return conversation
	}
	selected, err := selectedModelConfig(m.runner.runtime.cfg)
	if err != nil {
		m.emitCompactError(err)
		return conversation
	}
	prov := m.runner.runtime.provider
	if m.runner.runtime.providerFactory != nil {
		prov, err = m.runner.runtime.providerFactory(selected)
		if err != nil {
			m.emitCompactError(err)
			return conversation
		}
	}
	modelBudget := prompt.ModelTokenBudget{
		ContextSize:         selected.ContextSize,
		MaxCompletionTokens: selected.MaxCompletionTokens,
		SafetyMarginTokens:  selected.Compaction.SafetyMarginTokens,
		SummaryMaxTokens:    selected.Compaction.SummaryMaxTokens,
	}
	assembly := prompt.AssemblyOptions{
		HomeDir:         m.runner.runtime.homeDir,
		ProjectRoot:     m.runner.runtime.workDir,
		SkillsRoot:      prompt.DefaultSkillsRoot(m.runner.runtime.homeDir),
		ModelBudget:     modelBudget,
		PromptOverrides: selected.Prompts,
	}

	compactReq := agent.RunRequest{
		Provider:    prov,
		Prompt:      assembly,
		ModelBudget: modelBudget,
		Model:       selected.Model,
		Events:      m.rt.events,
	}
	agentRunner := agent.NewRunner()
	newConv, err := agentRunner.Compact(m.ctx, compactReq, conversation)
	if err != nil {
		m.emitCompactError(err)
		return conversation
	}
	m.rt.events.Emit(output.NewContextReportEvent("Compaction triggered manually."))
	return newConv
}

func (m *interactiveMode) emitCompactError(err error) {
	m.rt.events.Emit(output.Event{
		Type:    output.EventTypeStopReason,
		Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)},
	})
}

func (m *interactiveMode) handleSubmission(text string) {
	m.conversation = append(m.conversation, agent.Message{Role: agent.MessageRoleUser, Content: text})
	runCtx, cancel := context.WithCancel(m.ctx)
	m.runController.Set(cancel)
	result, err := m.runner.Run(runCtx, m.conversation, m.enabledSkills.Snapshot())
	cancel()
	m.runController.Clear()
	if err != nil {
		m.rt.events.Emit(output.Event{
			Type:    output.EventTypeStopReason,
			Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Error: %v", err)},
		})
		return
	}
	if m.rt.historyWriter != nil {
		if err := m.rt.historyWriter.Record(text); err != nil {
			m.rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("history record: %v", err)},
			}))
		}
		prompts, err := m.rt.historyWriter.Load()
		if err != nil {
			m.rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("history load: %v", err)},
			}))
			prompts = nil
		}
		m.rt.events.Emit(output.NewHistoryLoadedEvent(prompts))
	}
	m.conversation = result.Conversation
}

func buildConfigOverlayReport(cfg config.Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal resolved config: %w", err)
	}
	return "```yaml\n" + strings.TrimRight(string(data), "\n") + "\n```", nil
}
