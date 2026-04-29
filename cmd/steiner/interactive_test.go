package main

import (
	"context"
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
