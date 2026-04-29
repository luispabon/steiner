package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/config"
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
