package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Failure recovery. A failure before any output is retried inside the provider.
// A failure after output is surfaced with the partial kept, and the next request
// resumes the stored checkpoint through the stopped-turn recovery. The fake CLI
// crashes by closing its connection right after it pushed a scripted batch (see
// claudeSubFakeConn.exitAfterNextSend), which is what an exited CLI looks like to
// the provider.

// claudeSubMidResponseError returns the result the CLI reports when an API
// failure cut a response off mid-stream: the error result names api_error, the
// text it already delivered stays where it is, and the process stays alive.
func claudeSubMidResponseError() claudeSubEvent {
	return claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"api_error","api_error_status":529,"num_turns":1,"result":"API Error: Server error mid-response. The response above may be incomplete.","session_id":"sess-1"}`)}
}

// claudeSubCrashResult returns the result a crashed CLI synthesises: is_error,
// the error_during_execution subtype, and no terminal reason.
func claudeSubCrashResult(text string) claudeSubEvent {
	return claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"` + text + `","session_id":"sess-1"}`)}
}

// claudeSubMaxTurnsResult returns an error_max_turns result: the CLI ending the
// turn on purpose, which no recovery touches.
func claudeSubMaxTurnsResult() claudeSubEvent {
	return claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"stopped early","session_id":"sess-1"}`)}
}

// claudeSubStreamedTextBatch returns the fixture's message start and its first
// text delta: the part of a reply a caller has already seen when the turn fails.
func claudeSubStreamedTextBatch(t *testing.T) []claudeSubEvent {
	t.Helper()
	return claudeSubDecodeEvents(t, "turn_text.jsonl")[:8]
}

// claudeSubStreamedText is the text claudeSubStreamedTextBatch streams.
const claudeSubStreamedText = "Hello"

// claudeSubCrashingPool returns a pool whose i-th spawned process answers user
// lines from scripts[i] and whose first crashSpawns processes exit right after
// the user line that carried that batch.
func claudeSubCrashingPool(t *testing.T, crashSpawns int, scripts ...*claudeSubScript) (*ClaudeSubscriptionPool, *claudeSubTestSpawner) {
	t.Helper()
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		// onSpawn runs before the spawner records the call, so index is 0-based.
		index := len(spawner.calls)
		var script *claudeSubScript
		if index < len(scripts) {
			script = scripts[index]
		}
		crash := index < crashSpawns
		c.mu.Lock()
		c.responder = func(line []byte) []claudeSubEvent {
			var events []claudeSubEvent
			if script != nil {
				events = script.respond(line)
			}
			if crash && strings.Contains(string(line), `"type":"user"`) {
				c.exitAfterNextSend()
			}
			return events
		}
		c.mu.Unlock()
	}
	return pool, spawner
}

// claudeSubRunMeasuredCollected runs one request through the streaming wrapper,
// which attaches the call diagnostics to the terminal chunk, and records every
// chunk it emitted.
func claudeSubRunMeasuredCollected(t *testing.T, pool *ClaudeSubscriptionPool, req ChatRequest) claudeSubCollected {
	t.Helper()
	stream, err := claudeSubStream(context.Background(), pool, req, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got claudeSubCollected
	for chunk := range stream {
		got.chunks = append(got.chunks, chunk)
		if chunk.Error != "" && got.err == nil {
			got.err = chunk.OriginalError
			if got.err == nil {
				got.err = errors.New(chunk.Error)
			}
		}
	}
	return got
}

// claudeSubRecoveryNoticeChunk reports whether chunk is a recovery notice this
// provider emitted, as opposed to one of the CLI's own api_retry notices.
func claudeSubRecoveryNoticeChunk(chunk ChatChunk) bool {
	return chunk.RetryAttempt > 0 && chunk.cliRetry == nil
}

// claudeSubFailureWarnings returns the call's warnings that are not recovery
// notices, so a test can check the warnings it expects on their own.
func claudeSubFailureWarnings(got claudeSubCollected) []string {
	var out []string
	for _, chunk := range got.chunks {
		if chunk.Diagnostic != "" && !claudeSubRecoveryNoticeChunk(chunk) {
			out = append(out, chunk.Diagnostic)
		}
	}
	return out
}

// claudeSubWantFailureWarning asserts the call reported exactly the one warning
// wantText, not counting its recovery notices.
func claudeSubWantFailureWarning(t *testing.T, got claudeSubCollected, wantText string) {
	t.Helper()
	warnings := claudeSubFailureWarnings(got)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", warnings)
	}
	if want := claudeSubWarningChunk(wantText).Diagnostic; warnings[0] != want {
		t.Errorf("warning = %q, want %q", warnings[0], want)
	}
}

// claudeSubWantRetryNotice asserts the call emitted exactly one recovery notice
// carrying wantText and wantAttempt, with no output and no reset.
func claudeSubWantRetryNotice(t *testing.T, got claudeSubCollected, wantText string, wantAttempt int) {
	t.Helper()
	var notices []ChatChunk
	for _, chunk := range got.chunks {
		if claudeSubRecoveryNoticeChunk(chunk) {
			notices = append(notices, chunk)
		}
	}
	if len(notices) != 1 {
		t.Fatalf("recovery notices = %+v, want one", notices)
	}
	notice := notices[0]
	if notice.Diagnostic != wantText {
		t.Errorf("notice = %q, want %q", notice.Diagnostic, wantText)
	}
	if notice.RetryAttempt != wantAttempt || notice.RetryMaxAttempts != claudeSubRetryMaxRecoveries {
		t.Errorf("notice attempt = %d/%d, want %d/%d", notice.RetryAttempt, notice.RetryMaxAttempts, wantAttempt, claudeSubRetryMaxRecoveries)
	}
	if notice.RetryReset || notice.Severity != "warning" || notice.Delta.Content != "" || notice.Thinking != "" {
		t.Errorf("notice = %+v, want a warning with no output and no reset", notice)
	}
}

// claudeSubWantNoRecoveryNotice asserts the call recovered nothing.
func claudeSubWantNoRecoveryNotice(t *testing.T, got claudeSubCollected) {
	t.Helper()
	for _, chunk := range got.chunks {
		if claudeSubRecoveryNoticeChunk(chunk) {
			t.Errorf("recovery notice = %q, want none", chunk.Diagnostic)
		}
	}
}

// claudeSubFailedTurn builds the error a failed CLI turn carries.
func claudeSubFailedTurn(subtype, terminalReason, text string) error {
	return claudeSubTurnFailure(&claudeSubResult{Subtype: subtype, TerminalReason: terminalReason, Text: text, IsError: true})
}

// TestClaudeSubRetryDecision proves the recovery decision: a crash before output
// is retried, a failure after output is never recovered, and every refusal,
// deliberate end, spent budget and in-flight tool call ends the call.
func TestClaudeSubRetryDecision(t *testing.T) {
	crash := claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "boom")
	apiError := claudeSubFailedTurn(claudeSubCrashResultSubtype, "api_error", "API Error: Server error mid-response.")
	connClosed := errClaudeSubConnClosed
	normal := claudeSubCallKindNormal
	tests := []struct {
		name  string
		err   error
		facts claudeSubRetryFacts
		want  claudeSubRetryAction
	}{
		{name: "crash before output is retried", err: crash, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}, want: claudeSubRetryPreOutput},
		{name: "closed connection before output is retried", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}, want: claudeSubRetryPreOutput},
		{name: "connection sentinel without a liveness flag is retried", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal}, want: claudeSubRetryPreOutput},
		{name: "crash after output is surfaced", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Visible: true, ProcessGone: true}},
		{name: "closed connection after output is surfaced", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal, Visible: true, ProcessGone: true}},
		{name: "mid-response API error after output is surfaced", err: apiError, facts: claudeSubRetryFacts{CallKind: normal, Visible: true}},
		{name: "mid-response API error before output is not retried", err: apiError, facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "sub-agent crash is retried", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindSubAgent}, want: claudeSubRetryPreOutput},
		{name: "advisor call keeps its behaviour", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindAdvisor}},
		{name: "summary call keeps its behaviour", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindSummary}},
		{name: "no error", facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "caller cancelled", err: crash, facts: claudeSubRetryFacts{CallKind: normal, ContextErr: context.Canceled}},
		{name: "CLI already retried", err: crash, facts: claudeSubRetryFacts{CallKind: normal, CLIRetried: true}},
		{name: "tool call in flight", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Pending: true}},
		{name: "recovery budget spent", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Recoveries: claudeSubRetryMaxRecoveries}},
		{name: "aborted streaming result", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "aborted_streaming", "stopped"), facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "aborted tools result", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "aborted_tools", "stopped"), facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "interrupted result without a terminal reason", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "[Request interrupted by user for tool use]"), facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "error_max_turns", err: claudeSubFailedTurn("error_max_turns", "", "stopped early"), facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "auth refusal text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "request failed with status 401"), facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "billing refusal text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "billing not enabled"), facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "invalid request text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "api_error", "invalid_request_error: max_tokens"), facts: claudeSubRetryFacts{CallKind: normal, Visible: true}},
		{name: "decode failure", err: errors.Join(errClaudeSubDecodeStream, errors.New("boom")), facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "result envelope failure", err: errClaudeSubResultEnvelope, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "oversized line", err: &claudeSubLineTooLargeError{limit: 1}, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "unsafe session", err: errClaudeSubSessionUnsafe, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "usage limit", err: &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "claude_subscription"}, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "usage gate refusal", err: &claudeSubUsageGateError{err: claudeSubUsageNotReady("rate_limits_available=false")}, facts: claudeSubRetryFacts{CallKind: normal, ProcessGone: true}},
		{name: "a control failure is not a crash", err: errors.New("claude CLI set_model: model unavailable"), facts: claudeSubRetryFacts{CallKind: normal}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubRetryDecision(tt.err, tt.facts); got != tt.want {
				t.Errorf("decision = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClaudeSubRetryActionLabels proves the only recovery action reports the
// label the call diagnostics name, and that nothing else does.
func TestClaudeSubRetryActionLabels(t *testing.T) {
	tests := []struct {
		action claudeSubRetryAction
		want   string
	}{
		{claudeSubRetryNone, "none"},
		{claudeSubRetryPreOutput, claudeSubRecoveryRetryPreOutput},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("action %d String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

// TestClaudeSubRecoveryDelay proves the backoff schedule: 1s then 3s, with the
// last entry repeating.
func TestClaudeSubRecoveryDelay(t *testing.T) {
	// TestMain shortens the schedule every other test pays, so pin the production
	// one here.
	prev := claudeSubRetryBackoff
	claudeSubRetryBackoff = []time.Duration{time.Second, 3 * time.Second}
	t.Cleanup(func() { claudeSubRetryBackoff = prev })
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 3 * time.Second},
		{3, 3 * time.Second},
	}
	for _, tt := range tests {
		if got := claudeSubRecoveryDelay(tt.attempt); got != tt.want {
			t.Errorf("delay(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

// TestClaudeSubRecoveryNotice proves the recovery notice wording and fields.
func TestClaudeSubRecoveryNotice(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "crash result", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "boom"), want: "claude_subscription turn failed (error_during_execution); retrying, attempt 1/2"},
		{name: "closed connection", err: errClaudeSubConnClosed, want: "claude_subscription turn failed (connection closed); retrying, attempt 1/2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := claudeSubRecoveryNotice(tt.err, 1, time.Second)
			if chunk.Diagnostic != tt.want {
				t.Errorf("diagnostic = %q, want %q", chunk.Diagnostic, tt.want)
			}
			if chunk.Severity != "warning" || chunk.RetryAttempt != 1 || chunk.RetryMaxAttempts != claudeSubRetryMaxRecoveries || chunk.RetryDelay != time.Second {
				t.Errorf("notice = %+v, want a warning carrying attempt 1/2 in 1s", chunk)
			}
			if chunk.RetryReset || chunk.cliRetry != nil || claudeSubVisibleChunk(chunk) {
				t.Errorf("notice = %+v, want no reset, no CLI notice and no visible output", chunk)
			}
		})
	}
}

// TestClaudeSubRecoverPreOutputCrashResumesParent proves a parent process that
// crashes before producing output is replaced, and the request resumes the stored
// checkpoint in a fork instead of replaying the conversation flattened.
func TestClaudeSubRecoverPreOutputCrashResumesParent(t *testing.T) {
	model := claudeSubTestSpec().Model
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err != nil {
		t.Fatalf("turn error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the crashed process and its resume)", n)
	}
	serving := spawner.call(t, 1)
	resume, _ := claudeSubArgValue(serving.Args, "--resume")
	at, _ := claudeSubArgValue(serving.Args, "--resume-session-at")
	if resume != st.CLISessionID || at != st.FinalUUID {
		t.Errorf("retry resume = %q at %q, want %q at %q", resume, at, st.CLISessionID, st.FinalUUID)
	}
	if !claudeSubHasArg(serving.Args, "--fork-session") {
		t.Errorf("retry args %q, want --fork-session", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("retry user lines = %q, want one plain line", lines)
	}
	if blocks := claudeSubUserTextBlocks(t, lines[0]); len(blocks) != 1 || blocks[0] != "more" {
		t.Errorf("retry blocks = %q, want the request's own message", blocks)
	}
	if warnings := claudeSubFailureWarnings(got); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a resumed retry", warnings)
	}
	final := got.final(t)
	if final.Delta.Content != "Hello world" || final.BackendState == nil {
		t.Errorf("final = %q state %s, want the reply and a checkpoint", final.Delta.Content, final.BackendState)
	}
	if label := claudeSubRecoveryLabel(final); label != claudeSubRecoveryRetryPreOutput {
		t.Errorf("recovery = %q, want %q", label, claudeSubRecoveryRetryPreOutput)
	}
	claudeSubWantRetryNotice(t, got, "claude_subscription turn failed (connection closed); retrying, attempt 1/2", 1)
}

// TestClaudeSubRecoverPreOutputCrashReseedsSubAgent proves a sub-agent process
// that crashes before output is replaced, and the retry reseeds the flattened
// conversation because a sub-agent holds no checkpoint to resume.
func TestClaudeSubRecoverPreOutputCrashReseedsSubAgent(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession:       "child-1",
		ParentTransportSession: "parent",
		Model:                  model,
		Messages: []Message{
			claudeSubUserText("read notes"),
			{Role: MessageRoleAssistant, Content: "answer"},
			claudeSubUserText("more"),
		},
	})
	if got.err != nil {
		t.Fatalf("sub-agent turn error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the crashed process and its reseed)", n)
	}
	serving := spawner.call(t, 1)
	if _, ok := claudeSubArgValue(serving.Args, "--resume"); ok {
		t.Errorf("reseed spawn resumed: %q", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("reseed user lines = %q, want one rendered transcript", lines)
	}
	if final := got.final(t); final.Delta.Content != "Hello world" {
		t.Errorf("final content = %q, want %q", final.Delta.Content, "Hello world")
	}
	claudeSubWantFailureWarning(t, got, claudeSubFlattenedReseedWarning)
	claudeSubWantRetryNotice(t, got, "claude_subscription turn failed (connection closed); retrying, attempt 1/2", 1)
}

// TestClaudeSubRecoverNeverRetries proves each failure the recovery must leave
// alone ends the call on the process it happened on.
func TestClaudeSubRecoverNeverRetries(t *testing.T) {
	model := claudeSubTestSpec().Model
	rejected := claudeSubEvent{Type: "rate_limit_event", Raw: json.RawMessage(`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","overageStatus":"rejected","resetsAt":1791477000}}`)}
	tests := []struct {
		name    string
		batch   func(t *testing.T) []claudeSubEvent
		wantErr string
	}{
		{
			name:    "api error before output",
			batch:   func(*testing.T) []claudeSubEvent { return []claudeSubEvent{claudeSubMidResponseError()} },
			wantErr: "claude_subscription turn failed: error_during_execution: API Error: Server error mid-response. The response above may be incomplete.",
		},
		{
			name:    "error_max_turns",
			batch:   func(*testing.T) []claudeSubEvent { return []claudeSubEvent{claudeSubMaxTurnsResult()} },
			wantErr: "claude_subscription turn failed: error_max_turns: stopped early",
		},
		{
			name: "aborted streaming",
			batch: func(*testing.T) []claudeSubEvent {
				return []claudeSubEvent{{Type: "result", Raw: json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","result":"stopped","session_id":"sess-1"}`)}}
			},
			wantErr: "claude_subscription turn failed: error_during_execution: stopped",
		},
		{
			name: "interrupted result without a terminal reason",
			batch: func(t *testing.T) []claudeSubEvent {
				return claudeSubDecodeEvents(t, "result_error.jsonl")
			},
			wantErr: claudeSubRecoveryTurnFailure,
		},
		{
			name: "the CLI retried its own request",
			batch: func(t *testing.T) []claudeSubEvent {
				return claudeSubRetryThenFailBatch(t, claudeSubRetryOverloaded)
			},
			wantErr: "claude_subscription turn failed: error_during_execution: resume failed",
		},
		{
			name: "auth refusal text",
			batch: func(*testing.T) []claudeSubEvent {
				return []claudeSubEvent{claudeSubCrashResult("request failed with status 401")}
			},
			wantErr: "claude_subscription turn failed: error_during_execution: request failed with status 401",
		},
		{
			name:    "usage limit",
			batch:   func(*testing.T) []claudeSubEvent { return []claudeSubEvent{rejected} },
			wantErr: "usage limit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assistant, st := claudeSubOpeningTurn(t)
			pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{tt.batch(t)}}, claudeSubTextTurns(t, 1))
			got := claudeSubRunCollected(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            model,
				Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
				BackendState:     claudeSubEncodeState(t, st),
			})
			if got.err == nil {
				t.Fatalf("turn error = nil, want %q", tt.wantErr)
			}
			if n := spawner.callCount(); n != 1 {
				t.Fatalf("spawn count = %d, want 1 (the failure is not recovered)", n)
			}
			if tt.name == "usage limit" {
				if _, ok := AsUsageLimit(got.err); !ok {
					t.Fatalf("turn error = %v, want a *UsageLimitError", got.err)
				}
			} else if got.err.Error() != tt.wantErr {
				t.Errorf("turn error = %q, want %q", got.err, tt.wantErr)
			}
			claudeSubWantNoRecoveryNotice(t, got)
		})
	}
}

// TestClaudeSubRecoverPendingToolUse proves a turn whose tool call is still in
// flight is never recovered: no attempt may re-run or re-request a tool call.
func TestClaudeSubRecoverPendingToolUse(t *testing.T) {
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	batch := append(claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:5], claudeSubCrashResult("boom"))
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{batch}}, claudeSubTextTurns(t, 1))
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Tools:            tools,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	if got.err == nil || got.err.Error() != "claude_subscription turn failed: error_during_execution: boom" {
		t.Fatalf("turn error = %v, want the crash failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (a tool call was in flight)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
}

// TestClaudeSubRecoverBoundsAttempts proves a call spends at most
// claudeSubRetryMaxRecoveries recoveries and then returns the failure.
func TestClaudeSubRecoverBoundsAttempts(t *testing.T) {
	pool, spawner := claudeSubCrashingPool(t, 3, &claudeSubScript{}, &claudeSubScript{}, &claudeSubScript{})
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	if got.err == nil || got.err.Error() != errClaudeSubConnClosed.Error() {
		t.Fatalf("turn error = %v, want the connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1+claudeSubRetryMaxRecoveries {
		t.Fatalf("spawn count = %d, want %d (two recoveries and then the failure)", n, 1+claudeSubRetryMaxRecoveries)
	}
	var attempts []int
	for _, chunk := range got.chunks {
		if claudeSubRecoveryNoticeChunk(chunk) {
			attempts = append(attempts, chunk.RetryAttempt)
		}
	}
	if len(attempts) != claudeSubRetryMaxRecoveries || attempts[0] != 1 || attempts[1] != 2 {
		t.Errorf("recovery attempts = %v, want [1 2]", attempts)
	}
}

// TestClaudeSubRecoverCancelDuringBackoff proves a caller that stops during a
// recovery wait ends the call promptly instead of starting the next attempt.
func TestClaudeSubRecoverCancelDuringBackoff(t *testing.T) {
	prev := claudeSubRetryBackoff
	claudeSubRetryBackoff = []time.Duration{time.Hour, time.Hour}
	t.Cleanup(func() { claudeSubRetryBackoff = prev })
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(25*time.Millisecond, cancel)
	defer timer.Stop()
	start := time.Now()
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes")},
	}, func(ChatChunk) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("turn error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancelled recovery waited %s, want it to return promptly", elapsed)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the recovery never started)", n)
	}
}

// TestClaudeSubRecoverAdvisorUnchanged proves an advisor call keeps its
// behaviour: a mid-response failure is returned, never recovered.
func TestClaudeSubRecoverAdvisorUnchanged(t *testing.T) {
	mid := append(claudeSubStreamedTextBatch(t), claudeSubMidResponseError())
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{mid}})
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:       "parent-advisor",
		ParentTransportSession: "parent",
		AdvisorCacheProfile:    true,
		Model:                  claudeSubTestSpec().Model,
		Messages:               []Message{claudeSubUserText("is it right?")},
	})
	if got.err == nil {
		t.Fatal("advisor turn error = nil, want the failure")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the advisor path does not recover)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
}

// TestClaudeSubFailAfterVisibleCrashResumesNextTurn proves a crash after output
// ends the call with the error and no retry, and the next request resumes the
// previous checkpoint warm with the stopped-reply note. The history is what the
// agent keeps: the partial becomes an incomplete assistant message.
func TestClaudeSubFailAfterVisibleCrashResumesNextTurn(t *testing.T) {
	model := claudeSubTestSpec().Model
	assistant, st := claudeSubOpeningTurn(t)
	partial := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubStreamedTextBatch(t)}}
	pool, spawner := claudeSubCrashingPool(t, 1, partial, claudeSubTextTurns(t, 1))
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || got.err.Error() != errClaudeSubConnClosed.Error() {
		t.Fatalf("turn error = %v, want the connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (no attempt follows visible output)", n)
	}
	if streamed := claudeSubStreamedText; !strings.Contains(streamed, "Hello") {
		t.Fatalf("streamed text = %q", streamed)
	}
	claudeSubWantNoRecoveryNotice(t, got)

	// The next request carries the partial the agent kept and resumes the stored
	// checkpoint in a fork, with the stopped-reply note quoting that partial.
	next := []Message{
		claudeSubUserText("read notes"),
		assistant,
		claudeSubUserText("more"),
		claudeSubRecoveryPartial(claudeSubStreamedText),
		claudeSubUserText("again"),
	}
	resumed := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         next,
		BackendState:     claudeSubEncodeState(t, st),
	})
	if resumed.err != nil {
		t.Fatalf("resumed turn error = %v", resumed.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the crashed process and the resumed fork)", n)
	}
	serving := spawner.call(t, 1)
	resume, _ := claudeSubArgValue(serving.Args, "--resume")
	at, _ := claudeSubArgValue(serving.Args, "--resume-session-at")
	if resume != st.CLISessionID || at != st.FinalUUID {
		t.Errorf("resume = %q at %q, want %q at %q", resume, at, st.CLISessionID, st.FinalUUID)
	}
	if !claudeSubHasArg(serving.Args, "--fork-session") {
		t.Errorf("resume args %q, want --fork-session", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("resume user lines = %q, want one note line, not a flattened transcript", lines)
	}
	blocks := claudeSubUserTextBlocks(t, lines[0])
	if len(blocks) != 3 || blocks[0] != "more" || blocks[2] != "again" {
		t.Fatalf("resume blocks = %q, want the stopped content, the note and the new message", blocks)
	}
	if !strings.Contains(blocks[1], claudeSubStreamedText) || !strings.Contains(blocks[1], claudeSubStoppedReplyNoteOpening) {
		t.Errorf("note = %q, want the partial quoted in the stopped-reply note", blocks[1])
	}
	if warnings := claudeSubFailureWarnings(resumed); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none (the fork resume reports no replacement)", warnings)
	}
	if label := claudeSubRecoveryLabel(resumed.final(t)); label != claudeSubRecoveryResumeAfterStop {
		t.Errorf("recovery = %q, want %q", label, claudeSubRecoveryResumeAfterStop)
	}
}

// TestClaudeSubFailMidResponseAPIErrorResumesNextTurn proves a mid-response API
// error after output ends the call with the error and no continuation. The live
// process cannot serve the next request: its record is one delivery ahead of the
// stored checkpoint and holds no assistant entry for the text Steiner streamed,
// so the request retires it, reports one replacement warning, and fork-resumes the
// stored checkpoint with the stopped-reply note.
func TestClaudeSubFailMidResponseAPIErrorResumesNextTurn(t *testing.T) {
	model := claudeSubTestSpec().Model
	assistant, st := claudeSubOpeningTurn(t)
	mid := append(claudeSubStreamedTextBatch(t), claudeSubMidResponseError())
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{mid}},
		claudeSubTextTurns(t, 1),
	)
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "API Error: Server error mid-response") {
		t.Fatalf("turn error = %v, want the mid-response API error", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (no attempt follows visible output)", n)
	}
	if lines := claudeSubUserLines(spawner.call(t, 0).Conn); len(lines) != 1 {
		t.Fatalf("user lines = %q, want only the request: no continuation note", lines)
	}
	claudeSubWantNoRecoveryNotice(t, got)
	// The live record holds the delivered user line and no assistant entry, so it
	// cannot match the request that carries Steiner's streamed partial.
	if live := claudeSubRecoveryRecordLen(t, pool, "parent"); live != st.SyncEntries+1 {
		t.Fatalf("live record entries = %d, want %d", live, st.SyncEntries+1)
	}

	next := []Message{
		claudeSubUserText("read notes"),
		assistant,
		claudeSubUserText("more"),
		claudeSubRecoveryPartial(claudeSubStreamedText),
		claudeSubUserText("again"),
	}
	resumed := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         next,
		BackendState:     claudeSubEncodeState(t, st),
	})
	if resumed.err != nil {
		t.Fatalf("resumed turn error = %v", resumed.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the retired live process and the resumed fork)", n)
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("the mismatching live process was reused instead of retired")
	}
	serving := spawner.call(t, 1)
	resume, _ := claudeSubArgValue(serving.Args, "--resume")
	at, _ := claudeSubArgValue(serving.Args, "--resume-session-at")
	if resume != st.CLISessionID || at != st.FinalUUID {
		t.Errorf("resume = %q at %q, want %q at %q", resume, at, st.CLISessionID, st.FinalUUID)
	}
	if !claudeSubHasArg(serving.Args, "--fork-session") {
		t.Errorf("resume args %q, want --fork-session", serving.Args)
	}
	lines := claudeSubUserLines(serving.Conn)
	if len(lines) != 1 || strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("resume user lines = %q, want one note line, not a flattened transcript", lines)
	}
	blocks := claudeSubUserTextBlocks(t, lines[0])
	if len(blocks) != 3 || blocks[0] != "more" || blocks[2] != "again" {
		t.Fatalf("resume blocks = %q, want the stopped content, the note and the new message", blocks)
	}
	if !strings.Contains(blocks[1], claudeSubStreamedText) || !strings.Contains(blocks[1], claudeSubStoppedReplyNoteOpening) {
		t.Errorf("note = %q, want the partial quoted in the stopped-reply note", blocks[1])
	}
	claudeSubWantFailureWarning(t, resumed, claudeSubReplacedProcessWarning)
	if label := claudeSubRecoveryLabel(resumed.final(t)); label != claudeSubRecoveryResumeAfterStop {
		t.Errorf("recovery = %q, want %q", label, claudeSubRecoveryResumeAfterStop)
	}
}

// TestClaudeSubFailAfterVisibleSubAgentReturnsError proves a sub-agent crash after
// output ends the call with the error: nothing is reseeded.
func TestClaudeSubFailAfterVisibleSubAgentReturnsError(t *testing.T) {
	partial := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubStreamedTextBatch(t)}}
	pool, spawner := claudeSubCrashingPool(t, 1, partial, claudeSubTextTurns(t, 1))
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:       "child-1",
		ParentTransportSession: "parent",
		Model:                  claudeSubTestSpec().Model,
		Messages: []Message{
			claudeSubUserText("read notes"),
			{Role: MessageRoleAssistant, Content: "answer"},
			claudeSubUserText("more"),
		},
	})
	if got.err == nil || got.err.Error() != errClaudeSubConnClosed.Error() {
		t.Fatalf("sub-agent turn error = %v, want the connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (nothing is reseeded after output)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
}
