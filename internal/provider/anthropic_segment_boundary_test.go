package provider

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func segmentBoundaryRequest(messages ...Message) ChatRequest {
	return ChatRequest{Model: "claude-3-5-sonnet", Messages: messages}
}

// withoutCacheBoundary returns a copy of messages with CacheBoundary cleared.
func withoutCacheBoundary(messages []Message) []Message {
	cleared := make([]Message, len(messages))
	copy(cleared, messages)
	for i := range cleared {
		cleared[i].CacheBoundary = false
	}
	return cleared
}

func marshalAnthropicRequest(t *testing.T, request ChatRequest) []byte {
	t.Helper()
	data, err := json.Marshal(anthropicRequestWire(request, "default-model", false))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func TestAnthropicSegmentBoundary_MarksLastBoundaryMessageLastBlock(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleSystem, Content: "sys"},
		{Role: MessageRoleUser, Content: "Segment 1.", CacheBoundary: true},
		{Role: MessageRoleUser, Content: "Segment 2.", CacheBoundary: true},
		{Role: MessageRoleUser, Content: "tail"},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: "question one"},
		{Role: MessageRoleAssistant, Content: "reply"},
		{Role: MessageRoleUser, Content: "question two"},
	}

	baseline := anthropicRequestWire(segmentBoundaryRequest(withoutCacheBoundary(messages)...), "default-model", false)
	marked := anthropicRequestWire(segmentBoundaryRequest(messages...), "default-model", false)

	if got, want := markedBlocks(baseline), []string{"system", "4.0", "6.0"}; !slices.Equal(got, want) {
		t.Fatalf("baseline cache_control on %v, want the existing three placements %v", got, want)
	}
	if got, want := markedBlocks(marked), []string{"system", "1.0", "4.0", "6.0"}; !slices.Equal(got, want) {
		t.Fatalf("marked cache_control on %v, want %v", got, want)
	}
	if marked.Messages[0].Content[0].CacheControl != nil {
		t.Fatal("first segment carries a breakpoint, want only the last boundary message")
	}

	// The payload differs from the unmarked payload by exactly one
	// cache_control, on the last block of the last boundary message.
	marked.Messages[1].Content[0].CacheControl = nil
	baselineData, err := json.Marshal(baseline)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	markedData, err := json.Marshal(marked)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(markedData) != string(baselineData) {
		t.Fatalf("payload with marker differs beyond the one added cache_control:\n got %s\nwant %s", markedData, baselineData)
	}
}

// A coalesced tool-result batch packs several input messages into one wire
// message, so the boundary breakpoint must land on the tracked marked block
// rather than on the batch's last block.
func TestAnthropicSegmentBoundary_MarksExactToolResultInCoalescedBatch(t *testing.T) {
	toolCalls := []ToolCall{{ID: "toolu_a", Name: "read"}, {ID: "toolu_b", Name: "read"}}
	tests := []struct {
		name         string
		messages     []Message
		wantBaseline []string
		wantMarked   []string
	}{
		{
			name: "batch is the final message",
			messages: []Message{
				{Role: MessageRoleSystem, Content: "sys"},
				{Role: MessageRoleUser, Content: "Segment 1."},
				{Role: MessageRoleAssistant, Content: "calling", ToolCalls: toolCalls},
				{Role: MessageRoleTool, ToolCallID: "toolu_a", Content: "result a", CacheBoundary: true},
				{Role: MessageRoleTool, ToolCallID: "toolu_b", Content: "result b"},
			},
			wantBaseline: []string{"system", "0.0", "2.1"},
			wantMarked:   []string{"system", "0.0", "2.0", "2.1"},
		},
		{
			name: "batch is mid-conversation",
			messages: []Message{
				{Role: MessageRoleSystem, Content: "sys"},
				{Role: MessageRoleUser, Content: "Segment 1."},
				{Role: MessageRoleAssistant, Content: "calling", ToolCalls: toolCalls},
				{Role: MessageRoleTool, ToolCallID: "toolu_a", Content: "result a", CacheBoundary: true},
				{Role: MessageRoleTool, ToolCallID: "toolu_b", Content: "result b"},
				{Role: MessageRoleUser, Content: "tail"},
				{Role: MessageRoleAssistant, Content: "ok"},
				{Role: MessageRoleUser, Content: "question"},
			},
			wantBaseline: []string{"system", "3.0", "5.0"},
			wantMarked:   []string{"system", "2.0", "3.0", "5.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseline := anthropicRequestWire(segmentBoundaryRequest(withoutCacheBoundary(tt.messages)...), "default-model", false)
			marked := anthropicRequestWire(segmentBoundaryRequest(tt.messages...), "default-model", false)

			if got := markedBlocks(baseline); !slices.Equal(got, tt.wantBaseline) {
				t.Fatalf("baseline cache_control on %v, want existing placements %v", got, tt.wantBaseline)
			}
			if got := markedBlocks(marked); !slices.Equal(got, tt.wantMarked) {
				t.Fatalf("marked cache_control on %v, want %v", got, tt.wantMarked)
			}
			if marked.Messages[2].Content[0].ToolUseID != "toolu_a" || marked.Messages[2].Content[0].CacheControl == nil {
				t.Fatalf("boundary breakpoint not on the marked tool_result for toolu_a: %#v", marked.Messages[2].Content[0])
			}

			marked.Messages[2].Content[0].CacheControl = nil
			baselineData, err := json.Marshal(baseline)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			markedData, err := json.Marshal(marked)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(markedData) != string(baselineData) {
				t.Fatalf("payload with marker differs beyond the one added cache_control:\n got %s\nwant %s", markedData, baselineData)
			}
		})
	}
}

func TestAnthropicSegmentBoundary_DedupesWithSecondToLastUserBreakpoint(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleSystem, Content: "sys"},
		{Role: MessageRoleUser, Content: "Segment 1."},
		{Role: MessageRoleUser, Content: "Segment 2.", CacheBoundary: true},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: "question"},
	}

	wire := anthropicRequestWire(segmentBoundaryRequest(messages...), "default-model", false)

	if got, want := markedBlocks(wire), []string{"system", "1.0", "3.0"}; !slices.Equal(got, want) {
		t.Fatalf("cache_control on %v, want the existing placement without a duplicate %v", got, want)
	}
	if got := len(markedBlocks(wire)); got > anthropicMaxCacheBreakpoints {
		t.Fatalf("breakpoints = %d, want at most %d", got, anthropicMaxCacheBreakpoints)
	}
}

func TestAnthropicSegmentBoundary_VolatileBlocks(t *testing.T) {
	img := []ImageBlock{{MediaType: "image/png", Data: "img"}}

	tests := []struct {
		name       string
		messages   []Message
		wantMarked []string
	}{
		{
			name: "image after boundary keeps the boundary breakpoint",
			messages: []Message{
				{Role: MessageRoleSystem, Content: "sys"},
				{Role: MessageRoleUser, Content: "Segment 1."},
				{Role: MessageRoleUser, Content: "Segment 2.", CacheBoundary: true},
				{Role: MessageRoleUser, Content: "look", Images: img},
				{Role: MessageRoleAssistant, Content: "ack"},
				{Role: MessageRoleUser, Content: "question one"},
				{Role: MessageRoleAssistant, Content: "reply"},
				{Role: MessageRoleUser, Content: "question two"},
			},
			wantMarked: []string{"system", "1.0"},
		},
		{
			name: "image before boundary skips the boundary breakpoint",
			messages: []Message{
				{Role: MessageRoleSystem, Content: "sys"},
				{Role: MessageRoleUser, Content: "look", Images: img},
				{Role: MessageRoleUser, Content: "Segment 1."},
				{Role: MessageRoleUser, Content: "Segment 2.", CacheBoundary: true},
				{Role: MessageRoleAssistant, Content: "ack"},
				{Role: MessageRoleUser, Content: "question one"},
				{Role: MessageRoleAssistant, Content: "reply"},
				{Role: MessageRoleUser, Content: "question two"},
			},
			wantMarked: []string{"system"},
		},
		{
			name: "volatile boundary block skips the boundary breakpoint",
			messages: []Message{
				{Role: MessageRoleSystem, Content: "sys"},
				{Role: MessageRoleUser, Content: "Segment 1."},
				{Role: MessageRoleUser, Content: "Segment 2.", Images: img, CacheBoundary: true},
				{Role: MessageRoleUser, Content: "tail"},
				{Role: MessageRoleAssistant, Content: "ack"},
				{Role: MessageRoleUser, Content: "question one"},
				{Role: MessageRoleAssistant, Content: "reply"},
				{Role: MessageRoleUser, Content: "question two"},
			},
			wantMarked: []string{"system", "0.0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := anthropicRequestWire(segmentBoundaryRequest(tt.messages...), "default-model", false)
			if got := markedBlocks(wire); !slices.Equal(got, tt.wantMarked) {
				t.Fatalf("cache_control on %v, want %v", got, tt.wantMarked)
			}
		})
	}
}

func TestAnthropicSegmentBoundary_AdvisorProfileUnchanged(t *testing.T) {
	request := advisorShapedRequest(30, true)
	marked := request
	marked.Messages = append([]Message(nil), request.Messages...)
	marked.Messages[5].CacheBoundary = true

	baselineData := marshalAnthropicRequest(t, request)
	markedData := marshalAnthropicRequest(t, marked)
	if string(markedData) != string(baselineData) {
		t.Fatalf("advisor payload changed with CacheBoundary:\n got %s\nwant %s", markedData, baselineData)
	}
}

func TestAnthropicSegmentBoundary_RequestWithoutMarkerUnchanged(t *testing.T) {
	request := ChatRequest{
		Model:     "claude-3-5-sonnet",
		MaxTokens: intPtr(512),
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "system prompt"},
			{Role: MessageRoleUser, Content: "first"},
			{
				Role:    MessageRoleAssistant,
				Content: "answer",
				ToolCalls: []ToolCall{{
					ID: "toolu_1", Name: "lookup", Arguments: map[string]any{"q": "x"},
				}},
			},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "result"},
			{Role: MessageRoleUser, Content: "second"},
		},
		Tools: []ToolSpec{{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:       "lookup",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	}

	got := string(marshalAnthropicRequest(t, request))
	want := `{"max_tokens":512,"messages":[{"role":"user","content":[{"text":"first","type":"text"}]},{"role":"assistant","content":[{"text":"answer","type":"text"},{"id":"toolu_1","input":{"q":"x"},"name":"lookup","type":"tool_use"}]},{"role":"user","content":[{"cache_control":{"type":"ephemeral"},"content":"result","tool_use_id":"toolu_1","type":"tool_result"}]},{"role":"user","content":[{"cache_control":{"type":"ephemeral"},"text":"second","type":"text"}]}],"model":"claude-3-5-sonnet","system":[{"cache_control":{"type":"ephemeral"},"text":"system prompt","type":"text"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`
	if got != want {
		t.Fatalf("payload without CacheBoundary changed:\n got %s\nwant %s", got, want)
	}
}

func TestSegmentBoundary_OtherSerializersIgnoreMarker(t *testing.T) {
	base := []Message{
		{Role: MessageRoleSystem, Content: "sys"},
		{Role: MessageRoleUser, Content: "Segment 1."},
		{Role: MessageRoleUser, Content: "tail"},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: "question"},
	}
	marked := withoutCacheBoundary(base)
	marked[2].CacheBoundary = true

	baseRequest := ChatRequest{Model: "gpt-5.1", Messages: base}
	markedRequest := ChatRequest{Model: "gpt-5.1", Messages: marked}

	baseChat, err := chatRequestWire(baseRequest, "default-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	markedChat, err := chatRequestWire(markedRequest, "default-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	baseChatData, err := json.Marshal(baseChat)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	markedChatData, err := json.Marshal(markedChat)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(markedChatData) != string(baseChatData) {
		t.Fatalf("OpenAI chat payload changed with CacheBoundary:\n got %s\nwant %s", markedChatData, baseChatData)
	}

	baseResponses, err := responsesRequestWire(baseRequest, "default-model", false)
	if err != nil {
		t.Fatalf("responsesRequestWire() error = %v", err)
	}
	markedResponses, err := responsesRequestWire(markedRequest, "default-model", false)
	if err != nil {
		t.Fatalf("responsesRequestWire() error = %v", err)
	}
	baseResponsesData, err := json.Marshal(baseResponses)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	markedResponsesData, err := json.Marshal(markedResponses)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(markedResponsesData) != string(baseResponsesData) {
		t.Fatalf("Responses payload changed with CacheBoundary:\n got %s\nwant %s", markedResponsesData, baseResponsesData)
	}

	for i := range base {
		if got, want := claudeSubDigest(marked[i]), claudeSubDigest(base[i]); got != want {
			t.Fatalf("claude_subscription digest[%d] changed with CacheBoundary: got %s want %s", i, got, want)
		}
		if got, want := AppendMessageHashInput(nil, marked[i]), AppendMessageHashInput(nil, base[i]); !reflect.DeepEqual(got, want) {
			t.Fatalf("message hash input[%d] changed with CacheBoundary", i)
		}
	}

	plain, err := json.Marshal(base[2])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	flagged, err := json.Marshal(marked[2])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(flagged) != string(plain) {
		t.Fatalf("message JSON changed with CacheBoundary: %s vs %s", flagged, plain)
	}
}
