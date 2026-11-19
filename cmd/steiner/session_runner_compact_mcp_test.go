package main

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
)

// TestSessionRunnerCompactWaitsForMCPInit proves a manual compaction waits for
// the one-shot MCP initialization before the runner rebuilds a missing parent
// tool registry, so the rebuilt set matches what a run would send.
func TestSessionRunnerCompactWaitsForMCPInit(t *testing.T) {
	t.Parallel()
	fixtureBin := buildMCPFixture(t)

	// Gate the stall server's launch so the wait is deterministic: Connect
	// builds each server command (and calls WrapFn) before that server's
	// connect timeout starts.
	stallAtGate := make(chan struct{})
	stallGate := make(chan struct{})
	var atGateOnce, openOnce sync.Once
	openGate := func() { openOnce.Do(func() { close(stallGate) }) }
	wrap := func(cmd *exec.Cmd) (*exec.Cmd, error) {
		if len(cmd.Args) > 1 && cmd.Args[1] == "stall" {
			atGateOnce.Do(func() { close(stallAtGate) })
			<-stallGate
		}
		return cmd, nil
	}

	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"stall": {
				Enabled:        true,
				Command:        fixtureBin,
				Args:           []string{"stall"},
				Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
				ConnectTimeout: config.MustDuration("1s"),
			},
			"good": {Enabled: true, Command: fixtureBin},
		},
	}, config.LimitsConfig{}, wrap, mcp.ConnectOptions{WarnFn: func(string) {}, InfoFn: func(string) {}, Stderr: io.Discard})
	defer mgr.Close() //nolint:errcheck
	// Registered after mgr.Close so it runs first on the way out: a connect
	// goroutine still parked at the gate must be released before shutdown.
	defer openGate()

	select {
	case <-stallAtGate:
	case <-time.After(5 * time.Second):
		t.Fatal("stall server never reached the launch gate")
	}

	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr, nil)
	rt := cliRuntime{
		// No such model: the runner fails fast right after the MCP init.
		cfg:        config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "nope", ActiveOrchestratorModel: "nope"}}},
		registry:   registry,
		mcpManager: mgr,
		mcpState:   &mcpStateProducer{},
		events:     output.SinkFunc(func(output.Event) {}),
	}
	sr := sessionRunner{runner: cliRunner{runtime: rt}, mcpInit: &mcpInitOnce{}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, _, err := sr.CompactWithLineage(ctx, []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}, agent.ConversationLineage{}, agent.LastMainRequest{}, nil, "")
		done <- err
	}()

	// The stall server is parked, so MCP init cannot finish and the compaction
	// must not return.
	select {
	case err := <-done:
		t.Fatalf("CompactWithLineage returned %v before MCP init finished", err)
	case <-time.After(100 * time.Millisecond):
	}

	openGate()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CompactWithLineage error = nil, want the model resolution failure after MCP init")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CompactWithLineage did not return after MCP init finished")
	}

	// Init ran before the rebuild: the good server's defs reached the shared
	// registry the rebuild clones.
	if _, ok := registry.Get("mcp__good__echo"); !ok {
		t.Fatalf("registry missing the connected MCP tool after compaction; names: %v", registry.Names())
	}
	states := mgr.ServerStates()
	if got := mcpServerStateByName(states, "good"); got == nil || got.Status != mcp.ServerStatusConnected {
		t.Fatalf("good state = %+v, want connected", got)
	}
}
