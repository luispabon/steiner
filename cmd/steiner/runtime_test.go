package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// TestCloseRuntimeTerminatesMCPServers proves closeRuntime invokes the MCP
// manager's Close: after teardown a connected server no longer serves calls.
func TestCloseRuntimeTerminatesMCPServers(t *testing.T) {
	mgr := mcpFixtureManager(t)
	echo := findToolDef(t, mgr.ToolDefs(), "mcp__fixture__echo")

	rt := cliRuntime{
		mcpManager: mgr,
		events:     output.NoopSink{},
	}
	closeRuntime(&rt)

	// The fixture echo tool returns OK when the server is alive. After
	// closeRuntime a successful call proves the manager was never closed.
	env, err := echo.Handler(context.Background(), map[string]any{"text": "hi"})
	if err == nil {
		if ok, isEnv := env.(tool.JSONEnvelope); isEnv && ok.OK {
			t.Fatal("MCP echo call succeeded after closeRuntime: manager Close was not invoked")
		}
	}
}

// TestCloseRuntimeWithoutMCPIsSafe ensures teardown tolerates a nil manager.
func TestCloseRuntimeWithoutMCPIsSafe(_ *testing.T) {
	rt := cliRuntime{events: output.NoopSink{}}
	closeRuntime(&rt)
}

// findToolDef returns the definition with the given registry name.
func findToolDef(t *testing.T, defs []tool.ToolDef, name string) tool.ToolDef {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("tool %q not found in %v", name, defs)
	return tool.ToolDef{}
}
