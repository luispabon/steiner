package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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
	// No tool-call usage was reported in this query, so the residual is the
	// full result.
	wantUsage := UsageStats{PromptTokens: 4452, CacheCreationInputTokens: 1689, CacheReadInputTokens: 2757, CompletionTokens: 343, TotalTokens: 4795}
	if got := chunks[len(chunks)-1].Usage; got == nil || *got != wantUsage {
		t.Errorf("final usage = %+v, want the full result %+v", got, wantUsage)
	}
}

// TestClaudeSubConsumeUsageCountsEachMessageOnce drives a tool, tool, text
// query. Each consume returns at one message, so the three Done chunks must
// carry that message's usage and sum to the result's cumulative usage.
func TestClaudeSubConsumeUsageCountsEachMessageOnce(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "usage-sequence", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	events := claudeSubDecodeEvents(t, "turn_tool_tool_text_usage.jsonl")
	conn := spawner.call(t, 0).Conn
	for _, ev := range events {
		conn.push(ev)
	}
	wantFinish := []string{"tool_calls", "tool_calls", "stop"}
	wantUsage := []UsageStats{
		{PromptTokens: 12, CacheCreationInputTokens: 10, CompletionTokens: 40, TotalTokens: 52},
		{PromptTokens: 13, CacheReadInputTokens: 10, CompletionTokens: 60, TotalTokens: 73},
		{PromptTokens: 29, CacheReadInputTokens: 25, CompletionTokens: 30, TotalTokens: 59},
	}
	var sum UsageStats
	for i := range wantFinish {
		var final ChatChunk
		if err := claudeSubConsume(context.Background(), s, func(chunk ChatChunk) error { final = chunk; return nil }); err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
		if !final.Done || final.FinishReason != wantFinish[i] || final.Usage == nil || *final.Usage != wantUsage[i] {
			t.Fatalf("consume %d final = %+v, want finish %q usage %+v", i, final, wantFinish[i], wantUsage[i])
		}
		sum.PromptTokens += final.Usage.PromptTokens
		sum.CompletionTokens += final.Usage.CompletionTokens
		sum.CacheCreationInputTokens += final.Usage.CacheCreationInputTokens
		sum.CacheReadInputTokens += final.Usage.CacheReadInputTokens
	}
	// The result reports the query's cumulative usage. Decode only the final
	// message and its result, from the last message_start onward.
	start := 0
	for i, ev := range events {
		if strings.Contains(string(ev.Raw), `"type":"message_start"`) {
			start = i
		}
	}
	results := claudeSubDecodedOfKind(claudeSubDecodeAll(t, claudeSubDecodeHooks{}, events[start:]), claudeSubDecodeResult)
	if len(results) != 1 || results[0].Result.Usage == nil {
		t.Fatalf("results = %+v, want one result with usage", results)
	}
	query := *results[0].Result.Usage
	if sum.PromptTokens != query.PromptTokens || sum.CompletionTokens != query.CompletionTokens ||
		sum.CacheCreationInputTokens != query.CacheCreationInputTokens || sum.CacheReadInputTokens != query.CacheReadInputTokens {
		t.Errorf("summed chunk usage %+v != query result usage %+v; each message must count once", sum, query)
	}
}

// claudeSubScript answers control requests and, on each user line, pushes the
// next scripted batch of stream events. setModelErr makes set_model fail.
// usage replaces the get_usage reply and usageErr makes get_usage fail.
type claudeSubScript struct {
	batches     [][]claudeSubEvent
	users       int
	setModelErr bool
	usage       json.RawMessage
	usageErr    string
}

func (sc *claudeSubScript) respond(line []byte) []claudeSubEvent {
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
		switch envelope.Request.Subtype {
		case "get_usage":
			if sc.usageErr != "" {
				return []claudeSubEvent{claudeSubErrorEvent(envelope.RequestID, sc.usageErr)}
			}
			if sc.usage != nil {
				return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, sc.usage)}
			}
			return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
		case "set_model":
			if sc.setModelErr {
				return []claudeSubEvent{claudeSubErrorEvent(envelope.RequestID, "model unavailable")}
			}
		}
		return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
	case "user":
		var batch []claudeSubEvent
		if sc.users < len(sc.batches) {
			batch = sc.batches[sc.users]
		}
		sc.users++
		return batch
	}
	return nil
}

// claudeSubScriptedPool returns a pool whose session under key answers from sc.
func claudeSubScriptedPool(t *testing.T, key string, sc *claudeSubScript) (*ClaudeSubscriptionPool, claudeSubStartSpec) {
	t.Helper()
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), key, spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = sc.respond
	conn.mu.Unlock()
	pool.release(s)
	return pool, spec
}

// claudeSubToolResultMessages is the history after the first turn's tool call
// toolu_a, with its result delivered.
func claudeSubToolResultMessages() []Message {
	return []Message{
		{Role: MessageRoleUser, Content: "read notes"},
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_a", Name: "read", Arguments: map[string]any{}}}},
		{Role: MessageRoleTool, ToolCallID: "toolu_a", Content: "notes body"},
	}
}

// TestClaudeSubTurnPreConsumeFailureRetryCountsUsageOnce fails the tool-result
// continuation before consume, then retries it. The tool turn's usage must still
// be subtracted from the query result, so the two chunks sum to the result once.
func TestClaudeSubTurnPreConsumeFailureRetryCountsUsageOnce(t *testing.T) {
	sc := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_tool_text_extra_usage.jsonl")}}
	pool, _ := claudeSubScriptedPool(t, "retry", sc)
	model := claudeSubTestSpec().Model
	noop := func(ChatChunk) error { return nil }
	var toolChunk ChatChunk
	if err := claudeSubTurn(context.Background(), pool, ChatRequest{TransportSession: "retry", Model: model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}, func(chunk ChatChunk) error {
		if chunk.Done {
			toolChunk = chunk
		}
		return nil
	}); err != nil {
		t.Fatalf("tool turn: %v", err)
	}
	wantTool := UsageStats{PromptTokens: 12, CacheCreationInputTokens: 10, CompletionTokens: 40, TotalTokens: 52}
	if toolChunk.FinishReason != "tool_calls" || toolChunk.Usage == nil || *toolChunk.Usage != wantTool {
		t.Fatalf("tool chunk = %+v, want tool_calls usage %+v", toolChunk, wantTool)
	}
	// A model switch fails in set_model, before the tool result is delivered.
	sc.setModelErr = true
	continuation := ChatRequest{TransportSession: "retry", Model: "claude-sonnet-5-5", Messages: claudeSubToolResultMessages()}
	if err := claudeSubTurn(context.Background(), pool, continuation, noop); err == nil {
		t.Fatal("continuation with failing set_model returned nil error")
	}
	sc.setModelErr = false
	var final ChatChunk
	if err := claudeSubTurn(context.Background(), pool, continuation, func(chunk ChatChunk) error {
		if chunk.Done {
			final = chunk
		}
		return nil
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !final.Done || final.Usage == nil {
		t.Fatalf("retry final = %+v, want a Done chunk with usage", final)
	}
	total := UsageStats{}
	claudeSubAddUsage(&total, toolChunk.Usage)
	claudeSubAddUsage(&total, final.Usage)
	wantTotal := UsageStats{PromptTokens: 62, CacheCreationInputTokens: 14, CacheReadInputTokens: 40, CompletionTokens: 75, TotalTokens: 137}
	if total != wantTotal {
		t.Errorf("summed usage = %+v, want query result %+v counted once", total, wantTotal)
	}
}

// TestClaudeSubTurnErrorResultResetsQueryUsage checks that a consume error ends
// the query, so no usage from its tool turn carries into the session.
func TestClaudeSubTurnErrorResultResetsQueryUsage(t *testing.T) {
	events := claudeSubDecodeEvents(t, "turn_tool_text_extra_usage.jsonl")[:8]
	events = append(events, claudeSubEvent{Type: "result", Subtype: "error_during_execution", Raw: json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`)})
	sc := &claudeSubScript{batches: [][]claudeSubEvent{events}}
	pool, spec := claudeSubScriptedPool(t, "error-reset", sc)
	noop := func(ChatChunk) error { return nil }
	if err := claudeSubTurn(context.Background(), pool, ChatRequest{TransportSession: "error-reset", Model: claudeSubTestSpec().Model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}, noop); err != nil {
		t.Fatalf("tool turn: %v", err)
	}
	if err := claudeSubTurn(context.Background(), pool, ChatRequest{TransportSession: "error-reset", Model: claudeSubTestSpec().Model, Messages: claudeSubToolResultMessages()}, noop); err == nil {
		t.Fatal("error result returned nil error")
	}
	s, err := pool.acquire(context.Background(), "error-reset", spec)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	defer pool.release(s)
	if s.queryUsage != (UsageStats{}) {
		t.Errorf("query usage after error result = %+v, want reset", s.queryUsage)
	}
}

// TestClaudeSubTurnInterruptResetsQueryUsage checks that interrupting an
// abandoned tool call ends its query, so the next query's residual is its own.
func TestClaudeSubTurnInterruptResetsQueryUsage(t *testing.T) {
	sc := &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubDecodeEvents(t, "turn_tool_text_extra_usage.jsonl")[:8],
		claudeSubDecodeEvents(t, "turn_text.jsonl"),
	}}
	pool, _ := claudeSubScriptedPool(t, "interrupt", sc)
	model := claudeSubTestSpec().Model
	noop := func(ChatChunk) error { return nil }
	if err := claudeSubTurn(context.Background(), pool, ChatRequest{TransportSession: "interrupt", Model: model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}, noop); err != nil {
		t.Fatalf("tool turn: %v", err)
	}
	// The follow-up user message abandons toolu_a, so the CLI is interrupted.
	abandoned := []Message{
		{Role: MessageRoleUser, Content: "read notes"},
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_a", Name: "read", Arguments: map[string]any{}}}},
		{Role: MessageRoleUser, Content: "never mind"},
	}
	var final ChatChunk
	if err := claudeSubTurn(context.Background(), pool, ChatRequest{TransportSession: "interrupt", Model: model, Messages: abandoned}, func(chunk ChatChunk) error {
		if chunk.Done {
			final = chunk
		}
		return nil
	}); err != nil {
		t.Fatalf("follow-up turn: %v", err)
	}
	// Without the reset, the follow-up would subtract the abandoned tool usage.
	want := UsageStats{PromptTokens: 4452, CacheCreationInputTokens: 1689, CacheReadInputTokens: 2757, CompletionTokens: 343, TotalTokens: 4795}
	if final.Usage == nil || *final.Usage != want {
		t.Errorf("follow-up usage = %+v, want full result %+v", final.Usage, want)
	}
}

// TestClaudeSubConsumeTextUsageFallsBackToMessageUsage checks that a text chunk
// without a result usage reports its own message's usage.
func TestClaudeSubConsumeTextUsageFallsBackToMessageUsage(t *testing.T) {
	events := claudeSubDecodeEvents(t, "turn_tool_text_extra_usage.jsonl")
	events[len(events)-1].Raw = json.RawMessage(`{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"All done.","stop_reason":"end_turn","session_id":"sess-1","permission_denials":[]}`)
	sc := &claudeSubScript{batches: [][]claudeSubEvent{events}}
	pool, spec := claudeSubScriptedPool(t, "fallback", sc)
	s, err := pool.acquire(context.Background(), "fallback", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	for _, ev := range events {
		s.queue.push(ev)
	}
	if final := claudeSubConsumeFinal(t, s); final.FinishReason != "tool_calls" {
		t.Fatalf("tool chunk = %+v, want tool_calls", final)
	}
	final := claudeSubConsumeFinal(t, s)
	want := UsageStats{PromptTokens: 29, CacheReadInputTokens: 25, CompletionTokens: 30, TotalTokens: 59}
	if !final.Done || final.Usage == nil || *final.Usage != want {
		t.Errorf("text usage = %+v, want message usage %+v", final.Usage, want)
	}
}

// claudeSubConsumeFinal runs one claudeSubConsume call and returns the last
// chunk it emitted, which is the Done chunk that ends the call.
func claudeSubConsumeFinal(t *testing.T, s *claudeSubSession) ChatChunk {
	t.Helper()
	var final ChatChunk
	if err := claudeSubConsume(context.Background(), s, func(chunk ChatChunk) error { final = chunk; return nil }); err != nil {
		t.Fatalf("consume: %v", err)
	}
	return final
}

// TestClaudeSubConsumeTextUsageResidual checks the final text chunk reports the
// result minus the tool-call usage already reported in the same query.
func TestClaudeSubConsumeTextUsageResidual(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    UsageStats
	}{
		{
			name:    "result equals message sum",
			fixture: "turn_tool_tool_text_usage.jsonl",
			want:    UsageStats{PromptTokens: 29, CacheReadInputTokens: 25, CompletionTokens: 30, TotalTokens: 59},
		},
		{
			// The result carries accounting beyond the messages; the residual keeps it.
			name:    "result exceeds message sum including cache",
			fixture: "turn_tool_text_extra_usage.jsonl",
			want:    UsageStats{PromptTokens: 50, CacheCreationInputTokens: 4, CacheReadInputTokens: 40, CompletionTokens: 35, TotalTokens: 85},
		},
		{
			// The text message has no usage and nothing was reported: the full result.
			name:    "nil message usage yields full result",
			fixture: "turn_text_nil_usage.jsonl",
			want:    UsageStats{PromptTokens: 506, CacheReadInputTokens: 500, CompletionTokens: 50, TotalTokens: 556},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
			spec := claudeSubTestSpec()
			spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
			s, err := pool.acquire(context.Background(), "residual", spec)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			defer pool.release(s)
			conn := spawner.call(t, 0).Conn
			for _, ev := range claudeSubDecodeEvents(t, tc.fixture) {
				conn.push(ev)
			}
			final := claudeSubConsumeFinal(t, s)
			for final.FinishReason == "tool_calls" {
				final = claudeSubConsumeFinal(t, s)
			}
			if !final.Done || final.Usage == nil || *final.Usage != tc.want {
				t.Errorf("text usage = %+v, want %+v", final.Usage, tc.want)
			}
		})
	}
}

// TestClaudeSubConsumeQueryUsageResetsBetweenQueries checks that a finished
// query leaves no reported usage for the next query on the same session.
func TestClaudeSubConsumeQueryUsageResetsBetweenQueries(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "query-reset", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	conn := spawner.call(t, 0).Conn
	for _, name := range []string{"turn_tool_tool_text_usage.jsonl", "turn_text.jsonl"} {
		for _, ev := range claudeSubDecodeEvents(t, name) {
			conn.push(ev)
		}
	}
	// Query one: tool, tool, then the text that its result closes.
	for range 2 {
		if final := claudeSubConsumeFinal(t, s); final.FinishReason != "tool_calls" {
			t.Fatalf("query one tool chunk = %+v, want tool_calls", final)
		}
	}
	if final := claudeSubConsumeFinal(t, s); final.FinishReason != "stop" {
		t.Fatalf("query one text chunk = %+v, want stop", final)
	}
	if s.queryUsage != (UsageStats{}) {
		t.Fatalf("query usage after result = %+v, want reset", s.queryUsage)
	}
	// Query two reported no tool-call usage, so its residual is its full result.
	final := claudeSubConsumeFinal(t, s)
	want := UsageStats{PromptTokens: 4452, CacheCreationInputTokens: 1689, CacheReadInputTokens: 2757, CompletionTokens: 343, TotalTokens: 4795}
	if !final.Done || final.Usage == nil || *final.Usage != want {
		t.Errorf("query two usage = %+v, want full result %+v; query one usage leaked", final.Usage, want)
	}
}

func TestClaudeSubResidualUsageClampsPerField(t *testing.T) {
	if got := claudeSubResidualUsage(nil, UsageStats{PromptTokens: 1}); got != nil {
		t.Errorf("residual of nil result = %+v, want nil", got)
	}
	result := &UsageStats{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13, CacheReadInputTokens: 5}
	reported := UsageStats{PromptTokens: 12, CompletionTokens: 1, TotalTokens: 13, CacheReadInputTokens: 2}
	want := UsageStats{CompletionTokens: 2, CacheReadInputTokens: 3}
	if got := claudeSubResidualUsage(result, reported); got == nil || *got != want {
		t.Errorf("residual = %+v, want %+v (prompt and total clamp to zero per field)", got, want)
	}
}

func TestClaudeSubConsumeStopsAtStreamEchoedToolCall(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "stream-echo-tool", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	conn := spawner.call(t, 0).Conn
	for _, ev := range claudeSubDecodeEvents(t, "turn_stream_echo_blocks.jsonl") {
		conn.push(ev)
	}
	var chunks []ChatChunk
	if err := claudeSubConsume(context.Background(), s, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("consume: %v", err)
	}
	var doneToolCalls int
	for _, chunk := range chunks {
		if chunk.Done && chunk.FinishReason == "tool_calls" {
			doneToolCalls++
			if len(chunk.Delta.ToolCalls) != 1 {
				t.Fatalf("tool-call chunk = %+v, want one tool call", chunk)
			}
		}
	}
	if doneToolCalls != 1 {
		t.Fatalf("chunks = %+v, want exactly one Done tool_calls chunk", chunks)
	}
	if len(s.pending) != 1 || s.pending[0].ID != "toolu_stream_echo" {
		t.Fatalf("pending = %+v, want opaque streamed-echo tool handle", s.pending)
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

// claudeSubRetryFault arms one failure for the first attempt of
// TestClaudeSubTurnFailureThenRetry.
type claudeSubRetryFault struct {
	modelErr bool // set_model returns an error
	usageErr bool // get_usage returns an error
	refuse   bool // get_usage reports credits not confirmed off
	writeErr bool // the user line write after the usage check fails
}

// TestClaudeSubTurnFailureThenRetry proves a turn that fails after planning
// leaves the sync record unchanged, except for tool results already resolved
// into the CLI. Retrying the same request must not error on a delivered result,
// resolve it again, or send the user content twice.
func TestClaudeSubTurnFailureThenRetry(t *testing.T) {
	tests := []struct {
		name  string
		model string // empty uses the session model
		fault claudeSubRetryFault
		// delivered is true when the tool result is resolved before the failure.
		delivered bool
	}{
		{name: "set model fails", model: "claude-sonnet-5-5", fault: claudeSubRetryFault{modelErr: true}},
		{name: "usage lookup fails", fault: claudeSubRetryFault{usageErr: true}},
		{name: "usage gate refuses", fault: claudeSubRetryFault{refuse: true}},
		{name: "user write fails after tool result", fault: claudeSubRetryFault{writeErr: true}, delivered: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
			spec := claudeSubTestSpec()
			spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
			ctx := context.Background()
			var fault claudeSubRetryFault
			var conn *claudeSubFakeConn
			userSends := 0
			responder := func(line []byte) []claudeSubEvent {
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
					switch envelope.Request.Subtype {
					case "set_model":
						if fault.modelErr {
							return []claudeSubEvent{claudeSubErrorEvent(envelope.RequestID, "model unavailable")}
						}
					case "get_usage":
						if fault.usageErr {
							return []claudeSubEvent{claudeSubErrorEvent(envelope.RequestID, "usage unavailable")}
						}
						if fault.refuse {
							return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":false}`))}
						}
						if fault.writeErr {
							// Fails the next send, which is the user line after the usage check.
							conn.mu.Lock()
							conn.sendErr = errors.New("write failed")
							conn.mu.Unlock()
						}
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
			s, err := pool.acquire(ctx, "retry", spec)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			conn = spawner.call(t, 0).Conn
			conn.mu.Lock()
			conn.responder = responder
			conn.mu.Unlock()
			pool.release(s)

			first := ChatRequest{TransportSession: "retry", Model: spec.Model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}
			if err := claudeSubTurn(ctx, pool, first, func(ChatChunk) error { return nil }); err != nil {
				t.Fatalf("first turn: %v", err)
			}
			s, err = pool.acquire(ctx, "retry", spec)
			if err != nil {
				t.Fatalf("reacquire: %v", err)
			}
			if len(s.pending) != 1 {
				t.Fatalf("pending = %+v, want one opaque call", s.pending)
			}
			call := s.pending[0].Handle
			before := append([]claudeSubEntry(nil), s.sync.entries...)
			pool.release(s)

			model := tc.model
			if model == "" {
				model = spec.Model
			}
			second := ChatRequest{
				TransportSession: "retry",
				Model:            model,
				Messages: []Message{
					{Role: MessageRoleUser, Content: "read notes"},
					{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read", Arguments: map[string]any{}}}, Content: ""},
					{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "notes body"},
					{Role: MessageRoleUser, Content: "summarize it"},
				},
			}
			fault = tc.fault
			if err := claudeSubTurn(ctx, pool, second, func(ChatChunk) error { return nil }); err == nil {
				t.Fatal("failing turn returned nil error")
			}
			fault = claudeSubRetryFault{}
			conn.mu.Lock()
			conn.sendErr = nil
			conn.mu.Unlock()

			wantFailed := before
			if tc.delivered {
				wantFailed = append(append([]claudeSubEntry(nil), before...), claudeSubEntryFromMessage(second.Messages[2]))
			}
			s, err = pool.acquire(ctx, "retry", spec)
			if err != nil {
				t.Fatalf("acquire after failure: %v", err)
			}
			if !reflect.DeepEqual(s.sync.entries, wantFailed) {
				t.Errorf("sync entries after failure = %+v, want %+v", s.sync.entries, wantFailed)
			}
			wantPending := 1
			if tc.delivered {
				wantPending = 0
			}
			if len(s.pending) != wantPending {
				t.Errorf("pending after failure = %+v, want %d calls", s.pending, wantPending)
			}
			pool.release(s)

			if err := claudeSubTurn(ctx, pool, second, func(ChatChunk) error { return nil }); err != nil {
				t.Fatalf("retry turn: %v", err)
			}
			s, err = pool.acquire(ctx, "retry", spec)
			if err != nil {
				t.Fatalf("final acquire: %v", err)
			}
			defer pool.release(s)
			wantEntries := append(append([]claudeSubEntry(nil), before...), claudeSubEntryFromMessage(second.Messages[2]), claudeSubEntryFromMessage(second.Messages[3]))
			// The turn's assistant reply is committed after the follow-up user.
			if got := s.sync.entries; len(got) != len(wantEntries)+1 || !reflect.DeepEqual(got[:len(wantEntries)], wantEntries) {
				t.Errorf("sync entries after retry = %+v, want %+v plus the assistant reply", got, wantEntries)
			}
			if result, ok := s.host.claimResult(call); !ok || result.Text != "notes body" || result.IsError {
				t.Errorf("opaque result = %+v, %v, want notes body", result, ok)
			}
			sent := 0
			for _, line := range conn.sentLines() {
				if strings.Contains(line, "summarize it") {
					sent++
				}
			}
			if sent != 1 {
				t.Errorf("follow-up user line sent %d times, want 1", sent)
			}
		})
	}
}

// TestClaudeSubDecodeErrorRetiresProcess proves a decode stream error retires
// the process in normal and advisor turns. The failed process keeps the tail of
// its turn queued, so reusing it would feed stale events to the next request.
func TestClaudeSubDecodeErrorRetiresProcess(t *testing.T) {
	turn := claudeSubDecodeEvents(t, "turn_text.jsonl")
	// system init, message_start, the text block up to "Hello", a duplicate
	// message_start (decode error), then message_stop, which stays queued as
	// the stale tail. A reused process answers the next user line with a batch
	// that starts at message_start, so the stale message_stop comes first.
	broken := append([]claudeSubEvent{}, turn[:8]...)
	broken = append(broken, turn[1], turn[11])
	advisorMessages := []Message{claudeSubUserText("read notes"), {Role: MessageRoleAssistant, Content: "Hello world"}, claudeSubUserText("is it right?")}
	tests := []struct {
		name string
		req  ChatRequest
	}{
		{name: "normal", req: ChatRequest{TransportSession: "decode", Messages: []Message{claudeSubUserText("read notes")}}},
		{name: "advisor", req: ChatRequest{TransportSession: "decode-advisor", ParentTransportSession: "parent", AdvisorCacheProfile: true, Messages: advisorMessages}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, spawner := claudeSubPoolWithScripts(t,
				&claudeSubScript{batches: [][]claudeSubEvent{broken, turn[1:]}},
				claudeSubTextTurns(t, 1))
			req := tc.req
			req.Model = claudeSubTestSpec().Model
			var streamed []ChatChunk
			err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
				streamed = append(streamed, chunk)
				return nil
			})
			if !errors.Is(err, errClaudeSubDecodeStream) {
				t.Fatalf("first turn error = %v, want decode stream error", err)
			}
			emittedText := false
			for _, chunk := range streamed {
				emittedText = emittedText || strings.Contains(chunk.Delta.Content, "Hello")
			}
			if !emittedText {
				t.Error("first turn emitted no text before the decode error, want the output path")
			}
			if !spawner.call(t, 0).Conn.isClosed() {
				t.Error("failed process was not closed")
			}
			// The second request adds a user line so the reused process, if any,
			// writes a line and reads its next batch.
			second := req
			second.Messages = append(append([]Message{}, req.Messages...), claudeSubUserText("more"))
			final := claudeSubRunTurn(t, pool, second)
			if final.Delta.Content != "Hello world" {
				t.Errorf("second turn text = %q, want Hello world", final.Delta.Content)
			}
			if n := spawner.callCount(); n != 2 {
				t.Errorf("spawn count = %d, want 2 (fresh process after decode error)", n)
			}
		})
	}
}

// TestClaudeSubDecodeErrorOnResumeDoesNotReseed proves a decode stream error on
// a resumed process with no output retires the process without an inline
// reseed. The next request starts the fresh process instead.
func TestClaudeSubDecodeErrorOnResumeDoesNotReseed(t *testing.T) {
	turn := claudeSubDecodeEvents(t, "turn_text.jsonl")
	// system init, then a text delta with no open content block (decode error
	// before any chunk is emitted), then the rest of the turn.
	broken := append([]claudeSubEvent{turn[0], turn[7]}, turn[8:]...)
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{broken}}, claudeSubTextTurns(t, 1))
	msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	req := ChatRequest{TransportSession: "parent", Model: claudeSubTestSpec().Model, Messages: msgs, BackendState: claudeSubEncodeState(t, st)}
	err := claudeSubTurn(context.Background(), pool, req, func(ChatChunk) error { return nil })
	if !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("turn error = %v, want decode stream error", err)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 0).Args, "--resume"); resume != st.CLISessionID {
		t.Errorf("spawn --resume = %q, want %q (resumed process)", resume, st.CLISessionID)
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (no inline reseed after decode error)", n)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("resumed process was not closed")
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
	stream, err := claudeSubStream(ctx, pool, ChatRequest{TransportSession: "gate", Model: claudeSubTestSpec().Model, Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}}, nil)
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

func TestClaudeSubUsageGateRefusesToolResultsWithUserBeforeResolve(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	ctx := context.Background()
	refuseUsage := false
	var conn *claudeSubFakeConn
	responder := func(line []byte) []claudeSubEvent {
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
				if refuseUsage {
					return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":false}`))}
				}
				return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
			}
			return []claudeSubEvent{claudeSubSuccessEvent(envelope.RequestID, nil)}
		case "user":
			for _, ev := range claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:9] {
				conn.push(ev)
			}
		}
		return nil
	}
	s, err := pool.acquire(ctx, "b1", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn = spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = responder
	conn.mu.Unlock()
	pool.release(s)

	if err := claudeSubTurn(ctx, pool, ChatRequest{TransportSession: "b1", Model: spec.Model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}, func(ChatChunk) error { return nil }); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	s, err = pool.acquire(ctx, "b1", spec)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending = %+v, want one opaque call", s.pending)
	}
	call := s.pending[0].Handle
	entries := len(s.sync.entries)
	pool.release(s)
	countLines := func(needle string) int {
		n := 0
		for _, line := range conn.sentLines() {
			if strings.Contains(line, needle) {
				n++
			}
		}
		return n
	}
	userWrites := countLines(`"type":"user"`)

	refuseUsage = true
	second := ChatRequest{
		TransportSession: "b1",
		Model:            "claude-sonnet-5-5",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "read notes"},
			{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read", Arguments: map[string]any{}}}, Content: ""},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "notes body"},
			{Role: MessageRoleUser, Content: "summarize it"},
		},
	}
	if err := claudeSubTurn(ctx, pool, second, func(ChatChunk) error { return nil }); err == nil || err.Error() != claudeSubFailClosedWant {
		t.Fatalf("second turn error = %v, want fail-closed usage gate", err)
	}

	s, err = pool.acquire(ctx, "b1", spec)
	if err != nil {
		t.Fatalf("final reacquire: %v", err)
	}
	defer pool.release(s)
	if len(s.pending) != 1 || s.pending[0].ID != "toolu_1" {
		t.Fatalf("pending = %+v, want toolu_1 still pending", s.pending)
	}
	if result, ok := s.host.claimResult(call); ok {
		t.Errorf("refused tool result was resolved: %+v", result)
	}
	if got := len(s.sync.entries); got != entries {
		t.Errorf("sync entries = %d, want %d: refused turn committed", got, entries)
	}
	if got := countLines(`"type":"user"`); got != userWrites {
		t.Errorf("user writes = %d, want %d: refused turn wrote user content", got, userWrites)
	}
}

func TestClaudeSubDuplicateSuffixToolResultResolvesNothing(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	ctx := context.Background()
	var conn *claudeSubFakeConn
	responder := func(line []byte) []claudeSubEvent {
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
			for _, ev := range claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:9] {
				conn.push(ev)
			}
		}
		return nil
	}
	s, err := pool.acquire(ctx, "dup", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn = spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = responder
	conn.mu.Unlock()
	pool.release(s)

	if err := claudeSubTurn(ctx, pool, ChatRequest{TransportSession: "dup", Model: spec.Model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}, func(ChatChunk) error { return nil }); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	s, err = pool.acquire(ctx, "dup", spec)
	if err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending = %+v, want one opaque call", s.pending)
	}
	call := s.pending[0].Handle
	pool.release(s)
	countLines := func(needle string) int {
		n := 0
		for _, line := range conn.sentLines() {
			if strings.Contains(line, needle) {
				n++
			}
		}
		return n
	}
	userWrites := countLines(`"type":"user"`)

	duplicate := ChatRequest{
		TransportSession: "dup",
		Model:            spec.Model,
		Messages: []Message{
			{Role: MessageRoleUser, Content: "read notes"},
			{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read", Arguments: map[string]any{}}}, Content: ""},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "first"},
			{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "second"},
		},
	}
	// The duplicate result cannot be appended to the live process. That process
	// is retired and the whole history is reseeded on a fresh one, which answers
	// the control requests and the transcript like the first process.
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		sc := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")}}
		c.mu.Lock()
		c.responder = sc.respond
		c.mu.Unlock()
	}
	if err := claudeSubTurn(ctx, pool, duplicate, func(ChatChunk) error { return nil }); err != nil {
		t.Fatalf("duplicate turn: %v", err)
	}
	if result, ok := s.host.claimResult(call); ok {
		t.Errorf("duplicate tool results resolved the retired call: %+v", result)
	}
	if got := countLines(`"type":"user"`); got != userWrites {
		t.Errorf("user writes on retired process = %d, want %d: duplicate turn appended to it", got, userWrites)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (retired process replaced)", n)
	}
	var reseed []string
	for _, line := range spawner.call(t, 1).Conn.sentLines() {
		if strings.Contains(line, `"type":"user"`) {
			reseed = append(reseed, line)
		}
	}
	if len(reseed) != 1 || !strings.Contains(reseed[0], "first") || !strings.Contains(reseed[0], "second") {
		t.Errorf("reseed user lines = %q, want one transcript carrying both tool results", reseed)
	}
}

func TestClaudeSubFreshProcessSendsGetUsageBeforeUserWrite(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()
	spec := claudeSubTestSpec()
	s, err := pool.acquire(ctx, "fresh", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
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
			for _, ev := range claudeSubDecodeEvents(t, "turn_text.jsonl") {
				conn.push(ev)
			}
		}
		return nil
	}
	conn.mu.Unlock()
	pool.release(s)

	if err := claudeSubTurn(ctx, pool, ChatRequest{TransportSession: "fresh", Model: spec.Model, Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}}, func(ChatChunk) error { return nil }); err != nil {
		t.Fatalf("turn: %v", err)
	}
	usageIndex, userIndex := -1, -1
	for i, line := range conn.sentLines() {
		if usageIndex < 0 && strings.Contains(line, `"subtype":"get_usage"`) {
			usageIndex = i
		}
		if userIndex < 0 && strings.Contains(line, `"type":"user"`) {
			userIndex = i
		}
	}
	if usageIndex < 0 || userIndex < 0 || usageIndex > userIndex {
		t.Fatalf("sent lines = %v, want get_usage before the first user write", conn.sentLines())
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

func TestClaudeSubTurnFailureIncludesResultText(t *testing.T) {
	cases := []struct {
		name    string
		subtype string
		text    string
		want    string
	}{
		{
			name:    "result text is quoted",
			subtype: "error_max_turns",
			text:    "stopped early",
			want:    "claude_subscription turn failed: error_max_turns: stopped early",
		},
		{
			name:    "empty text keeps the subtype only",
			subtype: "error_during_execution",
			want:    "claude_subscription turn failed: error_during_execution",
		},
		{
			name:    "long text is cut to the limit",
			subtype: "error_max_turns",
			text:    strings.Repeat("a", 400),
			want:    "claude_subscription turn failed: error_max_turns: " + strings.Repeat("a", 300) + "...",
		},
		{
			name:    "cut backs up to a rune boundary",
			subtype: "error_max_turns",
			text:    "a" + strings.Repeat("\u00e9", 200),
			want:    "claude_subscription turn failed: error_max_turns: a" + strings.Repeat("\u00e9", 149) + "...",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := claudeSubTurnFailure(&claudeSubResult{Subtype: tc.subtype, Text: tc.text, IsError: true})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestClaudeSubTurnFinalChunkBackendState checks the backend state on the Done
// chunk of a parent tool turn and that an advisor turn carries none.
func TestClaudeSubTurnFinalChunkBackendState(t *testing.T) {
	model := claudeSubTestSpec().Model
	t.Run("parent tool turn carries state", func(t *testing.T) {
		sc := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_tool_use.jsonl")}}
		pool, _ := claudeSubScriptedPool(t, "state-parent", sc)
		req := ChatRequest{TransportSession: "state-parent", Model: model, Messages: []Message{{Role: MessageRoleUser, Content: "read notes"}}}
		var final ChatChunk
		if err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
			if chunk.Done {
				final = chunk
			}
			return nil
		}); err != nil {
			t.Fatalf("turn: %v", err)
		}
		if !final.Done || final.FinishReason != "tool_calls" || len(final.Delta.ToolCalls) != 1 {
			t.Fatalf("final chunk = %+v, want Done tool_calls chunk with one tool call", final)
		}
		if final.BackendState == nil {
			t.Fatal("parent tool turn final chunk has nil backend state")
		}
		var state claudeSubBackendState
		if err := json.Unmarshal(final.BackendState, &state); err != nil {
			t.Fatalf("decode backend state: %v", err)
		}
		if state.SyncChainDigest == "" || state.FinalMessageDigest == "" || state.SyncPrefixDigest == "" {
			t.Errorf("state digests = %q, %q, %q, want all set", state.SyncChainDigest, state.FinalMessageDigest, state.SyncPrefixDigest)
		}
		// The CLI session ID comes from the system/init envelope, and the
		// fingerprint from the prompt and tools of the first normal call.
		fingerprint, err := claudeSubFingerprint(claudeSubSystemPrompt(req.Messages), nil)
		if err != nil {
			t.Fatalf("claudeSubFingerprint: %v", err)
		}
		state.SyncChainDigest, state.FinalMessageDigest, state.SyncPrefixDigest = "", "", ""
		want := claudeSubBackendState{
			Provider:               "claude_subscription",
			Version:                1,
			CLISessionID:           "sess-1",
			TransportSession:       "state-parent",
			SyncEntries:            2,
			FinalUUID:              "u7",
			PromptToolsFingerprint: fingerprint,
		}
		if state != want {
			t.Errorf("backend state = %+v, want %+v", state, want)
		}
	})
	t.Run("advisor turn carries no state", func(t *testing.T) {
		sc := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")}}
		pool, _ := claudeSubScriptedPool(t, "state-advisor|advisor", sc)
		req := ChatRequest{ParentTransportSession: "state-advisor", AdvisorCacheProfile: true, Model: model, Messages: []Message{{Role: MessageRoleUser, Content: "is this right?"}}}
		var final ChatChunk
		if err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
			if chunk.Done {
				final = chunk
			}
			return nil
		}); err != nil {
			t.Fatalf("advisor turn: %v", err)
		}
		if !final.Done || final.BackendState != nil {
			t.Errorf("advisor final chunk = %+v, want Done with nil backend state", final)
		}
	})
}

func TestClaudeSubConsumeErrorResultReportsText(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "error-result", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)
	spawner.call(t, 0).Conn.push(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"error_max_turns","is_error":true,"result":"stopped early"}`)})
	err = claudeSubConsume(context.Background(), s, func(ChatChunk) error { return nil })
	const want = "claude_subscription turn failed: error_max_turns: stopped early"
	if err == nil || err.Error() != want {
		t.Fatalf("consume error = %v, want %q", err, want)
	}
}

// claudeSubPoolWithScripts returns a pool whose i-th spawned process answers
// user lines from scripts[i]. Processes past the end of scripts get no responder.
func claudeSubPoolWithScripts(t *testing.T, scripts ...*claudeSubScript) (*ClaudeSubscriptionPool, *claudeSubTestSpawner) {
	t.Helper()
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		if i := len(spawner.calls); i < len(scripts) {
			c.mu.Lock()
			c.responder = scripts[i].respond
			c.mu.Unlock()
		}
	}
	return pool, spawner
}

// claudeSubTextTurns returns a script that answers n user lines with the text
// turn fixture.
func claudeSubTextTurns(t *testing.T, n int) *claudeSubScript {
	t.Helper()
	sc := &claudeSubScript{}
	for range n {
		sc.batches = append(sc.batches, claudeSubDecodeEvents(t, "turn_text.jsonl"))
	}
	return sc
}

// claudeSubRunTurn runs one request to completion and returns its Done chunk.
func claudeSubRunTurn(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) ChatChunk {
	t.Helper()
	var final ChatChunk
	err := claudeSubTurn(context.Background(), pool, req, func(chunk ChatChunk) error {
		if chunk.Done {
			final = chunk
		}
		return nil
	})
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if !final.Done {
		t.Fatal("turn emitted no Done chunk")
	}
	return final
}

// claudeSubUserLines returns the user lines a fake process received.
func claudeSubUserLines(conn *claudeSubFakeConn) []string {
	var out []string
	for _, line := range conn.sentLines() {
		if strings.Contains(line, `"type":"user"`) {
			out = append(out, line)
		}
	}
	return out
}

func claudeSubUserText(content string) Message {
	return Message{Role: MessageRoleUser, Content: content}
}

func claudeSubDecodeState(t *testing.T, raw json.RawMessage) claudeSubBackendState {
	t.Helper()
	var st claudeSubBackendState
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("decode backend state %s: %v", raw, err)
	}
	return st
}

func claudeSubEncodeState(t *testing.T, st claudeSubBackendState) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("encode backend state: %v", err)
	}
	return raw
}

// claudeSubOpeningTurn runs the first parent request on its own pool and returns
// the assistant message and backend state a restarted steiner would hold.
func claudeSubOpeningTurn(t *testing.T) (Message, claudeSubBackendState) {
	t.Helper()
	pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	done := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("read notes")}})
	return done.Delta, claudeSubDecodeState(t, done.BackendState)
}

// TestClaudeSubRestoreSelection proves a restarted parent resumes its stored CLI
// session when the state is valid, forks when the owner changed, and reseeds
// with the rendered transcript for every other failed check (D3, D7).
func TestClaudeSubRestoreSelection(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name           string
		mutate         func(*claudeSubBackendState)
		noState        bool
		system         string
		wantResume     bool
		wantFork       bool
		wantTranscript bool
	}{
		{name: "restart resumes the stored session", wantResume: true},
		{name: "owner mismatch forks", mutate: func(st *claudeSubBackendState) { st.TransportSession = "previous-owner" }, wantResume: true, wantFork: true},
		{name: "stale prompt and tools fingerprint reseeds", mutate: func(st *claudeSubBackendState) { st.PromptToolsFingerprint = "stale" }, wantTranscript: true},
		{name: "changed system prompt reseeds", system: "a new prompt", wantTranscript: true},
		{name: "missing final uuid reseeds", mutate: func(st *claudeSubBackendState) { st.FinalUUID = "" }, wantTranscript: true},
		{name: "final message digest mismatch reseeds", mutate: func(st *claudeSubBackendState) { st.FinalMessageDigest = "other" }, wantTranscript: true},
		{name: "foreign schema version reseeds", mutate: func(st *claudeSubBackendState) { st.Version = claudeSubBackendStateVersion + 1 }, wantTranscript: true},
		{name: "no stored state reseeds", noState: true, wantTranscript: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assistant, st := claudeSubOpeningTurn(t)
			var raw json.RawMessage
			if !tt.noState {
				if tt.mutate != nil {
					tt.mutate(&st)
				}
				raw = claudeSubEncodeState(t, st)
			}
			msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
			if tt.system != "" {
				msgs = append([]Message{{Role: MessageRoleSystem, Content: tt.system}}, msgs...)
			}
			pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			final := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: msgs, BackendState: raw})
			if n := spawner.callCount(); n != 1 {
				t.Fatalf("spawn count = %d, want 1", n)
			}
			args := spawner.call(t, 0).Args
			if resume, _ := claudeSubArgValue(args, "--resume"); (resume != "") != tt.wantResume {
				t.Errorf("resumed = %v, want %v (args %q)", resume != "", tt.wantResume, args)
			}
			if at, _ := claudeSubArgValue(args, "--resume-session-at"); tt.wantResume && at != st.FinalUUID {
				t.Errorf("--resume-session-at = %q, want %q", at, st.FinalUUID)
			}
			if got := claudeSubHasArg(args, "--fork-session"); got != tt.wantFork {
				t.Errorf("forked = %v, want %v", got, tt.wantFork)
			}
			lines := claudeSubUserLines(spawner.call(t, 0).Conn)
			if len(lines) != 1 {
				t.Fatalf("user lines = %d, want 1", len(lines))
			}
			if got := strings.Contains(lines[0], "Conversation so far"); got != tt.wantTranscript {
				t.Errorf("transcript reseed = %v, want %v", got, tt.wantTranscript)
			}
			if !strings.Contains(lines[0], "more") {
				t.Errorf("user line %s does not carry the new message", lines[0])
			}
			if final.BackendState == nil {
				t.Error("parent turn returned no backend state")
			}
		})
	}
}

// TestClaudeSubLiveAppendSelection proves a live process takes a request as an
// append only when its record matches the stored state and the request extends
// it. Anything else retires the process and reseeds or forks (D3, D5).
func TestClaudeSubLiveAppendSelection(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name           string
		mutate         func(*claudeSubBackendState)
		system         string
		rewrite        bool
		dead           bool
		wantSpawns     int
		wantResume     bool
		wantFork       bool
		wantTranscript bool
	}{
		{name: "matching state appends", wantSpawns: 1},
		{name: "changed system prompt still appends", system: "a new prompt", wantSpawns: 1},
		{name: "chain mismatch reseeds", mutate: func(st *claudeSubBackendState) { st.SyncChainDigest = "bad" }, wantSpawns: 2, wantTranscript: true},
		{name: "dead process reseeds", dead: true, wantSpawns: 2, wantTranscript: true},
		{name: "owner mismatch forks", mutate: func(st *claudeSubBackendState) { st.TransportSession = "previous-owner" }, wantSpawns: 2, wantResume: true, wantFork: true},
		{name: "rewritten history reseeds", rewrite: true, wantSpawns: 2, wantTranscript: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 2), claudeSubTextTurns(t, 1))
			first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
			st := claudeSubDecodeState(t, first.BackendState)
			if tt.mutate != nil {
				tt.mutate(&st)
			}
			if tt.dead {
				if err := spawner.call(t, 0).Conn.Close(context.Background()); err != nil {
					t.Fatalf("close fake CLI: %v", err)
				}
				claudeSubWaitExited(t, pool, "parent")
			}
			msgs := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
			if tt.rewrite {
				msgs[0] = claudeSubUserText("rewritten")
			}
			if tt.system != "" {
				msgs = append([]Message{{Role: MessageRoleSystem, Content: tt.system}}, msgs...)
			}
			claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: msgs, BackendState: claudeSubEncodeState(t, st)})
			if n := spawner.callCount(); n != tt.wantSpawns {
				t.Fatalf("spawn count = %d, want %d", n, tt.wantSpawns)
			}
			serving := spawner.call(t, tt.wantSpawns-1)
			if resume, _ := claudeSubArgValue(serving.Args, "--resume"); (resume != "") != tt.wantResume {
				t.Errorf("resumed = %v, want %v", resume != "", tt.wantResume)
			}
			if got := claudeSubHasArg(serving.Args, "--fork-session"); got != tt.wantFork {
				t.Errorf("forked = %v, want %v", got, tt.wantFork)
			}
			lines := claudeSubUserLines(serving.Conn)
			if len(lines) == 0 {
				t.Fatal("serving process received no user line")
			}
			if got := strings.Contains(lines[len(lines)-1], "Conversation so far"); got != tt.wantTranscript {
				t.Errorf("transcript reseed = %v, want %v", got, tt.wantTranscript)
			}
		})
	}
}

// TestClaudeSubResumeFailureReseeds proves a resumed process that fails before
// producing output is retired, and the request reseeds one fresh process.
func TestClaudeSubResumeFailureReseeds(t *testing.T) {
	model := claudeSubTestSpec().Model
	errEvent := claudeSubEvent{
		Type:      "result",
		Subtype:   "error_during_execution",
		SessionID: "sess-1",
		Raw:       json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"No conversation found with session ID: sess-1","session_id":"sess-1"}`),
	}
	failing := &claudeSubScript{batches: [][]claudeSubEvent{{errEvent}}}
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, failing, claudeSubTextTurns(t, 1))
	msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	final := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: msgs, BackendState: claudeSubEncodeState(t, st)})
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (failed resume, then reseed)", n)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 0).Args, "--resume"); resume != st.CLISessionID {
		t.Errorf("first spawn --resume = %q, want %q", resume, st.CLISessionID)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); resume != "" {
		t.Errorf("reseed spawn --resume = %q, want none", resume)
	}
	lines := claudeSubUserLines(spawner.call(t, 1).Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Errorf("reseed user lines = %q, want one transcript", lines)
	}
	if final.BackendState == nil {
		t.Error("reseeded parent turn returned no backend state")
	}
}

// TestClaudeSubPersistenceByCallKind proves only a parent call starts a persisted
// CLI session and returns backend state. Advisor and sub-agent calls keep
// --no-session-persistence and return none (D4).
func TestClaudeSubPersistenceByCallKind(t *testing.T) {
	model := claudeSubTestSpec().Model
	tests := []struct {
		name        string
		req         ChatRequest
		wantPersist bool
	}{
		{name: "parent", req: ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}}, wantPersist: true},
		{name: "advisor", req: ChatRequest{TransportSession: "parent-advisor", ParentTransportSession: "parent", AdvisorCacheProfile: true, Model: model, Messages: []Message{claudeSubUserText("is it right?")}}},
		{name: "sub-agent", req: ChatRequest{TransportSession: "parent-agent-1", ParentTransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("look")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			final := claudeSubRunTurn(t, pool, tt.req)
			args := spawner.call(t, 0).Args
			if got := !claudeSubHasArg(args, "--no-session-persistence"); got != tt.wantPersist {
				t.Errorf("persisted = %v, want %v", got, tt.wantPersist)
			}
			if got := final.BackendState != nil; got != tt.wantPersist {
				t.Errorf("backend state returned = %v, want %v", got, tt.wantPersist)
			}
		})
	}
}

// TestClaudeSubAdvisorRecoversFromChangedSnapshot proves an advisor whose parent
// snapshot diverged from what it holds is discarded, and the snapshot is rendered
// again on a fresh process instead of refusing the call (D19).
func TestClaudeSubAdvisorRecoversFromChangedSnapshot(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1), claudeSubTextTurns(t, 1))
	advisor := func(first string) ChatRequest {
		return ChatRequest{
			TransportSession:       "parent-advisor",
			ParentTransportSession: "parent",
			AdvisorCacheProfile:    true,
			Model:                  claudeSubTestSpec().Model,
			Messages:               []Message{claudeSubUserText(first), {Role: MessageRoleAssistant, Content: "Hello world"}, claudeSubUserText("is it right?")},
		}
	}
	claudeSubRunTurn(t, pool, advisor("read notes"))
	claudeSubRunTurn(t, pool, advisor("rewritten"))
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (advisor replaced)", n)
	}
	lines := claudeSubUserLines(spawner.call(t, 1).Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "rewritten") || !strings.Contains(lines[0], "Conversation so far") {
		t.Errorf("replacement advisor user lines = %q, want one transcript of the changed snapshot", lines)
	}
}
