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
		{
			name:    "failure with empty error",
			enabled: true,
			servers: []MCPServerStatus{
				{Name: "foo", State: "failed", Error: ""},
			},
			want: []string{
				`⚠ MCP server "foo" failed to connect: unknown error`,
				`⚠ MCP startup incomplete (failed: foo)`,
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

func TestMCPStartupWarningsUnavailable(t *testing.T) {
	servers := []MCPServerStatus{
		{Name: "foo", State: "unavailable", Error: "exhausted retries"},
	}
	got := mcpStartupWarnings(servers, true)
	want := []string{
		`⚠ MCP server "foo" failed to connect: exhausted retries`,
		`⚠ MCP startup incomplete (failed: foo)`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mcpStartupWarnings() = %v, want %v", got, want)
	}
}

func TestMCPTransitionWarnings(t *testing.T) {
	failed := []MCPServerStatus{{Name: "foo", State: "failed", Error: "boom"}}
	unavailable := []MCPServerStatus{{Name: "foo", State: "unavailable", Error: "exhausted"}}
	connected := []MCPServerStatus{{Name: "foo", State: "connected"}}

	t.Run("first failure warns once", func(t *testing.T) {
		lines, warned := mcpTransitionWarnings(failed, nil)
		if len(lines) != 1 || lines[0] != `⚠ MCP server "foo" failed to connect: boom` {
			t.Fatalf("lines = %v, want single failure line", lines)
		}
		if !warned["foo"] {
			t.Fatal("warned[foo] = false, want true")
		}

		lines, _ = mcpTransitionWarnings(failed, warned)
		if len(lines) != 0 {
			t.Fatalf("second failure event emitted %d lines, want 0", len(lines))
		}
	})

	t.Run("unavailable is a failure state", func(t *testing.T) {
		lines, warned := mcpTransitionWarnings(unavailable, nil)
		if len(lines) != 1 {
			t.Fatalf("lines = %v, want single unavailable line", lines)
		}
		lines, _ = mcpTransitionWarnings(unavailable, warned)
		if len(lines) != 0 {
			t.Fatalf("second unavailable event emitted %d lines, want 0", len(lines))
		}
	})

	t.Run("recovery clears flag so failure warns again", func(t *testing.T) {
		_, warned := mcpTransitionWarnings(failed, nil)
		mcpTransitionWarnings(connected, warned)
		if warned["foo"] {
			t.Fatal("warned[foo] = true after connected, want cleared")
		}
		lines, _ := mcpTransitionWarnings(failed, warned)
		if len(lines) != 1 {
			t.Fatalf("failure after recovery emitted %d lines, want 1", len(lines))
		}
	})

	t.Run("in-flight and healthy states never warn", func(t *testing.T) {
		for _, state := range []string{"connecting", "reconnecting", "connected", "disabled"} {
			lines, _ := mcpTransitionWarnings([]MCPServerStatus{{Name: "foo", State: state}}, nil)
			if len(lines) != 0 {
				t.Errorf("state %q emitted %d lines, want 0", state, len(lines))
			}
		}
	})

	t.Run("empty error falls back", func(t *testing.T) {
		lines, _ := mcpTransitionWarnings([]MCPServerStatus{{Name: "foo", State: "failed"}}, nil)
		if len(lines) != 1 || lines[0] != `⚠ MCP server "foo" failed to connect: unknown error` {
			t.Fatalf("lines = %v, want unknown-error fallback", lines)
		}
	})
}
