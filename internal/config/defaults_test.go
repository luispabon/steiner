package config

import (
	"testing"
	"time"
)

func TestDefaultSubAgentMaxTurns(t *testing.T) {
	cfg := defaultConfig()
	if cfg.SubAgent.MaxTurns != 30 {
		t.Errorf("SubAgent.MaxTurns = %d, want 30", cfg.SubAgent.MaxTurns)
	}
	if cfg.SubAgent.MaxParallel != 3 {
		t.Errorf("SubAgent.MaxParallel = %d, want 3", cfg.SubAgent.MaxParallel)
	}
}

func TestDefaultModesExecutionMode(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Modes.Default != ExecutionModeBuild {
		t.Errorf("Modes.Default = %q, want %q", cfg.Modes.Default, ExecutionModeBuild)
	}
}

func TestApplyMCPDefaultsApproval(t *testing.T) {
	cfg := MCPConfig{
		Servers: map[string]MCPServerConfig{
			"empty":    {Command: "npx"},
			"explicit": {Approval: "deny", Command: "npx"},
		},
	}
	applyMCPDefaults(&cfg)
	if got := cfg.Servers["empty"].Approval; got != "ask" {
		t.Errorf("empty approval = %q, want %q", got, "ask")
	}
	if got := cfg.Servers["explicit"].Approval; got != "deny" {
		t.Errorf("explicit approval = %q, want %q", got, "deny")
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

func TestDefaultConfigMCPEnabled(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.MCP.Enabled {
		t.Errorf("DefaultConfig().MCP.Enabled = false, want true")
	}
}

func TestDefaultLimitsModelCallTimeout(t *testing.T) {
	cfg := defaultConfig()
	wantNanos := (10 * time.Minute).Nanoseconds()
	if cfg.Limits.ModelCallTimeout.Duration() != wantNanos {
		t.Errorf("Limits.ModelCallTimeout = %v nanoseconds, want %v", cfg.Limits.ModelCallTimeout.Duration(), wantNanos)
	}
}
