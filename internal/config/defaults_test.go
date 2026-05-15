package config

import (
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
	for _, tool := range cfg.SubAgent.AllowedTools {
		if tool == "scratchpad" {
			return
		}
	}
	t.Errorf("scratchpad not found in SubAgent.AllowedTools: %v", cfg.SubAgent.AllowedTools)
}
