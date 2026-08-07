package config

import (
	"testing"
	"time"
)

func TestApplyMCPDefaultsConnectTimeout(t *testing.T) {
	if defaultMCPConnectTimeout != 15*time.Second {
		t.Fatalf("defaultMCPConnectTimeout = %v, want 15s", defaultMCPConnectTimeout)
	}

	tests := []struct {
		name    string
		timeout Duration
		want    Duration
	}{
		{name: "absent defaults to 15s", want: MustDuration("15s")},
		{name: "explicit zero defaults to 15s", timeout: MustDuration("0s"), want: MustDuration("15s")},
		{name: "explicit value passes through", timeout: MustDuration("30s"), want: MustDuration("30s")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := MCPConfig{
				Servers: map[string]MCPServerConfig{
					"example": {Command: "npx", ConnectTimeout: tt.timeout},
				},
			}
			applyMCPDefaults(&cfg)
			if got := cfg.Servers["example"].ConnectTimeout; got != tt.want {
				t.Errorf("connect_timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMCPConnectTimeoutPatch(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      connect_timeout: 30s
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.MCP == nil || patch.MCP.Servers == nil {
		t.Fatal("patch.MCP.Servers = nil, want parsed mcp patch")
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.ConnectTimeout == nil {
		t.Fatal("srv.ConnectTimeout = nil, want parsed connect_timeout")
	}
	if got, want := *srv.ConnectTimeout, MustDuration("30s"); got != want {
		t.Fatalf("connect_timeout = %v, want %v", got, want)
	}

	var dst MCPServerConfig
	applyMCPServerPatch(&dst, &srv)
	if got, want := dst.ConnectTimeout, MustDuration("30s"); got != want {
		t.Fatalf("applied connect_timeout = %v, want %v", got, want)
	}
}
