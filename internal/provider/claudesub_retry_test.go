package provider

import (
	"encoding/json"
	"testing"
	"time"
)

// The CLI's api_retry notices, as scripted for the fake CLI. The malformed line
// has a string attempt, which the decoder must drop instead of failing the turn.
const (
	claudeSubRetryOverloaded = `{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":1500,"error_status":529,"error":"overloaded","uuid":"retry-1","session_id":"sess-1"}`
	claudeSubRetryRateLimit  = `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"retry_delay_ms":250,"error_status":null,"error":"rate_limit","uuid":"retry-2","session_id":"sess-1"}`
	claudeSubRetryMalformed  = `{"type":"system","subtype":"api_retry","attempt":"first","error":"unknown","uuid":"retry-3","session_id":"sess-1"}`
)

// claudeSubAPIRetryEvents builds stream-json envelopes from raw api_retry lines.
func claudeSubAPIRetryEvents(t *testing.T, lines ...string) []claudeSubEvent {
	t.Helper()
	events := make([]claudeSubEvent, 0, len(lines))
	for _, line := range lines {
		var env struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			UUID      string `json:"uuid"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse api_retry event %q: %v", line, err)
		}
		events = append(events, claudeSubEvent{Type: env.Type, Subtype: env.Subtype, UUID: env.UUID, SessionID: env.SessionID, Raw: json.RawMessage(line)})
	}
	return events
}

// claudeSubTurnWithAPIRetries returns the turn_text batch with API retry notices
// inserted after init, where the CLI emits them: before it streams any content.
func claudeSubTurnWithAPIRetries(t *testing.T, lines ...string) []claudeSubEvent {
	t.Helper()
	events := claudeSubDecodeEvents(t, "turn_text.jsonl")
	out := make([]claudeSubEvent, 0, len(events)+len(lines))
	out = append(out, events[0])
	out = append(out, claudeSubAPIRetryEvents(t, lines...)...)
	return append(out, events[1:]...)
}

// claudeSubRetryThenFailBatch returns init, API retry notices and then a failed
// terminal result: a resumed process that only retried before it failed.
func claudeSubRetryThenFailBatch(t *testing.T, lines ...string) []claudeSubEvent {
	t.Helper()
	init := claudeSubDecodeEvents(t, "turn_text.jsonl")[0]
	out := []claudeSubEvent{init}
	out = append(out, claudeSubAPIRetryEvents(t, lines...)...)
	return append(out, claudeSubFailedResumeBatch()...)
}

// TestDecodeClaudeSubAPIRetry proves the decoder maps every api_retry field and
// ignores a malformed notice or an unrelated system envelope instead of failing
// the turn.
func TestDecodeClaudeSubAPIRetry(t *testing.T) {
	tests := []struct {
		name string
		line string
		want *claudeSubAPIRetry
	}{
		{
			name: "status and delay",
			line: claudeSubRetryOverloaded,
			want: &claudeSubAPIRetry{Attempt: 1, MaxRetries: 10, RetryDelay: 1500 * time.Millisecond, ErrorStatus: 529, Error: "overloaded"},
		},
		{
			name: "null status",
			line: claudeSubRetryRateLimit,
			want: &claudeSubAPIRetry{Attempt: 2, MaxRetries: 10, RetryDelay: 250 * time.Millisecond, Error: "rate_limit"},
		},
		{
			name: "negative delay is clamped",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"retry_delay_ms":-5,"error":"unknown"}`,
			want: &claudeSubAPIRetry{Attempt: 1, Error: "unknown"},
		},
		{
			name: "overflowing delay is clamped",
			line: `{"type":"system","subtype":"api_retry","attempt":1,"retry_delay_ms":9223372036854775807,"error":"unknown"}`,
			want: &claudeSubAPIRetry{Attempt: 1, RetryDelay: time.Duration(claudeSubMaxRetryDelayMS) * time.Millisecond, Error: "unknown"},
		},
		{name: "malformed attempt is ignored", line: claudeSubRetryMalformed},
		{name: "missing attempt is ignored", line: `{"type":"system","subtype":"api_retry","max_retries":10,"error":"unknown"}`},
		{name: "other system envelope is ignored", line: `{"type":"system","subtype":"notification","message":"hello"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := claudeSubAPIRetryEvents(t, tt.line)
			got, err := newClaudeSubDecoder(claudeSubDecodeHooks{}).decode(events[0])
			if err != nil {
				t.Fatalf("decode system envelope: %v", err)
			}
			if tt.want == nil {
				if len(got) != 0 {
					t.Fatalf("decoded %+v, want no events", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != claudeSubDecodeAPIRetry || got[0].Retry == nil {
				t.Fatalf("decoded %+v, want one api_retry event", got)
			}
			if *got[0].Retry != *tt.want {
				t.Errorf("retry = %+v, want %+v", *got[0].Retry, *tt.want)
			}
		})
	}
}

// TestClaudeSubAPIRetryReported proves the CLI's own API retries reach the caller
// as warning diagnostics carrying their attempt, limit and delay; a malformed
// notice is ignored and the turn's content is unchanged.
func TestClaudeSubAPIRetryReported(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, _ := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTurnWithAPIRetries(t, claudeSubRetryOverloaded, claudeSubRetryRateLimit, claudeSubRetryMalformed),
	}})
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         []Message{claudeSubUserText("read notes")},
	})
	if got.err != nil {
		t.Fatalf("turn error = %v", got.err)
	}
	want := []struct {
		attempt int
		max     int
		delay   time.Duration
		text    string
	}{
		{
			attempt: 1,
			max:     10,
			delay:   1500 * time.Millisecond,
			text:    "claude CLI retrying API request after overloaded (status 529), attempt 1/10, next try in 1.5s",
		},
		{
			attempt: 2,
			max:     10,
			delay:   250 * time.Millisecond,
			text:    "claude CLI retrying API request after rate_limit, attempt 2/10, next try in 250ms",
		},
	}
	var retries []ChatChunk
	for _, chunk := range got.chunks {
		if chunk.RetryAttempt > 0 {
			retries = append(retries, chunk)
		}
	}
	if len(retries) != len(want) {
		t.Fatalf("retry chunks = %+v, want %d", retries, len(want))
	}
	for i, w := range want {
		chunk := retries[i]
		if chunk.RetryAttempt != w.attempt || chunk.RetryMaxAttempts != w.max || chunk.RetryDelay != w.delay {
			t.Errorf("retry chunk %d = attempt %d/%d in %s, want %d/%d in %s",
				i, chunk.RetryAttempt, chunk.RetryMaxAttempts, chunk.RetryDelay, w.attempt, w.max, w.delay)
		}
		if chunk.Diagnostic != w.text {
			t.Errorf("retry chunk %d diagnostic = %q, want %q", i, chunk.Diagnostic, w.text)
		}
		if chunk.Severity != "warning" {
			t.Errorf("retry chunk %d severity = %q, want warning", i, chunk.Severity)
		}
		if chunk.RetryReset {
			t.Errorf("retry chunk %d reset the stream; the CLI retries before output", i)
		}
		if chunk.Done || chunk.Delta.Content != "" {
			t.Errorf("retry chunk %d carries output: %+v", i, chunk)
		}
	}
	if text := got.final(t).Delta.Content; text != "Hello world" {
		t.Errorf("final content = %q, want %q", text, "Hello world")
	}
}

// TestClaudeSubAPIRetryReportedForSubAgent proves a sub-agent call surfaces the
// CLI's own API retries the same way a parent call does.
func TestClaudeSubAPIRetryReportedForSubAgent(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, _ := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{
		claudeSubTurnWithAPIRetries(t, claudeSubRetryOverloaded),
	}})
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession:       "parent-agent-1",
		ParentTransportSession: "parent",
		Model:                  model,
		Messages:               []Message{claudeSubUserText("read notes")},
	})
	if got.err != nil {
		t.Fatalf("sub-agent turn error = %v", got.err)
	}
	var retries []ChatChunk
	for _, chunk := range got.chunks {
		if chunk.RetryAttempt > 0 {
			retries = append(retries, chunk)
		}
	}
	if len(retries) != 1 {
		t.Fatalf("retry chunks = %+v, want one", retries)
	}
	if retries[0].RetryAttempt != 1 || retries[0].RetryMaxAttempts != 10 || retries[0].RetryDelay != 1500*time.Millisecond {
		t.Errorf("retry chunk = %+v, want attempt 1/10 in 1.5s", retries[0])
	}
}

// TestClaudeSubAPIRetryDisablesSteinerRetry proves the CLI's own retries spend
// the retry budget: a resumed process that already retried an API request and
// then failed before output is not replaced, because a second process would only
// repeat the wait.
func TestClaudeSubAPIRetryDisablesSteinerRetry(t *testing.T) {
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubRetryThenFailBatch(t, claudeSubRetryOverloaded, claudeSubRetryRateLimit)}},
		claudeSubTextTurns(t, 1),
	)
	got := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            claudeSubTestSpec().Model,
		BackendState:     claudeSubEncodeState(t, st),
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
	})
	if got.err == nil {
		t.Fatal("turn error = nil, want the resumed failure")
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn count = %d, want 1 (the CLI retried already, so steiner must not replace the process)", n)
	}
	var retries int
	for _, chunk := range got.chunks {
		if chunk.RetryAttempt > 0 {
			retries++
		}
	}
	if retries != 2 {
		t.Errorf("retry chunks = %d, want the two notices the failed attempt reported", retries)
	}
}

// TestClaudeSubAPIRetryWarningWaitsForOutput proves a retry notice does not let a
// pending replacement warning stand in for output an attempt never produced: the
// attempt that only retried reports nothing, and the next request that succeeds
// reports the warning once.
func TestClaudeSubAPIRetryWarningWaitsForOutput(t *testing.T) {
	model := claudeSubTestSpec().Model
	pool, spawner := claudeSubPoolWithScripts(t,
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubDecodeEvents(t, "turn_text.jsonl"), nil}},
		&claudeSubScript{batches: [][]claudeSubEvent{claudeSubRetryThenFailBatch(t, claudeSubRetryOverloaded), claudeSubDecodeEvents(t, "turn_text.jsonl")}},
		claudeSubTextTurns(t, 1),
	)
	first := claudeSubRunTurn(t, pool, ChatRequest{TransportSession: "parent", Model: model, Messages: []Message{claudeSubUserText("read notes")}})
	stored := claudeSubDecodeState(t, first.BackendState)
	history := []Message{claudeSubUserText("read notes"), first.Delta, claudeSubUserText("more")}
	if err := claudeSubCancelMidTurn(t, pool, spawner.call(t, 0).Conn, 4, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         history,
	}); err == nil {
		t.Fatal("cancelled turn returned no error")
	}

	// The replacement attempt retries an API request itself and then fails before
	// producing output, so it must report no replacement warning: nothing reached
	// the user, and the notice stays pending for the next request.
	replacementWarnings := func(got claudeSubCollected) []string {
		want := claudeSubWarningChunk(claudeSubReplacedProcessWarning).Diagnostic
		var out []string
		for _, chunk := range got.chunks {
			if chunk.Diagnostic == want {
				out = append(out, chunk.Diagnostic)
			}
		}
		return out
	}
	retried := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		BackendState:     claudeSubEncodeState(t, stored),
		Messages:         append(append([]Message(nil), history...), claudeSubUserText("again")),
	})
	if retried.err == nil {
		t.Fatal("retried turn error = nil, want the resumed failure")
	}
	if warnings := replacementWarnings(retried); len(warnings) != 0 {
		t.Fatalf("retried attempt replacement warnings = %q, want none", warnings)
	}

	next := claudeSubRunCollected(t, pool, ChatRequest{
		TransportSession: "parent",
		Model:            model,
		Messages:         append(append([]Message(nil), history...), claudeSubUserText("again"), claudeSubUserText("please retry")),
	})
	if next.err != nil {
		t.Fatalf("next turn error = %v", next.err)
	}
	warnings := append(replacementWarnings(retried), replacementWarnings(next)...)
	if len(warnings) != 1 {
		t.Fatalf("replacement warnings = %q, want exactly one across both requests", warnings)
	}
	if want := claudeSubWarningChunk(claudeSubReplacedProcessWarning).Diagnostic; warnings[0] != want {
		t.Errorf("warning = %q, want %q", warnings[0], want)
	}
	if pool.replacementPending("parent") {
		t.Error("the replacement notice survived being reported")
	}
}

// TestDecodeClaudeSubSystemPhases proves only the streaming phase surfaces the
// CLI's API retry, and that every other system subtype is ignored in every phase.
func TestDecodeClaudeSubSystemPhases(t *testing.T) {
	subtypes := []struct {
		name string
		line string
	}{
		{name: "api_retry", line: claudeSubRetryOverloaded},
		{name: "init", line: `{"type":"system","subtype":"init","session_id":"sess-1"}`},
		{name: "status", line: `{"type":"system","subtype":"status","message":"busy"}`},
		{name: "notification", line: `{"type":"system","subtype":"notification","message":"hello"}`},
		{name: "thinking_tokens", line: `{"type":"system","subtype":"thinking_tokens","tokens":12}`},
	}
	phases := []struct {
		name  string
		phase claudeSubDecodePhase
	}{
		{name: "streaming", phase: claudeSubPhaseStreaming},
		{name: "stopped", phase: claudeSubPhaseStopped},
		{name: "terminal", phase: claudeSubPhaseTerminal},
	}
	for _, phase := range phases {
		for _, subtype := range subtypes {
			t.Run(phase.name+"/"+subtype.name, func(t *testing.T) {
				want := 0
				if phase.phase == claudeSubPhaseStreaming && subtype.name == "api_retry" {
					want = 1
				}
				d := newClaudeSubDecoder(claudeSubDecodeHooks{})
				d.phase = phase.phase
				got, err := d.decode(claudeSubAPIRetryEvents(t, subtype.line)[0])
				if err != nil {
					t.Fatalf("decode system envelope: %v", err)
				}
				if len(got) != want {
					t.Fatalf("decoded %d items, want %d: %+v", len(got), want, got)
				}
				if want == 1 && got[0].Kind != claudeSubDecodeAPIRetry {
					t.Errorf("decoded kind = %v, want api retry", got[0].Kind)
				}
			})
		}
	}
}

// TestClaudeSubAPIRetryMessage proves the wording states only what is known: the
// failure category, the status when there was a response, the attempt against the
// limit when the CLI reported one, and the delay.
func TestClaudeSubAPIRetryMessage(t *testing.T) {
	tests := []struct {
		name  string
		retry claudeSubAPIRetry
		want  string
	}{
		{
			name:  "category, status and limit",
			retry: claudeSubAPIRetry{Attempt: 1, MaxRetries: 10, RetryDelay: 1500 * time.Millisecond, ErrorStatus: 529, Error: "overloaded"},
			want:  "claude CLI retrying API request after overloaded (status 529), attempt 1/10, next try in 1.5s",
		},
		{
			name:  "no status and no limit",
			retry: claudeSubAPIRetry{Attempt: 2, RetryDelay: 250 * time.Millisecond, Error: "rate_limit"},
			want:  "claude CLI retrying API request after rate_limit, attempt 2, next try in 250ms",
		},
		{
			name:  "unknown category",
			retry: claudeSubAPIRetry{Attempt: 1, MaxRetries: 3},
			want:  "claude CLI retrying API request after unknown, attempt 1/3, next try in 0s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubAPIRetryMessage(tt.retry); got != tt.want {
				t.Errorf("message = %q, want %q", got, tt.want)
			}
		})
	}
}
