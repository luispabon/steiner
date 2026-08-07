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
