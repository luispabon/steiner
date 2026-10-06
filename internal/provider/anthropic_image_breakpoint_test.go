package provider

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func markedBlocks(wire anthropicRequest) []string {
	var marked []string
	if len(wire.System) > 0 && wire.System[len(wire.System)-1].CacheControl != nil {
		marked = append(marked, "system")
	}
	for i, msg := range wire.Messages {
		for j, block := range msg.Content {
			if block.CacheControl != nil {
				marked = append(marked, fmt.Sprintf("%d.%d", i, j))
			}
		}
	}
	return marked
}

func TestAnthropicRequestWire_ImageBreakpointPlacement(t *testing.T) {
	img := []ImageBlock{{MediaType: "image/png", Data: "img"}}
	sys := Message{Role: MessageRoleSystem, Content: "sys"}
	prompt := Message{Role: MessageRoleUser, Content: "prompt"}
	reply := Message{Role: MessageRoleAssistant, Content: "reply"}
	imgTool := func(id string) Message {
		return Message{Role: MessageRoleTool, ToolCallID: id, Content: "result " + id, Images: img}
	}

	tests := []struct {
		name     string
		messages []Message
		want     []string
	}{
		{
			name: "no images keeps rolling placement",
			messages: []Message{
				sys, prompt,
				toolCallsMsg("a", "b"), toolResultMsg("a"), toolResultMsg("b"),
				toolCallsMsg("c", "d"), toolResultMsg("c"), toolResultMsg("d"),
			},
			want: []string{"system", "2.1", "4.1"},
		},
		{
			name:     "pasted image after assistant reply",
			messages: []Message{sys, prompt, reply, {Role: MessageRoleUser, Content: "look", Images: img}},
			want:     []string{"system", "0.0", "1.0"},
		},
		{
			name: "image tool result mid batch",
			messages: []Message{
				sys, prompt, toolCallsMsg("a", "b", "c"),
				toolResultMsg("a"), imgTool("b"), toolResultMsg("c"),
			},
			want: []string{"system", "0.0", "2.0"},
		},
		{
			name: "image tool result first in batch",
			messages: []Message{
				sys, prompt, toolCallsMsg("a", "b"),
				imgTool("a"), toolResultMsg("b"),
			},
			want: []string{"system", "0.0", "1.1"},
		},
		{
			name:     "image user message first",
			messages: []Message{sys, {Role: MessageRoleUser, Content: "look", Images: img}},
			want:     []string{"system"},
		},
		{
			name: "image message then steer",
			messages: []Message{
				sys, prompt, reply,
				{Role: MessageRoleUser, Content: "look", Images: img},
				{Role: MessageRoleUser, Content: "steer"},
			},
			want: []string{"system", "1.0"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := anthropicRequestWire(ChatRequest{Model: "m", Messages: tt.messages}, "m", false)
			if got := markedBlocks(wire); !slices.Equal(got, tt.want) {
				t.Fatalf("cache_control on %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnthropicContentBlock_VolatileNotSerialised(t *testing.T) {
	plain, err := json.Marshal(anthropicContentBlock{Type: "text", Text: "hi"})
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	marked, err := json.Marshal(anthropicContentBlock{Type: "text", Text: "hi", volatile: true})
	if err != nil {
		t.Fatalf("marshal volatile: %v", err)
	}
	if string(plain) != string(marked) {
		t.Fatalf("volatile changed JSON: %s vs %s", marked, plain)
	}
	if strings.Contains(strings.ToLower(string(marked)), "volatile") {
		t.Fatalf("volatile leaked into JSON: %s", marked)
	}
}
