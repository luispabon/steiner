package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Failure recovery. A failure before any output is retried inside the provider,
// only when the request carries a stored checkpoint the retry can fork-resume. A
// failure after output is surfaced with the partial kept, and the next request
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

// claudeSubThinkingOnlyBatch returns the fixture's message start and its thinking
// delta, before any text: the shape of an attempt that only thought before it
// failed.
func claudeSubThinkingOnlyBatch(t *testing.T) []claudeSubEvent {
	t.Helper()
	return claudeSubDecodeEvents(t, "turn_text.jsonl")[:6]
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

// TestClaudeSubRetryDecision proves the recovery decision: a typed
// connection-ended failure before output is retried when a stored checkpoint can
// be resumed, a failure after output is never recovered, and every refusal,
// ambiguous error, deliberate end and spent budget ends the call.
func TestClaudeSubRetryDecision(t *testing.T) {
	crash := claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "boom")
	apiError := claudeSubFailedTurn(claudeSubCrashResultSubtype, "api_error", "API Error: Server error mid-response.")
	connClosed := errClaudeSubConnClosed
	processExit := fmt.Errorf("%w: %w", errClaudeSubConnClosed, errors.New("claude CLI process exited: signal: killed"))
	normal := claudeSubCallKindNormal
	retry := claudeSubRetryFacts{CallKind: normal, Restorable: true}
	tests := []struct {
		name  string
		err   error
		facts claudeSubRetryFacts
		want  claudeSubRetryAction
	}{
		{name: "crash result before output is retried", err: crash, facts: retry, want: claudeSubRetryPreOutput},
		{name: "closed connection before output is retried", err: connClosed, facts: retry, want: claudeSubRetryPreOutput},
		{name: "recorded process exit before output is retried", err: processExit, facts: retry, want: claudeSubRetryPreOutput},
		{name: "crash after output is surfaced", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Visible: true}},
		{name: "closed connection after output is surfaced", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Visible: true}},
		{name: "thinking-only output is surfaced", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Visible: true}},
		{name: "mid-response API error after output is surfaced", err: apiError, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Visible: true}},
		{name: "mid-response API error before output is not retried", err: apiError, facts: retry},
		{name: "no restorable checkpoint is not retried", err: connClosed, facts: claudeSubRetryFacts{CallKind: normal}},
		{name: "sub-agent without a checkpoint is not retried", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindSubAgent}},
		{name: "advisor call keeps its behaviour", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindAdvisor, Restorable: true}},
		{name: "summary call keeps its behaviour", err: crash, facts: claudeSubRetryFacts{CallKind: claudeSubCallKindSummary, Restorable: true}},
		{name: "no error", facts: retry},
		{name: "caller cancelled", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, ContextErr: context.Canceled}},
		{name: "CLI already retried", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, CLIRetried: true}},
		{name: "tool call in flight", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Pending: true}},
		{name: "recovery budget spent", err: crash, facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Recoveries: claudeSubRetryMaxRecoveries}},
		{name: "aborted streaming result", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "aborted_streaming", "stopped"), facts: retry},
		{name: "aborted tools result", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "aborted_tools", "stopped"), facts: retry},
		{name: "interrupted result without a terminal reason", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "[Request interrupted by user for tool use]"), facts: retry},
		{name: "error_max_turns", err: claudeSubFailedTurn("error_max_turns", "", "stopped early"), facts: retry},
		{name: "auth refusal text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "request failed with status 401"), facts: retry},
		{name: "API failure text without a terminal reason", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "API Error: Server error mid-response. The response above may be incomplete."), facts: retry},
		{name: "unconsumed turn", err: fmt.Errorf("%w: %w", errClaudeSubUnconsumedTurn, errClaudeSubConnClosed), facts: retry},
		{name: "billing refusal text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "billing not enabled"), facts: retry},
		{name: "invalid request text", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "api_error", "invalid_request_error: max_tokens"), facts: claudeSubRetryFacts{CallKind: normal, Restorable: true, Visible: true}},
		{name: "decode failure", err: errors.Join(errClaudeSubDecodeStream, errors.New("boom")), facts: retry},
		{name: "result envelope failure", err: errClaudeSubResultEnvelope, facts: retry},
		{name: "oversized stdin line", err: &claudeSubLineTooLargeError{limit: 1}, facts: retry},
		{name: "oversized stdout line", err: fmt.Errorf("read claude CLI stdout: %w", bufio.ErrTooLong), facts: retry},
		{name: "unsafe session", err: errClaudeSubSessionUnsafe, facts: retry},
		{name: "usage limit", err: &UsageLimitError{Kind: UsageLimitKindUsage, Provider: "claude_subscription"}, facts: retry},
		{name: "usage gate refusal", err: &claudeSubUsageGateError{err: claudeSubUsageNotReady("rate_limits_available=false")}, facts: retry},
		{name: "paid extra usage", err: errClaudeSubPaidExtraUsage, facts: retry},
		{name: "unrecognised rate-limit status", err: errClaudeSubUnrecognisedStatus, facts: retry},
		{name: "wrapped paid extra usage", err: fmt.Errorf("turn: %w", errClaudeSubPaidExtraUsage), facts: retry},
		{name: "ambiguous live control failure is not retried", err: errors.New("claude CLI get_usage: usage unavailable"), facts: retry},
		{name: "a control failure is not a crash", err: errors.New("claude CLI set_model: model unavailable"), facts: retry},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubRetryDecision(tt.err, tt.facts); got != tt.want {
				t.Errorf("decision = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClaudeSubRetryRefusedEvidence proves the refusal classifier reads only
// trustworthy structured text: the CLI's result text when the turn reported one,
// and the safe error form otherwise, which excludes the stderr tail. Incidental
// stderr words therefore never block a legitimate retry, while a real refusal in
// the result text still does.
func TestClaudeSubRetryRefusedEvidence(t *testing.T) {
	stderrOnly := &claudeSubDetailError{
		detail: errors.New("claude CLI process exited: exit status 2 (stderr: permission denied, status 401)"),
		safe:   errors.New("claude CLI process exited: exit status 2"),
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "incidental stderr text is not a refusal", err: stderrOnly},
		{name: "a crash is not a refusal", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "boom")},
		{name: "result text naming auth is a refusal", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "request failed with status 401"), want: true},
		{name: "result text naming permission is a refusal", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "permission denied for this model"), want: true},
		{name: "result text naming billing is a refusal", err: claudeSubFailedTurn(claudeSubCrashResultSubtype, "", "billing not enabled"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubRetryRefused(tt.err); got != tt.want {
				t.Errorf("refused = %v, want %v", got, tt.want)
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

// TestClaudeSubRecoverPreOutputCrashSubAgentDoesNotRetry proves a sub-agent holds
// no checkpoint to fork-resume, so a pre-output crash ends the call: nothing is
// reseeded or replayed.
func TestClaudeSubRecoverPreOutputCrashSubAgentDoesNotRetry(t *testing.T) {
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
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
		t.Fatalf("spawn count = %d, want 1 (a sub-agent has no checkpoint to resume)", n)
	}
	if warnings := claudeSubFailureWarnings(got); len(warnings) != 0 {
		t.Errorf("warnings = %q, want none: nothing was reseeded", warnings)
	}
	claudeSubWantNoRecoveryNotice(t, got)
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
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 3, &claudeSubScript{}, &claudeSubScript{}, &claudeSubScript{})
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
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
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(25*time.Millisecond, cancel)
	defer timer.Stop()
	start := time.Now()
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
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

// TestClaudeSubRecoverSendClosedIsRetried proves a write that fails because the
// CLI connection closed is typed evidence: before any output the request is
// retried on a replacement process that resumes the stored checkpoint.
func TestClaudeSubRecoverSendClosedIsRetried(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	scripts := []*claudeSubScript{{}, claudeSubTextTurns(t, 1)}
	pool, spawner := claudeSubPoolWithScripts(t, scripts...)
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		index := len(spawner.calls)
		c.mu.Lock()
		script := scripts[min(index, len(scripts)-1)]
		c.responder = func(line []byte) []claudeSubEvent {
			var env struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(line, &env) != nil || !strings.Contains(string(line), `"subtype":"get_usage"`) {
				return script.respond(line)
			}
			if index == 0 {
				// The next write finds the connection closed.
				c.mu.Lock()
				c.sendErr = fmt.Errorf("%w: fake send closed", errClaudeSubConnClosed)
				c.mu.Unlock()
			}
			return []claudeSubEvent{claudeSubSuccessEvent(env.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
		}
		c.mu.Unlock()
	}
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err != nil {
		t.Fatalf("turn error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the closed connection and its retry)", n)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); resume != st.CLISessionID {
		t.Errorf("retry --resume = %q, want %q", resume, st.CLISessionID)
	}
	if final := got.final(t); final.Delta.Content != "Hello world" {
		t.Errorf("final content = %q, want %q", final.Delta.Content, "Hello world")
	}
	claudeSubWantRetryNotice(t, got, "claude_subscription turn failed (connection closed); retrying, attempt 1/2", 1)
}

// TestClaudeSubRecoverGateTransportIsNotRetried proves an ambiguous live failure
// is not retried: a get_usage transport error may come from a process that is
// still alive, so the call ends.
func TestClaudeSubRecoverGateTransportIsNotRetried(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{usageErr: "usage unavailable"}, claudeSubTextTurns(t, 1))
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "usage unavailable") {
		t.Fatalf("turn error = %v, want the get_usage transport error", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (an ambiguous live failure is not retried)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
}

// TestClaudeSubRecoverUnconfirmedRetireEndsCall proves an automatic retry never
// reseeds: when the failed process cannot be proven gone the call returns its
// original failure, keeps the reseed marker for the next user request, and does
// not attempt anything else on that process.
func TestClaudeSubRecoverUnconfirmedRetireEndsCall(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	pool.confirmRetire = func(*claudeSubSession) bool { return false }
	req := ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	}
	got := claudeSubRunMeasuredCollected(t, pool, req)
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
		t.Fatalf("turn error = %v, want the original connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (an unconfirmed teardown never retries)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
	if got.final(t).Done && claudeSubRecoveryLabel(got.final(t)) != "" {
		t.Errorf("recovery = %q, want none", claudeSubRecoveryLabel(got.final(t)))
	}
	if !pool.reseedPending("parent") {
		t.Error("the unconfirmed teardown left no reseed marker for the next request")
	}

	// The next user request reseeds instead of resuming the process that may still
	// be alive.
	next := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         append(append([]Message(nil), req.Messages...), claudeSubUserText("again")),
		BackendState:     claudeSubEncodeState(t, st),
	})
	if next.err != nil {
		t.Fatalf("next turn error = %v", next.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the next request reseeds)", n)
	}
	serving := spawner.call(t, 1)
	if _, ok := claudeSubArgValue(serving.Args, "--resume"); ok {
		t.Errorf("next request resumed: %q, want a reseed", serving.Args)
	}
	if lines := claudeSubUserLines(serving.Conn); len(lines) != 1 || !strings.Contains(lines[0], "Conversation so far") {
		t.Fatalf("reseed user lines = %q, want one rendered transcript", lines)
	}
	if got := next.final(t).Diagnostics.ClaudeSub.ReseedReason; got != claudeSubReseedUnconfirmed {
		t.Errorf("reseed reason = %q, want %q", got, claudeSubReseedUnconfirmed)
	}
}

// TestClaudeSubRetryReportsReplacementOnce proves a retry reports the pending
// replacement warning once, even when the failed attempt already reported it
// after a usage-only chunk, and that the call clears the notice it reported.
func TestClaudeSubRetryReportsReplacementOnce(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	usageOnly := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl")[:2]}}
	pool, spawner := claudeSubCrashingPool(t, 1, usageOnly, claudeSubTextTurns(t, 1))
	pool.noteReplacement("parent")
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err != nil {
		t.Fatalf("turn error = %v", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the usage-only attempt and its retry)", n)
	}
	claudeSubWantFailureWarning(t, got, claudeSubReplacedProcessWarning)
	if pool.replacementPending("parent") {
		t.Error("the reported replacement notice survived the successful call")
	}
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
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
		t.Fatalf("turn error = %v, want the connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (no attempt follows visible output)", n)
	}
	var streamed strings.Builder
	for _, chunk := range got.chunks {
		if !chunk.Done {
			streamed.WriteString(chunk.Delta.Content)
		}
	}
	if streamed.String() != claudeSubStreamedText {
		t.Fatalf("streamed text = %q, want %q", streamed.String(), claudeSubStreamedText)
	}
	claudeSubWantNoRecoveryNotice(t, got)

	// The next request carries the partial the agent kept and resumes the stored
	// checkpoint in a fork, with the stopped-reply note quoting that partial.
	next := []Message{
		claudeSubUserText("read notes"),
		assistant,
		claudeSubUserText("more"),
		claudeSubRecoveryPartial(streamed.String()),
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
	// A dead process leaves nothing to replace, and the parent stop design keeps
	// that recovery silent: no warning is reported here.
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

// TestClaudeSubFailThinkingOnlyReturnsError proves thinking alone counts as
// visible output: the call ends with its error instead of retrying, and no text
// partial is preserved for the next turn.
func TestClaudeSubFailThinkingOnlyReturnsError(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	thinking := &claudeSubScript{batches: [][]claudeSubEvent{claudeSubThinkingOnlyBatch(t)}}
	pool, spawner := claudeSubCrashingPool(t, 1, thinking, claudeSubTextTurns(t, 1))
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
		t.Fatalf("turn error = %v, want the connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (thinking alone is visible output)", n)
	}
	var thought, text bool
	for _, chunk := range got.chunks {
		thought = thought || chunk.Thinking != ""
		text = text || chunk.Delta.Content != ""
	}
	if !thought || text {
		t.Errorf("streamed thinking = %v text = %v, want thinking only", thought, text)
	}
	claudeSubWantNoRecoveryNotice(t, got)
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

// TestClaudeSubEmptyDeltaSameHistoryRejected proves a request that adds nothing
// to the live record fails promptly instead of parking the call on an idle CLI,
// and that it never interrupts that process.
func TestClaudeSubEmptyDeltaSameHistoryRejected(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	st := claudeSubDecodeState(t, first.BackendState)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := claudeSubTurn(ctx, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes"), first.Delta},
		BackendState:     claudeSubEncodeState(t, st),
	}, func(ChatChunk) error { return nil })
	if !errors.Is(err, errClaudeSubEmptyDelivery) {
		t.Fatalf("turn error = %v, want %v", err, errClaudeSubEmptyDelivery)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("the empty request waited instead of failing")
	}
	conn := spawner.call(t, 0).Conn
	if got := claudeSubUsageProbeCount(conn, `"subtype":"interrupt"`); got != 0 {
		t.Errorf("interrupt requests = %d, want none for an idle process", got)
	}
	if got := len(claudeSubUserLines(conn)); got != 1 {
		t.Errorf("user lines = %d, want only the first turn's", got)
	}
}

// TestClaudeSubEmptyDeliveryRejectedBeforeWrite proves the delivery guard refuses
// a delta that would write nothing, before anything reaches the CLI.
func TestClaudeSubEmptyDeliveryRejectedBeforeWrite(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	err = claudeSubDeliver(context.Background(), s, ChatRequest{TransportSession: "parent", Model: claudeSubTestSpec().Model}, claudeSubDelta{}, func(ChatChunk) error { return nil })
	if !errors.Is(err, errClaudeSubEmptyDelivery) {
		t.Fatalf("deliver error = %v, want %v", err, errClaudeSubEmptyDelivery)
	}
	conn := spawner.call(t, 0).Conn
	if lines := claudeSubUserLines(conn); len(lines) != 0 {
		t.Errorf("user lines = %q, want none", lines)
	}
	if got := claudeSubUsageProbeCount(conn, `"subtype":"interrupt"`); got != 0 {
		t.Errorf("interrupt requests = %d, want none", got)
	}
}

// TestClaudeSubStalePendingAfterTerminalDoesNotInterrupt proves the stale-call
// cleanup never interrupts a CLI that already reported its terminal result: the
// handles are retired, nothing is marked drained, and the next request follows the
// ordinary mismatch and resume rules.
func TestClaudeSubStalePendingAfterTerminalDoesNotInterrupt(t *testing.T) {
	model := claudeSubTestSpec().Model
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	// The opening turn runs with the same tools, so the stored prompt/tools
	// fingerprint matches this request and it resumes the checkpoint.
	openPool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	opened := claudeSubRunTurn(t, openPool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	assistant, st := opened.Delta, claudeSubDecodeState(t, opened.BackendState)
	batch := append(claudeSubDecodeEvents(t, "turn_tool_use.jsonl")[:5], claudeSubMaxTurnsResult())
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{batch}},
		claudeSubTextTurns(t, 1),
	)
	history := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	failed := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         history,
		BackendState:     claudeSubEncodeState(t, st),
	})
	if failed.err == nil {
		t.Fatal("tool turn error = nil, want the terminal failure")
	}
	if !claudeSubLiveSession(pool, "parent").delivery.terminal {
		t.Fatal("the failed delivery did not read its terminal result")
	}
	conn := spawner.call(t, 0).Conn
	// The agent never received a tool call, so its next request drops the call and
	// adds a message.
	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Tools:            tools,
		Messages:         append(append([]Message(nil), history...), claudeSubUserText("again")),
		BackendState:     claudeSubEncodeState(t, st),
	})
	if next.err != nil {
		t.Fatalf("next turn error = %v", next.err)
	}
	if got := claudeSubUsageProbeCount(conn, `"subtype":"interrupt"`); got != 0 {
		t.Errorf("interrupt requests = %d, want none: the turn already ended", got)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the ended process and its resume)", n)
	}
	if resume, _ := claudeSubArgValue(spawner.call(t, 1).Args, "--resume"); resume != st.CLISessionID {
		t.Errorf("next spawn --resume = %q, want %q", resume, st.CLISessionID)
	}
	if final := next.final(t); final.Delta.Content != "Hello world" {
		t.Errorf("next content = %q, want %q", final.Delta.Content, "Hello world")
	}
}

// TestClaudeSubAPIRetryOverageExemption proves an api_retry notice whose own
// error text starts with an explicit overage notice is treated as one, while a
// plain retry, an unrelated word and a non-string error stay retries.
func TestClaudeSubAPIRetryOverageExemption(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "plain retry", line: claudeSubRetryOverloaded},
		{name: "usage credits notice", line: `{"type":"system","subtype":"api_retry","attempt":1,"error":"You're now using usage credits","uuid":"r1"}`, want: true},
		{name: "extra usage notice", line: `{"type":"system","subtype":"api_retry","attempt":1,"error":"now using extra usage"}`, want: true},
		{name: "unrelated error word", line: `{"type":"system","subtype":"api_retry","attempt":1,"error":"usage credits are enabled"}`},
		{name: "numeric error stays a retry", line: `{"type":"system","subtype":"api_retry","attempt":1,"error":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := claudeSubEvent{Type: "system", Subtype: "api_retry", Raw: json.RawMessage(tt.line)}
			if got := claudeSubEventIsOverage(ev); got != tt.want {
				t.Errorf("claudeSubEventIsOverage(%s) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

// TestClaudeSubOversizedUserLineIsNotARetry proves the CLI's stdin limit
// refusal is reported as a limit refusal and never as a closed connection, so
// the turn is not retried.
func TestClaudeSubOversizedUserLineIsNotARetry(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	spawner.onSpawn = func(c *claudeSubFakeConn) {
		index := len(spawner.calls)
		c.mu.Lock()
		c.responder = func(line []byte) []claudeSubEvent {
			var env struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(line, &env) != nil || !strings.Contains(string(line), `"subtype":"get_usage"`) {
				return nil
			}
			if index == 0 {
				// The CLI refuses the user line as oversized.
				c.mu.Lock()
				c.sendErr = &claudeSubLineTooLargeError{limit: 512}
				c.mu.Unlock()
			}
			return []claudeSubEvent{claudeSubSuccessEvent(env.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
		}
		c.mu.Unlock()
	}
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText(strings.Repeat("a", 2048))},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil {
		t.Fatal("turn error = nil, want the stdin limit refusal")
	}
	var tooLarge *claudeSubLineTooLargeError
	if !errors.As(got.err, &tooLarge) {
		t.Fatalf("turn error = %v, want a *claudeSubLineTooLargeError", got.err)
	}
	if errors.Is(got.err, errClaudeSubConnClosed) {
		t.Errorf("limit refusal %v was labelled a closed connection", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (an oversized line is not retried)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
}

// TestClaudeSubToolResultsThenFailedWriteAbandonsSession proves a delivery that
// already handed the CLI its tool results and then cannot write its user line
// abandons the process: nothing may be replayed on a CLI that may hold an
// unconsumed turn.
func TestClaudeSubToolResultsThenFailedWriteAbandonsSession(t *testing.T) {
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = tools
	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn
	conn.mu.Lock()
	conn.responder = func(line []byte) []claudeSubEvent {
		var env struct {
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(line, &env) != nil || !strings.Contains(string(line), `"subtype":"get_usage"`) {
			return nil
		}
		// The usage gate answers, then the user write fails.
		conn.mu.Lock()
		conn.sendErr = errors.New("fake write failed")
		conn.mu.Unlock()
		return []claudeSubEvent{claudeSubSuccessEvent(env.RequestID, json.RawMessage(`{"rate_limits_available":true,"rate_limits":{"extra_usage":{"is_enabled":false}}}`))}
	}
	conn.mu.Unlock()
	if call := s.beginPendingCall("toolu_1"); call == nil {
		t.Fatal("beginPendingCall returned no handle")
	}
	delta := claudeSubDelta{
		ToolResults: []Message{{Role: MessageRoleTool, ToolCallID: "toolu_1", Content: "notes body"}},
		User:        []Message{claudeSubUserText("more")},
	}
	err = claudeSubDeliver(context.Background(), s, ChatRequest{TransportSession: "parent", Model: spec.Model, Tools: tools}, delta, func(ChatChunk) error { return nil })
	if !errors.Is(err, errClaudeSubUnconsumedTurn) {
		t.Fatalf("deliver error = %v, want %v", err, errClaudeSubUnconsumedTurn)
	}
	if !s.unsafeForReuse() {
		t.Error("the session was left reusable with a possibly active turn")
	}
	if !pool.replacementPending("parent") {
		t.Error("the abandoned session recorded no replacement notice")
	}
	if got := claudeSubRetryDecision(err, claudeSubRetryFacts{CallKind: claudeSubCallKindNormal, Restorable: true}); got != claudeSubRetryNone {
		t.Errorf("decision = %v, want no retry for an unconsumed turn", got)
	}
	pool.release(s)
}

// TestClaudeSubCrashResultWithAPIFailureTextFailsClosed proves a result that
// names a CLI API failure but carries no terminal reason is not retried: the
// wording is ambiguous, so the turn ends.
func TestClaudeSubCrashResultWithAPIFailureTextFailsClosed(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	for _, text := range []string{
		"API Error: Server error mid-response. The response above may be incomplete.",
		"API Error: 500 internal error",
	} {
		t.Run(text, func(t *testing.T) {
			pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{{claudeSubCrashResult(text)}}}, claudeSubTextTurns(t, 1))
			got := claudeSubRunCollected(t, pool, ChatRequest{
				TransportSession: "parent",
				Model:            claudeSubTestSpec().Model,
				Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
				BackendState:     claudeSubEncodeState(t, st),
			})
			if got.err == nil || !strings.Contains(got.err.Error(), text) {
				t.Fatalf("turn error = %v, want the API failure text", got.err)
			}
			if n := spawner.callCount(); n != 1 {
				t.Fatalf("spawn count = %d, want 1 (ambiguous API failure text fails closed)", n)
			}
			claudeSubWantNoRecoveryNotice(t, got)
		})
	}
}

// TestClaudeSubRetryForkOnlyWhenMarkerSetDuringBackoff proves a retry enforces
// the fork-only rule at attach: when another same-key call records a reseed
// requirement while this call waits to retry, the retry fails with the original
// error, starts no process, and leaves the marker for the next user request.
func TestClaudeSubRetryForkOnlyWhenMarkerSetDuringBackoff(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	pool.confirmRetire = func(*claudeSubSession) bool {
		// Another call records the requirement between this attempt and its retry.
		pool.noteReseedKey("parent")
		return true
	}
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
		t.Fatalf("turn error = %v, want the original connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (a retry never reseeds)", n)
	}
	if !pool.reseedPending("parent") {
		t.Error("the retry cleared the reseed marker another call recorded")
	}
	claudeSubWantNoRecoveryNotice(t, got)
}

// claudeSubForkOnlyAttach builds the attach input a retry runs with: the stored
// checkpoint of an opening turn and the request that extends it.
func claudeSubForkOnlyAttach(t *testing.T, msgs []Message, st claudeSubBackendState) claudeSubAttach {
	t.Helper()
	spec := claudeSubStartSpec{Model: claudeSubTestSpec().Model, SystemPrompt: claudeSubSystemPrompt(msgs)}
	fingerprint, err := claudeSubFingerprint(spec.SystemPrompt, spec.Tools)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	a := claudeSubAttach{key: "parent", spec: spec, fingerprint: fingerprint, msgs: msgs, stored: st, hasState: true, partial: -1}
	a.resume, a.restorable, _, err = claudeSubRestoreFor(msgs, st, true, "parent", fingerprint)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !a.restorable {
		t.Fatal("the opening turn's stored state is not restorable")
	}
	return a
}

// TestClaudeSubAttachForkOnlyRefusesFreshSpawn proves a fork-only attach never
// starts a fresh conversation: a request with nothing to resume, and a forced
// reseed, both fail before a process exists.
func TestClaudeSubAttachForkOnlyRefusesFreshSpawn(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	fork := claudeSubForkOnlyAttach(t, msgs, st)
	req := ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         msgs,
		BackendState:     claudeSubEncodeState(t, st),
	}
	tests := []struct {
		name        string
		mutate      func(*claudeSubAttach)
		forceReseed bool
	}{
		{name: "nothing to resume", mutate: func(a *claudeSubAttach) { a.resume, a.restorable = claudeSubResume{}, false }},
		{name: "forced reseed", forceReseed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
			a := fork
			if tt.mutate != nil {
				tt.mutate(&a)
			}
			if _, err := claudeSubAttachSession(context.Background(), pool, req, a, tt.forceReseed, true); !errors.Is(err, errClaudeSubRetryNeedsReseed) {
				t.Fatalf("attach error = %v, want errClaudeSubRetryNeedsReseed", err)
			}
			if n := spawner.callCount(); n != 0 {
				t.Errorf("spawn count = %d, want 0 (a retry never starts a fresh conversation)", n)
			}
			if pool.reseedPending("parent") {
				t.Error("a refused attach recorded a reseed requirement")
			}
		})
	}
}

// TestClaudeSubAttachForkOnlyRefusesLiveProcess proves a fork-only attach never
// delivers on a pooled live process, even one a normal request would reuse: a
// retry resumes the stored checkpoint in a fork or refuses, and the process is
// left for the request it belongs to.
func TestClaudeSubAttachForkOnlyRefusesLiveProcess(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	pool, spawner := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	// One normal turn leaves a primed process pooled under the key.
	claudeSubRunTurn(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	fork := claudeSubForkOnlyAttach(t, msgs, st)
	req := ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         msgs,
		BackendState:     claudeSubEncodeState(t, st),
	}
	got, err := claudeSubAttachSession(context.Background(), pool, req, fork, false, true)
	if !errors.Is(err, errClaudeSubRetryNeedsReseed) {
		t.Fatalf("attach error = %v, want errClaudeSubRetryNeedsReseed", err)
	}
	if got.session != nil {
		t.Error("a refused attach returned a process")
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (a refused retry starts no process)", n)
	}
	// The primed process is untouched, so the request that owns it still finds it.
	live, err := pool.acquireLive(context.Background(), "parent")
	if err != nil || live == nil {
		t.Fatalf("acquireLive = %v, %v, want the pooled process", live, err)
	}
	pool.release(live)
	if pool.reseedPending("parent") {
		t.Error("a refused attach recorded a reseed requirement")
	}
}

// TestClaudeSubAttachForkOnlyRefusesMarkerSetUnderLease proves the fork-only
// check made after the process is leased: a reseed requirement another call
// records while the retry waits for that lease ends the attach and keeps the
// marker for the next user request.
func TestClaudeSubAttachForkOnlyRefusesMarkerSetUnderLease(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	msgs := []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")}
	fork := claudeSubForkOnlyAttach(t, msgs, st)
	req := ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         msgs,
		BackendState:     claudeSubEncodeState(t, st),
	}
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	holder, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}
	attachErr := make(chan error, 1)
	go func() {
		_, err := claudeSubAttachSession(context.Background(), pool, req, fork, false, true)
		attachErr <- err
	}()
	// The retry passed its pre-acquire check, took its reservation, and is parked
	// on the lease this test holds.
	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return pool.active >= 2
	})
	// Another same-key call records the requirement while the retry waits.
	pool.noteReseedKey("parent")
	select {
	case err := <-attachErr:
		t.Fatalf("attach = %v while the lease was held, want it parked on the lease", err)
	default:
	}
	pool.release(holder)
	select {
	case err := <-attachErr:
		if !errors.Is(err, errClaudeSubRetryNeedsReseed) {
			t.Fatalf("attach error = %v, want errClaudeSubRetryNeedsReseed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attach did not settle after the lease was released")
	}
	if !pool.reseedPending("parent") {
		t.Error("the refused attach cleared the marker another call recorded")
	}
	if n := spawner.callCount(); n != 1 {
		t.Errorf("spawn count = %d, want 1 (only the process the holder owns)", n)
	}
}

// TestClaudeSubRetryDeniedAttachKeepsFailure proves a retry whose attach refuses
// fork-only recovery reports nothing it did not do: the call returns the
// original failure, emits no retry notice, records no recovery, and keeps the
// marker for the next user request.
func TestClaudeSubRetryDeniedAttachKeepsFailure(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	retrying := false
	pool.confirmRetire = func(*claudeSubSession) bool {
		retrying = true
		return true
	}
	pool.afterAdmission = func() {
		// The retry's process is published and leased. Another same-key call
		// records the requirement before the attach's under-lease check.
		if retrying {
			pool.noteReseedKey("parent")
		}
	}
	got := claudeSubRunMeasuredCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	})
	if got.err == nil || !strings.Contains(got.err.Error(), "claude CLI connection closed") {
		t.Fatalf("turn error = %v, want the original connection-closed failure", got.err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Errorf("spawn count = %d, want 2 (the crash and the refused retry's process)", n)
	}
	claudeSubWantNoRecoveryNotice(t, got)
	if final := got.final(t); final.Done && claudeSubRecoveryLabel(final) != "" {
		t.Errorf("recovery = %q, want none (no retry was delivered)", claudeSubRecoveryLabel(final))
	}
	if !pool.reseedPending("parent") {
		t.Error("the refused retry cleared the reseed marker another call recorded")
	}
}

// TestClaudeSubRetryNoticeEmitFailureReleasesLease proves a retry whose notice
// cannot be emitted ends the call without keeping the process lease: the attempt
// never wrote to the CLI, so the forked process stays reusable and the same key
// can be acquired again immediately.
func TestClaudeSubRetryNoticeEmitFailureReleasesLease(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubCrashingPool(t, 1, &claudeSubScript{}, claudeSubTextTurns(t, 1))
	noticeErr := errors.New("stream closed")
	err := claudeSubTurn(context.Background(), pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	}, func(chunk ChatChunk) error {
		if claudeSubRecoveryNoticeChunk(chunk) {
			return noticeErr
		}
		return nil
	})
	if !errors.Is(err, noticeErr) {
		t.Fatalf("turn error = %v, want the notice emit failure", err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (the crash and its forked retry)", n)
	}
	// The lease is free, so a later call on the same key acquires it instead of
	// parking on a lease the failed call never released.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := pool.acquireLive(ctx, "parent")
	if err != nil {
		t.Fatalf("acquireLive after the failed notice = %v, want the released process", err)
	}
	if s == nil {
		t.Fatal("acquireLive after the failed notice = nil, want the released process")
	}
	pool.release(s)
}
