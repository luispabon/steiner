//go:build claudesub_probe

package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeSubProbeSelfCheckInit is a valid init event: the synthetic tool, a model
// matching the default alias, one MCP server named steiner, and load metadata to
// record by name.
const claudeSubProbeSelfCheckInit = `{"type":"system","subtype":"init","session_id":"sess-probe","model":"claude-haiku-5-5","tools":["mcp__steiner__probe_wait"],"mcp_servers":[{"name":"steiner"}],"capabilities":["interrupt_receipt_v1"],"claude_code_version":"2.1.205","plugins":["probe-plugin"],"hooks":{"SessionStart":[]}}`

// claudeSubProbeSelfCheckInitInvalidTools reports a CLI built-in tool, which the
// init gate must reject.
const claudeSubProbeSelfCheckInitInvalidTools = `{"type":"system","subtype":"init","session_id":"sess-probe","model":"claude-haiku-5-5","tools":["mcp__steiner__probe_wait","Bash"]}`

// claudeSubProbeSelfCheckInitForeignMCP reports an MCP server other than steiner.
const claudeSubProbeSelfCheckInitForeignMCP = `{"type":"system","subtype":"init","model":"claude-haiku-5-5","tools":["mcp__steiner__probe_wait"],"mcp_servers":[{"name":"steiner"},{"name":"other"}]}`

// TestClaudeSubProbeHarness records a scripted exchange through the probe's tee
// and checks what the probe derives from it. It uses the in-memory fake
// connection, so it starts no process and needs no CLI.
func TestClaudeSubProbeHarness(t *testing.T) {
	process, conn := claudeSubProbeTestProcess(t)
	rec := process.rec
	claudeSubProbePushEvent(t, conn, claudeSubProbeSelfCheckInit)
	gateCtx, cancelGate := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := process.pumpUntil(gateCtx, 5*time.Second, "init event", claudeSubProbeIsInit); err != nil {
		cancelGate()
		t.Fatalf("init gate error = %v", err)
	}
	cancelGate()
	rec.beginTurn("PROBE-T1 self check")
	claudeSubProbePushEvent(t, conn, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"par"}}}`)
	if err := process.sendPrompt("PROBE-T1 self check"); err != nil {
		t.Fatalf("sendPrompt() error = %v, want a prompt after a valid init", err)
	}
	interrupt := claudeSubProbeMustJSON(t, map[string]any{
		"type":       "control_request",
		"request_id": "steiner-1",
		"request":    map[string]any{"subtype": "interrupt"},
	})
	if err := process.tee.Send(interrupt); err != nil {
		t.Fatalf("tee.Send() error = %v", err)
	}
	claudeSubProbePushEvent(t, conn, `{"type":"control_response","response":{"subtype":"success","request_id":"steiner-1"}}`)
	claudeSubProbePushEvent(t, conn, `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":5,"cache_creation_input_tokens":6,"cache_read_input_tokens":7,"output_tokens":8}}}`)
	claudeSubProbePushEvent(t, conn, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming","stop_reason":"end_turn","result":"[Request interrupted by user]","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}`)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entry, err := process.pumpUntil(ctx, 5*time.Second, "result", claudeSubProbeIsResult)
	if err != nil {
		t.Fatalf("pumpUntil() error = %v", err)
	}
	if entry.TerminalReason != "aborted_streaming" {
		t.Fatalf("result terminal_reason = %q, want aborted_streaming", entry.TerminalReason)
	}

	report := process.rep
	if report.Init == nil {
		t.Fatal("report has no init event")
	}
	if got := report.Init.Extras["plugins"].Names; len(got) != 1 || got[0] != "probe-plugin" {
		t.Fatalf("init plugins = %v, want [probe-plugin]", got)
	}
	if got := report.Init.Extras["mcp_servers"].Names; len(got) != 1 || got[0] != "steiner" {
		t.Fatalf("init mcp_servers = %v, want [steiner]", got)
	}
	if _, ok := report.Init.Extras["hooks"]; !ok {
		t.Fatal("init hooks were not recorded")
	}
	if report.SessionID != "sess-probe" {
		t.Fatalf("report session id = %q, want sess-probe", report.SessionID)
	}
	if len(report.Turns) != 1 || report.Turns[0].Result == nil {
		t.Fatalf("report turns = %+v, want one turn with a result", report.Turns)
	}
	if got := report.Turns[0].StreamUsage; got == nil || got.CacheReadInputTokens != 7 {
		t.Fatalf("stream usage = %+v, want the message_delta usage with cache_read 7", got)
	}
	if got := report.Turns[0].AnswerText; got != "par" {
		t.Fatalf("answer text = %q, want the streamed delta only", got)
	}
	findings := strings.Join(claudeSubProbeFindings(report), "\n")
	for _, want := range []string{"control_response before result", "subtype=\"error_during_execution\"", "init plugins: [probe-plugin]"} {
		if !strings.Contains(findings, want) {
			t.Fatalf("findings missing %q:\n%s", want, findings)
		}
	}
	if process.outboundInterruptAfter(0) < 0 {
		t.Fatal("timeline has no outbound interrupt control request")
	}
}

// TestClaudeSubProbeInitGate checks that a prompt is refused until a valid init
// event has been recorded for the process.
func TestClaudeSubProbeInitGate(t *testing.T) {
	cases := []struct {
		name     string
		script   string
		wantErr  string
		wantSent int
	}{
		{name: "prompt refused before init", wantErr: "not validated", wantSent: 0},
		{name: "prompt accepted after a valid init", script: claudeSubProbeSelfCheckInit, wantSent: 1},
		{name: "prompt refused after an invalid init", script: claudeSubProbeSelfCheckInitInvalidTools, wantErr: "want exactly", wantSent: 0},
		{name: "prompt refused after a foreign MCP server", script: claudeSubProbeSelfCheckInitForeignMCP, wantErr: "MCP server", wantSent: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			process, conn := claudeSubProbeTestProcess(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if tc.script != "" {
				claudeSubProbePushEvent(t, conn, tc.script)
				if _, err := process.pumpUntil(ctx, 5*time.Second, "init event", claudeSubProbeIsInit); err != nil && tc.wantErr == "" {
					t.Fatalf("init pump error = %v", err)
				}
			}
			err := process.sendPrompt("PROBE-T1 gate check")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("sendPrompt() error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("sendPrompt() error = %v, want nil", err)
			}
			if got := len(claudeSubProbeSentLines(conn)); got != tc.wantSent {
				t.Fatalf("sent lines = %d, want %d", got, tc.wantSent)
			}
		})
	}
}

// TestClaudeSubProbeInitGateMissingInit checks that a missing init event ends the
// gate with its own reason.
func TestClaudeSubProbeInitGateMissingInit(t *testing.T) {
	process, _ := claudeSubProbeTestProcess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := process.pumpUntil(ctx, 100*time.Millisecond, "init event", claudeSubProbeIsInit)
	if err == nil || !strings.Contains(err.Error(), "no init event within") {
		t.Fatalf("pumpUntil() error = %v, want a missing-init reason", err)
	}
	if err := process.sendPrompt("PROBE-T1 gate check"); err == nil {
		t.Fatal("sendPrompt() succeeded without a validated init")
	}
}

// TestClaudeSubProbeCleanup checks session id validation and that cleanup never
// leaves the probe's own project directory.
func TestClaudeSubProbeCleanup(t *testing.T) {
	cases := []struct {
		name        string
		id          string
		wantRemoved bool
		wantSkipped bool
	}{
		{name: "plain session id", id: "sess-abc-123", wantRemoved: true},
		{name: "traversal id", id: "../../escape", wantSkipped: true},
		{name: "dot id", id: "sess.abc", wantSkipped: true},
		{name: "space id", id: "sess abc", wantSkipped: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			cfg := claudeSubProbeConfig{WorkDir: filepath.Join(configDir, "probe-work"), Cleanup: true}
			projectDir := claudeSubProbeProjectDir(configDir, cfg.WorkDir)
			if err := os.MkdirAll(projectDir, 0o700); err != nil {
				t.Fatalf("create project dir: %v", err)
			}
			var wanted string
			if claudeSubProbeSessionIDValid(tc.id) {
				wanted = filepath.Join(projectDir, tc.id+".jsonl")
				if err := os.WriteFile(wanted, []byte("{}\n"), 0o600); err != nil {
					t.Fatalf("create transcript: %v", err)
				}
			}
			// A decoy with the same name in another project directory must survive.
			decoyDir := claudeSubProbeProjectDir(configDir, filepath.Join(configDir, "other-work"))
			if err := os.MkdirAll(decoyDir, 0o700); err != nil {
				t.Fatalf("create decoy dir: %v", err)
			}
			decoy := filepath.Join(decoyDir, "sess-abc-123.jsonl")
			if err := os.WriteFile(decoy, []byte("{}\n"), 0o600); err != nil {
				t.Fatalf("create decoy transcript: %v", err)
			}
			report := &claudeSubProbeReport{Experiments: []*claudeSubProbeExperimentReport{{
				Processes: []*claudeSubProbeProcessReport{{Name: "probe", SessionID: tc.id}},
			}}}
			out := claudeSubProbeCleanupSessions(cfg, configDir, report)
			if got := len(out.Removed) == 1; got != tc.wantRemoved {
				t.Fatalf("removed = %v, want removed %v", out.Removed, tc.wantRemoved)
			}
			if got := len(out.Skipped) == 1; got != tc.wantSkipped {
				t.Fatalf("skipped = %v, want skipped %v", out.Skipped, tc.wantSkipped)
			}
			if tc.wantRemoved {
				if _, err := os.Stat(wanted); !os.IsNotExist(err) {
					t.Fatalf("transcript still present after cleanup: %v", err)
				}
			}
			if _, err := os.Stat(decoy); err != nil {
				t.Fatalf("decoy transcript outside the probe project directory was touched: %v", err)
			}
		})
	}
}

// TestClaudeSubProbeReportMode checks the report file's exact permissions.
func TestClaudeSubProbeReportMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := claudeSubProbeWriteReport(path, &claudeSubProbeReport{Mode: "self-check"}); err != nil {
		t.Fatalf("claudeSubProbeWriteReport() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat report: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("report mode = %o, want 600", got)
	}
}

// TestClaudeSubProbeEnvRedaction checks that only the open keys carry values.
func TestClaudeSubProbeEnvRedaction(t *testing.T) {
	env := []string{"HOME=/home/probe", "ANTHROPIC_API_KEY=sk-probe-secret", "CLAUDE_CONFIG_DIR=/probe/cfg", "PATH=/usr/bin"}
	summary := claudeSubProbeEnvSummary(env)
	cases := []struct {
		name string
		key  string
		want string
	}{
		{name: "home value reported", key: "HOME", want: "/home/probe"},
		{name: "config dir value reported", key: "CLAUDE_CONFIG_DIR", want: "/probe/cfg"},
		{name: "unset resume prompt marked", key: "CLAUDE_CODE_RESUME_PROMPT", want: "(unset)"},
		{name: "unset resume flag marked", key: "CLAUDE_CODE_RESUME_INTERRUPTED_TURN", want: "(unset)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary.Values[tc.key]; got != tc.want {
				t.Fatalf("Values[%s] = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
	if len(summary.Values) != len(claudeSubProbeOpenEnvKeys) {
		t.Fatalf("reported value count = %d, want %d", len(summary.Values), len(claudeSubProbeOpenEnvKeys))
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("encode env summary: %v", err)
	}
	if strings.Contains(string(encoded), "sk-probe-secret") {
		t.Fatalf("env summary leaked a secret value: %s", encoded)
	}
	found := false
	for _, key := range summary.Keys {
		if key == "ANTHROPIC_API_KEY" {
			found = true
		}
	}
	if !found {
		t.Fatal("env summary did not report the stripped key's name")
	}
}

// TestClaudeSubProbeTimelineCap checks that hitting the timeline cap is recorded,
// reported as a finding, and fails the experiment.
func TestClaudeSubProbeTimelineCap(t *testing.T) {
	old := claudeSubProbeTimelineMax
	claudeSubProbeTimelineMax = 2
	t.Cleanup(func() { claudeSubProbeTimelineMax = old })

	process, conn := claudeSubProbeTestProcess(t)
	for i := 0; i < 3; i++ {
		claudeSubProbePushEvent(t, conn, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"tick"}]},"uuid":"u1"}`)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if process.rec.timelineLen() == 2 && process.rep.TimelineTruncated {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeline stayed at %d entries with truncated=%v, want the cap recorded", process.rec.timelineLen(), process.rep.TimelineTruncated)
		}
		time.Sleep(claudeSubProbeHandlerPoll)
	}
	if err := claudeSubProbeProcessFailure(process.rep); err == nil || !strings.Contains(err.Error(), "entry cap") {
		t.Fatalf("process failure = %v, want the cap failure", err)
	}
	findings := strings.Join(claudeSubProbeFindings(process.rep), "\n")
	if !strings.Contains(findings, "timeline truncated") {
		t.Fatalf("findings missing the truncation note:\n%s", findings)
	}
}

// TestClaudeSubProbeCounting checks the result and inbound-text helpers used by
// E4 and E6, including that an outbound prompt never satisfies a text check.
func TestClaudeSubProbeCounting(t *testing.T) {
	timeline := []claudeSubProbeEntry{
		{Dir: "out", Type: "user", Text: "PROBE-T2E6 Reply with exactly: queued-ok"},
		{Dir: "in", Type: "assistant", Text: "queued-ok"},
		{Dir: "in", Type: "result", Text: "[Request interrupted by user]"},
		{Dir: "in", Type: "result", Text: "queued-ok"},
	}
	cases := []struct {
		name       string
		index      int
		wantText   string
		wantResult int
		wantMatch  bool
	}{
		{name: "results counted from the start", index: 0, wantResult: 2},
		{name: "results carrying a marker", index: 0, wantText: "queued-ok", wantResult: 1, wantMatch: true},
		{name: "results from the last entry", index: 3, wantResult: 1},
		{name: "inbound text match", index: 0, wantText: "queued-ok", wantResult: 1, wantMatch: true},
		{name: "missing marker", index: 0, wantText: "absent-marker", wantResult: 0, wantMatch: false},
		{name: "index past the end", index: 9, wantText: "queued-ok", wantResult: 0, wantMatch: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(claudeSubProbeResultsWithTextIn(timeline, tc.index, tc.wantText)); got != tc.wantResult {
				t.Fatalf("results = %d, want %d", got, tc.wantResult)
			}
			if got := claudeSubProbeInboundTextIn(timeline, tc.index, tc.wantText); got != tc.wantMatch {
				t.Fatalf("inbound text match = %v, want %v", got, tc.wantMatch)
			}
		})
	}

	outboundOnly := []claudeSubProbeEntry{{Dir: "out", Type: "user", Text: "queued-ok"}}
	if claudeSubProbeInboundTextIn(outboundOnly, 0, "queued-ok") {
		t.Fatal("an outbound prompt satisfied the inbound text check")
	}
	if got := len(claudeSubProbeResultsWithTextIn(outboundOnly, 0, "queued-ok")); got != 0 {
		t.Fatalf("outbound-only timeline reported %d results", got)
	}
	if got := claudeSubProbeOutboundInterruptIn([]claudeSubProbeEntry{{Dir: "in", Type: "result"}, {Dir: "out", Type: "control_request", ControlSubtype: "interrupt"}}, 0); got != 1 {
		t.Fatalf("outbound interrupt index = %d, want 1", got)
	}
}

// TestClaudeSubProbeAnswerAppend checks that assistant text is capped without an
// inserted marker.
func TestClaudeSubProbeAnswerAppend(t *testing.T) {
	long := strings.Repeat("a", claudeSubProbeAnswerMaxRunes+10)
	cases := []struct {
		name          string
		current       string
		add           string
		wantLen       int
		wantTruncated bool
	}{
		{name: "short append", current: "ab", add: "cd", wantLen: 4},
		{name: "cap crossed", current: "ab", add: long, wantLen: claudeSubProbeAnswerMaxRunes, wantTruncated: true},
		{name: "already capped", current: strings.Repeat("a", claudeSubProbeAnswerMaxRunes), add: "more", wantLen: claudeSubProbeAnswerMaxRunes, wantTruncated: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := claudeSubProbeAppendAnswer(tc.current, false, tc.add)
			if len([]rune(got)) != tc.wantLen {
				t.Fatalf("answer length = %d, want %d", len([]rune(got)), tc.wantLen)
			}
			if truncated != tc.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
			if strings.Contains(got, "...") {
				t.Fatal("answer text carries an inserted marker")
			}
		})
	}
}

// claudeSubProbeTestProcess builds a probe process around the in-memory fake
// connection, so harness behaviour can be checked without starting anything.
func claudeSubProbeTestProcess(t *testing.T) (*claudeSubProbeProcess, *claudeSubFakeConn) {
	t.Helper()
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
	process := &claudeSubProbeProcess{
		key:  "probe-self-check",
		sess: &claudeSubSession{conn: tee},
		tee:  tee,
		rec:  rec,
		rep:  report,
	}
	return process, conn
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

// claudeSubProbeSentLines copies the fake connection's recorded outbound lines.
func claudeSubProbeSentLines(conn *claudeSubFakeConn) [][]byte {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	out := make([][]byte, len(conn.sent))
	copy(out, conn.sent)
	return out
}
