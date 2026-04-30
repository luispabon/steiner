package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

func TestActiveRunControllerInterruptCancelsCurrentRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	controller := &activeRunController{}
	controller.Set(cancel)

	controller.Interrupt()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected interrupt to cancel the active run")
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
