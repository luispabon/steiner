package main

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestInteractiveProgramOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fps      int
		wantOpts int
	}{
		{name: "zero falls through to bubbletea default", fps: 0, wantOpts: 0},
		{name: "negative falls through", fps: -5, wantOpts: 0},
		{name: "default 60", fps: 60, wantOpts: 1},
		{name: "raised to 120", fps: 120, wantOpts: 1},
		{name: "minimum 1", fps: 1, wantOpts: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{TUI: config.TUIConfig{FPS: tc.fps}}
			if got := interactiveProgramOptions(cfg); len(got) != tc.wantOpts {
				t.Errorf("interactiveProgramOptions(fps=%d) returned %d options, want %d",
					tc.fps, len(got), tc.wantOpts)
			}
		})
	}
}

// TestInteractiveProgramOptionsAtDefaultFPS pins that the shipped default of
// 60 produces a renderer option rather than silently falling through. The
// default value itself is asserted in internal/config.
func TestInteractiveProgramOptionsAtDefaultFPS(t *testing.T) {
	t.Parallel()

	cfg := config.Config{TUI: config.TUIConfig{FPS: 60}}
	if got := interactiveProgramOptions(cfg); len(got) != 1 {
		t.Errorf("default fps produced %d renderer options, want 1", len(got))
	}
}
