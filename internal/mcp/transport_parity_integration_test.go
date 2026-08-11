package mcp

import (
	"context"
	"io"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestMCPTransportParity drives the common user-visible MCP contract through
// the Manager and tool-call wiring for both transports: stdio (the hand-written
// SDK-free fixtureserver binary) and HTTP (the shared loopback SDK server).
//
// Each case runs the full manager path (Connect -> WaitInit -> UpdateApprover ->
// ToolDefs -> Handler) and asserts the same five outcomes for both transports:
// connect+init, discovery, a successful tool call, an MCP isError result
// surfacing as a non-OK envelope, and an initial connection failure. Transport
// internals (headers, sessions, reaping, reconnect) are intentionally out of
// scope and covered by the per-transport tests.
func TestMCPTransportParity(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	// One shared loopback HTTP peer: the SDK server registers test_tool (echo)
	// plus an error tool via withErrorTool. Subtests connect sequentially.
	httpServer := newTestMCPServer(t, withErrorTool("error_tool"))
	defer httpServer.Close()

	cases := []struct {
		name       string
		serverName string
		transport  string
		echoTool   string
		errorTool  string
		connect    config.MCPServerConfig // healthy peer for the connected outcomes
		fail       config.MCPServerConfig // unreachable peer for the failure outcome
	}{
		{
			name:       "stdio",
			serverName: "fixture",
			transport:  "stdio",
			echoTool:   "mcp__fixture__echo",
			errorTool:  "mcp__fixture__boom",
			connect:    config.MCPServerConfig{Enabled: true, Command: fixtureBin},
			fail:       config.MCPServerConfig{Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
		},
		{
			name:       "http",
			serverName: "remote",
			transport:  "http",
			echoTool:   "mcp__remote__test_tool",
			errorTool:  "mcp__remote__error_tool",
			connect:    config.MCPServerConfig{Enabled: true, Transport: "http", URL: httpServer.URL},
			fail:       config.MCPServerConfig{Enabled: true, Transport: "http", URL: "http://127.0.0.1:1/no-such-server"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name+"/connect_init", func(t *testing.T) {
			m := connectParity(t, tt.serverName, tt.connect)
			defer m.Close() //nolint:errcheck

			state := stateByName(m.ServerStates(), tt.serverName)
			if state == nil {
				t.Fatalf("state for server %q not found", tt.serverName)
			}
			if state.Status != ServerStatusConnected {
				t.Errorf("state.Status = %q, want %q", state.Status, ServerStatusConnected)
			}
			if state.Transport != tt.transport {
				t.Errorf("state.Transport = %q, want %q", state.Transport, tt.transport)
			}
			if state.ProtocolVersion == "" {
				t.Error("state.ProtocolVersion is empty, want non-empty")
			}
			if state.Err != "" {
				t.Errorf("state.Err = %q, want empty", state.Err)
			}
		})

		t.Run(tt.name+"/discovery", func(t *testing.T) {
			m := connectParity(t, tt.serverName, tt.connect)
			defer m.Close() //nolint:errcheck

			// findTool fails the test if either the echo or the error tool is
			// missing, so the discovery assertion is the lookup itself.
			for _, want := range []string{tt.echoTool, tt.errorTool} {
				findTool(t, m.ToolDefs(), want)
			}
		})

		t.Run(tt.name+"/successful_call", func(t *testing.T) {
			m := connectParity(t, tt.serverName, tt.connect)
			defer m.Close() //nolint:errcheck

			env, err := findTool(t, m.ToolDefs(), tt.echoTool).Handler(context.Background(), map[string]any{"text": "hi"})
			if err != nil {
				t.Fatalf("handler returned Go error %v, want nil", err)
			}
			envelope, ok := env.(tool.JSONEnvelope)
			if !ok {
				t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
			}
			if !envelope.OK {
				t.Errorf("OK = false, want true (error: %+v)", envelope.Error)
			}
			if envelope.Result != "hi" {
				t.Errorf("result = %v, want %q", envelope.Result, "hi")
			}
		})

		t.Run(tt.name+"/isError_result", func(t *testing.T) {
			m := connectParity(t, tt.serverName, tt.connect)
			defer m.Close() //nolint:errcheck

			env, err := findTool(t, m.ToolDefs(), tt.errorTool).Handler(context.Background(), map[string]any{})
			if err != nil {
				t.Fatalf("handler returned Go error %v, want nil %s envelope", err, "mcp_tool_error")
			}
			envelope, ok := env.(tool.JSONEnvelope)
			if !ok {
				t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
			}
			if envelope.OK {
				t.Error("OK = true, want false")
			}
			if envelope.Error == nil || envelope.Error.Kind != "mcp_tool_error" {
				t.Errorf("error = %+v, want kind %s", envelope.Error, "mcp_tool_error")
			}
		})

		t.Run(tt.name+"/initial_connection_failure", func(t *testing.T) {
			cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{tt.serverName: tt.fail}}
			m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
				func(string) {}, func(string) {}, io.Discard, nil)
			defer m.Close() //nolint:errcheck
			waitInit(t, m)

			state := stateByName(m.ServerStates(), tt.serverName)
			if state == nil {
				t.Fatalf("state for server %q not found", tt.serverName)
			}
			if state.Status != ServerStatusFailed {
				t.Errorf("state.Status = %q, want %q", state.Status, ServerStatusFailed)
			}
			if state.Err == "" {
				t.Error("state.Err is empty, want non-empty")
			}
			if state.Transport != tt.transport {
				t.Errorf("state.Transport = %q, want %q", state.Transport, tt.transport)
			}
		})
	}
}

// connectParity connects the manager to a single healthy server through the
// same seam the other integration tests use (Connect -> WaitInit ->
// UpdateApprover) and returns the manager with every approval allowed.
func connectParity(t *testing.T, name string, srv config.MCPServerConfig) *Manager {
	t.Helper()
	cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{name: srv}}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
		func(string) {}, func(string) {}, io.Discard, nil)
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	return m
}
