package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCloneMessagesFidelity(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "all fields populated",
			message: Message{
				Role:             MessageRoleAssistant,
				Content:          "content",
				ReasoningContent: "reasoning",
				Name:             "name",
				ToolCallID:       "call_1",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "grep",
						Arguments: map[string]any{
							"pattern": "foo",
						},
						RawArguments: `{"pattern":"foo"}`,
					},
				},
				Images: []ImageBlock{
					{
						ID:        "img_1",
						FilePath:  "/tmp/img.png",
						MediaType: "image/png",
						Data:      "aGVsbG8=",
						Width:     100,
						Height:    50,
						SizeBytes: 12,
					},
				},
				Source:   "source",
				ByteSize: 1234,
				Turn:     3,
				Retention: &MessageRetention{
					Kind:       "summary",
					Summary:    "sum",
					AgentID:    "agent",
					Status:     "ok",
					TurnCount:  2,
					TokenCount: 10,
				},
				ProviderMetadata: &MessageProviderMetadata{
					Anthropic: &AnthropicMessageMetadata{ThinkingSignature: "sig"},
					Codex:     &CodexMessageMetadata{ReasoningID: "rid"},
				},
			},
		},
		{
			name: "nested arguments",
			message: Message{
				Role: MessageRoleAssistant,
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "tool",
						Arguments: map[string]any{
							"nested": map[string]any{
								"key":  "value",
								"list": []any{map[string]any{"a": 1}},
							},
							"slice": []any{"x", map[string]any{"b": 2}},
							"raw":   json.RawMessage(`{"x":1}`),
						},
					},
				},
			},
		},
		{
			name: "images with and without data",
			message: Message{
				Role: MessageRoleUser,
				Images: []ImageBlock{
					{ID: "img_empty", Data: ""},
					{ID: "img_data", Data: "aGVsbG8="},
				},
			},
		},
		{
			name:    "summary role",
			message: Message{Role: MessageRoleSummary, Content: "summarized"},
		},
		{
			name: "provider metadata both set",
			message: Message{
				Role: MessageRoleAssistant,
				ProviderMetadata: &MessageProviderMetadata{
					Anthropic: &AnthropicMessageMetadata{ThinkingSignature: "ts"},
					Codex:     &CodexMessageMetadata{ReasoningID: "rid"},
				},
			},
		},
		{
			name: "tool call raw arguments",
			message: Message{
				Role: MessageRoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "c1", Name: "read", Arguments: map[string]any{"path": "x"}, RawArguments: `{"path":"x"}`},
				},
			},
		},
		// No nil-Arguments ToolCall here: cloneInput intentionally converts a nil
		// Arguments map to an empty non-nil map (pre-existing behaviour), so
		// DeepEqual against the original would fail by design.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloned := cloneMessage(tt.message)
			if !reflect.DeepEqual(cloned, tt.message) {
				t.Errorf("cloneMessage mismatch:\n got: %#v\nwant: %#v", cloned, tt.message)
			}
			if len(tt.message.ToolCalls) > 0 && &cloned.ToolCalls[0] == &tt.message.ToolCalls[0] {
				t.Error("ToolCalls backing array shared with original")
			}
			if len(tt.message.Images) > 0 && &cloned.Images[0] == &tt.message.Images[0] {
				t.Error("Images backing array shared with original")
			}
			if tt.message.Retention != nil && cloned.Retention == tt.message.Retention {
				t.Error("Retention pointer shared with original")
			}
			if tt.message.ProviderMetadata != nil {
				if cloned.ProviderMetadata == tt.message.ProviderMetadata {
					t.Error("ProviderMetadata pointer shared with original")
				}
				if tt.message.ProviderMetadata.Anthropic != nil && cloned.ProviderMetadata.Anthropic == tt.message.ProviderMetadata.Anthropic {
					t.Error("Anthropic metadata pointer shared with original")
				}
				if tt.message.ProviderMetadata.Codex != nil && cloned.ProviderMetadata.Codex == tt.message.ProviderMetadata.Codex {
					t.Error("Codex metadata pointer shared with original")
				}
			}
		})
	}
}

func TestCloneMessageMutationIndependence(t *testing.T) {
	original := Message{
		Role: MessageRoleAssistant,
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Name: "tool",
				Arguments: map[string]any{
					"nested": map[string]any{
						"key": "value",
					},
					"list": []any{
						map[string]any{"a": 1},
						"b",
					},
				},
			},
		},
	}

	cloned := cloneMessage(original)

	// Mutate deeply-nested values and append to a nested []any in the clone.
	clonedNested := cloned.ToolCalls[0].Arguments["nested"].(map[string]any)
	clonedNested["key"] = "mutated"
	clonedList := cloned.ToolCalls[0].Arguments["list"].([]any)
	cloned.ToolCalls[0].Arguments["list"] = append(clonedList, map[string]any{"c": 3})
	cloned.ToolCalls[0].Arguments["list"].([]any)[0].(map[string]any)["a"] = 99

	originalNested := original.ToolCalls[0].Arguments["nested"].(map[string]any)
	if originalNested["key"] != "value" {
		t.Errorf("original nested map mutated: key = %q, want %q", originalNested["key"], "value")
	}
	originalList := original.ToolCalls[0].Arguments["list"].([]any)
	if len(originalList) != 2 {
		t.Errorf("original list length = %d, want 2", len(originalList))
	}
	if first, ok := originalList[0].(map[string]any); !ok || first["a"] != 1 {
		t.Errorf("original list element mutated: %#v", originalList[0])
	}
}

// TestCloneMessagesPreservesSourceByteSizeAndDataLessImages documents the
// deliberate behavior change: the old provider round-trip (cloneMessages via
// ToProviderMessages/fromProviderMessages) silently zeroed Source and ByteSize
// (provider.Message carries neither) and dropped Images entries with empty Data
// (message_convert.go skips img.Data == ""). The direct deep-clone preserves
// all three.
func TestCloneMessagesPreservesSourceByteSizeAndDataLessImages(t *testing.T) {
	original := Message{
		Role:     MessageRoleUser,
		Source:   "http://example.com/doc",
		ByteSize: 4096,
		Images: []ImageBlock{
			{ID: "data_less", Data: ""},
			{ID: "with_data", Data: "aGVsbG8="},
		},
	}

	cloned := cloneMessages([]Message{original})
	if len(cloned) != 1 {
		t.Fatalf("cloneMessages returned %d messages, want 1", len(cloned))
	}
	if cloned[0].Source != original.Source {
		t.Errorf("Source = %q, want %q", cloned[0].Source, original.Source)
	}
	if cloned[0].ByteSize != original.ByteSize {
		t.Errorf("ByteSize = %d, want %d", cloned[0].ByteSize, original.ByteSize)
	}
	if len(cloned[0].Images) != len(original.Images) {
		t.Fatalf("len(Images) = %d, want %d (data-less image must survive)", len(cloned[0].Images), len(original.Images))
	}
	if cloned[0].Images[0].Data != "" || cloned[0].Images[0].ID != "data_less" {
		t.Errorf("data-less image not preserved: %#v", cloned[0].Images[0])
	}
	if cloned[0].Images[1].Data != "aGVsbG8=" {
		t.Errorf("data image not preserved: %#v", cloned[0].Images[1])
	}
}

func TestCloneMessageRetentionDeepEqual(t *testing.T) {
	original := Message{
		Role: MessageRoleSummary,
		Retention: &MessageRetention{
			Kind:       "summary",
			Summary:    "sum",
			AgentID:    "agent",
			Status:     "ok",
			TurnCount:  2,
			TokenCount: 10,
		},
	}

	cloned := cloneMessage(original)
	if cloned.Retention == nil {
		t.Fatal("cloned Retention is nil")
	}
	if !reflect.DeepEqual(cloned.Retention, original.Retention) {
		t.Errorf("Retention mismatch: got %#v, want %#v", cloned.Retention, original.Retention)
	}

	cloned.Retention.Kind = "mutated"
	cloned.Retention.TurnCount = 999
	if original.Retention.Kind != "summary" || original.Retention.TurnCount != 2 {
		t.Errorf("original Retention mutated: %#v", original.Retention)
	}
}

func TestCloneMessagesEmptyReturnsNil(t *testing.T) {
	if cloned := cloneMessages(nil); cloned != nil {
		t.Errorf("cloneMessages(nil) = %#v, want nil", cloned)
	}
	if cloned := cloneMessages([]Message{}); cloned != nil {
		t.Errorf("cloneMessages([]Message{}) = %#v, want nil", cloned)
	}
}
