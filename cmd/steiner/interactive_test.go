package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func TestNewOneshotRunnerFactoryBuilderBindsIdentity(t *testing.T) {
	cmd := &cobra.Command{}
	flags := &cliFlags{}
	const projectRoot = "/tmp/steiner-project"
	var sink output.EventSink = output.SinkFunc(func(output.Event) {})

	builder := newOneshotRunnerFactoryBuilder(cmd, flags, projectRoot, sink)
	if builder == nil {
		t.Fatal("newOneshotRunnerFactoryBuilder() returned nil; oneshot would report runner factory not configured")
	}

	identity, err := oneshot.NewRunIdentity("fix the bug")
	if err != nil {
		t.Fatalf("NewRunIdentity() error = %v", err)
	}

	factory := builder(identity)
	prf, ok := factory.(phaseRunnerFactory)
	if !ok {
		t.Fatalf("builder returned %T, want phaseRunnerFactory", factory)
	}
	if prf.cmd != cmd {
		t.Error("phaseRunnerFactory.cmd not threaded through")
	}
	if prf.flags != flags {
		t.Error("phaseRunnerFactory.flags not threaded through")
	}
	if prf.rootDir != projectRoot {
		t.Errorf("phaseRunnerFactory.rootDir = %q, want %q", prf.rootDir, projectRoot)
	}
	if prf.identity.ID != identity.ID {
		t.Errorf("phaseRunnerFactory.identity.ID = %q, want %q", prf.identity.ID, identity.ID)
	}
	if prf.events == nil {
		t.Error("phaseRunnerFactory.events is nil, want the supplied event sink")
	}
}

func TestClearTerminalScreenWritesANSISequence(t *testing.T) {
	var buf bytes.Buffer

	clearTerminalScreen(&buf)

	if got, want := buf.String(), terminalClearSequence; got != want {
		t.Fatalf("clearTerminalScreen() = %q, want %q", got, want)
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
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg: config.Config{
				DefaultModel: "test",
				Providers:    map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}},
				Models:       map[string]config.ModelConfig{"test": {Provider: "local", ID: "test-model"}},
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
	buildRuntime = func(_ context.Context, _ *cobra.Command, _ *cliFlags) (cliRuntime, error) {
		return cliRuntime{
			cfg: config.Config{
				DefaultModel: "test",
				Providers:    map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}},
				Models:       map[string]config.ModelConfig{"test": {Provider: "local", ID: "test-model"}},
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

func TestInteractiveEventSinkDoesNotDuplicateTUIEvents(t *testing.T) {
	tests := []struct {
		name string
		wire func(sess *interactive.Session, tuiSink output.EventSink) output.EventSink
		want int
	}{
		{
			name: "fixed wiring does not duplicate",
			wire: func(sess *interactive.Session, _ output.EventSink) output.EventSink {
				return sess.EventSink()
			},
			want: 1,
		},
		{
			name: "buggy wiring duplicates TUI events",
			wire: func(sess *interactive.Session, tuiSink output.EventSink) output.EventSink {
				return output.NewMultiSink(sess.EventSink(), tuiSink)
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var count int
			tuiSink := output.SinkFunc(func(output.Event) {
				count++
			})

			sess, err := interactive.NewSession(interactive.Dependencies{
				BaseEvents: output.NoopSink{},
			})
			if err != nil {
				t.Fatalf("NewSession failed: %v", err)
			}
			sess.DisplaySink().Set(tuiSink)

			events := tt.wire(sess, tuiSink)
			events.Emit(output.NewRunStartedEvent("interactive", "test-model", "", 0, 0))

			if count != tt.want {
				t.Fatalf("TUI sink received %d events, want %d", count, tt.want)
			}
		})
	}
}
