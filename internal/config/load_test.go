package config

import (
	"reflect"
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

func TestMCPFilterAndSubAgentsYAMLParsing(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      allowed_tools:
        - echo
        - read_file
      blocked_tools:
        - dangerous_tool
      sub_agents:
        - explore
        - code
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	if patch.MCP == nil || patch.MCP.Servers == nil {
		t.Fatal("patch.MCP.Servers = nil, want parsed mcp patch")
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.AllowedTools == nil {
		t.Fatal("srv.AllowedTools = nil, want parsed allowed_tools")
	}
	if srv.BlockedTools == nil {
		t.Fatal("srv.BlockedTools = nil, want parsed blocked_tools")
	}
	if srv.SubAgents == nil {
		t.Fatal("srv.SubAgents = nil, want parsed sub_agents")
	}
	if got, want := *srv.AllowedTools, []string{"echo", "read_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed_tools = %#v, want %#v", got, want)
	}
	if got, want := *srv.BlockedTools, []string{"dangerous_tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked_tools = %#v, want %#v", got, want)
	}
	if got, want := *srv.SubAgents, []string{"explore", "code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sub_agents = %#v, want %#v", got, want)
	}

	var dst MCPServerConfig
	applyMCPServerPatch(&dst, &srv)
	if got, want := dst.AllowedTools, []string{"echo", "read_file"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied allowed_tools = %#v, want %#v", got, want)
	}
	if got, want := dst.BlockedTools, []string{"dangerous_tool"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied blocked_tools = %#v, want %#v", got, want)
	}
	if got, want := dst.SubAgents, []string{"explore", "code"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied sub_agents = %#v, want %#v", got, want)
	}
}

func TestMCPFilterAndSubAgentsYAMLParsingEmpty(t *testing.T) {
	patch, err := parseConfigPatch(`mcp:
  servers:
    example:
      transport: stdio
      command: npx
      allowed_tools: []
      blocked_tools: []
      sub_agents: []
`)
	if err != nil {
		t.Fatalf("parseConfigPatch() error = %v", err)
	}
	srv := (*patch.MCP.Servers)["example"]
	if srv.AllowedTools == nil || len(*srv.AllowedTools) != 0 {
		t.Fatalf("srv.AllowedTools = %#v, want explicit empty slice", srv.AllowedTools)
	}
	if srv.BlockedTools == nil || len(*srv.BlockedTools) != 0 {
		t.Fatalf("srv.BlockedTools = %#v, want explicit empty slice", srv.BlockedTools)
	}
	if srv.SubAgents == nil || len(*srv.SubAgents) != 0 {
		t.Fatalf("srv.SubAgents = %#v, want explicit empty slice", srv.SubAgents)
	}
}
