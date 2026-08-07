package mcp

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestServerStatusWireValues(t *testing.T) {
	// These strings reach the TUI and logs, so pin them.
	want := map[ServerStatus]string{
		ServerStatusConnecting:   "connecting",
		ServerStatusConnected:    "connected",
		ServerStatusFailed:       "failed",
		ServerStatusReconnecting: "reconnecting",
		ServerStatusUnavailable:  "unavailable",
		ServerStatusDisabled:     "disabled",
	}
	for status, wantStr := range want {
		if got := string(status); got != wantStr {
			t.Errorf("ServerStatus(%q) = %q, want %q", status, got, wantStr)
		}
	}
}

func TestToolOutcomeWireValues(t *testing.T) {
	// These values are compared in tests and may reach the TUI, so pin them.
	if ToolRegistered != 0 || ToolFiltered != 1 || ToolDenied != 2 {
		t.Errorf("ToolOutcome values = %d, %d, %d, want 0, 1, 2", ToolRegistered, ToolFiltered, ToolDenied)
	}
}

func TestDeclaredStates(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.MCPConfig
		want []ServerState
	}{
		{
			name: "no servers",
			cfg:  config.MCPConfig{},
			want: nil,
		},
		{
			name: "sorted order and default transport",
			cfg: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"zeta":  {Command: "zeta-bin"},
					"alpha": {Command: "alpha-bin", Transport: "stdio"},
				},
			},
			want: []ServerState{
				{Name: "alpha", Status: ServerStatusDisabled, Transport: "stdio"},
				{Name: "zeta", Status: ServerStatusDisabled, Transport: "stdio"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeclaredStates(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DeclaredStates() = %+v, want %+v", got, tt.want)
			}
			// Declared states predate discovery: no advertised tools yet.
			for _, s := range got {
				if len(s.AdvertisedTools) != 0 {
					t.Errorf("DeclaredStates() AdvertisedTools = %+v, want empty", s.AdvertisedTools)
				}
			}
		})
	}
}
