package main

import (
	"context"
	"errors"
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

// TestMCPInitRetriesAfterCancelledCompaction proves a manual compaction
// cancelled while MCP servers are still connecting does not poison the one-time
// initialization: a later request retries it and proceeds normally.
func TestMCPInitRetriesAfterCancelledCompaction(t *testing.T) {
	t.Parallel()
	fixtureBin := buildMCPFixture(t)

	// Park the stall server's launch so the first attempt cannot finish before
	// the test cancels it.
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
		// No such model: a compaction that gets past initialization fails on
		// model resolution instead of reaching the provider.
		cfg:        config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "nope", ActiveOrchestratorModel: "nope"}}},
		registry:   registry,
		mcpManager: mgr,
		mcpState:   &mcpStateProducer{},
		events:     output.SinkFunc(func(output.Event) {}),
	}
	sr := sessionRunner{runner: cliRunner{runtime: rt}, mcpInit: &mcpInitOnce{}}
	conversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, err := sr.CompactWithLineage(ctx, conversation, agent.ConversationLineage{}, agent.LastMainRequest{}, nil, "")
		done <- err
	}()
	// The gate keeps initialization blocked, so the compaction is inside the
	// MCP wait when it is cancelled.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled compaction error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled compaction did not return")
	}

	// The later request must retry the initialization and succeed.
	openGate()
	if err := sr.waitMCPInit(context.Background()); err != nil {
		t.Fatalf("waitMCPInit after a cancelled attempt = %v, want nil", err)
	}
	if _, ok := registry.Get("mcp__good__echo"); !ok {
		t.Fatalf("registry missing the connected MCP tool after the retry; names: %v", registry.Names())
	}

	// A later compaction gets past initialization; its error is the unknown
	// model, not a permanent cancellation.
	_, _, _, err := sr.CompactWithLineage(context.Background(), conversation, agent.ConversationLineage{}, agent.LastMainRequest{}, nil, "")
	if err == nil {
		t.Fatal("compaction after the retry returned no error, want the model resolution failure")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("compaction after the retry error = %v, want a non-cancellation error", err)
	}
}
