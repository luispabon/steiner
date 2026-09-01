package config

import (
	"strings"
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

func TestApplyAdvisorPatchMaxUsesPerSubAgent(t *testing.T) {
	tests := []struct {
		name    string
		initial AdvisorConfig
		patch   advisorPatch
		want    AdvisorConfig
	}{
		{
			name:    "default value",
			initial: defaultConfig().Advisor,
			patch:   advisorPatch{},
			want: AdvisorConfig{
				Enabled:            false,
				MaxUsesPerRun:      3,
				MaxUsesPerSubAgent: 1,
				Timeout:            advisorTimeout(),
			},
		},
		{
			name:    "explicit value",
			initial: AdvisorConfig{Enabled: true, MaxUsesPerSubAgent: 1},
			patch:   advisorPatch{MaxUsesPerSubAgent: intPtr(5)},
			want: AdvisorConfig{
				Enabled:            true,
				MaxUsesPerSubAgent: 5,
			},
		},
		{
			name:    "invalid zero when enabled",
			initial: AdvisorConfig{Enabled: true, MaxUsesPerSubAgent: 0},
			patch:   advisorPatch{},
			want: AdvisorConfig{
				Enabled:            true,
				MaxUsesPerSubAgent: 0,
			},
		},
		{
			name:    "invalid negative when enabled",
			initial: AdvisorConfig{Enabled: true, MaxUsesPerSubAgent: -1},
			patch:   advisorPatch{},
			want: AdvisorConfig{
				Enabled:            true,
				MaxUsesPerSubAgent: -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := tt.initial
			applyAdvisorPatch(&dst, &tt.patch)
			if dst.MaxUsesPerSubAgent != tt.want.MaxUsesPerSubAgent {
				t.Fatalf("applyAdvisorPatch() MaxUsesPerSubAgent = %d, want %d", dst.MaxUsesPerSubAgent, tt.want.MaxUsesPerSubAgent)
			}
		})
	}
}

func TestValidateAdvisorConfigMaxUsesPerSubAgent(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AdvisorConfig
		wantErr bool
	}{
		{
			name: "valid when enabled and >= 1",
			cfg:  AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, MaxUsesPerSubAgent: 1},
		},
		{
			name: "valid when enabled and > 1",
			cfg:  AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, MaxUsesPerSubAgent: 5},
		},
		{
			name: "disabled with 0 is valid",
			cfg:  AdvisorConfig{Enabled: false, MaxUsesPerSubAgent: 0},
		},
		{
			name:    "invalid when enabled and 0",
			cfg:     AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, MaxUsesPerSubAgent: 0},
			wantErr: true,
		},
		{
			name:    "invalid when enabled and negative",
			cfg:     AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, MaxUsesPerSubAgent: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateAdvisorConfig(&problems, tt.cfg)
			hasErr := false
			for _, p := range problems {
				if strings.Contains(p, "max_uses_per_sub_agent") {
					hasErr = true
					break
				}
			}
			if hasErr != tt.wantErr {
				t.Fatalf("validateAdvisorConfig() found error = %v, want %v; problems = %v", hasErr, tt.wantErr, problems)
			}
		})
	}
}
