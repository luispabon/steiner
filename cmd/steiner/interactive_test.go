package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/spf13/cobra"
)

func TestActiveRunControllerInterruptCancelsCurrentRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	controller := &interactive.ActiveRunController{}
	controller.Set(cancel)

	controller.Interrupt()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected interrupt to cancel the active run")
	}
}

func TestRunManualCompactionEmitsLifecycleAndClearsControllerOnSuccess(t *testing.T) {
	var events []output.Event
	sess := interactive.NewSession(interactive.Dependencies{
		DisplaySink: nil,
	})
	ctrl := sess.ActiveRunController()
	mode := &interactiveMode{
		ctx:     context.Background(),
		session: sess,
		rt: cliRuntime{
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		},
	}

	result, err := mode.runManualCompaction("test-model", func(ctx context.Context) ([]agent.Message, error) {
		mode.rt.events.Emit(output.NewAssistantChunkEvent(1, "streamed chunk"))
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	})
	if err != nil {
		t.Fatalf("runManualCompaction() error = %v, want nil", err)
	}
	if got, want := len(result), 1; got != want {
		t.Fatalf("result len = %d, want %d", got, want)
	}

	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after successful compaction")
	}

	if got, want := len(events), 4; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeAssistantChunk; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}
	if got, want := events[3].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[3].Type = %q, want %q", got, want)
	}

	started, ok := events[0].Payload.(output.RunStartedEvent)
	if !ok {
		t.Fatalf("events[0].Payload type = %T, want output.RunStartedEvent", events[0].Payload)
	}
	if got, want := started.Mode, "interactive"; got != want {
		t.Fatalf("run started mode = %q, want %q", got, want)
	}
	if got, want := started.Model, "test-model"; got != want {
		t.Fatalf("run started model = %q, want %q", got, want)
	}

	compacting, ok := events[1].Payload.(output.ContextDiagnosticsEvent)
	if !ok {
		t.Fatalf("events[1].Payload type = %T, want output.ContextDiagnosticsEvent", events[1].Payload)
	}
	if got, want := compacting.Kind, "compaction"; got != want {
		t.Fatalf("compaction kind = %q, want %q", got, want)
	}
	if got, want := compacting.Severity, "compacting"; got != want {
		t.Fatalf("compaction severity = %q, want %q", got, want)
	}

	finished, ok := events[3].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[3].Payload type = %T, want output.RunFinishedEvent", events[3].Payload)
	}
	if got, want := finished.Reason, "complete"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, ""; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionEmitsRunFinishedAndClearsControllerOnError(t *testing.T) {
	var events []output.Event
	sess := interactive.NewSession(interactive.Dependencies{
		DisplaySink: nil,
	})
	ctrl := sess.ActiveRunController()
	mode := &interactiveMode{
		ctx:     context.Background(),
		session: sess,
		rt: cliRuntime{
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		},
	}

	_, err := mode.runManualCompaction("test-model", func(context.Context) ([]agent.Message, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatal("runManualCompaction() error = nil, want non-nil")
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after failed compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "error"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, "boom"; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionCancelsAndClearsController(t *testing.T) {
	var events []output.Event
	sess := interactive.NewSession(interactive.Dependencies{
		DisplaySink: nil,
	})
	ctrl := sess.ActiveRunController()
	mode := &interactiveMode{
		ctx:     context.Background(),
		session: sess,
		rt: cliRuntime{
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		},
	}

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-started
		ctrl.Interrupt()
		close(done)
	}()

	_, err := mode.runManualCompaction("test-model", func(ctx context.Context) ([]agent.Message, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runManualCompaction() error = %v, want context.Canceled", err)
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after cancelled compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "cancelled"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, context.Canceled.Error(); got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestClearTerminalScreenWritesANSISequence(t *testing.T) {
	var buf bytes.Buffer

	clearTerminalScreen(&buf)

	if got, want := buf.String(), terminalClearSequence; got != want {
		t.Fatalf("clearTerminalScreen() = %q, want %q", got, want)
	}
}

func TestSwitchModelConfigByAliasUpdatesRuntimeConfig(t *testing.T) {
	cfg := config.Config{
		Model: config.ModelConfig{
			Model:   "old-model",
			BaseURL: "http://old.example/v1",
		},
		Models: map[string]config.ModelConfig{
			"fast": {
				Model:   "new-model",
				BaseURL: "http://new.example/v1",
			},
		},
	}

	selected, err := switchModelConfigByAlias(&cfg, "fast")
	if err != nil {
		t.Fatalf("switchModelConfigByAlias() error = %v", err)
	}
	if got, want := selected.Model, "new-model"; got != want {
		t.Fatalf("selected model = %q, want %q", got, want)
	}
	if got, want := selected.BaseURL, "http://new.example/v1"; got != want {
		t.Fatalf("selected base URL = %q, want %q", got, want)
	}
	if got, want := cfg.Model.Model, "new-model"; got != want {
		t.Fatalf("cfg.Model.Model = %q, want %q", got, want)
	}
	if got, want := cfg.Model.BaseURL, "http://new.example/v1"; got != want {
		t.Fatalf("cfg.Model.BaseURL = %q, want %q", got, want)
	}
}

func TestInteractiveRunnerUsesWrappedEventSink(t *testing.T) {
	baseEvents := 0
	bridgedEvents := 0

	rt := cliRuntime{
		events: output.SinkFunc(func(output.Event) {
			baseEvents++
		}),
	}
	runner := cliRunner{runtime: rt}

	rt.events = output.NewMultiSink(
		rt.events,
		output.SinkFunc(func(output.Event) {
			bridgedEvents++
		}),
	)
	runner.runtime.events = rt.events

	runner.runtime.events.Emit(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))

	if got, want := baseEvents, 1; got != want {
		t.Fatalf("base event count = %d, want %d", got, want)
	}
	if got, want := bridgedEvents, 1; got != want {
		t.Fatalf("bridged event count = %d, want %d", got, want)
	}
}

func TestInteractiveRunnerUsesRebuiltInteractiveRegistry(t *testing.T) {
	initial := tool.NewRegistry(tool.ToolDef{Name: "display_file", Description: "non-interactive"})
	interactive := tool.NewRegistry(tool.ToolDef{Name: "display_file", Description: "interactive"})

	rt := cliRuntime{
		registry:  initial,
		toolNames: initial.Names(),
	}
	runner := cliRunner{runtime: rt}

	rt.registry = interactive
	rt.toolNames = interactive.Names()
	runner.runtime.registry = interactive
	runner.runtime.toolNames = append([]string(nil), rt.toolNames...)

	got, ok := runner.runtime.registry.Get("display_file")
	if !ok {
		t.Fatal("runner registry missing display_file")
	}
	if got.Description != "interactive" {
		t.Fatalf("runner display_file description = %q, want %q", got.Description, "interactive")
	}
	if len(runner.runtime.toolNames) != 1 || runner.runtime.toolNames[0] != "display_file" {
		t.Fatalf("runner toolNames = %#v, want [display_file]", runner.runtime.toolNames)
	}
}

func TestInteractiveModeEmitsWarningWhenTUIProgramFails(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldRunTeaProgram := runTeaProgram
	oldQuitTeaProgram := quitTeaProgram
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		runTeaProgram = oldRunTeaProgram
		quitTeaProgram = oldQuitTeaProgram
	})

	var events []output.Event
	buildRuntime = func(context.Context, *cobra.Command, *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg: config.Config{
				Model: config.ModelConfig{
					Model:   "test-model",
					BaseURL: "http://localhost:11434/v1",
				},
			},
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		}, nil
	}
	runTeaProgram = func(*tea.Program) (tea.Model, error) {
		return nil, errors.New("boom")
	}
	quitTeaProgram = func(*tea.Program) {}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runInteractiveMode(cmd, &cliFlags{}); err != nil {
		t.Fatalf("runInteractiveMode() error = %v, want nil", err)
	}

	var found bool
	for _, event := range events {
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			continue
		}
		if payload.Severity == "warning" && strings.Contains(strings.Join(payload.Notes, " "), "tui runtime failed: boom") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want warning diagnostic for TUI failure", events)
	}
}

func TestInteractiveModeSuppressesProgramKilled(t *testing.T) {
	oldBuildRuntime := buildRuntime
	oldRunTeaProgram := runTeaProgram
	oldQuitTeaProgram := quitTeaProgram
	t.Cleanup(func() {
		buildRuntime = oldBuildRuntime
		runTeaProgram = oldRunTeaProgram
		quitTeaProgram = oldQuitTeaProgram
	})

	var events []output.Event
	buildRuntime = func(context.Context, *cobra.Command, *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg: config.Config{
				Model: config.ModelConfig{
					Model:   "test-model",
					BaseURL: "http://localhost:11434/v1",
				},
			},
			registry: tool.NewRegistry(),
			workDir:  t.TempDir(),
			homeDir:  t.TempDir(),
			events: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
		}, nil
	}
	runTeaProgram = func(*tea.Program) (tea.Model, error) {
		return nil, tea.ErrProgramKilled
	}
	quitTeaProgram = func(*tea.Program) {}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := runInteractiveMode(cmd, &cliFlags{}); err != nil {
		t.Fatalf("runInteractiveMode() error = %v, want nil", err)
	}

	for _, event := range events {
		if payload, ok := event.Payload.(output.ContextDiagnosticsEvent); ok && payload.Severity == "warning" && strings.Contains(strings.Join(payload.Notes, " "), "tui runtime failed") {
			t.Fatalf("events = %#v, want no warning diagnostic for program killed", events)
		}
	}
}

func TestBuildConfigOverlayReportFormatsResolvedYAML(t *testing.T) {
	report, err := buildConfigOverlayReport(config.Config{
		Model: config.ModelConfig{
			BaseURL: "http://localhost:11434/v1",
			Model:   "qwen",
		},
		Logging: config.LoggingConfig{
			Enabled: true,
			Level:   "debug",
		},
	})
	if err != nil {
		t.Fatalf("buildConfigOverlayReport() error = %v", err)
	}
	if !strings.HasPrefix(report, "```yaml\n") {
		t.Fatalf("report prefix = %q, want yaml fence", report)
	}
	for _, want := range []string{"model:", "base_url: http://localhost:11434/v1", "model: qwen", "logging:"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want %q", report, want)
		}
	}
	if !strings.HasSuffix(report, "\n```") {
		t.Fatalf("report suffix = %q, want closing fence", report)
	}
}
