package main

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/tool"
)

func TestMCPTUIStateEnabledMix(t *testing.T) {
	fixtureBin := buildMCPFixture(t)

	cfg := config.Config{
		MCP: config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"bad":      {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
				"good":     {Enabled: true, Command: fixtureBin},
				"switched": {Enabled: false},
			},
		},
	}

	mgr := mcp.Connect(context.Background(), cfg.MCP, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard, false)
	defer mgr.Close() //nolint:errcheck

	registry := tool.NewRegistry(mgr.ToolDefs()...)

	enabled, servers, origins := mcpTUIState(cfg, mgr, registry)
	if !enabled {
		t.Fatal("enabled = false, want true")
	}

	wantStates := mgr.ServerStates()
	if len(servers) != len(wantStates) {
		t.Fatalf("got %d servers, want %d", len(servers), len(wantStates))
	}
	for i, s := range wantStates {
		got := servers[i]
		if got.Name != s.Name || got.State != string(s.Status) || got.Transport != s.Transport || got.Error != s.Err {
			t.Errorf("server[%d] = %+v, want name=%s state=%s transport=%s err=%s", i, got, s.Name, s.Status, s.Transport, s.Err)
		}
		if !reflect.DeepEqual(got.Tools, s.Tools) {
			t.Errorf("server[%d].Tools = %v, want %v", i, got.Tools, s.Tools)
		}
	}

	for _, def := range registry.Definitions() {
		if def.MCP.Server == "" {
			continue
		}
		origin, ok := origins[def.Name]
		if !ok {
			t.Errorf("origins missing entry for %q", def.Name)
			continue
		}
		if origin.Server != def.MCP.Server || origin.Tool != def.MCP.ToolName {
			t.Errorf("origins[%q] = %+v, want server=%s tool=%s", def.Name, origin, def.MCP.Server, def.MCP.ToolName)
		}
	}
}

func TestMCPTUIStateDisabled(t *testing.T) {
	cfg := config.Config{
		MCP: config.MCPConfig{
			Enabled: false,
			Servers: map[string]config.MCPServerConfig{
				"one": {Enabled: true, Transport: "stdio"},
				"two": {Enabled: true},
			},
		},
	}

	registry := tool.NewRegistry()

	enabled, servers, origins := mcpTUIState(cfg, nil, registry)
	if enabled {
		t.Fatal("enabled = true, want false")
	}
	if len(servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(servers))
	}
	for _, s := range servers {
		if s.State != string(mcp.ServerStatusDisabled) {
			t.Errorf("server %q state = %q, want %q", s.Name, s.State, mcp.ServerStatusDisabled)
		}
	}
	if len(origins) != 0 {
		t.Errorf("origins = %v, want empty", origins)
	}
}

func TestMCPTUIStateOriginsWithUnderscoreServerName(t *testing.T) {
	cfg := config.Config{MCP: config.MCPConfig{Enabled: false}}

	registry := tool.NewRegistry(tool.ToolDef{
		Name: "mcp__my_tool_srv__do_thing",
		MCP:  tool.MCPProvenance{Server: "my_tool_srv", ToolName: "do_thing"},
	})

	_, _, origins := mcpTUIState(cfg, nil, registry)

	origin, ok := origins["mcp__my_tool_srv__do_thing"]
	if !ok {
		t.Fatal("origins missing entry for underscore-named server tool")
	}
	if origin.Server != "my_tool_srv" {
		t.Errorf("origin.Server = %q, want %q", origin.Server, "my_tool_srv")
	}
	if origin.Tool != "do_thing" {
		t.Errorf("origin.Tool = %q, want %q", origin.Tool, "do_thing")
	}
}
