package output

import (
	"encoding/json"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestRequestToolsBytes_MatchesEncoding(t *testing.T) {
	tools := []provider.ToolSpec{
		{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Parameters: map[string]any{"type": "object"}}},
		{Type: "function", Function: provider.ToolFunctionSpec{Name: "grep", Parameters: map[string]any{"type": "object"}}},
	}
	data, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if got := requestToolsBytes(tools); got != len(data) {
		t.Fatalf("requestToolsBytes() = %d, want %d", got, len(data))
	}

	// The size follows the current slice contents, so an in-place edit of the
	// tool definitions is reflected on the next call.
	tools[0].Function.Description = "Read a file from disk and return its contents."
	updated, err := json.Marshal(tools)
	if err != nil {
		t.Fatalf("marshal updated tools: %v", err)
	}
	if got := requestToolsBytes(tools); got != len(updated) {
		t.Fatalf("requestToolsBytes() after an in-place edit = %d, want %d", got, len(updated))
	}
}

func TestRequestToolsBytes_EmptyAndUnmarshalable(t *testing.T) {
	if got := requestToolsBytes(nil); got != 0 {
		t.Fatalf("requestToolsBytes(nil) = %d, want 0", got)
	}
	if got := requestToolsBytes([]provider.ToolSpec{}); got != 0 {
		t.Fatalf("requestToolsBytes(empty) = %d, want 0", got)
	}

	broken := []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "read", Parameters: map[string]any{"bad": func() {}}}}}
	if got := requestToolsBytes(broken); got != 0 {
		t.Fatalf("requestToolsBytes(unmarshalable) = %d, want 0", got)
	}
}
