package provider

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// The committed goldens were captured from origin/main, before
// Message.CacheBoundary existed, with this flag. Rerun it only to accept a
// deliberate wire-shape change, and capture the new bytes from origin/main.
var updateAnthropicWireFlagOffGoldens = flag.Bool("update-anthropic-wire-flag-off-goldens", false, "rewrite the flag-off Anthropic wire goldens")

func flagOffToolBatchRequest() ChatRequest {
	return ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "List the two files."},
			{Role: MessageRoleAssistant, Content: "Reading both.", ToolCalls: []ToolCall{
				{ID: "toolu_alpha", Name: "read", Arguments: map[string]any{"path": "alpha.go"}},
				{ID: "toolu_beta", Name: "read", Arguments: map[string]any{"path": "beta.go"}},
			}},
			{Role: MessageRoleTool, ToolCallID: "toolu_alpha", Content: "package alpha"},
			{Role: MessageRoleTool, ToolCallID: "toolu_beta", Content: "package beta"},
			{Role: MessageRoleUser, Content: "Summarize both."},
		},
	}
}

func flagOffImageRequest() ChatRequest {
	return ChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "What does this screenshot show?", Images: []ImageBlock{
				{ID: "img-1", MediaType: "image/png", Data: "iVBORw0KGgo=", Width: 64, Height: 64, SizeBytes: 12},
			}},
			{Role: MessageRoleAssistant, Content: "It shows a chart."},
			{Role: MessageRoleUser, Content: "And the trend?"},
		},
	}
}

// TestAnthropicWireFlagOffGoldens pins invariant I1: for a request built without
// the marker, the normal-profile Anthropic wire JSON is byte-identical to
// origin/main.
func TestAnthropicWireFlagOffGoldens(t *testing.T) {
	cases := []struct {
		name    string
		request ChatRequest
	}{
		{name: "tool_batch", request: flagOffToolBatchRequest()},
		{name: "image", request: flagOffImageRequest()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", "anthropic_wire_flag_off_"+tc.name+".json")
			got, err := json.Marshal(anthropicRequestWire(tc.request, "default-model", false))
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if *updateAnthropicWireFlagOffGoldens {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write golden %s: %v", path, err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("flag-off Anthropic wire JSON drifted from %s\ngot:  %s\nwant: %s\nregenerate with:\n  go test ./internal/provider -run TestAnthropicWireFlagOffGoldens -update-anthropic-wire-flag-off-goldens", path, got, want)
			}
		})
	}
}
