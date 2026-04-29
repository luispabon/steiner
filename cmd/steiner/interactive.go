package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tui"
	"github.com/spf13/cobra"
)

type activeRunController struct {
	mu     sync.Mutex
	cancel context.CancelFunc
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

func runInteractiveMode(cmd *cobra.Command, flags *cliFlags) error {
	rt, err := buildRuntime(cmd.Context(), cmd, flags)
	if err != nil {
		return err
	}
	defer closeRuntime(&rt)

	// Build a ForwardSink so the display_file tool can emit events even though
	// the TUI event sink is assembled after the registry. We update it below
	// once the TUI sink is ready.
	displaySink := output.NewForwardSink()

	// Rebuild the registry with interactive mode and the forward sink wired in.
	interactiveRegistry, err := runtimeRegistryWithSink(rt.cfg, rt.workDir, displaySink, true)
	if err != nil {
		return fmt.Errorf("build interactive registry: %w", err)
	}
	rt.registry = interactiveRegistry

	submissions := make(chan string, 1)
	contextInspect := make(chan struct{}, 1)
	clearSession := make(chan struct{}, 1)
	triggerCompact := make(chan struct{}, 1)
	enabledSkills := newInteractiveSkills(rt.skillNames)
	requestSnapshots := &requestSnapshotStore{}
	runController := &activeRunController{}
	runner := cliRunner{runtime: rt, streamingPreferred: true}
	selected, err := selectedModelConfig(rt.cfg)
	if err != nil {
		return err
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
			case submissions <- text:
			default:
			}
		},
		OnContextInspect: func() {
			select {
			case contextInspect <- struct{}{}:
			default:
			}
		},
		OnInterrupt: func() {
			runController.Interrupt()
		},
		OnSkillToggle: func(name string, enabled bool) {
			enabledSkills.Set(name, enabled)
		},
		OnModelSwitch: func(name string) (string, bool) {
			selected, err := switchModelConfigByAlias(&runner.runtime.cfg, name)
			if err != nil {
				rt.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Model switch failed: %v", err)))
				return "", false
			}
			return selected.BaseURL, true
		},
		OnClear: func() {
			select {
			case clearSession <- struct{}{}:
			default:
			}
		},
		OnCompact: func() {
			select {
			case triggerCompact <- struct{}{}:
			default:
			}
		},
	})
	rt.events = output.NewMultiSink(
		rt.events,
		tuiApp.EventSink(),
		output.SinkFunc(func(event output.Event) {
			if payload, ok := event.Payload.(output.APIRequestEvent); ok {
				requestSnapshots.Store(output.RequestContextSnapshot{
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
	runner.runtime.events = rt.events

	// Wire the TUI sink into the display_file forward sink so the tool can emit
	// display events once the TUI is running.
	displaySink.Set(tuiApp.EventSink())

	// Build the tea.Program so we can pass it to the huh approval responder.
	// The program reference lets the responder pause/restore the terminal around
	// huh form rendering (see huh_boundary.go).
	teaProgram := tuiApp.NewProgram()
	huhApprover := newHuhApprovalResponder(teaProgram)
	approver := agent.NewEventingApprover(rt.events, huhApprover)

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := teaProgram.Run(); err != nil {
			// Non-fatal: program exit errors are expected on normal quit.
			_ = err
		}
		stop()
	}()

	var conversation []agent.Message
	runner.approver = approver
	if rt.historyWriter != nil {
		prompts, err := rt.historyWriter.Load()
		if err != nil {
			rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to load history: %v", err)},
			}))
		}
		rt.events.Emit(output.NewHistoryLoadedEvent(prompts))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-contextInspect:
			if snapshot, ok := requestSnapshots.Snapshot(); ok {
				report, err := output.BuildContextReport(ctx, snapshot)
				if err != nil {
					rt.events.Emit(output.NewContextReportEvent("Context report unavailable.\n\n" + err.Error()))
					continue
				}
				rt.events.Emit(output.NewContextReportEvent(report))
				continue
			}
			rt.events.Emit(output.NewContextReportEvent("No request recorded yet in this interactive session."))
			continue
		case <-clearSession:
			conversation = nil
		case <-triggerCompact:
			if len(conversation) == 0 {
				rt.events.Emit(output.NewContextReportEvent("No conversation to compact."))
				continue
			}
			selected, err := selectedModelConfig(runner.runtime.cfg)
			if err != nil {
				rt.events.Emit(output.Event{
					Type:    output.EventTypeStopReason,
					Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)},
				})
				continue
			}
			prov := runner.runtime.provider
			if runner.runtime.providerFactory != nil {
				prov, err = runner.runtime.providerFactory(selected)
				if err != nil {
					rt.events.Emit(output.Event{
						Type:    output.EventTypeStopReason,
						Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)},
					})
					continue
				}
			}
			modelBudget := prompt.ModelTokenBudget{
				ContextSize:         selected.ContextSize,
				MaxCompletionTokens: selected.MaxCompletionTokens,
				SafetyMarginTokens:  selected.Compaction.SafetyMarginTokens,
				SummaryMaxTokens:    selected.Compaction.SummaryMaxTokens,
			}
			assembly := prompt.AssemblyOptions{
				HomeDir:         runner.runtime.homeDir,
				ProjectRoot:     runner.runtime.workDir,
				SkillsRoot:      prompt.DefaultSkillsRoot(runner.runtime.homeDir),
				ModelBudget:     modelBudget,
				PromptOverrides: selected.Prompts,
			}

			compactReq := agent.RunRequest{
				Provider:    prov,
				Prompt:      assembly,
				ModelBudget: modelBudget,
				Model:       selected.Model,
				Events:      rt.events,
			}
			agentRunner := agent.NewRunner()
			newConv, err := agentRunner.Compact(ctx, compactReq, conversation)
			if err != nil {
				rt.events.Emit(output.Event{
					Type:    output.EventTypeStopReason,
					Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Compaction error: %v", err)},
				})
				continue
			}
			conversation = newConv
			rt.events.Emit(output.NewContextReportEvent("Compaction triggered manually."))
		case text := <-submissions:
			conversation = append(conversation, agent.Message{Role: agent.MessageRoleUser, Content: text})
			runCtx, cancel := context.WithCancel(ctx)
			runController.Set(cancel)
			result, err := runner.Run(runCtx, conversation, enabledSkills.Snapshot())
			cancel()
			runController.Clear()
			if err != nil {
				rt.events.Emit(output.Event{
					Type:    output.EventTypeStopReason,
					Payload: output.StopReasonEvent{Reason: fmt.Sprintf("Error: %v", err)},
				})
				continue
			}
			if rt.historyWriter != nil {
				if err := rt.historyWriter.Record(text); err != nil {
					rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
						Kind:     "session_health",
						Severity: "warning",
						Notes:    []string{fmt.Sprintf("history record: %v", err)},
					}))
				}
				prompts, err := rt.historyWriter.Load()
				if err != nil {
					rt.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
						Kind:     "session_health",
						Severity: "warning",
						Notes:    []string{fmt.Sprintf("history load: %v", err)},
					}))
					prompts = nil
				}
				rt.events.Emit(output.NewHistoryLoadedEvent(prompts))
			}
			conversation = result.Conversation
		}
	}
}
