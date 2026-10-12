package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
)

// TestClaudeSubProviderEmitsOneDiagnosticRecord proves each claude_subscription
// turn writes exactly one provider-stream record through the provider's logger,
// on success, decode failure, and cancellation, and that the record carries the
// CLI transport and no request or response content.
func TestClaudeSubProviderEmitsOneDiagnosticRecord(t *testing.T) {
	turn := claudeSubDecodeEvents(t, "turn_text.jsonl")
	// A duplicate message_start after the text block is a decode error, as in
	// TestClaudeSubDecodeErrorRetiresProcess. The text before it is emitted.
	broken := append([]claudeSubEvent{}, turn[:8]...)
	broken = append(broken, turn[1], turn[11])

	tests := []struct {
		name           string
		req            ChatRequest
		script         *claudeSubScript
		cancelled      bool
		wantErr        bool
		wantOutcome    string
		wantCallKind   string
		wantErrorClass string
		wantPartial    bool
	}{
		{
			name:         "success",
			req:          ChatRequest{TransportSession: "diag", Messages: []Message{claudeSubUserText("marker-prompt")}},
			script:       claudeSubTextTurns(t, 1),
			wantOutcome:  "ok",
			wantCallKind: "normal",
		},
		{
			name:         "sub-agent success",
			req:          ChatRequest{ParentTransportSession: "diag-parent", Messages: []Message{claudeSubUserText("marker-prompt")}},
			script:       claudeSubTextTurns(t, 1),
			wantOutcome:  "ok",
			wantCallKind: "sub_agent",
		},
		{
			name:           "decode error",
			req:            ChatRequest{TransportSession: "diag-decode", Messages: []Message{claudeSubUserText("marker-prompt")}},
			script:         &claudeSubScript{batches: [][]claudeSubEvent{broken}},
			wantErr:        true,
			wantOutcome:    "exhausted",
			wantCallKind:   "normal",
			wantErrorClass: string(errorClassDecodeError),
			wantPartial:    true,
		},
		{
			name:           "cancelled",
			req:            ChatRequest{TransportSession: "diag-cancel", Messages: []Message{claudeSubUserText("marker-prompt")}},
			script:         claudeSubTextTurns(t, 1),
			cancelled:      true,
			wantErr:        true,
			wantOutcome:    "cancelled",
			wantCallKind:   "normal",
			wantErrorClass: string(errorClassContextCancelled),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := claudeSubPoolWithScripts(t, tt.script)
			logPath := filepath.Join(t.TempDir(), "stream-errors.log")
			streamLog, err := NewStreamErrorLogger(logPath)
			if err != nil {
				t.Fatalf("NewStreamErrorLogger() error = %v", err)
			}
			defer func() { _ = streamLog.Close() }()

			req := tt.req
			req.Model = claudeSubTestSpec().Model
			ctx := context.Background()
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err = NewClaudeSubscriptionProvider(pool, streamLog).ChatCompletion(ctx, req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ChatCompletion() error = %v, wantErr %v", err, tt.wantErr)
			}

			records := readStreamErrorRecords(t, logPath)
			if len(records) != 1 {
				t.Fatalf("stream error log records = %d, want 1", len(records))
			}
			rec := records[0]
			if got, want := rec.Provider, string(config.ProviderTypeClaudeSubscription); got != want {
				t.Errorf("provider = %q, want %q", got, want)
			}
			if got, want := rec.Model, req.Model; got != want {
				t.Errorf("model = %q, want %q", got, want)
			}
			if got, want := rec.Transport, "cli"; got != want {
				t.Errorf("transport = %q, want %q", got, want)
			}
			if got, want := rec.Attempts, 1; got != want {
				t.Errorf("attempts = %d, want %d", got, want)
			}
			if got := rec.Outcome; got != tt.wantOutcome {
				t.Errorf("outcome = %q, want %q", got, tt.wantOutcome)
			}
			if got := rec.CallKind; got != tt.wantCallKind {
				t.Errorf("call_kind = %q, want %q", got, tt.wantCallKind)
			}
			if got := rec.ErrorClass; got != tt.wantErrorClass {
				t.Errorf("error_class = %q, want %q", got, tt.wantErrorClass)
			}
			if got := rec.PartialStream; got != tt.wantPartial {
				t.Errorf("partial_stream = %v, want %v", got, tt.wantPartial)
			}
			if tt.wantErr && (rec.Error == "" || utf8.RuneCountInString(rec.Error) > maxErrorLength) {
				t.Errorf("error = %q, want non-empty and bounded to %d runes", rec.Error, maxErrorLength)
			}
			if !tt.wantErr && rec.Error != "" {
				t.Errorf("error = %q, want empty on success", rec.Error)
			}

			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read stream error log: %v", err)
			}
			for _, banned := range []string{"request_url", "request_headers", "request_body", "response_headers", "marker-prompt", "Hello world"} {
				if strings.Contains(string(raw), banned) {
					t.Errorf("record contains %q, want no request or response content: %s", banned, raw)
				}
			}
		})
	}
}

// claudeSubDiagLogger returns a stream-error logger writing to a fresh file and
// the file's path.
func claudeSubDiagLogger(t *testing.T) (*StreamErrorLogger, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "stream-errors.log")
	streamLog, err := NewStreamErrorLogger(logPath)
	if err != nil {
		t.Fatalf("NewStreamErrorLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = streamLog.Close() })
	return streamLog, logPath
}

// claudeSubDiagOneRecord returns the only provider record in path.
func claudeSubDiagOneRecord(t *testing.T, path string) streamErrorRecord {
	t.Helper()
	records := readStreamErrorRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("stream error log records = %d, want 1", len(records))
	}
	return records[0]
}

// TestClaudeSubDiagnosticsCancelAfterChunk proves a cancel after a chunk reached
// the caller records a cancelled outcome with partial_stream set.
func TestClaudeSubDiagnosticsCancelAfterChunk(t *testing.T) {
	pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
	streamLog, logPath := claudeSubDiagLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := claudeSubStream(ctx, pool, ChatRequest{TransportSession: "diag-cancel-chunk", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("marker-prompt")}}, streamLog)
	if err != nil {
		t.Fatalf("claudeSubStream() error = %v", err)
	}
	<-stream
	// The producer now blocks on its next send. Cancel, then wait for the record
	// without reading, so that send can only take the ctx.Done branch.
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, statErr := os.Stat(logPath); statErr == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no stream record after cancel")
		}
		time.Sleep(time.Millisecond)
	}
	for range stream {
	}
	rec := claudeSubDiagOneRecord(t, logPath)
	if rec.Outcome != "cancelled" || rec.ErrorClass != string(errorClassContextCancelled) {
		t.Errorf("outcome = %q error_class = %q, want cancelled and %s", rec.Outcome, rec.ErrorClass, errorClassContextCancelled)
	}
	if !rec.PartialStream || rec.Chunks != 1 {
		t.Errorf("partial_stream = %v chunks = %d, want true and 1", rec.PartialStream, rec.Chunks)
	}
	if rec.Attempts != 1 || rec.CallKind != "normal" {
		t.Errorf("attempts = %d call_kind = %q, want 1 and normal", rec.Attempts, rec.CallKind)
	}
}

// TestClaudeSubDiagnosticsCallKind proves advisor and summary calls each write
// one record carrying their call kind. The outcome is not asserted.
func TestClaudeSubDiagnosticsCallKind(t *testing.T) {
	u1 := claudeSubUserText("read notes")
	a1 := Message{Role: MessageRoleAssistant, Content: "Hello world"}
	u2 := claudeSubUserText("is it right?")
	model := claudeSubTestSpec().Model
	tests := []struct {
		name     string
		req      ChatRequest
		wantKind string
	}{
		{
			name:     "advisor",
			req:      ChatRequest{TransportSession: "diag-advisor", ParentTransportSession: "diag-parent", AdvisorCacheProfile: true, Model: model, Messages: []Message{u1, a1, u2}},
			wantKind: "advisor",
		},
		{
			name:     "summary",
			req:      ChatRequest{TransportSession: "diag-summary", Model: model, Messages: []Message{u1, a1, u2, claudeSubUserText("summarize the older part")}, CompactionSummary: true, CompactionSourceMessages: 2},
			wantKind: "summary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := claudeSubPoolWithScripts(t, claudeSubTextTurns(t, 1))
			streamLog, logPath := claudeSubDiagLogger(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// The error is ignored: this test checks only the record's call_kind.
			_, _ = NewClaudeSubscriptionProvider(pool, streamLog).ChatCompletion(ctx, tt.req)
			rec := claudeSubDiagOneRecord(t, logPath)
			if rec.CallKind != tt.wantKind || rec.Attempts != 1 {
				t.Errorf("call_kind = %q attempts = %d, want %q and 1", rec.CallKind, rec.Attempts, tt.wantKind)
			}
		})
	}
}

// TestClaudeSubDiagnosticsResumeReseed proves a resumed process that fails before
// output is reseeded inside the provider. The call still writes one successful
// record with attempts 1, because the internal reseed is not counted.
func TestClaudeSubDiagnosticsResumeReseed(t *testing.T) {
	errEvent := claudeSubEvent{
		Type:      "result",
		Subtype:   "error_during_execution",
		SessionID: "sess-1",
		Raw:       json.RawMessage(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"No conversation found with session ID: sess-1","session_id":"sess-1"}`),
	}
	assistant, st := claudeSubOpeningTurn(t)
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{batches: [][]claudeSubEvent{{errEvent}}}, claudeSubTextTurns(t, 1))
	streamLog, logPath := claudeSubDiagLogger(t)
	req := ChatRequest{
		TransportSession: "diag-resume",
		Model:            claudeSubTestSpec().Model,
		Messages:         []Message{claudeSubUserText("read notes"), assistant, claudeSubUserText("more")},
		BackendState:     claudeSubEncodeState(t, st),
	}
	if _, err := NewClaudeSubscriptionProvider(pool, streamLog).ChatCompletion(context.Background(), req); err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (failed resume, then reseed)", n)
	}
	rec := claudeSubDiagOneRecord(t, logPath)
	if rec.Outcome != "ok" || rec.Attempts != 1 || rec.Error != "" {
		t.Errorf("outcome = %q attempts = %d error = %q, want ok, 1, and no error", rec.Outcome, rec.Attempts, rec.Error)
	}
}

// TestClaudeSubDiagnosticsUsageGateRefusal proves a usage-gate refusal before
// any user write writes one exhausted record.
func TestClaudeSubDiagnosticsUsageGateRefusal(t *testing.T) {
	pool, spawner := claudeSubPoolWithScripts(t, &claudeSubScript{usage: json.RawMessage(`{"rate_limits_available":false}`)})
	streamLog, logPath := claudeSubDiagLogger(t)
	req := ChatRequest{TransportSession: "diag-gate", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("marker-prompt")}}
	if _, err := NewClaudeSubscriptionProvider(pool, streamLog).ChatCompletion(context.Background(), req); err == nil {
		t.Fatal("ChatCompletion() error = nil, want usage gate refusal")
	}
	if lines := claudeSubUserLines(spawner.call(t, 0).Conn); len(lines) != 0 {
		t.Errorf("user lines = %q, want none before the gate refuses", lines)
	}
	rec := claudeSubDiagOneRecord(t, logPath)
	if rec.Outcome != "exhausted" || rec.Attempts != 1 || rec.Chunks != 0 || rec.PartialStream {
		t.Errorf("outcome = %q attempts = %d chunks = %d partial = %v, want exhausted, 1, 0, false", rec.Outcome, rec.Attempts, rec.Chunks, rec.PartialStream)
	}
	if !strings.Contains(rec.Error, "fail closed") {
		t.Errorf("error = %q, want the fail-closed usage gate message", rec.Error)
	}
}

// TestClaudeSubDiagnosticsOmitsCLIPayload proves CLI result text and control
// response error text stay in the caller's error but never reach a record.
func TestClaudeSubDiagnosticsOmitsCLIPayload(t *testing.T) {
	resultFailure := claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"error_max_turns","is_error":true,"result":"SECRET-RESULT-TEXT"}`)}
	tests := []struct {
		name   string
		script *claudeSubScript
		marker string
	}{
		{name: "terminal result text", script: &claudeSubScript{batches: [][]claudeSubEvent{{resultFailure}}}, marker: "SECRET-RESULT-TEXT"},
		{name: "control response error", script: &claudeSubScript{usageErr: "SECRET-CONTROL-ERROR"}, marker: "SECRET-CONTROL-ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := claudeSubPoolWithScripts(t, tt.script)
			streamLog, logPath := claudeSubDiagLogger(t)
			stream, err := claudeSubStream(context.Background(), pool, ChatRequest{TransportSession: "diag-payload", Model: claudeSubTestSpec().Model, Messages: []Message{claudeSubUserText("marker-prompt")}}, streamLog)
			if err != nil {
				t.Fatalf("claudeSubStream() error = %v", err)
			}
			var last ChatChunk
			for chunk := range stream {
				last = chunk
			}
			if !strings.Contains(last.Error, tt.marker) {
				t.Errorf("terminal error = %q, want the CLI text kept for the caller", last.Error)
			}
			rec := claudeSubDiagOneRecord(t, logPath)
			if rec.Outcome != "exhausted" || rec.Error == "" {
				t.Errorf("outcome = %q error = %q, want exhausted with an error", rec.Outcome, rec.Error)
			}
			if strings.Contains(rec.Error, tt.marker) {
				t.Errorf("record error = %q, want no CLI payload", rec.Error)
			}
		})
	}
}

// TestClaudeSubDiagnosticErrorOmitsPayload proves the stderr tail and turn
// failure text are dropped from the diagnostics form of their errors.
func TestClaudeSubDiagnosticErrorOmitsPayload(t *testing.T) {
	proc := &claudeSubProcess{waitErr: errors.New("exit status 1"), stderrTail: newClaudeSubStderrTail(claudeSubStderrTailBytes)}
	if _, err := proc.stderrTail.Write([]byte("SECRET-STDERR-TAIL")); err != nil {
		t.Fatalf("write stderr tail: %v", err)
	}
	proc.finalizeErr()
	tests := []struct {
		name   string
		err    error
		marker string
	}{
		{name: "stderr tail", err: proc.err, marker: "SECRET-STDERR-TAIL"},
		{name: "turn failure text", err: claudeSubTurnFailure(&claudeSubResult{Subtype: "error_max_turns", Text: "SECRET-RESULT-TEXT", IsError: true}), marker: "SECRET-RESULT-TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.marker) {
				t.Errorf("caller error = %q, want the CLI text kept for the caller", tt.err)
			}
			if diag := claudeSubDiagnosticError(tt.err).Error(); strings.Contains(diag, tt.marker) {
				t.Errorf("diagnostic error = %q, want no CLI text", diag)
			}
		})
	}
}

// TestClaudeSubCallKind table-drives the call-kind precedence: advisor, then
// summary, then sub-agent, then normal.
func TestClaudeSubCallKind(t *testing.T) {
	tests := []struct {
		name string
		req  ChatRequest
		want string
	}{
		{name: "normal", req: ChatRequest{TransportSession: "parent"}, want: "normal"},
		{name: "sub-agent", req: ChatRequest{ParentTransportSession: "parent"}, want: "sub_agent"},
		{name: "advisor", req: ChatRequest{AdvisorCacheProfile: true, ParentTransportSession: "parent"}, want: "advisor"},
		{name: "summary", req: ChatRequest{CompactionSummary: true}, want: "summary"},
		{name: "summary over sub-agent", req: ChatRequest{CompactionSummary: true, ParentTransportSession: "parent"}, want: "summary"},
		{name: "advisor over summary", req: ChatRequest{AdvisorCacheProfile: true, CompactionSummary: true, ParentTransportSession: "parent"}, want: "advisor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubCallKind(tt.req); got != tt.want {
				t.Errorf("claudeSubCallKind() = %q, want %q", got, tt.want)
			}
		})
	}
}
