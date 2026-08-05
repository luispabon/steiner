package tui

import (
	"reflect"
	"testing"
)

func TestMCPStartupWarnings(t *testing.T) {
	tests := []struct {
		name    string
		servers []MCPServerStatus
		enabled bool
		want    []string
	}{
		{
			name:    "disabled with failures",
			enabled: false,
			servers: []MCPServerStatus{
				{Name: "foo", State: "failed", Error: "boom"},
			},
			want: nil,
		},
		{
			name:    "no servers",
			enabled: true,
			servers: nil,
			want:    nil,
		},
		{
			name:    "all connected",
			enabled: true,
			servers: []MCPServerStatus{
				{Name: "foo", State: "connected"},
				{Name: "bar", State: "connected"},
			},
			want: nil,
		},
		{
			name:    "one failure",
			enabled: true,
			servers: []MCPServerStatus{
				{Name: "foo", State: "failed", Error: "connection refused"},
			},
			want: []string{
				`⚠ MCP server "foo" failed to connect: connection refused`,
				`⚠ MCP startup incomplete (failed: foo)`,
			},
		},
		{
			name:    "multiple failures preserve order",
			enabled: true,
			servers: []MCPServerStatus{
				{Name: "foo", State: "failed", Error: "timeout"},
				{Name: "bar", State: "failed", Error: "auth error"},
			},
			want: []string{
				`⚠ MCP server "foo" failed to connect: timeout`,
				`⚠ MCP server "bar" failed to connect: auth error`,
				`⚠ MCP startup incomplete (failed: foo, bar)`,
			},
		},
		{
			name:    "mix of connected, failed, disabled",
			enabled: true,
			servers: []MCPServerStatus{
				{Name: "foo", State: "connected"},
				{Name: "bar", State: "failed", Error: "no such host"},
				{Name: "baz", State: "disabled"},
			},
			want: []string{
				`⚠ MCP server "bar" failed to connect: no such host`,
				`⚠ MCP startup incomplete (failed: bar)`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mcpStartupWarnings(tt.servers, tt.enabled)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mcpStartupWarnings() = %v, want %v", got, tt.want)
			}
		})
	}
}
