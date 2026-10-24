package provider

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The gate cases are offline and synthetic. Each case builds its evidence or report
// in memory, and none of them writes observations.json. Judge cases use fixed
// numbers, so each gate and each exact threshold is checked on its own.

// claudeGateBaseReport returns a passing report with fixed numbers. Judge cases
// change part of it.
func claudeGateBaseReport() claudeGateReport {
	return claudeGateReport{
		Text: []claudeGateComparison{
			claudeGateCompare(claudeLedgerSlotCodeJSON, 1000, 1500, 1000),
			claudeGateCompare(claudeLedgerSlotMarkdown, 1000, 1040, 1000),
			claudeGateCompare(claudeLedgerSlotUnicode, 1000, 1040, 1000),
		},
		Tool: claudeGateCompare(claudeLedgerSlotTools, 300, 400, 300),
		Full: claudeGateCompare(claudeLedgerSlotMixedTools, 5000, 5400, 5000),
	}
}

// setText returns a mutation that replaces text comparison i with slot.
func setText(i int, slot claudeLedgerSlot, observed, oldPred, newPred int64) func(*claudeGateReport) {
	return func(rep *claudeGateReport) {
		rep.Text[i] = claudeGateCompare(slot, observed, oldPred, newPred)
	}
}

// setAllText returns a mutation that replaces every text comparison.
func setAllText(observed, oldPred, newPred int64) func(*claudeGateReport) {
	return func(rep *claudeGateReport) {
		for i, slot := range claudeGateTextSlots {
			rep.Text[i] = claudeGateCompare(slot, observed, oldPred, newPred)
		}
	}
}

// setTool returns a mutation that replaces the tool comparison.
func setTool(observed, oldPred, newPred int64) func(*claudeGateReport) {
	return func(rep *claudeGateReport) {
		rep.Tool = claudeGateCompare(claudeLedgerSlotTools, observed, oldPred, newPred)
	}
}

// setFull returns a mutation that replaces the full comparison.
func setFull(observed, oldPred, newPred int64) func(*claudeGateReport) {
	return func(rep *claudeGateReport) {
		rep.Full = claudeGateCompare(claudeLedgerSlotMixedTools, observed, oldPred, newPred)
	}
}

// combine returns a mutation that applies each of muts in order.
func combine(muts ...func(*claudeGateReport)) func(*claudeGateReport) {
	return func(rep *claudeGateReport) {
		for _, m := range muts {
			m(rep)
		}
	}
}

// claudeGateJudgeCase is one judge input. A nil want means the report passes.
type claudeGateJudgeCase struct {
	name   string
	mutate func(rep *claudeGateReport)
	want   []string
}

// claudeGateRunJudgeCases judges each case on a fresh report from base and checks
// the exact failure codes.
func claudeGateRunJudgeCases(t *testing.T, base func() claudeGateReport, cases []claudeGateJudgeCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rep := base()
			tt.mutate(&rep)
			claudeGateJudge(&rep)
			if !slices.Equal(rep.Failures, tt.want) {
				t.Errorf("failures = %q, want %q", rep.Failures, tt.want)
			}
		})
	}
}

// TestClaudeTokenGateJudgeOneFailurePerGate changes one thing so that exactly one
// gate fails, and checks the failure code it produces.
func TestClaudeTokenGateJudgeOneFailurePerGate(t *testing.T) {
	claudeGateRunJudgeCases(t, claudeGateBaseReport, []claudeGateJudgeCase{
		{"text 10 percent over", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 1101), []string{claudeGateFailText}},
		{"text 5 percent under", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 949), []string{claudeGateFailText}},
		{"text worse than old", setText(1, claudeLedgerSlotMarkdown, 1000, 1010, 1020), []string{claudeGateFailText}},
		{"text observed zero", setText(0, claudeLedgerSlotCodeJSON, 0, 0, 0), []string{claudeGateFailObserved}},
		{"text slot missing", func(rep *claudeGateReport) { rep.Text = rep.Text[:2] }, []string{claudeGateFailText}},
		{"category 50 percent over", setAllText(1000, 1040, 1021), []string{claudeGateFailCategory}},
		{"tool worse than old", setTool(300, 400, 401), []string{claudeGateFailTool}},
		{"tool observed zero", setTool(0, 0, 0), []string{claudeGateFailObserved}},
		{"full worse than old", setFull(5000, 5400, 5401), []string{claudeGateFailFull}},
		{"full 60 percent over", setFull(5000, 6000, 5601), []string{claudeGateFailFull}},
		{"all old zero, new off by one", combine(setText(0, claudeLedgerSlotCodeJSON, 1000, 1000, 1001), setText(1, claudeLedgerSlotMarkdown, 1000, 1000, 1000), setText(2, claudeLedgerSlotUnicode, 1000, 1000, 1000), setTool(300, 300, 300), setFull(5000, 5000, 5000)), []string{claudeGateFailText, claudeGateFailCategory}},
		{"negative prediction", setText(2, claudeLedgerSlotUnicode, 1000, 1040, -1), []string{claudeGateFailNegative}},
	})
}

// TestClaudeTokenGateJudgeBoundaries checks each exact threshold at its boundary and
// one step past it.
func TestClaudeTokenGateJudgeBoundaries(t *testing.T) {
	claudeGateRunJudgeCases(t, claudeGateBaseReport, []claudeGateJudgeCase{
		{"text 10 percent over, exact", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 1100), nil},
		{"text 10 percent over, one past", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 1101), []string{claudeGateFailText}},
		{"text 5 percent under, exact", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 950), nil},
		{"text 5 percent under, one past", setText(0, claudeLedgerSlotCodeJSON, 1000, 1500, 949), []string{claudeGateFailText}},
		{"category 50 percent, exact", setAllText(1000, 1040, 1020), nil},
		{"category 50 percent, one past", setAllText(1000, 1040, 1021), []string{claudeGateFailCategory}},
		{"tool no worse, exact", setTool(300, 400, 400), nil},
		{"tool no worse, one past", setTool(300, 400, 401), []string{claudeGateFailTool}},
		{"full 60 percent, exact", setFull(5000, 6000, 5600), nil},
		{"full 60 percent, one past", setFull(5000, 6000, 5601), []string{claudeGateFailFull}},
		{"all old zero, new exact", combine(setAllText(1000, 1000, 1000), setTool(300, 300, 300), setFull(5000, 5000, 5000)), nil},
	})
}

// TestClaudeTokenGateJudgeMaxInt checks the judge at math.MaxInt, where a naive
// int64 product or sum would overflow.
func TestClaudeTokenGateJudgeMaxInt(t *testing.T) {
	const maxInt = int64(math.MaxInt)
	exact := func() claudeGateReport {
		return claudeGateReport{
			Text: []claudeGateComparison{
				claudeGateCompare(claudeLedgerSlotCodeJSON, maxInt, maxInt, maxInt),
				claudeGateCompare(claudeLedgerSlotMarkdown, maxInt, maxInt, maxInt),
				claudeGateCompare(claudeLedgerSlotUnicode, maxInt, maxInt, maxInt),
			},
			Tool: claudeGateCompare(claudeLedgerSlotTools, 1, 1, 1),
			Full: claudeGateCompare(claudeLedgerSlotMixedTools, maxInt, maxInt, maxInt),
		}
	}
	claudeGateRunJudgeCases(t, exact, []claudeGateJudgeCase{
		{"exact at MaxInt passes", func(*claudeGateReport) {}, nil},
		{"old sum overflows int64 and passes", setAllText(maxInt, 0, maxInt), nil},
		{"text far under at MaxInt", combine(setText(0, claudeLedgerSlotCodeJSON, maxInt, 0, maxInt/2), setText(1, claudeLedgerSlotMarkdown, maxInt, 0, maxInt), setText(2, claudeLedgerSlotUnicode, maxInt, 0, maxInt)), []string{claudeGateFailText}},
		{"full 60 percent at MaxInt", setFull(maxInt, 0, 0), []string{claudeGateFailFull}},
		{"MinInt64 prediction fails closed", setText(0, claudeLedgerSlotCodeJSON, maxInt, maxInt, math.MinInt64), []string{claudeGateFailNegative}},
	})
}

// TestClaudeTokenGateTextRatioBoundaries checks the text ratio at its exact
// boundaries, at 1e18, and at int64 extremes.
func TestClaudeTokenGateTextRatioBoundaries(t *testing.T) {
	const maxInt = int64(math.MaxInt)
	tests := []struct {
		name      string
		pred, obs int64
		want      bool
	}{
		{"exactly 10 percent over", 110, 100, true},
		{"just over 10 percent", 111, 100, false},
		{"exactly 5 percent under", 95, 100, true},
		{"just over 5 percent under", 94, 100, false},
		{"zero observation", 0, 0, false},
		{"negative observation", 100, -100, false},
		{"exactly 10 percent over at 1e18", 1100000000000000000, 1000000000000000000, true},
		{"one past 10 percent over at 1e18", 1100000000000000001, 1000000000000000000, false},
		{"exact at MaxInt", maxInt, maxInt, true},
		{"prediction 1 against MaxInt", 1, maxInt, false},
		{"MaxInt prediction against 100", maxInt, 100, false},
		{"MinInt64 prediction", math.MinInt64, 100, false},
	}
	for _, tt := range tests {
		if got := claudeGateTextRatioOK(tt.pred, tt.obs); got != tt.want {
			t.Errorf("%s: claudeGateTextRatioOK(%d, %d) = %v, want %v", tt.name, tt.pred, tt.obs, got, tt.want)
		}
	}
}

// TestClaudeTokenGateCompareSignedErrors checks that signed and absolute errors are
// exact, including differences that exceed int64.
func TestClaudeTokenGateCompareSignedErrors(t *testing.T) {
	const maxInt = int64(math.MaxInt)
	tests := []struct {
		name                       string
		observed, oldPred, newPred int64
		oldSigned, newSigned       string
		oldAbs, newAbs             string
	}{
		{"small", 5, 2, 9, "-3", "4", "3", "4"},
		{"negative observed", -maxInt, 0, maxInt, "9223372036854775807", "18446744073709551614", "9223372036854775807", "18446744073709551614"},
		{"MinInt64 observed", math.MinInt64, 0, maxInt, "9223372036854775808", "18446744073709551615", "9223372036854775808", "18446744073709551615"},
	}
	for _, tt := range tests {
		c := claudeGateCompare(claudeLedgerSlotTools, tt.observed, tt.oldPred, tt.newPred)
		if c.OldSigned.String() != tt.oldSigned || c.NewSigned.String() != tt.newSigned ||
			c.OldAbs.String() != tt.oldAbs || c.NewAbs.String() != tt.newAbs {
			t.Errorf("%s: signed %s/%s abs %s/%s, want %s/%s abs %s/%s", tt.name,
				c.OldSigned, c.NewSigned, c.OldAbs, c.NewAbs, tt.oldSigned, tt.newSigned, tt.oldAbs, tt.newAbs)
		}
	}
}

// claudeGateSyntheticEvidence builds passing evidence for every candidate model in
// memory. Each observed prompt total is the candidate counter's own prediction, so
// every new error is zero. The data is synthetic and is never written to disk.
func claudeGateSyntheticEvidence(t *testing.T, set claudeFixtureSet) claudeGateEvidence {
	t.Helper()
	ctx := context.Background()
	const basePrompt = 1000
	ev := claudeGateEvidence{
		SchemaVersion: claudeGateSchemaVersion,
		CLIVersion:    "synthetic-cli",
		LibraryCommit: set.Manifest.Library.Commit,
		PayloadSHA256: map[string]string{},
		ToolsSHA256:   set.Manifest.Tools.SHA256,
		Launch:        claudeGateLaunchPinned(),
	}
	for _, pin := range set.Manifest.Payloads {
		ev.PayloadSHA256[pin.Name] = pin.SHA256
	}
	for _, model := range claudeLedgerModels {
		counter, err := claudeFixtureCounter(model)
		if err != nil {
			t.Fatalf("select counter for %s: %v", model, err)
		}
		newBase, err := counter.Count(claudeFixtureSystem)
		if err != nil {
			t.Fatalf("count baseline for %s: %v", model, err)
		}
		prompts := map[claudeLedgerSlot]int{claudeLedgerSlotBaseline: basePrompt}
		for _, slot := range claudeGateTextSlots {
			p, err := claudeGatePayload(set, claudeGateSlotPayload[slot])
			if err != nil {
				t.Fatalf("payload for %s: %v", slot, err)
			}
			delta, err := claudeFixtureDelta(counter, newBase, string(p.Bytes))
			if err != nil {
				t.Fatalf("delta for %s: %v", slot, err)
			}
			prompts[slot] = basePrompt + delta
		}
		toolDelta, err := claudeGateToolDelta(ctx, set, model, counter)
		if err != nil {
			t.Fatalf("tool delta for %s: %v", model, err)
		}
		prompts[claudeLedgerSlotTools] = basePrompt + int(toolDelta)
		full, err := claudeGateFullCount(ctx, set, model, claudeLedgerSlotMixedTools, counter)
		if err != nil {
			t.Fatalf("full count for %s: %v", model, err)
		}
		prompts[claudeLedgerSlotMixedTools] = int(full)
		for _, slot := range claudeLedgerSlots {
			req, err := claudeGateRequest(model, slot, set)
			if err != nil {
				t.Fatalf("build %s %s: %v", model, slot, err)
			}
			raw, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("encode %s %s: %v", model, slot, err)
			}
			p := prompts[slot]
			ev.Attempts = append(ev.Attempts, claudeLedgerRecord{
				AttemptID:      len(ev.Attempts) + 1,
				TimestampUTC:   "2026-10-23T12:00:00Z",
				Slot:           slot,
				RequestedModel: model,
				ObservedModel:  model,
				Status:         claudeLedgerStatusSucceeded,
				CLIVersion:     ev.CLIVersion,
				LibraryCommit:  ev.LibraryCommit,
				PayloadSHA256:  claudeGatePayloadSHA(set, slot),
				ToolsSHA256:    claudeGateToolsSHA(set, slot),
				Request:        raw,
				Usage:          &claudeLedgerUsage{Input: &p, PromptTotal: p},
			})
		}
	}
	return ev
}

// claudeGateRenumber sets each attempt ID to its position, so the sequence check
// holds after a test removes or adds a record.
func claudeGateRenumber(ev *claudeGateEvidence) {
	for i := range ev.Attempts {
		ev.Attempts[i].AttemptID = i + 1
	}
}

// claudeGateRecordIndex returns the position of the attempt for model and slot.
func claudeGateRecordIndex(t *testing.T, ev *claudeGateEvidence, model string, slot claudeLedgerSlot) int {
	t.Helper()
	for i := range ev.Attempts {
		if ev.Attempts[i].RequestedModel == model && ev.Attempts[i].Slot == slot {
			return i
		}
	}
	t.Fatalf("no attempt for %s %s", model, slot)
	return -1
}

// TestClaudeTokenGateSyntheticPassingMatrix checks that synthetic passing evidence
// activates every candidate through the measure and judge path, and that the
// production ctok set stays empty.
func TestClaudeTokenGateSyntheticPassingMatrix(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	ev := claudeGateSyntheticEvidence(t, set)
	reports, err := claudeGateEvaluate(context.Background(), set, ev)
	if err != nil {
		t.Fatalf("evaluate synthetic claude gate: %v", err)
	}
	for _, rep := range reports {
		if !rep.Active {
			t.Errorf("%s inactive with passing synthetic evidence: %q", rep.Model, rep.Failures)
		}
		if rep.OldStrategy == "" || rep.OldStrategy == TokenizerStrategyCtok {
			t.Errorf("%s old strategy = %q, want a non-ctok strategy", rep.Model, rep.OldStrategy)
		}
		if len(rep.Text) != len(claudeGateTextSlots) {
			t.Errorf("%s has %d text comparisons, want %d", rep.Model, len(rep.Text), len(claudeGateTextSlots))
		}
		if rep.CategoryNewAbs == nil || rep.CategoryNewAbs.Sign() != 0 {
			t.Errorf("%s category new error = %v, want 0", rep.Model, rep.CategoryNewAbs)
		}
	}
	if got := claudeGateActiveModels(reports); !slices.Equal(got, claudeLedgerModels) {
		t.Errorf("active models = %q, want %q", got, claudeLedgerModels)
	}
	if n := len(ctokActiveModelIDs); n != 0 {
		t.Errorf("production ctok model set has %d entries, want 0", n)
	}
}

// TestClaudeTokenGateOldCounterOffline checks that the pre-ctok counter loads
// offline for every candidate and never reports the ctok strategy.
func TestClaudeTokenGateOldCounterOffline(t *testing.T) {
	for _, model := range claudeLedgerModels {
		counter, meta, err := claudeGateOldCounter(model)
		if err != nil {
			t.Fatalf("old counter for %s: %v", model, err)
		}
		if meta.strategy == TokenizerStrategyCtok {
			t.Errorf("%s old strategy is ctok", model)
		}
		if n, err := counter.Count(claudeFixtureSystem); err != nil || n <= 0 {
			t.Errorf("%s old counter Count = %d, %v; want a positive count", model, n, err)
		}
	}
}

// TestClaudeTokenGateEvidenceCorruptionMatrix corrupts one part of passing evidence
// and checks the failure code. Record-level and evidence-wide faults mark every
// model inactive. Model-level faults leave the other models active.
func TestClaudeTokenGateEvidenceCorruptionMatrix(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	opus, sonnet, haiku := claudeLedgerModels[0], claudeLedgerModels[1], claudeLedgerModels[2]
	reqJSON := func(t *testing.T, model string, slot claudeLedgerSlot) json.RawMessage {
		t.Helper()
		req, err := claudeGateRequest(model, slot, set)
		if err != nil {
			t.Fatalf("build %s %s: %v", model, slot, err)
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("encode %s %s: %v", model, slot, err)
		}
		return raw
	}
	rec := func(t *testing.T, ev *claudeGateEvidence, model string, slot claudeLedgerSlot) *claudeLedgerRecord {
		t.Helper()
		return &ev.Attempts[claudeGateRecordIndex(t, ev, model, slot)]
	}
	other := claudeGateSHA256([]byte("other"))
	tests := []struct {
		name   string
		scope  string
		want   []string
		mutate func(t *testing.T, ev *claudeGateEvidence)
	}{
		{"record observed model differs", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).ObservedModel = haiku
		}},
		{"record observed model missing", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).ObservedModel = ""
		}},
		{"record usage total differs from components", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).Usage.PromptTotal++
		}},
		{"record usage missing", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).Usage = nil
		}},
		{"record timestamp not UTC", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).TimestampUTC = "2026-10-23T12:00:00+01:00"
		}},
		{"record request names another model", "", []string{claudeGateFailRecord}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).Request = reqJSON(t, haiku, claudeLedgerSlotCodeJSON)
		}},
		{"record request for another slot", opus, []string{claudeGateFailRequest}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotMarkdown).Request = reqJSON(t, opus, claudeLedgerSlotCodeJSON)
		}},
		{"record payload hash differs", opus, []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotCodeJSON).PayloadSHA256 = other
		}},
		{"record tools hash differs", opus, []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotTools).ToolsSHA256 = other
		}},
		{"record library commit differs", opus, []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, opus, claudeLedgerSlotBaseline).LibraryCommit = "deadbeef"
		}},
		{"record CLI version differs from evidence", "", []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			rec(t, ev, sonnet, claudeLedgerSlotBaseline).CLIVersion = "other-cli"
		}},
		{"record failed at launch", haiku, []string{claudeGateFailState, claudeGateFailMissing}, func(t *testing.T, ev *claudeGateEvidence) {
			r := rec(t, ev, haiku, claudeLedgerSlotUnicode)
			r.Status, r.Failure, r.ObservedModel, r.Usage = claudeLedgerStatusFailed, claudeLedgerFailLaunch, "", nil
		}},
		{"record reserved without outcome", haiku, []string{claudeGateFailState, claudeGateFailMissing}, func(t *testing.T, ev *claudeGateEvidence) {
			r := rec(t, ev, haiku, claudeLedgerSlotUnicode)
			r.Status, r.ObservedModel, r.Usage = claudeLedgerStatusReserved, "", nil
		}},
		{"slot removed", sonnet, []string{claudeGateFailMissing}, func(t *testing.T, ev *claudeGateEvidence) {
			idx := claudeGateRecordIndex(t, ev, sonnet, claudeLedgerSlotMarkdown)
			ev.Attempts = slices.Delete(ev.Attempts, idx, idx+1)
			claudeGateRenumber(ev)
		}},
		{"duplicate attempt", "", []string{claudeGateFailDuplicate}, func(t *testing.T, ev *claudeGateEvidence) {
			idx := claudeGateRecordIndex(t, ev, opus, claudeLedgerSlotCodeJSON)
			dup := ev.Attempts[claudeGateRecordIndex(t, ev, opus, claudeLedgerSlotBaseline)]
			dup.AttemptID = ev.Attempts[idx].AttemptID
			ev.Attempts[idx] = dup
		}},
		{"attempt budget exceeded", "", []string{claudeGateFailBudget}, func(t *testing.T, ev *claudeGateEvidence) {
			dup := ev.Attempts[len(ev.Attempts)-1]
			dup.AttemptID = len(ev.Attempts) + 1
			ev.Attempts = append(ev.Attempts, dup)
		}},
		{"launch controls differ", "", []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			ev.Launch.MaxOutputTokens = 65
		}},
		{"payload pin differs", "", []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			ev.PayloadSHA256["code_json"] = other
		}},
		{"tools pin differs", "", []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			ev.ToolsSHA256 = other
		}},
		{"library pin differs", "", []string{claudeGateFailPins}, func(t *testing.T, ev *claudeGateEvidence) {
			ev.LibraryCommit = "deadbeef"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := claudeGateSyntheticEvidence(t, set)
			tt.mutate(t, &ev)
			reports, err := claudeGateEvaluate(ctx, set, ev)
			if err != nil {
				t.Fatalf("evaluate corrupted evidence: %v", err)
			}
			for _, rep := range reports {
				if tt.scope != "" && rep.Model != tt.scope {
					if !rep.Active {
						t.Errorf("%s inactive, want active: %q", rep.Model, rep.Failures)
					}
					continue
				}
				if rep.Active {
					t.Errorf("%s active, want inactive", rep.Model)
				}
				for _, code := range tt.want {
					if !slices.Contains(rep.Failures, code) {
						t.Errorf("%s failures %q lack %q", rep.Model, rep.Failures, code)
					}
				}
			}
		})
	}
}

// TestClaudeTokenGateEvidenceLoadRejects checks the evidence loader on documents
// with a duplicate key, an unknown field, trailing data, the wrong schema, or no
// attempts array.
func TestClaudeTokenGateEvidenceLoadRejects(t *testing.T) {
	const head = `"cli_version":"c","library_commit":"x","payload_sha256":{},"tools_sha256":"t","launch":{}`
	tests := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{"valid minimal document", `{"schema_version":1,` + head + `,"attempts":[]}`, false},
		{"duplicate key", `{"schema_version":1,"schema_version":1,` + head + `,"attempts":[]}`, true},
		{"unknown field", `{"schema_version":1,` + head + `,"attempts":[],"extra":1}`, true},
		{"trailing data", `{"schema_version":1,` + head + `,"attempts":[]} {}`, true},
		{"wrong schema version", `{"schema_version":2,` + head + `,"attempts":[]}`, true},
		{"attempts missing", `{"schema_version":1,` + head + `}`, true},
		{"attempts null", `{"schema_version":1,` + head + `,"attempts":null}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), claudeGateEvidenceName)
			if err := os.WriteFile(path, []byte(tt.doc), 0o600); err != nil {
				t.Fatalf("write evidence: %v", err)
			}
			if _, err := claudeGateLoadEvidence(path); (err != nil) != tt.wantErr {
				t.Errorf("load error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

// semanticRecordingCounter records every text it counts and returns one token per
// call, so a test can see which fields reach the counter and in what order.
type semanticRecordingCounter struct {
	texts []string
}

// Count records text and returns one token.
func (c *semanticRecordingCounter) Count(text string) (int, error) {
	c.texts = append(c.texts, text)
	return 1, nil
}

// TestClaudeTokenGateSemanticTraversal checks which request fields reach the
// counter, in order. Empty fields are skipped. Every other text, including
// whitespace and Unicode, is passed through unchanged. Cancellation is covered by
// TestEstimateMessageTokensRespectsCancelledCtx and
// TestCtokSelectedEstimatesHonourCancelledContext in token_estimator_test.go.
func TestClaudeTokenGateSemanticTraversal(t *testing.T) {
	req := ChatRequest{
		Model: "claude-test-model",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "sys \u00a0 text\n"},
			{Role: MessageRoleAssistant, Name: "asst", ReasoningContent: "reason é", ToolCalls: []ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "a/b"}}}},
			{Role: MessageRoleTool, ToolCallID: "call-1", Content: "result 日本"},
			{Role: MessageRoleUser, Content: " \t"},
		},
		Tools: []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read", Description: "Read the file", Parameters: map[string]any{"type": "object"}}}},
	}
	want := []string{
		"function", "read", "Read the file", "type", "object",
		"system", "sys \u00a0 text\n",
		"assistant", "asst", "reason é", "call-1", "read", "path", "a/b",
		"tool", "call-1", "result 日本",
		"user", " \t",
	}
	counter := &semanticRecordingCounter{}
	if _, err := countRequestWithCounter(context.Background(), req, counter); err != nil {
		t.Fatalf("count request: %v", err)
	}
	if !slices.Equal(counter.texts, want) {
		t.Errorf("counted texts = %q, want %q", counter.texts, want)
	}
}

// TestClaudeTokenGateSemanticMapOrderIsStable checks that a map with several keys
// gives the same total on every run, although Go randomizes map iteration order.
func TestClaudeTokenGateSemanticMapOrderIsStable(t *testing.T) {
	req := ChatRequest{Messages: []Message{{
		Role:    MessageRoleUser,
		Content: "hi",
		ToolCalls: []ToolCall{{ID: "1", Name: "f", Arguments: map[string]any{
			"a": "x", "b": map[string]any{"c": 1, "d": true}, "e": []any{"p", "q"}, "f": nil,
		}}},
	}}}
	first := -1
	for i := range 50 {
		total, err := countRequestWithCounter(context.Background(), req, &semanticRecordingCounter{})
		if err != nil {
			t.Fatalf("count request: %v", err)
		}
		if first == -1 {
			first = total
		} else if total != first {
			t.Fatalf("run %d total = %d, first run = %d", i, total, first)
		}
	}
}

// TestClaudeTokenGateSanitizerViolations checks the structural walk. Credential
// keys, raw CLI output keys, and absolute paths are violations. Relative authored
// paths and token-count keys are not.
func TestClaudeTokenGateSanitizerViolations(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want int
	}{
		{"relative authored path", `{"description":"write under .steiner/tmp/fetched"}`, 0},
		{"absolute path value", `{"path":"/tmp/fetched"}`, 1},
		{"home path value", `{"path":"~/fetched"}`, 1},
		{"credential key", `{"API_KEY":"x"}`, 1},
		{"stderr key", `{"stderr":""}`, 1},
		{"result usage key", `{"result_usage":{}}`, 1},
		{"credential key in list element", `{"a":[{"password":"x"}]}`, 1},
		{"token count key", `{"input_tokens":1}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc any
			if err := json.Unmarshal([]byte(tt.doc), &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := claudeGateViolations("doc", doc, nil); len(got) != tt.want {
				t.Errorf("violations = %q, want %d", got, tt.want)
			}
		})
	}
}
