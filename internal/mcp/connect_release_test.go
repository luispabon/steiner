package mcp

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestConnectReleaseHook checks that ConnectOptions.Release reaches the stdio
// transport: connecting a stdio server invokes it exactly once with the started
// server command, and a nil Release is tolerated.
func TestConnectReleaseHook(t *testing.T) {
	fixtureBin := buildFixture(t)
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"good": {Enabled: true, Command: fixtureBin},
		},
	}

	t.Run("invoked once with the started command", func(t *testing.T) {
		var calls atomic.Int32
		var gotCmd atomic.Pointer[exec.Cmd]
		release := func(cmd *exec.Cmd) {
			calls.Add(1)
			gotCmd.Store(cmd)
		}

		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, ConnectOptions{Release: release})
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		if good := stateByName(m.ServerStates(), "good"); good == nil || good.Status != ServerStatusConnected {
			t.Fatalf("good server state = %+v, want connected", good)
		}
		if got := calls.Load(); got != 1 {
			t.Errorf("release called %d times, want 1", got)
		}
		cmd := gotCmd.Load()
		if cmd == nil {
			t.Fatal("release received nil *exec.Cmd")
		}
		if cmd.Process == nil {
			t.Error("release received a command that was never started")
		}
	})

	t.Run("nil release does not panic", func(t *testing.T) {
		m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, ConnectOptions{})
		defer m.Close() //nolint:errcheck
		waitInit(t, m)

		if good := stateByName(m.ServerStates(), "good"); good == nil || good.Status != ServerStatusConnected {
			t.Errorf("good server state = %+v, want connected", good)
		}
	})
}
