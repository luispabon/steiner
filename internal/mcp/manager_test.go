package mcp

import (
	"bytes"
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

		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, func(string) {}, io.Discard)
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

		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, func(string) {}, io.Discard)
		if got := m.ToolDefs(); len(got) != 0 {
			t.Fatalf("ToolDefs() has %d tools, want 0", len(got))
		}
		if _, err := os.Stat(touch); !os.IsNotExist(err) {
			t.Fatalf("touch file exists (%v), want none: disabled server was started", err)
		}
	})

	t.Run("failed server is reported and does not abort others", func(t *testing.T) {
		var warns, infos []string
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"bad":  {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
				"good": server(nil),
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(),
			func(msg string) { warns = append(warns, msg) },
			func(msg string) { infos = append(infos, msg) },
			io.Discard)
		defer m.Close() //nolint:errcheck

		defs := m.ToolDefs()
		if len(defs) != 2 {
			t.Fatalf("ToolDefs() has %d tools, want 2 (echo, boom)", len(defs))
		}
		// onWarn carries failures only; a successful connect goes to onInfo so
		// a healthy startup emits no warnings.
		if len(warns) != 1 {
			t.Fatalf("got %d warnings, want 1 (the failure only): %v", len(warns), warns)
		}
		if !strings.Contains(warns[0], "bad") || !strings.Contains(warns[0], "failed to connect") {
			t.Errorf("warning %q does not report server %q failure", warns[0], "bad")
		}
		if len(infos) != 1 {
			t.Fatalf("got %d info messages, want 1 (the successful connect): %v", len(infos), infos)
		}
		if !strings.Contains(infos[0], `"good"`) || !strings.Contains(infos[0], "connected") {
			t.Errorf("info %q does not report server %q connect", infos[0], "good")
		}
		for _, d := range defs {
			if d.MCP.Server != "good" {
				t.Errorf("tool %q MCP.Server = %q, want %q", d.Name, d.MCP.Server, "good")
			}
		}
	})

	t.Run("nil reporting callbacks are tolerated", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"bad":  {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
				"good": server(nil),
			},
		}

		// A failing server with no reporting callbacks must not panic; the
		// healthy server's tools still arrive.
		m := Connect(context.Background(), cfg, nil, allowApprover(), nil, nil, io.Discard)
		defer m.Close() //nolint:errcheck

		if got := len(m.ToolDefs()); got != 2 {
			t.Fatalf("ToolDefs() has %d tools, want 2 (echo, boom)", got)
		}
	})

	t.Run("one stderr writer is shared safely across servers", func(t *testing.T) {
		// Every connected server gets this writer as its cmd.Stderr, and exec
		// copies each child's stderr into it from its own goroutine, so the
		// writes overlap for the whole session. Connect must serialise them:
		// under -race an unsynchronised bytes.Buffer here is a data race.
		var shared bytes.Buffer
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"alpha": server(nil),
				"beta":  server(nil),
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(), nil, nil, &shared)
		defer m.Close() //nolint:errcheck

		// The fixture logs to stderr on every notification, so exercising both
		// servers guarantees concurrent writes to the shared buffer.
		for _, name := range []string{"mcp__alpha__echo", "mcp__beta__echo"} {
			if _, err := findTool(t, m.ToolDefs(), name).Handler(context.Background(), map[string]any{"text": "hi"}); err != nil {
				t.Fatalf("%s: %v", name, err)
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

		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard)
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
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard)
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
		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		// echo would return OK with the text if it reached the server, so a
		// denial envelope proves the call never left the handler.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)
	})

	t.Run("approver denial denies without calling the server", func(t *testing.T) {
		approver := tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
			req.Response <- tool.ApprovalResponse{Allow: false}
			return nil
		})
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, approver, func(string) {}, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		// boom would return mcp_tool_error if it reached the server; the denial
		// envelope proves the call was gated before dispatch.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
		assertDenial(t, env, err)
	})

	t.Run("approver allow round-trips echo and surfaces boom as an envelope", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard)
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

	t.Run("UpdateApprover rebuilds defs with the new approver", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		// Connected with a nil approver: calls deny.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
		assertDenial(t, env, err)

		m.UpdateApprover(allowApprover())
		env, err = findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
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
	})

	t.Run("ServerStates reports disabled, failed, and connected outcomes", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"bad":  {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
				"off":  {Enabled: false, Command: fixtureBin},
				"good": server(nil),
			},
		}

		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard)
		defer m.Close() //nolint:errcheck

		states := m.ServerStates()
		if len(states) != 3 {
			t.Fatalf("ServerStates() has %d entries, want 3", len(states))
		}

		byName := make(map[string]ServerState, len(states))
		for _, s := range states {
			byName[s.Name] = s
		}

		bad, ok := byName["bad"]
		if !ok || bad.Status != ServerStatusFailed || bad.Err == "" || bad.Transport != "stdio" {
			t.Errorf("bad state = %+v, want failed with non-empty Err and stdio transport", bad)
		}

		off, ok := byName["off"]
		if !ok || off.Status != ServerStatusDisabled || off.Transport != "stdio" {
			t.Errorf("off state = %+v, want disabled with stdio transport", off)
		}

		good, ok := byName["good"]
		if !ok || good.Status != ServerStatusConnected || good.ProtocolVersion == "" || good.Transport != "stdio" {
			t.Errorf("good state = %+v, want connected with non-empty ProtocolVersion and stdio transport", good)
		}
		if !reflect.DeepEqual(good.Tools, []string{"echo", "boom"}) {
			t.Errorf("good.Tools = %v, want [echo boom]", good.Tools)
		}

		// Sorted name order.
		var names []string
		for _, s := range states {
			names = append(names, s.Name)
		}
		if !reflect.DeepEqual(names, []string{"bad", "good", "off"}) {
			t.Errorf("ServerStates() order = %v, want sorted [bad good off]", names)
		}
	})

	t.Run("ServerStates reports every declared server as disabled when MCP is off", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: false,
			Servers: map[string]config.MCPServerConfig{
				"fixture": server(nil),
			},
		}

		m := Connect(context.Background(), cfg, nil, nil, func(string) {}, func(string) {}, io.Discard)
		want := []ServerState{{Name: "fixture", Status: ServerStatusDisabled, Transport: "stdio"}}
		if got := m.ServerStates(); !reflect.DeepEqual(got, want) {
			t.Errorf("ServerStates() = %+v, want %+v", got, want)
		}
	})

	t.Run("ServerStates on a nil Manager returns nil", func(t *testing.T) {
		var m *Manager
		if got := m.ServerStates(); got != nil {
			t.Errorf("ServerStates() = %+v, want nil", got)
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
		m := Connect(context.Background(), cfg, nil, allowApprover(), func(string) {}, func(string) {}, io.Discard)
		if err := m.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		// A closed session must not serve calls on either server. Either the
		// handler returns a transport error, or it returns a non-OK envelope;
		// anything else means the session survived Close.
		for _, name := range []string{"mcp__alpha__echo", "mcp__beta__echo"} {
			env, err := findTool(t, m.ToolDefs(), name).Handler(context.Background(), map[string]any{"text": "hi"})
			if err != nil {
				continue // transport failure: the session is gone, as required
			}
			envelope, isEnv := env.(tool.JSONEnvelope)
			if !isEnv {
				t.Errorf("%s call after Close returned %T with a nil error, want a transport failure or a non-OK envelope", name, env)
				continue
			}
			if envelope.OK {
				t.Errorf("%s call after Close succeeded (result %v), want transport failure", name, envelope.Result)
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
func assertDenial(t *testing.T, env any, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned Go error %v, want nil %s envelope", err, "approval_denied")
	}
	denial, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("result type = %T, want tool.JSONEnvelope", env)
	}
	if denial.OK {
		t.Error("OK = true, want false")
	}
	if denial.Error == nil || denial.Error.Kind != "approval_denied" {
		t.Errorf("error = %+v, want kind %s", denial.Error, "approval_denied")
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
