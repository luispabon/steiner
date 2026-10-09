package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClaudeSubSessionKeySelection(t *testing.T) {
	if got := claudeSubSessionKey(ChatRequest{TransportSession: "child", ParentTransportSession: "parent"}); got != "child" {
		t.Errorf("child key = %q, want child", got)
	}
	if got := claudeSubSessionKey(ChatRequest{ParentTransportSession: "parent", AdvisorCacheProfile: true}); got != "parent|advisor" {
		t.Errorf("advisor key = %q, want parent|advisor", got)
	}
	if got := claudeSubSessionKey(ChatRequest{}); got != "default" {
		t.Errorf("default key = %q, want default", got)
	}
}

func TestClaudeSubTurnMultiTurnResolvesOpaqueToolResultAndOrdersCommit(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	conn := (*claudeSubFakeConn)(nil)
	var userSends int
	connResponder := func(line []byte) []claudeSubEvent {
		var envelope struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &envelope) != nil {
			return nil
		}
		switch envelope.Type {
		case "control_request":
			if envelope.Request.Subtype == "get_usage" {
				return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
			}
			return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
		case "user":
			userSends++
			all := claudeSubDecodeEvents(t, "turn_text.jsonl")
			if userSends == 1 {
				all = claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:9]
			}
			for _, ev := range all {
				conn.push(ev)
			}
		}
		return nil
	}
	s, err := pool.acquire(context.Background(), "multi", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn = spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = connResponder
	conn.mu.Unlock()
	pool.release(s)

	first := ChatRequest{TransportSession: "multi", Model: claudeSubTestSpec().Model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}
	var firstChunks []ChatChunk
	if err := claudeSubTurn(context.Background(), pool, first, func(chunk ChatChunk) error {
		firstChunks = append(firstChunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if len(firstChunks) == 0 || len(firstChunks[len(firstChunks)-1].Delta.ToolCalls) != 1 {
		t.Fatalf("first chunks = %+v, want tool call", firstChunks)
	}

	s, err = pool.acquire(context.Background(), "multi", spec)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending = %+v, want one opaque call", s.pending)
	}
	call := s.pending[0].Handle
	pool.release(s)
	second := ChatRequest{
		TransportSession: "multi",
		Model:            "claude-sonnet-5-5",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "read notes"},
			{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read", Arguments: map[string]any{}}}, Content: ""},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "notes body"},
			{Role: MessageRoleUser, Content: "summarize it"},
		},
	}
	if err := claudeSubTurn(context.Background(), pool, second, func(ChatChunk) error { return nil }); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	result, ok := func() (claudeSubToolResult, bool) {
		s, err := pool.acquire(context.Background(), "multi", spec)
		if err != nil {
			t.Fatalf("final reacquire: %v", err)
		}
		defer pool.release(s)
		return s.host.claimResult(call)
	}()
	if !ok || result.Text != "notes body" || result.IsError {
		t.Fatalf("opaque result = %+v, %v, want notes body", result, ok)
	}
	lines := conn.sentLines()
	modelIndex, userIndex := -1, -1
	for i, line := range lines {
		if strings.Contains(line, `"subtype":"set_model"`) {
			modelIndex = i
		}
		if strings.Contains(line, `"type":"user"`) {
			userIndex = i
		}
		if strings.Contains(line, `"subtype":"apply_flag_settings"`) {
			t.Fatalf("empty effort emitted an unverified reset control: %s", line)
		}
	}
	if modelIndex < 0 || userIndex < 0 || modelIndex > userIndex {
		t.Fatalf("sent lines = %v, want model switch before follow-up user write", lines)
	}
	s, err = pool.acquire(context.Background(), "multi", spec)
	if err != nil {
		t.Fatalf("ordering reacquire: %v", err)
	}
	if len(s.sync.entries) < 4 || s.sync.entries[2].Role != MessageRoleTool || s.sync.entries[3].Digest != claudeSubDigest(second.Messages[3]) {
		t.Fatalf("sync entries = %+v, want tool result then committed follow-up", s.sync.entries)
	}
	pool.release(s)
}

func TestClaudeSubConsumeNormalTextTurn(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "text", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl") {
		spawner.call(t, 0).Conn.push(ev)
	}
	var chunks []ChatChunk
	if err := claudeSubConsume(context.Background(), s, func(chunk ChatChunk) error { chunks = append(chunks, chunk); return nil }); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if len(chunks) == 0 || chunks[len(chunks)-1].Delta.Content != "Hello world" || !chunks[len(chunks)-1].Done {
		t.Fatalf("chunks = %+v, want final Hello world chunk", chunks)
	}
}

func TestClaudeSubConsumeStopsAtToolCall(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "tool", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	conn := spawner.call(t, 0).Conn
	for _, ev := range claudeSubDecodeEvents(t, "turn_tool_use.jsonl") {
		conn.push(ev)
	}
	var final ChatChunk
	if err := claudeSubConsume(context.Background(), s, func(chunk ChatChunk) error { final = chunk; return nil }); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !final.Done || final.FinishReason != "tool_calls" || len(final.Delta.ToolCalls) != 1 {
		t.Fatalf("final chunk = %+v, want one tool call at assistant boundary", final)
	}
	if len(s.pending) != 1 || s.pending[0].ID != "toolu_1" {
		t.Fatalf("pending = %+v, want opaque toolu_1 handle", s.pending)
	}
}

func TestClaudeSubEventUsageLimitPlainRejected(t *testing.T) {
	raw := json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","overageStatus":"rejected"}}`)
	limit := claudeSubEventUsageLimit(claudeSubEvent{Type: "rate_limit_event", Raw: raw})
	if limit == nil || limit.Provider != "claude_subscription" {
		t.Fatalf("usage limit = %+v, want Claude subscription limit", limit)
	}
	if claudeSubEventIsOverage(claudeSubEvent{Type: "rate_limit_event", Raw: raw}) {
		t.Error("plain rejected usage limit classified as overage")
	}
}

func TestClaudeSubUsageGateRejectsBeforeUserWrite(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "gate", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		var request struct {
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &request) != nil {
			return nil
		}
		if request.Request.Subtype == "get_usage" {
			return []claudeSubEvent{claudeSubSuccessEvent(request.RequestID, json.RawMessage(`{"rate_limits_available":false}`))}
		}
		return []claudeSubEvent{claudeSubSuccessEvent(request.RequestID, nil)}
	}
	conn.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := claudeSubStream(ctx, pool, ChatRequest{TransportSession: "gate", Model: claudeSubTestSpec().Model, Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got ChatChunk
	for chunk := range stream {
		got = chunk
	}
	if !strings.Contains(got.Error, "fail closed") {
		t.Fatalf("terminal error = %q, want fail-closed usage gate", got.Error)
	}
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"type":"user"`) {
			t.Fatal("usage gate wrote a user message")
		}
	}
}

func TestClaudeSubOverageObserverIsTerminal(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "overage", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	events := claudeSubDecodeEvents(t, "rate_limit_overage.jsonl")
	s.observeEvent(events[0])
	s.observeEvent(events[0])
	if !errors.Is(s.overage(), errClaudeSubPaidExtraUsage) {
		t.Fatalf("overage = %v, want stable paid-usage error", s.overage())
	}
	select {
	case <-s.overageAbort:
	case <-time.After(time.Second):
		t.Fatal("overage abort was not closed")
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("pool close after overage = %v, want nil", err)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("overage did not close the session connection")
	}
}

func TestClaudeSubQueueInterruptWinsOverBufferedEvent(t *testing.T) {
	q := newClaudeSubQueue()
	q.push(claudeSubEvent{Type: "ordinary"})
	interrupt := make(chan struct{})
	close(interrupt)
	_, ok, interrupted := q.popInterruptible(context.Background(), interrupt)
	if ok || !interrupted {
		t.Fatalf("pop = ok %v, interrupted %v, want false, true", ok, interrupted)
	}
	ev, ok := q.pop(context.Background())
	if !ok || ev.Type != "ordinary" {
		t.Fatalf("buffered event = %+v, %v, want ordinary, true", ev, ok)
	}
}

func TestClaudeSubRouteObserverPreservesFIFO(t *testing.T) {
	q := newClaudeSubQueue()
	seen := make([]string, 0, 2)
	events := make(chan claudeSubEvent, 2)
	events <- claudeSubEvent{Type: "first"}
	events <- claudeSubEvent{Type: "second"}
	close(events)
	claudeSubRoute(events, nil, nil, q, func(ev claudeSubEvent) { seen = append(seen, ev.Type) })
	if len(seen) != 2 || seen[0] != "first" || seen[1] != "second" {
		t.Fatalf("observer order = %v, want first second", seen)
	}
	for _, want := range []string{"first", "second"} {
		ev, ok := q.pop(context.Background())
		if !ok || ev.Type != want {
			t.Fatalf("queue event = %+v, %v, want %q, true", ev, ok, want)
		}
	}
}

func TestClaudeSubPendingCleanupInterruptsAndResolvesOpaqueHandle(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "pending-cleanup", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		var req struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(line, &req) != nil {
			return nil
		}
		return []claudeSubEvent{claudeSubSuccessEvent(req.RequestID, nil)}
	}
	conn.mu.Unlock()
	call := s.beginPendingCall("toolu-stale")
	if call == nil {
		t.Fatal("beginPendingCall returned nil")
	}
	if err := claudeSubFinishPending(context.Background(), s, ChatRequest{}); err != nil {
		t.Fatalf("finish pending: %v", err)
	}
	if len(s.pending) != 0 || !s.sync.interrupted {
		t.Fatalf("pending = %+v, interrupted = %v, want cleared and interrupted", s.pending, s.sync.interrupted)
	}
	result, ok := s.host.claimResult(call)
	if !ok || !result.IsError || result.Text != "tool call interrupted" {
		t.Fatalf("resolved result = %+v, %v, want interrupted error result", result, ok)
	}
}

func TestClaudeSubRouteNaturalCloseWakesConsumer(t *testing.T) {
	events := make(chan claudeSubEvent)
	q := newClaudeSubQueue()
	done := make(chan struct{})
	go func() {
		claudeSubRoute(events, nil, nil, q)
		close(done)
	}()
	close(events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("router did not stop after natural event close")
	}
	if _, ok := q.pop(context.Background()); ok {
		t.Fatal("closed router queue returned an event")
	}
}

func TestClaudeSubMalformedUsageEventsFailClosedAsOverage(t *testing.T) {
	for _, ev := range []claudeSubEvent{
		{Type: "rate_limit_event", Raw: json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":`)},
		{Type: "system", Raw: json.RawMessage(`{"type":"system","subtype":"notification","message":`)},
	} {
		if !claudeSubEventIsOverage(ev) {
			t.Errorf("event %+v was not classified as overage", ev)
		}
	}
}

func TestClaudeSubSystemNotificationOverage(t *testing.T) {
	ev := claudeSubEvent{Type: "system", Raw: json.RawMessage(`{"type":"system","subtype":"notification","message":"You're now using usage credits"}`)}
	if !claudeSubEventIsOverage(ev) {
		t.Fatal("usage-credit system notification was not classified as overage")
	}
}

func TestClaudeSubQueueCloseDrainsBeforeExit(t *testing.T) {
	q := newClaudeSubQueue()
	q.push(claudeSubEvent{Type: "buffered"})
	q.close()
	if ev, ok := q.pop(context.Background()); !ok || ev.Type != "buffered" {
		t.Fatalf("first pop = %+v, %v, want buffered, true", ev, ok)
	}
	if _, ok := q.pop(context.Background()); ok {
		t.Fatal("closed queue returned an event after draining")
	}
}
