package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The control audit checks offline that the launch controls are pinned as
// requested but unverified, and that each attempt's output and thinking
// observations are recorded as the stream shows them. Output and thinking are
// audit observations only. No gate reads them, and they do not prove the output
// cap or the thinking state of a launch.

// claudeAuditTextBlock is one streamed text block at index 0.
var claudeAuditTextBlock = []string{
	`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"sess-1","uuid":"a4"}`,
	`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}},"session_id":"sess-1","uuid":"a5"}`,
	`{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"sess-1","uuid":"a6"}`,
}

// claudeAuditStream returns a capture for claude-haiku-5-5 with one message. The
// message_start and message_delta usage objects are startUsage and deltaUsage, and
// blocks are the stream_event lines between them. The CLI result carries
// output_tokens 50, so a test that gets 50 has read the result usage.
func claudeAuditStream(startUsage, deltaUsage string, blocks ...string) []byte {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-haiku-5-5","uuid":"a0"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"type":"message","model":"claude-haiku-5-5","id":"msg_a","role":"assistant","content":[],"stop_reason":null,"usage":` + startUsage + `}},"session_id":"sess-1","uuid":"a1"}`,
	}
	lines = append(lines, blocks...)
	lines = append(lines,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":`+deltaUsage+`},"session_id":"sess-1","uuid":"a7"}`,
		`{"type":"stream_event","event":{"type":"message_stop"},"session_id":"sess-1","uuid":"a8"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"OK","stop_reason":"end_turn","session_id":"sess-1","usage":{"input_tokens":50,"cache_read_input_tokens":500,"output_tokens":50},"permission_denials":[]}`,
	)
	return []byte(strings.Join(lines, "\n") + "\n")
}

// claudeAuditIntString renders an optional count for failure messages.
func claudeAuditIntString(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}

// TestClaudeTokenAuditOutputIsAuthoritative checks that the output count comes from
// the message usage and never from the CLI result.
func TestClaudeTokenAuditOutputIsAuthoritative(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		start, delta string
		want         *int
	}{
		{"message_delta count wins", `{"input_tokens":2,"output_tokens":1}`, `{"input_tokens":2,"output_tokens":42}`, claudeLedgerIntPtr(42)},
		{"message_start count kept when message_delta omits it", `{"input_tokens":2,"output_tokens":7}`, `{"input_tokens":2}`, claudeLedgerIntPtr(7)},
		{"result count never fills a missing count", `{"input_tokens":2}`, `{"input_tokens":2}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := claudeLedgerObserveStream(claudeAuditStream(tt.start, tt.delta), "claude-haiku-5-5")
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if !reflect.DeepEqual(obs.OutputTokens, tt.want) {
				t.Errorf("output = %s, want %s", claudeAuditIntString(obs.OutputTokens), claudeAuditIntString(tt.want))
			}
		})
	}
}

// TestClaudeTokenAuditOutputZeroMissingNegative checks that an explicit zero is kept
// apart from a missing count, and that a negative count fails closed.
func TestClaudeTokenAuditOutputZeroMissingNegative(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		delta   string
		want    *int
		wantErr bool
	}{
		{"explicit zero is kept", `{"input_tokens":2,"output_tokens":0}`, claudeLedgerIntPtr(0), false},
		{"missing stays nil", `{"input_tokens":2}`, nil, false},
		{"negative fails closed", `{"input_tokens":2,"output_tokens":-1}`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := claudeLedgerObserveStream(claudeAuditStream(`{"input_tokens":2}`, tt.delta), "claude-haiku-5-5")
			if tt.wantErr {
				if !errors.Is(err, errClaudeLedgerStream) || !strings.Contains(err.Error(), "negative") {
					t.Errorf("err = %v, want errClaudeLedgerStream containing \"negative\"", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if !reflect.DeepEqual(obs.OutputTokens, tt.want) {
				t.Errorf("output = %s, want %s", claudeAuditIntString(obs.OutputTokens), claudeAuditIntString(tt.want))
			}
		})
	}
}

// TestClaudeTokenAuditThinkingObserved checks that a streamed thinking or redacted
// thinking block sets the audit flag, and that a text-only stream does not.
func TestClaudeTokenAuditThinkingObserved(t *testing.T) {
	t.Parallel()
	thinkingStart := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}},"session_id":"sess-1","uuid":"a4"}`
	thinkingDelta := `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}},"session_id":"sess-1","uuid":"a5"}`
	redactedStart := `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"x"}},"session_id":"sess-1","uuid":"a4"}`
	stop := `{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"sess-1","uuid":"a6"}`
	tests := []struct {
		name   string
		blocks []string
		want   bool
	}{
		{"text only", claudeAuditTextBlock, false},
		{"thinking block", []string{thinkingStart, thinkingDelta, stop}, true},
		{"redacted thinking block", []string{redactedStart, stop}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs, err := claudeLedgerObserveStream(claudeAuditStream(`{"input_tokens":2}`, `{"input_tokens":2,"output_tokens":5}`, tt.blocks...), "claude-haiku-5-5")
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			if obs.ThinkingBlocksObserved != tt.want {
				t.Errorf("thinking observed = %t, want %t", obs.ThinkingBlocksObserved, tt.want)
			}
		})
	}
}

// Foreign envelopes that carry a message_delta usage or a thinking block must not
// change the output count or the thinking flag.
func TestClaudeTokenAuditForeignEnvelopesIgnored(t *testing.T) {
	t.Parallel()
	foreign := []byte(
		`{"type":"system","subtype":"init","event":{"type":"message_delta","usage":{"output_tokens":999}}}` + "\n" +
			`{"type":"system","subtype":"init","event":{"type":"content_block_start","content_block":{"type":"thinking"}}}` + "\n")
	stream := append(foreign, claudeAuditStream(`{"input_tokens":2}`, `{"input_tokens":2,"output_tokens":42}`, claudeAuditTextBlock...)...)
	obs, err := claudeLedgerObserveStream(stream, "claude-haiku-5-5")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !reflect.DeepEqual(obs.OutputTokens, claudeLedgerIntPtr(42)) {
		t.Errorf("output = %s, want 42", claudeAuditIntString(obs.OutputTokens))
	}
	if obs.ThinkingBlocksObserved {
		t.Error("thinking observed from a foreign envelope")
	}
}

// Output and thinking persist with their attempt. A zero count stays distinct from
// a missing count after the ledger is read back.
func TestClaudeTokenAuditExtrasPersistInLedger(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	dir, l := claudeLedgerCreateForTest(t)
	const model = "claude-haiku-5-5"
	launches := []*claudeLedgerObservation{
		{ObservedModel: model, claudeLedgerUsage: claudeLedgerUsage{Input: claudeLedgerIntPtr(3), OutputTokens: claudeLedgerIntPtr(0), PromptTotal: 3}, ThinkingBlocksObserved: true},
		{ObservedModel: model, claudeLedgerUsage: claudeLedgerUsage{Input: claudeLedgerIntPtr(3), PromptTotal: 3}},
	}
	for i, slot := range []claudeLedgerSlot{claudeLedgerSlotBaseline, claudeLedgerSlotCodeJSON} {
		if _, err := l.RunAttempt(claudeLedgerTestInput(set, model, slot), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
			return launches[i], nil
		}); err != nil {
			t.Fatalf("run attempt %d: %v", i+1, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close ledger: %v", err)
	}
	recs := claudeLedgerOnDisk(t, dir)
	if len(recs) != 2 || recs[0].Usage == nil || recs[1].Usage == nil {
		t.Fatalf("ledger holds %d records, want 2 succeeded records", len(recs))
	}
	if got := recs[0].Usage.OutputTokens; got == nil || *got != 0 || !recs[0].ThinkingBlocksObserved {
		t.Errorf("attempt 1 output = %s, thinking = %t, want 0 and true", claudeAuditIntString(got), recs[0].ThinkingBlocksObserved)
	}
	if got := recs[1].Usage.OutputTokens; got != nil || recs[1].ThinkingBlocksObserved {
		t.Errorf("attempt 2 output = %s, thinking = %t, want nil and false", claudeAuditIntString(got), recs[1].ThinkingBlocksObserved)
	}
}

// Extras the stream cannot support fail closed when the ledger is read.
func TestClaudeTokenAuditLedgerRejectsInvalidExtras(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	tests := []struct {
		name string
		edit func(*claudeLedgerRecord)
	}{
		{"negative output", func(r *claudeLedgerRecord) { r.Usage.OutputTokens = claudeLedgerIntPtr(-1) }},
		{"thinking on a reserved record", func(r *claudeLedgerRecord) {
			r.Status, r.ObservedModel, r.Usage, r.ThinkingBlocksObserved = claudeLedgerStatusReserved, "", nil, true
		}},
		{"thinking on a failed record", func(r *claudeLedgerRecord) {
			r.Status, r.Failure, r.ObservedModel, r.Usage, r.ThinkingBlocksObserved = claudeLedgerStatusFailed, claudeLedgerFailLaunch, "", nil, true
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := claudeLedgerEditRecord(t, claudeLedgerValidFile(t, set), tt.edit)
			if _, err := claudeLedgerDecode(data); !errors.Is(err, errClaudeLedgerCorrupt) {
				t.Errorf("decode err = %v, want errClaudeLedgerCorrupt", err)
			}
		})
	}
}

// The gates give identical reports whatever output and thinking the attempts carry.
func TestClaudeTokenAuditGatesIgnoreExtras(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	base, err := claudeGateEvaluate(ctx, set, claudeGateSyntheticEvidence(t, set))
	if err != nil {
		t.Fatalf("evaluate base evidence: %v", err)
	}
	if n := len(claudeGateActiveModels(base)); n != len(claudeLedgerModels) {
		t.Fatalf("base evidence activates %d models, want %d", n, len(claudeLedgerModels))
	}
	tests := []struct {
		name     string
		output   *int
		thinking bool
	}{
		{"output absent, thinking off", nil, false},
		{"output zero, thinking on", claudeLedgerIntPtr(0), true},
		{"output large, thinking on", claudeLedgerIntPtr(64000), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := claudeGateSyntheticEvidence(t, set)
			for i := range ev.Attempts {
				ev.Attempts[i].Usage.OutputTokens = tt.output
				ev.Attempts[i].ThinkingBlocksObserved = tt.thinking
			}
			reports, err := claudeGateEvaluate(ctx, set, ev)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if !reflect.DeepEqual(reports, base) {
				t.Error("reports differ from the base evidence")
			}
		})
	}
}

// The launch pins name the output cap as requested and the thinking state as
// unverified. The cap itself stays at 64 tokens.
func TestClaudeTokenAuditLaunchPinsAreUnverified(t *testing.T) {
	t.Parallel()
	got := claudeGateLaunchPinned()
	if got.Reasoning != "disabled_requested_effective_unverified" {
		t.Errorf("reasoning pin = %q", got.Reasoning)
	}
	if got.MaxOutputTokens != 64 {
		t.Errorf("max output tokens pin = %d, want 64", got.MaxOutputTokens)
	}
	if got.OutputLimitMechanism != "CLAUDE_CODE_MAX_OUTPUT_TOKENS" {
		t.Errorf("output limit mechanism pin = %q", got.OutputLimitMechanism)
	}
	if got.OutputLimitVerification != "requested_not_verified" {
		t.Errorf("output limit verification pin = %q", got.OutputLimitVerification)
	}
}

// The gate loader reads only schema 3. Schema 2 evidence lacks the output limit pins
// and fails instead of being read with them missing.
func TestClaudeTokenAuditGateSchemaV2Rejected(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), claudeGateEvidenceName)
	doc := `{"schema_version":2,"cli_version":"c","library_commit":"x","payload_sha256":{},"tools_sha256":"t","launch":{},"attempts":[]}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	if _, err := claudeGateLoadEvidence(path); err == nil || !strings.Contains(err.Error(), "want 3") {
		t.Errorf("load err = %v, want schema_version rejection containing \"want 3\"", err)
	}
}
