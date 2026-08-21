package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// advisorShapedRequest builds a request resembling an advisor call: a system
// prompt, tailMessages single-block user messages standing in for the
// flattened conversation snapshot, and a final unique user message (the
// files+question+advisorUserPrompt suffix that is never reusable).
func advisorShapedRequest(tailMessages int, advisorCacheProfile bool) ChatRequest {
	messages := make([]Message, 0, tailMessages+2)
	messages = append(messages, Message{Role: MessageRoleSystem, Content: "You are the advisor"})
	for i := range tailMessages {
		messages = append(messages, Message{Role: MessageRoleUser, Content: fmt.Sprintf("tail message %d", i)})
	}
	messages = append(messages, Message{Role: MessageRoleUser, Content: "final unique question"})

	return ChatRequest{
		Model:               "claude-3-5-sonnet",
		Messages:            messages,
		AdvisorCacheProfile: advisorCacheProfile,
	}
}

// breakpointBlock identifies one content block carrying a non-nil
// CacheControl, along with its backward distance (in blocks) from the end of
// the reusable tail (the last block before the final unique message).
type breakpointBlock struct {
	distanceFromEnd int
	ttl             string
}

func collectAdvisorBreakpoints(t *testing.T, wire anthropicRequest) (systemTTL string, hasSystemBreakpoint bool, tailBreakpoints []breakpointBlock, finalMessageHasBreakpoint bool) {
	t.Helper()

	if len(wire.System) > 0 && wire.System[len(wire.System)-1].CacheControl != nil {
		hasSystemBreakpoint = true
		systemTTL = wire.System[len(wire.System)-1].CacheControl.TTL
	}

	if len(wire.Messages) == 0 {
		return systemTTL, hasSystemBreakpoint, nil, false
	}

	finalIdx := len(wire.Messages) - 1
	finalMsg := wire.Messages[finalIdx]
	if len(finalMsg.Content) > 0 && finalMsg.Content[len(finalMsg.Content)-1].CacheControl != nil {
		finalMessageHasBreakpoint = true
	}

	distanceFromEnd := 0
	for i := finalIdx - 1; i >= 0; i-- {
		for j := len(wire.Messages[i].Content) - 1; j >= 0; j-- {
			block := wire.Messages[i].Content[j]
			if block.CacheControl != nil {
				tailBreakpoints = append(tailBreakpoints, breakpointBlock{
					distanceFromEnd: distanceFromEnd,
					ttl:             block.CacheControl.TTL,
				})
			}
			distanceFromEnd++
		}
	}
	return systemTTL, hasSystemBreakpoint, tailBreakpoints, finalMessageHasBreakpoint
}

func TestAnthropicAdvisorCacheProfile_OptedIn_LongTail(t *testing.T) {
	t.Parallel()

	request := advisorShapedRequest(80, true)
	wire := anthropicRequestWire(request, "default-model", false)

	systemTTL, hasSystemBreakpoint, tailBreakpoints, finalHasBreakpoint := collectAdvisorBreakpoints(t, wire)

	if !hasSystemBreakpoint {
		t.Fatal("system block has no cache breakpoint, want one")
	}
	if systemTTL != anthropicAdvisorCacheTTL {
		t.Fatalf("system block TTL = %q, want %q", systemTTL, anthropicAdvisorCacheTTL)
	}
	if finalHasBreakpoint {
		t.Fatal("final message carries a cache breakpoint, want none when opted in")
	}

	wantDistances := []int{0, 15, 30}
	if len(tailBreakpoints) != len(wantDistances) {
		t.Fatalf("tail breakpoints = %d, want %d (distances %v)", len(tailBreakpoints), len(wantDistances), tailBreakpoints)
	}
	for i, want := range wantDistances {
		got := tailBreakpoints[i]
		if got.distanceFromEnd != want {
			t.Fatalf("tail breakpoint[%d] distance = %d, want %d", i, got.distanceFromEnd, want)
		}
		if got.ttl != anthropicAdvisorCacheTTL {
			t.Fatalf("tail breakpoint[%d] TTL = %q, want %q", i, got.ttl, anthropicAdvisorCacheTTL)
		}
	}

	total := len(tailBreakpoints)
	if hasSystemBreakpoint {
		total++
	}
	if total > 4 {
		t.Fatalf("total breakpoints = %d, want <= 4", total)
	}
}

func TestAnthropicAdvisorCacheProfile_OptedIn_ShortTail(t *testing.T) {
	t.Parallel()

	// A 10-message tail only fits one intermediate breakpoint (distance 0)
	// under the budget consumed by the system block, so this exercises the
	// "not enough blocks to need spacing" edge distinct from the long-tail case.
	request := advisorShapedRequest(10, true)
	wire := anthropicRequestWire(request, "default-model", false)

	_, hasSystemBreakpoint, tailBreakpoints, finalHasBreakpoint := collectAdvisorBreakpoints(t, wire)

	if !hasSystemBreakpoint {
		t.Fatal("system block has no cache breakpoint, want one")
	}
	if finalHasBreakpoint {
		t.Fatal("final message carries a cache breakpoint, want none when opted in")
	}
	if len(tailBreakpoints) != 1 {
		t.Fatalf("tail breakpoints = %d, want 1", len(tailBreakpoints))
	}
	if tailBreakpoints[0].distanceFromEnd != 0 {
		t.Fatalf("tail breakpoint distance = %d, want 0", tailBreakpoints[0].distanceFromEnd)
	}
}

func TestAnthropicAdvisorCacheProfile_NotOptedIn_MatchesDefaultPlacement(t *testing.T) {
	t.Parallel()

	request := advisorShapedRequest(30, false)
	wire := anthropicRequestWire(request, "default-model", false)

	if len(wire.System) != 1 || wire.System[0].CacheControl == nil {
		t.Fatal("system block missing cache breakpoint")
	}
	if wire.System[0].CacheControl.TTL != "" {
		t.Fatalf("system block TTL = %q, want empty (no extended TTL when not opted in)", wire.System[0].CacheControl.TTL)
	}

	// Default placement marks: last system block, last block of the final
	// message (Messages[lastIdx]), and last block of the second-to-last user
	// message (Messages[lastIdx-1]) — 3 breakpoints total, since there is no
	// assistant message to separate them here.
	lastIdx := len(wire.Messages) - 1
	if wire.Messages[lastIdx].Content[0].CacheControl == nil {
		t.Fatal("final message has no cache breakpoint, want the default placement to mark it")
	}
	if wire.Messages[lastIdx-1].Content[0].CacheControl == nil {
		t.Fatal("second-to-last user message has no cache breakpoint, want the default placement to mark it")
	}

	numMarked := 0
	for _, msg := range wire.Messages {
		for _, block := range msg.Content {
			if block.CacheControl != nil {
				numMarked++
				if block.CacheControl.TTL != "" {
					t.Fatalf("message block TTL = %q, want empty when not opted in", block.CacheControl.TTL)
				}
			}
		}
	}
	if numMarked != 2 {
		t.Fatalf("marked message blocks = %d, want 2", numMarked)
	}

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), `"ttl"`) {
		t.Fatalf("marshalled payload contains ttl field when not opted in: %s", data)
	}
}

func TestAnthropicAdvisorCacheProfile_WireJSON(t *testing.T) {
	t.Parallel()

	request := advisorShapedRequest(30, true)
	wire := anthropicRequestWire(request, "default-model", false)

	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	system, ok := parsed["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatal("system array missing or empty")
	}
	systemBlock, ok := system[len(system)-1].(map[string]any)
	if !ok {
		t.Fatalf("system block type = %T, want map[string]any", system[len(system)-1])
	}
	wantCacheControl := map[string]any{"type": "ephemeral", "ttl": "1h"}
	gotCacheControl, ok := systemBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("cache_control missing from system block JSON")
	}
	if gotCacheControl["type"] != wantCacheControl["type"] || gotCacheControl["ttl"] != wantCacheControl["ttl"] {
		t.Fatalf("system cache_control = %v, want %v", gotCacheControl, wantCacheControl)
	}

	messages, ok := parsed["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatal("messages array missing or empty")
	}
	// The last content block of the last tail message (index len-2, since the
	// final unique message is at len-1) should carry the same cache_control
	// shape, proving anthropicContentBlock.MarshalJSON's map path preserves TTL.
	tailMsg, ok := messages[len(messages)-2].(map[string]any)
	if !ok {
		t.Fatalf("tail message type = %T, want map[string]any", messages[len(messages)-2])
	}
	content, ok := tailMsg["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("tail message content missing or empty")
	}
	contentBlock, ok := content[len(content)-1].(map[string]any)
	if !ok {
		t.Fatalf("content block type = %T, want map[string]any", content[len(content)-1])
	}
	gotBlockCacheControl, ok := contentBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("cache_control missing from tail message content block JSON")
	}
	if gotBlockCacheControl["type"] != wantCacheControl["type"] || gotBlockCacheControl["ttl"] != wantCacheControl["ttl"] {
		t.Fatalf("tail block cache_control = %v, want %v", gotBlockCacheControl, wantCacheControl)
	}
}
