package config

import (
	"strings"
	"testing"
)

func TestValidateMCPConnectTimeout(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPConfig
		wantErr string
	}{
		{
			name: "absent is valid",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx"},
				},
			},
		},
		{
			name: "explicit positive passes through",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", ConnectTimeout: MustDuration("30s")},
				},
			},
		},
		{
			name: "explicit zero is valid (defaults later)",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", ConnectTimeout: MustDuration("0s")},
				},
			},
		},
		{
			name: "negative rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", ConnectTimeout: MustDuration("-5s")},
				},
			},
			wantErr: "mcp.servers.example.connect_timeout must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateMCPConfig(&problems, tt.cfg)
			got := strings.Join(problems, "; ")
			if tt.wantErr == "" {
				if got != "" {
					t.Fatalf("validateMCPConfig() = %q, want no problems", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantErr) {
				t.Fatalf("validateMCPConfig() = %q, want containing %q", got, tt.wantErr)
			}
		})
	}
}

func TestValidateMCPSubAgents(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPConfig
		wantErr string
	}{
		{
			name: "missing sub_agents is valid",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx"},
				},
			},
		},
		{
			name: "explicit empty sub_agents is valid",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", SubAgents: []string{}},
				},
			},
		},
		{
			name: "all valid agent types accepted",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", SubAgents: []string{"explore", "research", "code", "evaluate", "sanity_check", "review", "vision"}},
				},
			},
		},
		{
			name: "unknown agent type rejected",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", SubAgents: []string{"explore", "bogus"}},
				},
			},
			wantErr: `mcp.servers.example.sub_agents: unknown agent type "bogus"`,
		},
		{
			name: "unknown allowed_tools and blocked_tools names do not error",
			cfg: MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Transport: "stdio", Command: "npx", AllowedTools: []string{"echo", "no_such_tool"}, BlockedTools: []string{"no_such_tool"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateMCPConfig(&problems, tt.cfg)
			got := strings.Join(problems, "; ")
			if tt.wantErr == "" {
				if got != "" {
					t.Fatalf("validateMCPConfig() = %q, want no problems", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantErr) {
				t.Fatalf("validateMCPConfig() = %q, want containing %q", got, tt.wantErr)
			}
		})
	}
}
