package mcp

import (
	"context"
	"io"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestConnectOptionsZeroValueDefaults checks that a zero-value ConnectOptions
// behaves like the defaults Connect applied before the options struct: plan
// mode off, nil callbacks tolerated, and every server resolved. Explicit no-op
// callbacks must give the same outcome as the zero value.
func TestConnectOptionsZeroValueDefaults(t *testing.T) {
	fixtureBin := buildFixture(t)
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"bad":  {Enabled: true, Command: "/nonexistent/steiner-no-such-binary"},
			"good": {Enabled: true, Command: fixtureBin},
		},
	}
	noop := func(string) {}

	tests := []struct {
		name         string
		opts         ConnectOptions
		wantPlanMode bool
	}{
		{name: "zero value", opts: ConnectOptions{}, wantPlanMode: false},
		{
			name: "explicit no-op callbacks",
			opts: ConnectOptions{
				WarnFn:        noop,
				InfoFn:        noop,
				Stderr:        io.Discard,
				OnStateChange: func() {},
			},
			wantPlanMode: false,
		},
		{name: "plan mode only", opts: ConnectOptions{PlanMode: true}, wantPlanMode: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, tt.opts)
			defer m.Close() //nolint:errcheck
			waitInit(t, m)

			if m.planMode != tt.wantPlanMode {
				t.Errorf("planMode = %v, want %v", m.planMode, tt.wantPlanMode)
			}
			if got := len(m.ToolDefs()); got != 6 {
				t.Errorf("ToolDefs() has %d tools, want 6", got)
			}
			if bad := stateByName(m.ServerStates(), "bad"); bad == nil || bad.Status != ServerStatusFailed || bad.Err == "" {
				t.Errorf("bad server state = %+v, want failed with error text", bad)
			}
			if good := stateByName(m.ServerStates(), "good"); good == nil || good.Status != ServerStatusConnected {
				t.Errorf("good server state = %+v, want connected", good)
			}
		})
	}
}
