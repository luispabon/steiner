package mcp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestManagerConnect drives the Manager against the step-1 fixture server
// unsandboxed (wrap == nil) so the tests run on every platform.
func TestManagerConnect(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	server := func(env map[string]string) config.MCPServerConfig {
		return config.MCPServerConfig{Enabled: true, Command: fixtureBin, Env: env}
	}

	t.Run("disabled config starts nothing", func(t *testing.T) {
		touch := filepath.Join(t.TempDir(), "touch.txt")
		cfg := config.MCPConfig{
			Enabled: false,
			Servers: map[string]config.MCPServerConfig{
				"fixture": server(map[string]string{"STEINER_FIXTURE_TOUCH": touch}),
			},
		}

		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, io.Discard)
		if got := m.ToolDefs(); len(got) != 0 {
			t.Fatalf("ToolDefs() has %d tools, want 0", len(got))
		}
		if _, err := os.Stat(touch); !os.IsNotExist(err) {
			t.Fatalf("touch file exists (%v), want none: server was started", err)
		}
	})

	t.Run("disabled server is skipped", func(t *testing.T) {
		touch := filepath.Join(t.TempDir(), "touch.txt")
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"fixture": {
					Enabled: false,
					Command: fixtureBin,
					Env:     map[string]string{"STEINER_FIXTURE_TOUCH": touch},
				},
			},
		}

		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, io.Discard)
		if got := m.ToolDefs(); len(got) != 0 {
			t.Fatalf("ToolDefs() has %d tools, want 0", len(got))
		}
		if _, err := os.Stat(touch); !os.IsNotExist(err) {
			t.Fatalf("touch file exists (%v), want none: disabled server was started", err)
		}
	})

	t.Run("failed server is reported and does not abort others", func(t *testing.T) {
		var warns []string
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"bad":  {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
				"good": server(nil),
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(), func(msg string) { warns = append(warns, msg) }, io.Discard)
		defer m.Close() //nolint:errcheck

		defs := m.ToolDefs()
		if len(defs) != 2 {
			t.Fatalf("ToolDefs() has %d tools, want 2 (echo, boom)", len(defs))
		}
		if len(warns) != 2 {
			t.Fatalf("got %d warnings, want 2 (failure + connected): %v", len(warns), warns)
		}
		if !strings.Contains(warns[0], "bad") || !strings.Contains(warns[0], "failed to connect") {
			t.Errorf("first warning %q does not report server %q failure", warns[0], "bad")
		}
		if !strings.Contains(warns[1], "good") || !strings.Contains(warns[1], "connected") {
			t.Errorf("second warning %q does not report server %q connect", warns[1], "good")
		}
		for _, d := range defs {
			if d.MCP.Server != "good" {
				t.Errorf("tool %q MCP.Server = %q, want %q", d.Name, d.MCP.Server, "good")
			}
		}
	})

	t.Run("ToolDefs is idempotent and starts no new processes", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "record.txt")
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"fixture": server(map[string]string{"STEINER_FIXTURE_RECORD": record}),
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		first := m.ToolDefs()
		second := m.ToolDefs()
		if !reflect.DeepEqual(first, second) {
			t.Fatal("ToolDefs() results differ across calls")
		}

		// Each connect writes exactly one protocol_version line to the record;
		// ToolDefs() must not dial again.
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		if got := strings.Count(string(data), "protocol_version="); got != 1 {
			t.Fatalf("record has %d protocol_version lines, want 1 (a new process connected)", got)
		}
	})

	t.Run("names and provenance", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		defs := m.ToolDefs()
		if len(defs) != 2 {
			t.Fatalf("ToolDefs() has %d tools, want 2", len(defs))
		}
		for _, tt := range []struct {
			name     string
			toolName string
		}{
			{name: "mcp__fixture__echo", toolName: "echo"},
			{name: "mcp__fixture__boom", toolName: "boom"},
		} {
			def := findTool(t, defs, tt.name)
			if def.MCP != (tool.MCPProvenance{Server: "fixture", ToolName: tt.toolName}) {
				t.Errorf("%s MCP = %+v, want {fixture %s}", tt.name, def.MCP, tt.toolName)
			}
			// Handler dispatch only: no ExecPath/Subcommand/Timeout (ticket #5 owns timeouts).
			if def.ExecPath != "" || def.Subcommand != "" || def.Timeout != 0 {
				t.Errorf("%s has ExecPath=%q Subcommand=%q Timeout=%v, want all zero", tt.name, def.ExecPath, def.Subcommand, def.Timeout)
			}
		}
	})

	t.Run("nil approver denies without calling the server", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		// echo would return OK with the text if it reached the server, so a
		// denial envelope proves the call never left the handler.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err, "approval_denied")
	})

	t.Run("approver denial denies without calling the server", func(t *testing.T) {
		approver := tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
			req.Response <- tool.ApprovalResponse{Allow: false}
			return nil
		})
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, approver, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		// boom would return mcp_tool_error if it reached the server; the denial
		// envelope proves the call was gated before dispatch.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
		assertDenial(t, env, err, "approval_denied")
	})

	t.Run("approver allow round-trips echo and surfaces boom as an envelope", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("echo returned Go error %v, want nil", err)
		}
		echo, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("echo result type = %T, want tool.JSONEnvelope", env)
		}
		if !echo.OK {
			t.Errorf("echo OK = false, want true (error: %+v)", echo.Error)
		}
		if got := echo.Result; got != "hi" {
			t.Errorf("echo result = %v, want %q", got, "hi")
		}

		env, err = findTool(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
		if err != nil {
			t.Fatalf("boom returned Go error %v, want nil with an mcp_tool_error envelope", err)
		}
		boom, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("boom result type = %T, want tool.JSONEnvelope", env)
		}
		if boom.OK {
			t.Error("boom OK = true, want false")
		}
		if boom.Error == nil || boom.Error.Kind != "mcp_tool_error" {
			t.Errorf("boom error = %+v, want kind mcp_tool_error", boom.Error)
		}
		if boom.Error.Message != "deliberate failure" {
			t.Errorf("boom error message = %q, want %q", boom.Error.Message, "deliberate failure")
		}
	})

	t.Run("Close terminates every session", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"alpha": server(nil),
				"beta":  server(nil),
			},
		}
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, io.Discard)
		if err := m.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		// A closed session must not serve calls on either server.
		for _, name := range []string{"mcp__alpha__echo", "mcp__beta__echo"} {
			env, err := findTool(t, m.ToolDefs(), name).Handler(context.Background(), map[string]any{"text": "hi"})
			if err == nil {
				if ok, isEnv := env.(tool.JSONEnvelope); isEnv && ok.OK {
					t.Errorf("%s call after Close succeeded, want transport failure", name)
				}
			}
		}
	})
}

// allowApprover returns an ApprovalResponder that approves every request.
func allowApprover() tool.ApprovalResponder {
	return tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		req.Response <- tool.ApprovalResponse{Allow: true}
		return nil
	})
}

// assertDenial fails unless env is a JSONEnvelope with the given error kind and
// a nil Go error.
func assertDenial(t *testing.T, env any, err error, kind string) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned Go error %v, want nil %s envelope", err, kind)
	}
	denial, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
	}
	if denial.OK {
		t.Error("OK = true, want false")
	}
	if denial.Error == nil || denial.Error.Kind != kind {
		t.Errorf("error = %+v, want kind %s", denial.Error, kind)
	}
}

// findTool returns the definition with the given registry name.
func findTool(t *testing.T, defs []tool.ToolDef, name string) tool.ToolDef {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("tool %q not found in %v", name, defs)
	return tool.ToolDef{}
}

// buildFixture compiles the fixtureserver binary once per test run.
func buildFixture(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "fixtureserver")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fixtureserver") //nolint:noctx
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixtureserver: %v\n%s", err, out)
	}
	return bin
}
