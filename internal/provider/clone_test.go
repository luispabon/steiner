package provider

import (
	"encoding/json"
	"testing"
)

func TestCloneMessagesDeepCopy(t *testing.T) {
	t.Parallel()

	original := []Message{
		{
			Role:    MessageRoleAssistant,
			Content: "assistant",
			ToolCalls: []ToolCall{
				{
					ID:   "call-1",
					Name: "read",
					Arguments: map[string]any{
						"path":   "a.go",
						"nested": map[string]any{"key": "value"},
						"list": []any{
							"item",
							map[string]any{"deep": "value"},
						},
						"raw": json.RawMessage(`{"ok":true}`),
					},
				},
			},
			ProviderMetadata: &MessageProviderMetadata{
				Anthropic: &AnthropicMessageMetadata{ThinkingSignature: "sig"},
			},
		},
	}

	cloned := CloneMessages(original)
	if len(cloned) != 1 {
		t.Fatalf("len(cloned) = %d, want 1", len(cloned))
	}

	cloned[0].ToolCalls[0].Arguments["path"] = "b.go"
	cloned[0].ToolCalls[0].Arguments["nested"].(map[string]any)["key"] = "changed"
	cloned[0].ToolCalls[0].Arguments["list"].([]any)[1].(map[string]any)["deep"] = "changed"
	raw := cloned[0].ToolCalls[0].Arguments["raw"].(json.RawMessage)
	raw[0] = '['
	cloned[0].ProviderMetadata.Anthropic.ThinkingSignature = "changed"

	if got, want := original[0].ToolCalls[0].Arguments["path"], "a.go"; got != want {
		t.Fatalf("original path = %v, want %v", got, want)
	}
	if got, want := original[0].ToolCalls[0].Arguments["nested"].(map[string]any)["key"], "value"; got != want {
		t.Fatalf("original nested key = %v, want %v", got, want)
	}
	if got, want := original[0].ToolCalls[0].Arguments["list"].([]any)[1].(map[string]any)["deep"], "value"; got != want {
		t.Fatalf("original deep list value = %v, want %v", got, want)
	}
	if got, want := string(original[0].ToolCalls[0].Arguments["raw"].(json.RawMessage)), `{"ok":true}`; got != want {
		t.Fatalf("original raw = %s, want %s", got, want)
	}
	if got, want := original[0].ProviderMetadata.Anthropic.ThinkingSignature, "sig"; got != want {
		t.Fatalf("original signature = %q, want %q", got, want)
	}
}

func TestCloneHelpersPreserveNilAndEmptyConventions(t *testing.T) {
	t.Parallel()

	if got := CloneMessages(nil); got != nil {
		t.Fatalf("CloneMessages(nil) = %#v, want nil", got)
	}
	if got := CloneMessages([]Message{}); got != nil {
		t.Fatalf("CloneMessages(empty) = %#v, want nil", got)
	}
	if got := CloneTools(nil); got != nil {
		t.Fatalf("CloneTools(nil) = %#v, want nil", got)
	}
	if got := CloneToolCalls(nil); got != nil {
		t.Fatalf("CloneToolCalls(nil) = %#v, want nil", got)
	}
	if got := CloneToolArguments(nil); got != nil {
		t.Fatalf("CloneToolArguments(nil) = %#v, want nil", got)
	}
	if got := CloneToolArguments(map[string]any{}); got == nil {
		t.Fatal("CloneToolArguments(empty) = nil, want empty map")
	}
}

func TestCloneMessageMetadataPreservesWrapperWithoutAnthropic(t *testing.T) {
	t.Parallel()

	cloned := CloneMessageMetadata(&MessageProviderMetadata{})
	if cloned == nil {
		t.Fatal("CloneMessageMetadata() = nil, want non-nil wrapper")
	}
	if cloned.Anthropic != nil {
		t.Fatalf("cloned.Anthropic = %#v, want nil", cloned.Anthropic)
	}
}

func TestCloneToolsDeepCopyParameters(t *testing.T) {
	t.Parallel()

	original := []ToolSpec{
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name: "read",
				Parameters: map[string]any{
					"schema": map[string]any{"type": "object"},
				},
			},
		},
	}

	cloned := CloneTools(original)
	cloned[0].Function.Parameters["schema"].(map[string]any)["type"] = "string"

	if got, want := original[0].Function.Parameters["schema"].(map[string]any)["type"], "object"; got != want {
		t.Fatalf("original schema type = %v, want %v", got, want)
	}
}

func TestCloneToolsPreserveEmptyNestedSchemaMaps(t *testing.T) {
	t.Parallel()

	original := []ToolSpec{
		{
			Type: "function",
			Function: ToolFunctionSpec{
				Name: "advisor",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties":           map[string]any{},
				},
			},
		},
	}

	cloned := CloneTools(original)
	properties, ok := cloned[0].Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want empty map", cloned[0].Function.Parameters["properties"])
	}
	if properties == nil {
		t.Fatal("properties = nil, want empty map")
	}
	if len(properties) != 0 {
		t.Fatalf("len(properties) = %d, want 0", len(properties))
	}
}

func TestCloneMessageMetadataPreservesCodexField(t *testing.T) {
	t.Parallel()

	original := &MessageProviderMetadata{
		Codex: &CodexMessageMetadata{ReasoningID: "reasoning-123"},
	}

	cloned := CloneMessageMetadata(original)
	if cloned == nil {
		t.Fatal("CloneMessageMetadata() = nil, want non-nil")
	}
	if cloned.Codex == nil {
		t.Fatal("cloned.Codex = nil, want non-nil")
	}
	if got, want := cloned.Codex.ReasoningID, "reasoning-123"; got != want {
		t.Fatalf("cloned.Codex.ReasoningID = %q, want %q", got, want)
	}

	// Verify deep copy: mutating the clone's Codex.ReasoningID does not affect the original.
	cloned.Codex.ReasoningID = "reasoning-changed"
	if got, want := original.Codex.ReasoningID, "reasoning-123"; got != want {
		t.Fatalf("original.Codex.ReasoningID = %q, want %q (should not be mutated)", got, want)
	}
}
