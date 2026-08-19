package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
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

	mgr := mcp.Connect(context.Background(), cfg.MCP, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer mgr.Close() //nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}

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
		if len(got.Tools) != len(s.AdvertisedTools) {
			t.Errorf("server[%d] advertised tools = %d, want %d", i, len(got.Tools), len(s.AdvertisedTools))
			continue
		}
		for j, want := range s.AdvertisedTools {
			gotTool := got.Tools[j]
			if gotTool.Name != want.Name || gotTool.Outcome != mcpOutcomeLabel(want.Outcome) {
				t.Errorf("server[%d].Tools[%d] = %+v, want name=%s outcome=%s", i, j, gotTool, want.Name, mcpOutcomeLabel(want.Outcome))
			}
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

func TestMCPStateProducerDualListeners(t *testing.T) {
	producer := &mcpStateProducer{}
	preListenerCalls := 0
	listenerCalls := 0
	producer.setPreListener(func() { preListenerCalls++ })
	producer.setListener(func() { listenerCalls++ })

	// Pre-arm: stateChanged invokes preListener only
	producer.stateChanged()
	if preListenerCalls != 1 {
		t.Fatalf("pre-arm stateChanged: preListener calls = %d, want 1", preListenerCalls)
	}
	if listenerCalls != 0 {
		t.Fatalf("pre-arm stateChanged: listener calls = %d, want 0", listenerCalls)
	}

	// Arm emits one full snapshot
	producer.arm()
	if preListenerCalls != 1 {
		t.Fatalf("after arm: preListener calls = %d, want still 1", preListenerCalls)
	}
	if listenerCalls != 1 {
		t.Fatalf("after arm: listener calls = %d, want 1", listenerCalls)
	}

	// Second arm is idempotent
	producer.arm()
	if listenerCalls != 1 {
		t.Fatalf("second arm: listener calls = %d, want still 1", listenerCalls)
	}

	// Post-arm: stateChanged invokes listener only
	producer.stateChanged()
	if preListenerCalls != 1 {
		t.Fatalf("post-arm stateChanged: preListener calls = %d, want still 1", preListenerCalls)
	}
	if listenerCalls != 2 {
		t.Fatalf("post-arm stateChanged: listener calls = %d, want 2", listenerCalls)
	}
}

func TestEmitMCPServerStatesSnapshot(t *testing.T) {
	mgr := mcpFixtureManager(t)
	// Create a registry with MCP origins to verify they are NOT included
	registry := tool.NewRegistry(tool.ToolDef{
		Name: "mcp__fixture__echo",
		MCP:  tool.MCPProvenance{Server: "fixture", ToolName: "echo"},
	})
	rt := cliRuntime{
		cfg:        config.Config{MCP: config.MCPConfig{Enabled: true}},
		mcpManager: mgr,
		registry:   registry,
	}

	var got output.Event
	emitMCPServerStatesSnapshot(rt, output.SinkFunc(func(e output.Event) { got = e }))

	if got.Type != output.EventTypeMCPStatus {
		t.Fatalf("event type = %q, want %q", got.Type, output.EventTypeMCPStatus)
	}
	snap, ok := got.Payload.(output.MCPStatusEvent)
	if !ok {
		t.Fatalf("payload type = %T, want output.MCPStatusEvent", got.Payload)
	}
	if !snap.Enabled {
		t.Fatal("snapshot enabled = false, want true")
	}

	// Servers are populated
	srv, ok := snap.Servers["fixture"]
	if !ok {
		t.Fatalf("snapshot servers = %v, want fixture entry", snap.Servers)
	}
	if srv.State != string(mcp.ServerStatusConnected) {
		t.Fatalf("fixture state = %q, want %q", srv.State, mcp.ServerStatusConnected)
	}

	// Origins are empty (key difference from emitMCPStateSnapshot)
	if len(snap.Origins) != 0 {
		t.Errorf("snapshot origins = %v, want empty", snap.Origins)
	}
}

func TestEmitMCPStateSnapshot(t *testing.T) {
	mgr := mcpFixtureManager(t)
	registry := tool.NewRegistry(mgr.ToolDefs()...)
	rt := cliRuntime{
		cfg:        config.Config{MCP: config.MCPConfig{Enabled: true}},
		mcpManager: mgr,
		registry:   registry,
	}

	var got output.Event
	emitMCPStateSnapshot(rt, output.SinkFunc(func(e output.Event) { got = e }))

	if got.Type != output.EventTypeMCPStatus {
		t.Fatalf("event type = %q, want %q", got.Type, output.EventTypeMCPStatus)
	}
	snap, ok := got.Payload.(output.MCPStatusEvent)
	if !ok {
		t.Fatalf("payload type = %T, want output.MCPStatusEvent", got.Payload)
	}
	if !snap.Enabled {
		t.Fatal("snapshot enabled = false, want true")
	}
	srv, ok := snap.Servers["fixture"]
	if !ok {
		t.Fatalf("snapshot servers = %v, want fixture entry", snap.Servers)
	}
	if srv.State != string(mcp.ServerStatusConnected) {
		t.Fatalf("fixture state = %q, want %q", srv.State, mcp.ServerStatusConnected)
	}
	if len(srv.Tools) != 6 {
		t.Fatalf("fixture advertised tools = %+v, want 6 registered tools", srv.Tools)
	}
	for i, want := range []string{"echo", "boom", "readonly_echo", "die", "sleep", "big_output"} {
		got := srv.Tools[i]
		if got.Name != want || got.Outcome != "registered" {
			t.Fatalf("fixture tool[%d] = %+v, want %s registered", i, got, want)
		}
	}
	if _, ok := snap.Origins["mcp__fixture__echo"]; !ok {
		t.Fatalf("snapshot origins = %v, want mcp__fixture__echo", snap.Origins)
	}
}

func TestConnectRuntimeMCPBlockingWaitsAndRegistersTools(t *testing.T) {
	fixtureBin := buildMCPFixture(t)
	cfg := config.Config{
		MCP: config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"stall": {
					Enabled:        true,
					Command:        fixtureBin,
					Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
					ConnectTimeout: config.MustDuration("500ms"),
				},
				"good": {Enabled: true, Command: fixtureBin},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	discard := output.SinkFunc(func(output.Event) {})
	mgr, producer := connectRuntimeMCP(ctx, cfg, nil, false, discard, io.Discard)
	defer mgr.Close() //nolint:errcheck

	// The blocking path waits for every server: the stall resolved to failed
	// and the good server connected before connectRuntimeMCP returned.
	if producer != nil {
		t.Fatal("blocking path returned a state producer, want nil")
	}
	states := mgr.ServerStates()
	if got := mcpServerStateByName(states, "stall"); got == nil || got.Status != mcp.ServerStatusFailed {
		t.Fatalf("stall state = %+v, want failed after blocking connect", got)
	}
	if got := mcpServerStateByName(states, "good"); got == nil || got.Status != mcp.ServerStatusConnected {
		t.Fatalf("good state = %+v, want connected after blocking connect", got)
	}

	// The blocking path freezes the complete tool list, so the registry the
	// `steiner tools` command renders includes the connected server's tools.
	registry := tool.NewRegistry(mgr.ToolDefs()...)
	for _, want := range []string{"mcp__good__echo", "mcp__good__boom"} {
		if _, ok := registry.Get(want); !ok {
			t.Fatalf("registry missing %q; names: %v", want, registry.Names())
		}
	}
}

func TestConnectRuntimeMCPAsyncReturnsBeforeServersResolve(t *testing.T) {
	fixtureBin := buildMCPFixture(t)
	cfg := config.Config{
		MCP: config.MCPConfig{
			Enabled: true,
			Servers: map[string]config.MCPServerConfig{
				"stall": {
					Enabled:        true,
					Command:        fixtureBin,
					Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
					ConnectTimeout: config.MustDuration("30s"),
				},
				"good": {Enabled: true, Command: fixtureBin},
			},
		},
	}

	mgr, producer := connectRuntimeMCP(context.Background(), cfg, nil, true, output.SinkFunc(func(output.Event) {}), io.Discard)
	defer mgr.Close() //nolint:errcheck

	if producer == nil {
		t.Fatal("async path returned no state producer, want one")
	}
	// The async path returns before the stall resolves: it must still be
	// connecting, and the TUI paints while the connect proceeds.
	states := mgr.ServerStates()
	if got := mcpServerStateByName(states, "stall"); got == nil || got.Status != mcp.ServerStatusConnecting {
		t.Fatalf("stall state = %+v, want still connecting", got)
	}
}

func TestSessionRunnerRunWaitsForMCPInitAndRegistersDefs(t *testing.T) {
	fixtureBin := buildMCPFixture(t)
	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"stall": {
				Enabled:        true,
				Command:        fixtureBin,
				Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
				ConnectTimeout: config.MustDuration("500ms"),
			},
			"good": {Enabled: true, Command: fixtureBin},
		},
	}, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer mgr.Close() //nolint:errcheck

	// The interactive registry is built before all servers resolve. Fast servers
	// may already have contributed defs by construction time; the important
	// invariant is that Run() waits for every server and (re-)registers all defs.
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr)

	// Let the good server connect while the stall server is still handshaking:
	// the manager holds the defs, the registry still does not.
	deadline := time.Now().Add(5 * time.Second)
	for {
		states := mgr.ServerStates()
		good := mcpServerStateByName(states, "good")
		stall := mcpServerStateByName(states, "stall")
		if good != nil && good.Status == mcp.ServerStatusConnected && stall != nil && stall.Status == mcp.ServerStatusConnecting {
			break
		}
		if stall != nil && stall.Status != mcp.ServerStatusConnecting {
			t.Fatalf("stall resolved to %q before good connected; states=%+v", stall.Status, states)
		}
		if time.Now().After(deadline) {
			t.Fatalf("good server never connected while stall was connecting; states=%+v", states)
		}
		time.Sleep(10 * time.Millisecond)
	}

	producer := &mcpStateProducer{}
	snapshots := 0
	producer.setListener(func() { snapshots++ })

	rt := cliRuntime{
		// No such model: runner.Run fails fast right after the MCP init, which
		// keeps this test off the provider and discovery paths.
		cfg:        config.Config{Models: config.ModelsConfig{Default: "nope"}},
		registry:   registry,
		mcpManager: mgr,
		mcpState:   producer,
	}
	sr := sessionRunner{runner: cliRunner{runtime: rt}, mcpInit: &mcpInitOnce{}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := sr.Run(ctx, nil, nil, nil); err == nil {
		t.Fatal("Run() error = nil, want fast failure after MCP init")
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("Run() returned after %v, want it to wait for the stalling server (500ms connect timeout)", elapsed)
	}

	// Both servers resolved: stall failed, good connected.
	states := mgr.ServerStates()
	if got := mcpServerStateByName(states, "stall"); got == nil || got.Status != mcp.ServerStatusFailed {
		t.Fatalf("stall state = %+v, want failed after Run", got)
	}
	if got := mcpServerStateByName(states, "good"); got == nil || got.Status != mcp.ServerStatusConnected {
		t.Fatalf("good state = %+v, want connected after Run", got)
	}

	// The good server's defs were registered in place during Run, with no
	// duplicates: exactly one def per tool.
	for _, want := range []string{"mcp__good__echo", "mcp__good__boom"} {
		if _, ok := registry.Get(want); !ok {
			t.Fatalf("registry missing %q after Run; names: %v", want, registry.Names())
		}
	}
	if got := mcpRegistryToolNames(registry); len(got) != len(mgr.ToolDefs()) {
		t.Fatalf("registry MCP defs = %v, want exactly the manager's %v", got, mcpToolNames(mgr.ToolDefs()))
	}

	// The producer was armed after WaitInit + registration: one snapshot.
	if snapshots != 1 {
		t.Fatalf("producer emitted %d snapshots, want 1", snapshots)
	}
}

func mcpRegistryToolNames(registry *tool.Registry) []string {
	var names []string
	for _, name := range registry.Names() {
		if strings.HasPrefix(name, "mcp__") {
			names = append(names, name)
		}
	}
	return names
}

func mcpToolNames(defs []tool.ToolDef) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func mcpServerStateByName(states []mcp.ServerState, name string) *mcp.ServerState {
	for i := range states {
		if states[i].Name == name {
			return &states[i]
		}
	}
	return nil
}

func TestMCPInitOnceConcurrentRunsExactlyOnce(t *testing.T) {
	fixtureBin := buildMCPFixture(t)
	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"stall": {
				Enabled:        true,
				Command:        fixtureBin,
				Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
				ConnectTimeout: config.MustDuration("10s"),
			},
			"good": {Enabled: true, Command: fixtureBin},
		},
	}, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer mgr.Close() //nolint:errcheck

	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr)

	producer := &mcpStateProducer{}
	rt := cliRuntime{
		cfg:        config.Config{Models: config.ModelsConfig{Default: "nope"}},
		registry:   registry,
		mcpManager: mgr,
		mcpState:   producer,
	}

	init := &mcpInitOnce{}

	// Simulate background goroutine and turn calling once.Do concurrently
	var wg sync.WaitGroup
	var turnErr error
	wg.Add(2)

	// Background goroutine
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		init.once.Do(func() { init.run(ctx, rt) })
	}()

	// Turn (should block in once.Do until background completes, then observe error)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		init.once.Do(func() { init.run(ctx, rt) })
		turnErr = init.err
	}()

	wg.Wait()

	// Both should observe the same error (WaitInit timed out while the stall server was still connecting; its ConnectTimeout outlives the 500ms ctx)
	if turnErr == nil {
		t.Fatal("turn goroutine err = nil, want WaitInit timeout error")
	}

	// Error path means producer was NOT armed (stays unarmed indefinitely)
	// Verify by checking armed state indirectly: call stateChanged and verify
	// it only triggers preListener, not listener
	if producer.preListener == nil {
		// Setup a listener to verify it's not called
		preListenerCalled := false
		listenerCalled := false
		producer.setPreListener(func() { preListenerCalled = true })
		producer.setListener(func() { listenerCalled = true })
		producer.stateChanged()
		if listenerCalled {
			t.Fatal("listener was called after error path, want producer to stay unarmed")
		}
		if !preListenerCalled {
			t.Fatal("preListener was not called, want states-only mode on error path")
		}
	}
}
