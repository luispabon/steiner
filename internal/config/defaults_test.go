package config

import (
	"slices"
	"testing"
)

func TestDefaultSubAgentMaxTurns(t *testing.T) {
	cfg := defaultConfig()
	if cfg.SubAgent.MaxTurns != 30 {
		t.Errorf("SubAgent.MaxTurns = %d, want 30", cfg.SubAgent.MaxTurns)
	}
}

func TestDefaultSubAgentAllowedTools(t *testing.T) {
	cfg := defaultConfig()
	want := []string{"read", "glob", "grep", "ls", "bash"}
	if !slices.Equal(cfg.SubAgent.AllowedTools, want) {
		t.Fatalf("SubAgent.AllowedTools = %v, want %v", cfg.SubAgent.AllowedTools, want)
	}
}
