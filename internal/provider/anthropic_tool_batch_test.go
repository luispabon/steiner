package provider

import (
	"encoding/json"
	"slices"
	"testing"
)

func toolCallsMsg(ids ...string) Message {
	calls := make([]ToolCall, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, ToolCall{ID: id, Name: "read", Arguments: map[string]any{"path": id}})
	}
	return Message{Role: MessageRoleAssistant, ToolCalls: calls}
}

func toolResultMsg(id string) Message {
	return Message{Role: MessageRoleTool, ToolCallID: id, Content: "result " + id}
}

func toolResultIDs(msg anthropicMessage) []string {
	var ids []string
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			ids = append(ids, block.ToolUseID)
		}
	}
	return ids
}

func TestAnthropicRequestWire_CoalescesToolResults(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		roles    []string
		// batches lists, per wire message, the tool_result ids expected (nil for non-batch messages).
		batches [][]string
	}{
		{
			name:     "one assistant turn with three results",
			messages: []Message{toolCallsMsg("a", "b", "c"), toolResultMsg("a"), toolResultMsg("b"), toolResultMsg("c")},
			roles:    []string{"assistant", "user"},
			batches:  [][]string{nil, {"a", "b", "c"}},
		},
		{
			name: "batch then user message stays separate",
			messages: []Message{
				toolCallsMsg("a", "b"), toolResultMsg("a"), toolResultMsg("b"),
				{Role: MessageRoleUser, Content: "steer"},
			},
			roles:   []string{"assistant", "user", "user"},
			batches: [][]string{nil, {"a", "b"}, nil},
		},
		{
			name: "batches do not merge across assistant message",
			messages: []Message{
				toolCallsMsg("a", "b"), toolResultMsg("a"), toolResultMsg("b"),
				toolCallsMsg("c"), toolResultMsg("c"),
			},
			roles:   []string{"assistant", "user", "assistant", "user"},
			batches: [][]string{nil, {"a", "b"}, nil, {"c"}},
		},
		{
			name: "system message inside a run does not end it",
			messages: []Message{
				toolCallsMsg("a", "b"), toolResultMsg("a"),
				{Role: MessageRoleSystem, Content: "note"},
				toolResultMsg("b"),
			},
			roles:   []string{"assistant", "user"},
			batches: [][]string{nil, {"a", "b"}},
		},
		{
			name: "user message splits two tool runs",
			messages: []Message{
				toolResultMsg("a"), {Role: MessageRoleUser, Content: "hi"}, toolResultMsg("b"),
			},
			roles:   []string{"user", "user", "user"},
			batches: [][]string{{"a"}, nil, {"b"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: tt.messages}, "m", false)
			if got, want := len(wire.Messages), len(tt.roles); got != want {
				t.Fatalf("messages = %d, want %d", got, want)
			}
			for i, msg := range wire.Messages {
				if msg.Role != tt.roles[i] {
					t.Errorf("messages[%d].Role = %q, want %q", i, msg.Role, tt.roles[i])
				}
				if got := toolResultIDs(msg); !slices.Equal(got, tt.batches[i]) {
					t.Errorf("messages[%d] tool_result ids = %v, want %v", i, got, tt.batches[i])
				}
			}
		})
	}
}

func TestAnthropicRequestWire_BatchFollowedByUserKeepsTextSeparate(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: []Message{
		toolCallsMsg("a", "b"), toolResultMsg("a"), toolResultMsg("b"),
		{Role: MessageRoleUser, Content: "next"},
	}}, "m", false)
	if got, want := len(wire.Messages), 3; got != want {
		t.Fatalf("messages = %d, want %d", got, want)
	}
	for i, block := range wire.Messages[1].Content {
		if block.Type != "tool_result" {
			t.Errorf("batch block %d type = %q, want tool_result", i, block.Type)
		}
	}
	text := wire.Messages[2].Content
	if len(text) != 1 || text[0].Type != "text" || text[0].Text != "next" {
		t.Fatalf("user message content = %+v, want single text block %q", text, "next")
	}
}

func TestAnthropicRequestWire_SingleToolResultJSONUnchanged(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: []Message{
		toolCallsMsg("a"), toolResultMsg("a"),
	}}, "m", false)
	got, err := json.Marshal(wire.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"role":"assistant","content":[{"id":"a","input":{"path":"a"},"name":"read","type":"tool_use"}]},` +
		`{"role":"user","content":[{"cache_control":{"type":"ephemeral"},"content":"result a","tool_use_id":"a","type":"tool_result"}]}]`
	if string(got) != want {
		t.Fatalf("messages JSON =\n%s\nwant\n%s", got, want)
	}
}

func TestAnthropicRequestWire_BatchedImageToolResultsStayNested(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: []Message{
		toolCallsMsg("a", "b", "c"),
		toolResultMsg("a"),
		{Role: MessageRoleTool, ToolCallID: "b", Content: "result b", Images: []ImageBlock{{MediaType: "image/png", Data: "imgB"}}},
		{Role: MessageRoleTool, ToolCallID: "c", Images: []ImageBlock{{MediaType: "image/png", Data: "imgC"}}},
	}}, "m", false)
	if got, want := len(wire.Messages), 2; got != want {
		t.Fatalf("messages = %d, want %d", got, want)
	}
	batch := wire.Messages[1].Content
	if got, want := len(batch), 3; got != want {
		t.Fatalf("batch blocks = %d, want %d", got, want)
	}
	for i, block := range batch {
		if block.Type != "tool_result" {
			t.Fatalf("batch[%d].Type = %q, want tool_result (no sibling image blocks)", i, block.Type)
		}
	}
	if len(batch[0].ContentBlocks) != 0 || batch[0].Content != "result a" {
		t.Errorf("batch[0] = %+v, want plain string content", batch[0])
	}
	b := batch[1]
	if b.ToolUseID != "b" || len(b.ContentBlocks) != 2 || b.ContentBlocks[0].Type != "text" ||
		b.ContentBlocks[1].Type != "image" || b.ContentBlocks[1].Source.Data != "imgB" {
		t.Errorf("batch[1] = %+v, want text+imgB nested", b)
	}
	c := batch[2]
	if c.ToolUseID != "c" || len(c.ContentBlocks) != 1 || c.ContentBlocks[0].Type != "image" ||
		c.ContentBlocks[0].Source.Data != "imgC" {
		t.Errorf("batch[2] = %+v, want imgC nested only", c)
	}
}

func TestAnthropicRequestWire_ToolLoopBreakpointPlacement(t *testing.T) {
	wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: []Message{
		{Role: MessageRoleSystem, Content: "sys"},
		{Role: MessageRoleUser, Content: "prompt"},
		toolCallsMsg("a", "b"), toolResultMsg("a"), toolResultMsg("b"),
		toolCallsMsg("c", "d"), toolResultMsg("c"), toolResultMsg("d"),
		toolCallsMsg("e", "f"), toolResultMsg("e"), toolResultMsg("f"),
	}}, "m", false)

	if got, want := len(wire.Messages), 7; got != want {
		t.Fatalf("messages = %d, want %d", got, want)
	}
	var marked []string
	if wire.System[len(wire.System)-1].CacheControl != nil {
		marked = append(marked, "system")
	}
	for i, msg := range wire.Messages {
		for j, block := range msg.Content {
			if block.CacheControl == nil {
				continue
			}
			label := block.ToolUseID
			if label == "" {
				label = block.Type
			}
			marked = append(marked, label)
			if j != len(msg.Content)-1 {
				t.Errorf("messages[%d] block %d marked but is not the last block", i, j)
			}
		}
	}
	want := []string{"system", "d", "f"}
	if !slices.Equal(marked, want) {
		t.Fatalf("cache_control on %v, want %v", marked, want)
	}
}
