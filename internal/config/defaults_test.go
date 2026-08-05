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

func TestDefaultModesExecutionMode(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Modes.Default != ExecutionModeBuild {
		t.Errorf("Modes.Default = %q, want %q", cfg.Modes.Default, ExecutionModeBuild)
	}
}

func TestApplyMCPDefaultsTransport(t *testing.T) {
	cfg := MCPConfig{
		Servers: map[string]MCPServerConfig{
			"empty":    {Command: "npx"},
			"explicit": {Transport: "stdio", Command: "npx"},
		},
	}
	applyMCPDefaults(&cfg)
	if got := cfg.Servers["empty"].Transport; got != "stdio" {
		t.Errorf("empty transport = %q, want %q", got, "stdio")
	}
	if got := cfg.Servers["explicit"].Transport; got != "stdio" {
		t.Errorf("explicit transport = %q, want %q", got, "stdio")
	}
}
