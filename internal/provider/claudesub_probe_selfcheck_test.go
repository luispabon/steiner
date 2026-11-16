//go:build claudesub_probe

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestClaudeSubProbeHarness records a scripted exchange through the probe's tee
// and checks what the probe derives from it. It uses the in-memory fake
// connection, so it starts no process and needs no CLI; it is the harness's own
// regression check.
func TestClaudeSubProbeHarness(t *testing.T) {
	conn := newClaudeSubFakeConn()
	report := &claudeSubProbeProcessReport{Name: "self-check", Key: "probe-self-check"}
	rec := newClaudeSubProbeRecorder(report, claudeSubProbeDefaultModel)
	tee := newClaudeSubProbeTeeConn(conn, rec)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range tee.Events() {
		}
	}()
	t.Cleanup(func() {
		tee.shutdown()
		<-drained
		rec.close()
	})

	claudeSubProbePushEvent(t, conn, `{"type":"system","subtype":"init","session_id":"sess-probe","model":"claude-haiku-5-5","tools":["mcp__steiner__probe_wait"],"capabilities":["interrupt_receipt_v1"],"claude_code_version":"2.1.205"}`)
	rec.beginTurn("PROBE-T1 self check")
	claudeSubProbePushEvent(t, conn, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"par"}}}`)
	interrupt := claudeSubProbeMustJSON(t, map[string]any{
		"type":       "control_request",
		"request_id": "steiner-1",
		"request":    map[string]any{"subtype": "interrupt"},
	})
	if err := tee.Send(interrupt); err != nil {
		t.Fatalf("tee.Send() error = %v", err)
	}
	claudeSubProbePushEvent(t, conn, `{"type":"control_response","response":{"subtype":"success","request_id":"steiner-1"}}`)
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","stop_reason":"end_turn","result":"[Request interrupted by user]","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, ok, err := rec.pump(ctx, claudeSubProbeIsResult)
	if err != nil || !ok {
		t.Fatalf("pump() = %v, %v, want the result event", ok, err)
	}
	if entry.TerminalReason != "aborted_streaming" {
		t.Fatalf("result terminal_reason = %q, want aborted_streaming", entry.TerminalReason)
	}

	if report.Init == nil {
		t.Fatal("report has no init event")
	}
	if report.SessionID != "sess-probe" {
		t.Fatalf("report session id = %q, want sess-probe", report.SessionID)
	}
	if len(report.Turns) != 1 || report.Turns[0].Result == nil {
		t.Fatalf("report turns = %+v, want one turn with a result", report.Turns)
	}
	if got := report.Turns[0].Result.TerminalReason; got != "aborted_streaming" {
		t.Fatalf("turn terminal_reason = %q, want aborted_streaming", got)
	}
	if got := report.Turns[0].Result.Usage; got == nil || got.CacheReadInputTokens != 3 {
		t.Fatalf("turn usage = %+v, want cache_read_input_tokens 3", got)
	}
	findings := strings.Join(claudeSubProbeFindings(report), "\n")
	for _, want := range []string{"control_response before result", "subtype=\"error_during_execution\"", "terminal_reason=\"aborted_streaming\""} {
		if !strings.Contains(findings, want) {
			t.Fatalf("findings missing %q:\n%s", want, findings)
		}
	}
	if report.Timeline[0].Dir == "" {
		t.Fatalf("first timeline entry = %+v, want a recorded direction", report.Timeline[0])
	}
	inboundInit := false
	for _, timelineEntry := range report.Timeline {
		if timelineEntry.Dir == "in" && timelineEntry.Type == "system" && timelineEntry.Subtype == "init" {
			inboundInit = true
		}
	}
	if !inboundInit {
		t.Fatal("timeline has no inbound init event")
	}
	outboundInterrupt := false
	for _, timelineEntry := range report.Timeline {
		if timelineEntry.Dir == "out" && timelineEntry.Type == "control_request" && timelineEntry.RequestID == "steiner-1" && timelineEntry.ControlSubtype == "interrupt" {
			outboundInterrupt = true
		}
	}
	if !outboundInterrupt {
		t.Fatal("timeline has no outbound interrupt control request")
	}
}

// TestClaudeSubProbeHarnessRejectsUnexpectedTools checks that an init event
// naming a tool other than the synthetic probe tool aborts the experiment.
func TestClaudeSubProbeHarnessRejectsUnexpectedTools(t *testing.T) {
	conn := newClaudeSubFakeConn()
	report := &claudeSubProbeProcessReport{Name: "self-check", Key: "probe-self-check-reject"}
	rec := newClaudeSubProbeRecorder(report, claudeSubProbeDefaultModel)
	tee := newClaudeSubProbeTeeConn(conn, rec)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range tee.Events() {
		}
	}()
	t.Cleanup(func() {
		tee.shutdown()
		<-drained
		rec.close()
	})

	claudeSubProbePushEvent(t, conn, `{"type":"system","subtype":"init","model":"claude-haiku-5-5","tools":["mcp__steiner__probe_wait","Bash"]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := rec.pump(ctx, claudeSubProbeIsResult); err == nil {
		t.Fatal("pump() error = nil, want an init validation failure")
	}
}

// claudeSubProbePushEvent feeds one scripted line into the fake connection.
func claudeSubProbePushEvent(t *testing.T, conn *claudeSubFakeConn, line string) {
	t.Helper()
	var envelope struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		UUID      string `json:"uuid"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode scripted event: %v", err)
	}
	conn.push(claudeSubEvent{Type: envelope.Type, Subtype: envelope.Subtype, UUID: envelope.UUID, SessionID: envelope.SessionID, Raw: json.RawMessage(line)})
}

// claudeSubProbeMustJSON encodes a scripted outbound line.
func claudeSubProbeMustJSON(t *testing.T, value map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode scripted line: %v", err)
	}
	return data
}
