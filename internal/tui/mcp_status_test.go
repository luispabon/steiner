package tui

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestMCPStatusServersFromStatusEvent(t *testing.T) {
	t.Parallel()

	states := map[string]output.MCPServerState{
		"zeta": {
			State:     "connected",
			Transport: "stdio",
			Tools: []output.MCPAdvertisedTool{
				{Name: "tool_a", Outcome: "registered"},
			},
		},
		"alpha": {
			State:     "failed",
			Transport: "stdio",
			Error:     "boom",
		},
	}

	got := mcpServersFromStatusEvent(states)
	want := []MCPServerStatus{
		{
			Name:      "alpha",
			State:     "failed",
			Transport: "stdio",
			Tools:     []MCPToolStatus{},
			Error:     "boom",
		},
		{
			Name:      "zeta",
			State:     "connected",
			Transport: "stdio",
			Tools: []MCPToolStatus{
				{Name: "tool_a", Outcome: "registered"},
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mcpServersFromStatusEvent() = %+v, want %+v", got, want)
	}
}

func TestMCPStatusServersFromStatusEventEmpty(t *testing.T) {
	t.Parallel()

	got := mcpServersFromStatusEvent(map[string]output.MCPServerState{})
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %+v", got)
	}
}

func TestMCPStatusToolOriginsFromStatusEvent(t *testing.T) {
	t.Parallel()

	origins := map[string]output.MCPToolOrigin{
		"mcp__server__tool": {Server: "server", Tool: "tool"},
	}

	got := mcpToolOriginsFromStatusEvent(origins)
	want := map[string]MCPToolOrigin{
		"mcp__server__tool": {Server: "server", Tool: "tool"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mcpToolOriginsFromStatusEvent() = %+v, want %+v", got, want)
	}
}

func TestMCPStatusToolOriginsFromStatusEventNil(t *testing.T) {
	t.Parallel()

	if got := mcpToolOriginsFromStatusEvent(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %+v", got)
	}

	if got := mcpToolOriginsFromStatusEvent(map[string]output.MCPToolOrigin{}); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
}
