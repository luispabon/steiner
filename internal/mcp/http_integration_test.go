package mcp

import (
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestHTTPIntegration stands up a real HTTP MCP server and connects through
// the Manager, asserting end-to-end HTTP transport, headers on the wire, and
// sandbox evasion.
func TestHTTPIntegration(t *testing.T) {
	t.Run("remote http server connects, discovers tools, and invokes with headers", func(t *testing.T) {
		recordedHeaders := httpHeaderRecorder{}
		server := newTestMCPServer(t, withRequestObservation(recordedHeaders.wrap))
		defer server.Close()

		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"remote": {
					Enabled:   true,
					Transport: "http",
					URL:       server.URL,
					Headers: map[string]string{
						"Authorization": "Bearer test-token",
						"X-Custom":      "custom-value",
					},
				},
			},
		}

		var infos []string
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
			func(string) {}, func(msg string) { infos = append(infos, msg) }, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)
		m.UpdateApprover(allowApprover())

		defs := m.ToolDefs()
		if len(defs) != 1 {
			t.Fatalf("ToolDefs() has %d tools, want 1 (test_tool)", len(defs))
		}

		def := defs[0]
		if def.Name != "mcp__remote__test_tool" {
			t.Errorf("tool name = %q, want mcp__remote__test_tool", def.Name)
		}
		if def.MCP != (tool.MCPProvenance{Server: "remote", ToolName: "test_tool"}) {
			t.Errorf("MCP provenance = %+v, want {remote test_tool}", def.MCP)
		}

		states := m.ServerStates()
		if len(states) != 1 {
			t.Fatalf("ServerStates() has %d entries, want 1", len(states))
		}

		state := states[0]
		if state.Name != "remote" {
			t.Errorf("state.Name = %q, want remote", state.Name)
		}
		if state.Status != ServerStatusConnected {
			t.Errorf("state.Status = %q, want %q", state.Status, ServerStatusConnected)
		}
		if state.Transport != "http" {
			t.Errorf("state.Transport = %q, want http", state.Transport)
		}
		if state.ProtocolVersion == "" {
			t.Error("state.ProtocolVersion is empty, want non-empty")
		}
		if state.Err != "" {
			t.Errorf("state.Err = %q, want empty", state.Err)
		}

		env, err := def.Handler(context.Background(), map[string]any{"text": "hello"})
		if err != nil {
			t.Fatalf("tool call returned Go error %v, want nil", err)
		}

		text, ok := env.(string)
		if !ok {
			t.Fatalf("result type = %T, want string", env)
		}
		if text != "hello" {
			t.Errorf("result = %v, want %q", text, "hello")
		}

		snapshots := recordedHeaders.all()
		if len(snapshots) < 2 {
			t.Fatalf("recorded %d requests, want at least 2 (handshake + tool call)", len(snapshots))
		}
		for i, headers := range snapshots {
			if got := headers["Authorization"]; got != "Bearer test-token" {
				t.Errorf("request %d: Authorization header = %q, want %q", i, got, "Bearer test-token")
			}
			if got := headers["X-Custom"]; got != "custom-value" {
				t.Errorf("request %d: X-Custom header = %q, want %q", i, got, "custom-value")
			}
		}

		if len(infos) != 1 {
			t.Fatalf("got %d info messages, want 1 (successful connect): %v", len(infos), infos)
		}
	})

	t.Run("wrap func is never called on http server", func(t *testing.T) {
		server := newTestMCPServer(t)
		defer server.Close()

		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"remote": {
					Enabled:   true,
					Transport: "http",
					URL:       server.URL,
				},
			},
		}

		wrapCalled := false
		wrap := func(cmd *exec.Cmd) *exec.Cmd {
			wrapCalled = true
			t.Fatal("wrap func called for http server, want never called")
			return cmd
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, wrap, false,
			func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		_ = m.ToolDefs()
		if wrapCalled {
			t.Fatal("wrap was called, want never called")
		}
	})

	t.Run("unreachable http server yields failed state with error", func(t *testing.T) {
		server := newTestMCPServer(t)
		defer server.Close()

		// A closed loopback server gives a deterministic refused endpoint
		// without hardcoding a port another process could occupy.
		refused := newTestMCPServer(t)
		refusedURL := refused.URL + "/no-such-server"
		refused.Close()

		var warns []string
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"unreachable": {
					Enabled:   true,
					Transport: "http",
					URL:       refusedURL,
				},
				"reachable": {
					Enabled:   true,
					Transport: "http",
					URL:       server.URL,
				},
			},
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
			func(msg string) { warns = append(warns, msg) },
			func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		states := m.ServerStates()
		if len(states) != 2 {
			t.Fatalf("ServerStates() has %d entries, want 2", len(states))
		}

		byName := make(map[string]ServerState)
		for _, s := range states {
			byName[s.Name] = s
		}

		unreachable, ok := byName["unreachable"]
		if !ok {
			t.Fatal("unreachable state not found")
		}
		if unreachable.Status != ServerStatusFailed {
			t.Errorf("unreachable.Status = %q, want %q", unreachable.Status, ServerStatusFailed)
		}
		if unreachable.Err == "" {
			t.Error("unreachable.Err is empty, want non-empty")
		}
		if unreachable.Transport != "http" {
			t.Errorf("unreachable.Transport = %q, want http", unreachable.Transport)
		}

		reachable, ok := byName["reachable"]
		if !ok {
			t.Fatal("reachable state not found")
		}
		if reachable.Status != ServerStatusConnected {
			t.Errorf("reachable.Status = %q, want connected", reachable.Status)
		}

		if len(warns) != 1 || !strings.Contains(warns[0], "unreachable") {
			t.Errorf("expected exactly one warning about unreachable server, got %v", warns)
		}
	})

	t.Run("Close() succeeds for http session", func(t *testing.T) {
		server := newTestMCPServer(t)
		defer server.Close()

		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"remote": {
					Enabled:   true,
					Transport: "http",
					URL:       server.URL,
				},
			},
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
			func(string) {}, func(string) {}, io.Discard, nil)
		waitInit(t, m)

		if err := m.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

// httpHeaderRecorder records incoming request headers from every request, so
// tests can assert configured headers were sent on each request rather than
// only on whichever one happened to run last.
type httpHeaderRecorder struct {
	mu        sync.Mutex
	snapshots []map[string]string
}

// wrap returns a handler that records the request headers and then delegates
// to next, for use as a request-observation wrapper on the shared test server.
func (r *httpHeaderRecorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		headers := make(map[string]string)
		for k, vs := range req.Header {
			if len(vs) > 0 {
				headers[k] = vs[0]
			}
		}
		r.mu.Lock()
		r.snapshots = append(r.snapshots, headers)
		r.mu.Unlock()
		next.ServeHTTP(w, req)
	})
}

// all returns a copy of the recorded header snapshots, safe to read after
// the requests under test have completed.
func (r *httpHeaderRecorder) all() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]string(nil), r.snapshots...)
}
