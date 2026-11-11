package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The gate replay file holds the stored-report audit, the evaluator commit rule,
// and the pinned predictions. The gate recomputes every verdict from the attempts
// and the fixtures. The stored report is an audit copy: replay recomputes it and
// compares the bytes, and no gate reads it. Populated evidence is built in memory,
// so nothing here touches a live run.

// claudeGatePinnedCounts holds the full-request token counts of the baseline,
// tools, and mixed slots for one candidate model, under the old and new counters.
type claudeGatePinnedCounts struct {
	oldBaseline, newBaseline int64
	oldTools, newTools       int64
	oldMixed, newMixed       int64
}

// claudeGatePinnedPredictions holds the literal counts for the current fixtures.
// A change to a payload, to tools.json, or to either counter fails
// TestClaudeTokenGatePinnedPredictions.
var claudeGatePinnedPredictions = map[string]claudeGatePinnedCounts{
	"claude-opus-5-5":   {25, 31, 2182, 3067, 2935, 4121},
	"claude-sonnet-5-5": {25, 31, 2182, 3067, 2935, 4121},
	"claude-haiku-5-5":  {25, 31, 2182, 3067, 2935, 4121},
}

// claudeGateEncodeEvidence returns the canonical evidence document for ev with
// reports stored as its report. Two-space indent and one trailing newline make
// equal inputs produce equal bytes.
func claudeGateEncodeEvidence(ev claudeGateEvidence, reports []claudeGateReport) ([]byte, error) {
	report, err := json.Marshal(reports)
	if err != nil {
		return nil, fmt.Errorf("encode claude gate report: %w", err)
	}
	ev.Report = report
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode claude gate evidence: %w", err)
	}
	return append(data, '\n'), nil
}

// claudeGateStoredReportMatches returns an error unless the report stored in ev
// equals reports, compared in compact form.
func claudeGateStoredReportMatches(ev claudeGateEvidence, reports []claudeGateReport) error {
	want, err := json.Marshal(reports)
	if err != nil {
		return fmt.Errorf("encode claude gate report: %w", err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, ev.Report); err != nil {
		return fmt.Errorf("stored claude gate report is not JSON: %w", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		return errors.New("stored claude gate report differs from the recomputed report")
	}
	return nil
}

// TestClaudeTokenGatePinnedPredictions checks the old and new full-request counts
// of the baseline, tools, and mixed slots against literal values.
func TestClaudeTokenGatePinnedPredictions(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	for _, model := range claudeLedgerModels {
		oldC, _, err := claudeGateOldCounter(model)
		if err != nil {
			t.Fatalf("old counter for %s: %v", model, err)
		}
		newC, err := claudeFixtureCounter(model)
		if err != nil {
			t.Fatalf("new counter for %s: %v", model, err)
		}
		full := func(slot claudeLedgerSlot, counter textCounter) int64 {
			t.Helper()
			n, err := claudeGateFullCount(ctx, set, model, slot, counter)
			if err != nil {
				t.Fatalf("count %s %s: %v", model, slot, err)
			}
			return n
		}
		got := claudeGatePinnedCounts{
			oldBaseline: full(claudeLedgerSlotBaseline, oldC), newBaseline: full(claudeLedgerSlotBaseline, newC),
			oldTools: full(claudeLedgerSlotTools, oldC), newTools: full(claudeLedgerSlotTools, newC),
			oldMixed: full(claudeLedgerSlotMixedTools, oldC), newMixed: full(claudeLedgerSlotMixedTools, newC),
		}
		if want := claudeGatePinnedPredictions[model]; got != want {
			t.Errorf("%s predictions = %+v, want %+v", model, got, want)
		}
	}
}

// TestClaudeTokenGateStoredReportReplay writes populated synthetic evidence with its
// canonical report to a file and loads it back. Replay accepts the stored report only
// when it equals the recomputed report, and the gates give the same verdicts whatever
// the stored report holds.
func TestClaudeTokenGateStoredReportReplay(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	ev := claudeGateSyntheticEvidence(t, set)
	reports, err := claudeGateEvaluate(ctx, set, ev)
	if err != nil {
		t.Fatalf("evaluate synthetic evidence: %v", err)
	}
	data, err := claudeGateEncodeEvidence(ev, reports)
	if err != nil {
		t.Fatalf("encode synthetic evidence: %v", err)
	}
	path := filepath.Join(t.TempDir(), claudeGateEvidenceName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write synthetic evidence: %v", err)
	}
	loaded, err := claudeGateLoadEvidence(path)
	if err != nil {
		t.Fatalf("load synthetic evidence: %v", err)
	}
	want, err := claudeGateEvaluate(ctx, set, loaded)
	if err != nil {
		t.Fatalf("evaluate loaded evidence: %v", err)
	}
	if err := claudeGateStoredReportMatches(loaded, want); err != nil {
		t.Errorf("populated replay: %v", err)
	}
	if got := claudeGateActiveModels(want); !slices.Equal(got, claudeLedgerModels) {
		t.Errorf("populated replay active = %q, want %q", got, claudeLedgerModels)
	}
	if again, err := claudeGateEncodeEvidence(loaded, want); err != nil || !bytes.Equal(again, data) {
		t.Errorf("re-encoded evidence differs from the written file: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("encode reports: %v", err)
	}
	tampers := []struct {
		name  string
		alter func(reps []claudeGateReport)
	}{
		{"active flag flipped", func(r []claudeGateReport) { r[0].Active = false }},
		{"failure added", func(r []claudeGateReport) { r[1].Failures = []string{claudeGateFailTool} }},
		{"observed delta changed", func(r []claudeGateReport) { r[2].Tool.Observed++ }},
		{"prediction changed", func(r []claudeGateReport) { r[1].Full.NewPredicted++ }},
		{"model identity changed", func(r []claudeGateReport) { r[0].Model = claudeLedgerModels[1] }},
	}
	for _, tt := range tampers {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			altered, err := claudeGateEvaluate(ctx, set, loaded)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			tt.alter(altered)
			stored, err := json.Marshal(altered)
			if err != nil {
				t.Fatalf("encode altered report: %v", err)
			}
			tampered := loaded
			tampered.Report = stored
			if err := claudeGateStoredReportMatches(tampered, want); err == nil {
				t.Error("altered stored report passed replay")
			}
			got, err := claudeGateEvaluate(ctx, set, tampered)
			if err != nil {
				t.Fatalf("evaluate tampered evidence: %v", err)
			}
			if gotJSON, err := json.Marshal(got); err != nil || !bytes.Equal(gotJSON, wantJSON) {
				t.Errorf("verdicts changed with the stored report: %v", err)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		report json.RawMessage
	}{
		{"missing", nil},
		{"null", json.RawMessage("null")},
		{"truncated", json.RawMessage("[{")},
		{"empty array", json.RawMessage("[]")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tampered := loaded
			tampered.Report = tt.report
			if err := claudeGateStoredReportMatches(tampered, want); err == nil {
				t.Error("stored report passed replay")
			}
		})
	}
}

// TestClaudeTokenGateCommittedEvidenceIsCanonical checks that the committed file is
// the canonical encoding of its evidence and recomputed reports, byte for byte.
func TestClaudeTokenGateCommittedEvidenceIsCanonical(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	path := filepath.Join(claudeFixtureDir, claudeGateEvidenceName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read claude gate evidence: %v", err)
	}
	ev, err := claudeGateLoadEvidence(path)
	if err != nil {
		t.Fatalf("load claude gate evidence: %v", err)
	}
	reports, err := claudeGateEvaluate(context.Background(), set, ev)
	if err != nil {
		t.Fatalf("evaluate claude gate: %v", err)
	}
	data, err := claudeGateEncodeEvidence(ev, reports)
	if err != nil {
		t.Fatalf("encode claude gate evidence: %v", err)
	}
	if !bytes.Equal(data, raw) {
		t.Error("committed evidence is not the canonical encoding of its reports")
	}
}

// TestClaudeTokenGateEvaluatorCommit checks the evaluator_commit rule. An empty
// commit is allowed only with no attempts. A non-empty commit must be 40 lowercase
// hex characters, with or without attempts. A bad commit fails every model.
func TestClaudeTokenGateEvaluatorCommit(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	const good = claudeGateTestEvaluatorCommit
	tests := []struct {
		name     string
		commit   string
		attempts bool
		wantFail bool
	}{
		{"empty with no attempts", "", false, false},
		{"empty with attempts", "", true, true},
		{"valid with no attempts", good, false, false},
		{"valid with attempts", good, true, false},
		{"short with attempts", good[:39], true, true},
		{"long with attempts", good + "0", true, true},
		{"uppercase with attempts", strings.ToUpper(good), true, true},
		{"non-hex with attempts", strings.Repeat("g", 40), true, true},
		{"non-hex with no attempts", "not-a-commit", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := claudeGateSyntheticEvidence(t, set)
			ev.EvaluatorCommit = tt.commit
			if !tt.attempts {
				ev.Attempts = []claudeLedgerRecord{}
			}
			reports, err := claudeGateEvaluate(ctx, set, ev)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			for _, rep := range reports {
				has := slices.Contains(rep.Failures, claudeGateFailEvaluator)
				if has != tt.wantFail {
					t.Errorf("%s failures %q: evaluator failure = %v, want %v", rep.Model, rep.Failures, has, tt.wantFail)
				}
				if tt.wantFail && rep.Active {
					t.Errorf("%s is active with an invalid evaluator commit", rep.Model)
				}
			}
		})
	}
}
