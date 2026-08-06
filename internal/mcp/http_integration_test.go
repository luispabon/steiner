package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestHTTPIntegration stands up a real HTTP MCP server and connects through
// the Manager, asserting end-to-end HTTP transport, headers on the wire, and
// sandbox evasion.
func TestHTTPIntegration(t *testing.T) {
	t.Run("remote http server connects, discovers tools, and invokes with headers", func(t *testing.T) {
		recordedHeaders := httpHeaderRecorder{}
		server := newTestMCPServer(t, &recordedHeaders)
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
		m := Connect(context.Background(), cfg, nil, allowApprover(),
			func(string) {}, func(msg string) { infos = append(infos, msg) }, io.Discard, false)
		defer m.Close() //nolint:errcheck

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

		envelope, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
		}
		if !envelope.OK {
			t.Errorf("OK = false, want true (error: %+v)", envelope.Error)
		}
		if got := envelope.Result; got != "hello" {
			t.Errorf("result = %v, want %q", got, "hello")
		}

		recordedHeaders.mu.Lock()
		hasAuth := recordedHeaders.headers["Authorization"]
		hasCustom := recordedHeaders.headers["X-Custom"]
		recordedHeaders.mu.Unlock()

		if hasAuth != "Bearer test-token" {
			t.Errorf("Authorization header not recorded or wrong value: %q", hasAuth)
		}
		if hasCustom != "custom-value" {
			t.Errorf("X-Custom header not recorded or wrong value: %q", hasCustom)
		}

		if len(infos) != 1 {
			t.Fatalf("got %d info messages, want 1 (successful connect): %v", len(infos), infos)
		}
	})

	t.Run("wrap func is never called on http server", func(t *testing.T) {
		server := newTestMCPServer(t, nil)
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

		m := Connect(context.Background(), cfg, wrap, allowApprover(),
			func(string) {}, func(string) {}, io.Discard, false)
		defer m.Close() //nolint:errcheck

		_ = m.ToolDefs()
		if wrapCalled {
			t.Fatal("wrap was called, want never called")
		}
	})

	t.Run("unreachable http server yields failed state with error", func(t *testing.T) {
		server := newTestMCPServer(t, nil)
		defer server.Close()

		var warns []string
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"unreachable": {
					Enabled:   true,
					Transport: "http",
					URL:       "http://127.0.0.1:1/no-such-server",
				},
				"reachable": {
					Enabled:   true,
					Transport: "http",
					URL:       server.URL,
				},
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(),
			func(msg string) { warns = append(warns, msg) },
			func(string) {}, io.Discard, false)
		defer m.Close() //nolint:errcheck

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
		server := newTestMCPServer(t, nil)
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

		m := Connect(context.Background(), cfg, nil, allowApprover(),
			func(string) {}, func(string) {}, io.Discard, false)

		if err := m.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

// httpHeaderRecorder records incoming request headers from the first request.
type httpHeaderRecorder struct {
	mu      sync.Mutex
	headers map[string]string
}

// newTestMCPServer creates a test HTTP server with a single "test_tool" that
// echoes its "text" argument. If recorder is non-nil, headers from the
// initialize request are recorded in it.
func newTestMCPServer(t *testing.T, recorder *httpHeaderRecorder) *httptest.Server {
	server := mcpsdk.NewServer(
		&mcpsdk.Implementation{Name: "test", Version: "1.0"},
		nil,
	)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "test_tool",
		Description: "echoes text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args struct {
		Text string `json:"text"`
	}) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: args.Text},
			},
		}, nil, nil
	})

	handler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return server
	}, nil)

	var wrappedHandler http.Handler
	if recorder != nil {
		wrappedHandler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			recorder.mu.Lock()
			recorder.headers = make(map[string]string)
			for k, vs := range req.Header {
				if len(vs) > 0 {
					recorder.headers[k] = vs[0]
				}
			}
			recorder.mu.Unlock()
			handler.ServeHTTP(w, req)
		})
	} else {
		wrappedHandler = handler
	}

	return httptest.NewServer(wrappedHandler)
}
