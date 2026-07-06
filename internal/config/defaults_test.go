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
