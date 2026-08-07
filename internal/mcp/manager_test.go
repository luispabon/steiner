package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
			func(msg string) { warns = append(warns, msg) },
			func(msg string) { infos = append(infos, msg) },
			io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		defs := m.ToolDefs()
		if len(defs) != 6 {
			t.Fatalf("ToolDefs() has %d tools, want 6 (echo, boom, readonly_echo, die, sleep, big_output)", len(defs))
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
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, nil, nil, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		if got := len(m.ToolDefs()); got != 6 {
			t.Fatalf("ToolDefs() has %d tools, want 6 (echo, boom, readonly_echo, die, sleep, big_output)", got)
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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, nil, nil, &shared, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)
		m.UpdateApprover(allowApprover())

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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

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
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		defs := m.ToolDefs()
		if len(defs) != 6 {
			t.Fatalf("ToolDefs() has %d tools, want 6", len(defs))
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

	t.Run("deny server connects but registers no tools", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"denied": {Enabled: true, Command: fixtureBin, Approval: "deny"},
				"ask":    server(nil),
			},
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		defs := m.ToolDefs()
		if len(defs) != 6 {
			t.Fatalf("ToolDefs() has %d tools, want 6 (only the ask server's echo, boom, readonly_echo, die, sleep, big_output)", len(defs))
		}
		for _, d := range defs {
			if d.MCP.Server == "denied" {
				t.Errorf("tool %q from deny server registered, want none", d.Name)
			}
		}

		// The deny server still connects so its state is visible, but shows no
		// registered tools.
		states := m.ServerStates()
		var denied *ServerState
		for i := range states {
			if states[i].Name == "denied" {
				denied = &states[i]
			}
		}
		if denied == nil || denied.Status != ServerStatusConnected {
			t.Fatalf("denied state = %+v, want connected", denied)
		}
		if len(denied.Tools) != 0 {
			t.Errorf("denied.Tools = %v, want none registered", denied.Tools)
		}
		// Every advertised tool is retained with a denied outcome, in
		// advertised order, so the TUI can show why nothing registered.
		if !reflect.DeepEqual(denied.AdvertisedTools, wantAllOutcome(ToolDenied)) {
			t.Errorf("denied.AdvertisedTools = %+v, want all tools denied", denied.AdvertisedTools)
		}
	})

	t.Run("nil approver denies without calling the server", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

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
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)
		m.UpdateApprover(approver)

		// boom would return mcp_tool_error if it reached the server; the denial
		// envelope proves the call was gated before dispatch.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
		assertDenial(t, env, err)
	})

	t.Run("approver allow round-trips echo and surfaces boom as an envelope", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)
		m.UpdateApprover(allowApprover())

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

	t.Run("trust_annotations auto-allow readonly_echo and deny boom without an approver", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"fixture": {Enabled: true, Command: fixtureBin, Approval: "ask", TrustAnnotations: true},
			},
		}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		// readonly_echo advertises readOnlyHint: true, so trust_annotations
		// auto-allows the call even with a nil approver.
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__readonly_echo").Handler(context.Background(), map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("readonly_echo returned Go error %v, want nil", err)
		}
		echo, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("readonly_echo result type = %T, want tool.JSONEnvelope", env)
		}
		if !echo.OK {
			t.Errorf("readonly_echo OK = false, want true (error: %+v)", echo.Error)
		}
		if got := echo.Result; got != "hi" {
			t.Errorf("readonly_echo result = %v, want %q", got, "hi")
		}

		// boom has no readOnlyHint: it falls through to approval, and a nil
		// approver fails closed.
		env, err = findTool(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
		assertDenial(t, env, err)
	})

	t.Run("UpdateApprover wires the approver into existing defs", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

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

	t.Run("PlanMode reflects Connect and UpdatePlanMode", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, true, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		if got := m.PlanMode(); !got {
			t.Fatal("PlanMode() = false, want true after Connect with planMode=true")
		}

		m.UpdatePlanMode(false)
		if got := m.PlanMode(); got {
			t.Fatal("PlanMode() = true, want false after UpdatePlanMode(false)")
		}

		// UpdateApprover rebuilds defs with the stored approver; UpdatePlanMode
		// only flips the mode the handler closures read live, so the defs from
		// UpdateApprover must keep calls working.
		m.UpdateApprover(allowApprover())
		m.UpdatePlanMode(true)
		if got := m.PlanMode(); !got {
			t.Fatal("PlanMode() = false, want true after UpdatePlanMode(true)")
		}
		env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(context.Background(), map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("echo returned Go error %v, want nil", err)
		}
		echo, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("echo result type = %T, want tool.JSONEnvelope", env)
		}
		if !echo.OK || echo.Result != "hi" {
			t.Errorf("echo = %+v, want OK with result %q", echo, "hi")
		}
	})

	t.Run("PlanMode and UpdatePlanMode are nil-safe", func(t *testing.T) {
		var m *Manager
		if got := m.PlanMode(); got {
			t.Fatal("PlanMode() = true on nil Manager, want false")
		}
		m.UpdatePlanMode(true)
		m.UpdatePlanMode(false)
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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

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
		if !reflect.DeepEqual(good.Tools, []string{"echo", "boom", "readonly_echo", "die", "sleep", "big_output"}) {
			t.Errorf("good.Tools = %v, want [echo boom readonly_echo die sleep big_output]", good.Tools)
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

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
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

	t.Run("parallel connect: fast server resolves while a stalling server is still connecting", func(t *testing.T) {
		changed := make(chan struct{}, 1)
		onStateChange := func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		}

		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"stall": {
					Enabled:        true,
					Command:        fixtureBin,
					Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
					ConnectTimeout: config.MustDuration("500ms"),
				},
				"fast": server(nil),
			},
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, onStateChange)
		defer m.Close() //nolint:errcheck

		// The fast server must reach connected while the stall server is still
		// stuck in its handshake: direct evidence the stall did not block it, so
		// WaitInit latency is bounded by the slowest server, not the sum.
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			states := m.ServerStates()
			fast := stateByName(states, "fast")
			stall := stateByName(states, "stall")
			if fast == nil || stall == nil {
				t.Fatalf("states missing a server: %+v", states)
			}
			if fast.Status == ServerStatusConnected && stall.Status == ServerStatusConnecting {
				break
			}
			if stall.Status != ServerStatusConnecting {
				t.Fatalf("stall resolved to %q before fast connected (%q); connects are not parallel", stall.Status, fast.Status)
			}
			select {
			case <-changed:
			case <-timer.C:
				t.Fatalf("fast server never connected while stall was connecting; states=%+v", states)
			}
		}

		// WaitInit unblocks only after the stalling server hits its
		// connect_timeout and its transport is torn down, then both servers
		// have resolved. The SDK's teardown of a hung process adds up to 5s on
		// top of the 500ms timeout, so allow 10s.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.WaitInit(ctx); err != nil {
			t.Fatalf("WaitInit: %v", err)
		}
		states := m.ServerStates()
		if got := stateByName(states, "stall"); got.Status != ServerStatusFailed || got.Err == "" {
			t.Errorf("stall state = %+v, want failed with an error", got)
		}
		if got := stateByName(states, "fast"); got.Status != ServerStatusConnected {
			t.Errorf("fast state = %+v, want connected", got)
		}
	})

	t.Run("WaitInit returns when the context is cancelled", func(t *testing.T) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"stall": {
					Enabled:        true,
					Command:        fixtureBin,
					Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
					ConnectTimeout: config.MustDuration("5s"),
				},
			},
		}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := m.WaitInit(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitInit(cancelled ctx) = %v, want context.Canceled", err)
		}
	})

	t.Run("WaitInit is idempotent and nil-safe", func(t *testing.T) {
		var nilM *Manager
		if err := nilM.WaitInit(context.Background()); err != nil {
			t.Fatalf("WaitInit on nil Manager = %v, want nil", err)
		}

		m := Connect(context.Background(), config.MCPConfig{Enabled: false}, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := m.WaitInit(ctx); err != nil {
			t.Fatalf("WaitInit on MCP-off Manager = %v, want nil", err)
		}
		if err := m.WaitInit(ctx); err != nil {
			t.Fatalf("second WaitInit = %v, want nil", err)
		}
	})

	t.Run("ServerStates returns a deep copy", func(t *testing.T) {
		cfg := config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": server(nil)}}
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		states := m.ServerStates()
		if len(states) != 1 || len(states[0].Tools) != 6 {
			t.Fatalf("ServerStates() = %+v, want one connected server with 6 tools", states)
		}
		// Mutating the returned copy must not corrupt the manager's live state.
		states[0].Tools[0] = "mutated"
		states[0].Status = ServerStatusFailed
		if got := m.ServerStates()[0].Tools[0]; got == "mutated" {
			t.Error("ServerStates() shares its Tools slice with live state, want a deep copy")
		}
		states[0].AdvertisedTools[0].Outcome = ToolDenied
		if got := m.ServerStates()[0].AdvertisedTools[0].Outcome; got == ToolDenied {
			t.Error("ServerStates() shares its AdvertisedTools slice with live state, want a deep copy")
		}
		if got := m.ServerStates()[0].Status; got == ServerStatusFailed {
			t.Error("ServerStates() shares ServerState values with live state, want a copy")
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
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
		waitInit(t, m)
		m.UpdateApprover(allowApprover())
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

// TestFilter verifies allow/block filtering on connect: which tools register,
// the retained per-tool outcomes, and unknown-reference warnings.
func TestFilter(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	tests := []struct {
		name         string
		allowed      []string
		blocked      []string
		approval     string
		wantDefs     []string
		wantTools    []string
		wantOutcomes []AdvertisedTool
		wantWarnings []string
	}{
		{
			name:         "allow only restricts to listed names",
			allowed:      []string{"echo", "die"},
			wantDefs:     []string{"mcp__fixture__echo", "mcp__fixture__die"},
			wantTools:    []string{"echo", "die"},
			wantOutcomes: wantRegistered("echo", "die"),
		},
		{
			name:         "explicit empty allowlist registers nothing",
			allowed:      []string{},
			wantDefs:     nil,
			wantTools:    nil,
			wantOutcomes: wantRegistered(),
		},
		{
			name:         "block only removes listed names",
			blocked:      []string{"boom", "big_output"},
			wantDefs:     []string{"mcp__fixture__echo", "mcp__fixture__readonly_echo", "mcp__fixture__die", "mcp__fixture__sleep"},
			wantTools:    []string{"echo", "readonly_echo", "die", "sleep"},
			wantOutcomes: wantRegistered("echo", "readonly_echo", "die", "sleep"),
		},
		{
			name:         "same name in allow and block: block wins",
			allowed:      []string{"echo", "boom", "sleep"},
			blocked:      []string{"boom"},
			wantDefs:     []string{"mcp__fixture__echo", "mcp__fixture__sleep"},
			wantTools:    []string{"echo", "sleep"},
			wantOutcomes: wantRegistered("echo", "sleep"),
		},
		{
			name:         "allow then block: block removes allowed names",
			allowed:      []string{"echo", "boom", "readonly_echo"},
			blocked:      []string{"echo"},
			wantDefs:     []string{"mcp__fixture__boom", "mcp__fixture__readonly_echo"},
			wantTools:    []string{"boom", "readonly_echo"},
			wantOutcomes: wantRegistered("boom", "readonly_echo"),
		},
		{
			name:         "unknown references warn but connect succeeds",
			allowed:      []string{"echo", "no_such"},
			blocked:      []string{"zzz"},
			wantDefs:     []string{"mcp__fixture__echo"},
			wantTools:    []string{"echo"},
			wantOutcomes: wantRegistered("echo"),
			wantWarnings: []string{
				`allowed_tools: tool "no_such" not advertised by server "fixture"`,
				`blocked_tools: tool "zzz" not advertised by server "fixture"`,
			},
		},
		{
			name:         "deny is final and skips filter warnings",
			allowed:      []string{"echo"},
			blocked:      []string{"zzz"},
			approval:     "deny",
			wantDefs:     nil,
			wantTools:    nil,
			wantOutcomes: wantAllOutcome(ToolDenied),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warns []string
			cfg := config.MCPConfig{
				Enabled: true,
				Servers: map[string]config.MCPServerConfig{
					"fixture": {Enabled: true, Command: fixtureBin, Approval: tt.approval, AllowedTools: tt.allowed, BlockedTools: tt.blocked},
				},
			}

			m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(msg string) { warns = append(warns, msg) }, func(string) {}, io.Discard, nil)
			defer m.Close() //nolint:errcheck
			waitInit(t, m)

			var defNames []string
			for _, d := range m.ToolDefs() {
				defNames = append(defNames, d.Name)
			}
			if !reflect.DeepEqual(defNames, tt.wantDefs) {
				t.Errorf("ToolDefs names = %v, want %v", defNames, tt.wantDefs)
			}

			st := stateByName(m.ServerStates(), "fixture")
			if st == nil {
				t.Fatal("fixture state not found")
			}
			if !reflect.DeepEqual(st.Tools, tt.wantTools) {
				t.Errorf("state.Tools = %v, want %v", st.Tools, tt.wantTools)
			}
			if !reflect.DeepEqual(st.AdvertisedTools, tt.wantOutcomes) {
				t.Errorf("state.AdvertisedTools = %+v, want %+v", st.AdvertisedTools, tt.wantOutcomes)
			}
			if !reflect.DeepEqual(warns, tt.wantWarnings) {
				t.Errorf("warnings = %v, want %v", warns, tt.wantWarnings)
			}
		})
	}
}

// TestFilterDeterministic proves one config + one discovery yields identical
// defs, outcomes, and warnings across connects (D11): configured references are
// warned once each in sorted order regardless of list order or duplication.
func TestFilterDeterministic(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	connect := func() ([]string, []AdvertisedTool, []string) {
		cfg := config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"fixture": {
					Enabled:      true,
					Command:      fixtureBin,
					AllowedTools: []string{"echo", "nope", "echo", "zzz"},
					BlockedTools: []string{"nope"},
				},
			},
		}

		var warns []string
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(msg string) { warns = append(warns, msg) }, func(string) {}, io.Discard, nil)
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		var defs []string
		for _, d := range m.ToolDefs() {
			defs = append(defs, d.Name)
		}
		st := stateByName(m.ServerStates(), "fixture")
		if st == nil {
			t.Fatal("fixture state not found")
		}
		return defs, st.AdvertisedTools, warns
	}

	defs1, outcomes1, warns1 := connect()
	defs2, outcomes2, warns2 := connect()

	if !reflect.DeepEqual(defs1, defs2) {
		t.Errorf("defs differ across connects: %v vs %v", defs1, defs2)
	}
	if !reflect.DeepEqual(outcomes1, outcomes2) {
		t.Errorf("outcomes differ across connects: %+v vs %+v", outcomes1, outcomes2)
	}
	if !reflect.DeepEqual(warns1, warns2) {
		t.Errorf("warnings differ across connects: %v vs %v", warns1, warns2)
	}

	wantDefs := []string{"mcp__fixture__echo"}
	if !reflect.DeepEqual(defs1, wantDefs) {
		t.Errorf("defs = %v, want %v", defs1, wantDefs)
	}
	if !reflect.DeepEqual(outcomes1, wantRegistered("echo")) {
		t.Errorf("outcomes = %+v, want %+v", outcomes1, wantRegistered("echo"))
	}
	wantWarns := []string{
		`allowed_tools: tool "nope" not advertised by server "fixture"`,
		`allowed_tools: tool "zzz" not advertised by server "fixture"`,
		`blocked_tools: tool "nope" not advertised by server "fixture"`,
	}
	if !reflect.DeepEqual(warns1, wantWarns) {
		t.Errorf("warnings = %v, want %v", warns1, wantWarns)
	}
}

// wantRegistered marks every fixture tool as ToolFiltered except the named
// registered ones, in the fixture's advertised order.
func wantRegistered(registered ...string) []AdvertisedTool {
	isRegistered := make(map[string]bool, len(registered))
	for _, r := range registered {
		isRegistered[r] = true
	}
	var out []AdvertisedTool
	for _, name := range []string{"echo", "boom", "readonly_echo", "die", "sleep", "big_output"} {
		outcome := ToolFiltered
		if isRegistered[name] {
			outcome = ToolRegistered
		}
		out = append(out, AdvertisedTool{Name: name, Outcome: outcome})
	}
	return out
}

// wantAllOutcome marks every fixture tool with the given outcome, in the
// fixture's advertised order.
func wantAllOutcome(outcome ToolOutcome) []AdvertisedTool {
	var out []AdvertisedTool
	for _, name := range []string{"echo", "boom", "readonly_echo", "die", "sleep", "big_output"} {
		out = append(out, AdvertisedTool{Name: name, Outcome: outcome})
	}
	return out
}

// waitInit blocks until every enabled server resolves, failing the test on a
// cancelled wait. Connect is now parallel, so tests must wait before reading
// resolved state.
func waitInit(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}
}

// stateByName returns the state for the named server from a ServerStates copy.
func stateByName(states []ServerState, name string) *ServerState {
	for i := range states {
		if states[i].Name == name {
			return &states[i]
		}
	}
	return nil
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
