package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The Claude token gate measures each recorded attempt with the old tiktoken
// counter and the candidate ctok counter, then applies the acceptance gates. A
// model is active only when every gate passes. The gate never launches the CLI.
// Protocol, evidence loading, and request building are in
// token_estimator_claude_gate_protocol_test.go.

// claudeGateResidual is copied into every report. It states what the gates do
// not model.
const claudeGateResidual = "CLI framing (system prompt, tool note, CLI built-in tools) is not modeled. " +
	"Text and tool deltas cancel it only if it is the same in every slot. " +
	"The full slot includes it, so its error is not framing-free. " +
	"Tool and full slots have no underestimation limit."

// claudeGateComparison is one measured delta or full request. Signed errors are
// predicted minus observed.
type claudeGateComparison struct {
	Slot         string `json:"slot"`
	Observed     int64  `json:"observed"`
	OldPredicted int64  `json:"old_predicted"`
	NewPredicted int64  `json:"new_predicted"`
	OldSigned    int64  `json:"old_signed_error"`
	NewSigned    int64  `json:"new_signed_error"`
	OldAbs       int64  `json:"old_abs_error"`
	NewAbs       int64  `json:"new_abs_error"`
}

// claudeGateReport is the verdict for one candidate model.
type claudeGateReport struct {
	Model          string                 `json:"model"`
	Active         bool                   `json:"active"`
	Failures       []string               `json:"failures,omitempty"`
	OldStrategy    string                 `json:"old_strategy,omitempty"`
	Text           []claudeGateComparison `json:"text,omitempty"`
	Tool           claudeGateComparison   `json:"tool"`
	Full           claudeGateComparison   `json:"full"`
	CategoryOldAbs int64                  `json:"category_old_abs_sum"`
	CategoryNewAbs int64                  `json:"category_new_abs_sum"`
	Residual       string                 `json:"residual"`
}

// claudeGateOldCounter selects the pre-ctok counter for model: an empty ctok table
// with the real tiktoken codec loader. Its strategy must not be ctok.
func claudeGateOldCounter(model string) (textCounter, tokenizerMetadata, error) {
	counter, meta, err := selectTextCounter(model, counterSelectOptions{
		ids:         map[string]struct{}{},
		codecLoader: tokenizerForModel,
	})
	if err != nil {
		return nil, meta, fmt.Errorf("select old claude gate counter: %w", err)
	}
	if meta.strategy == TokenizerStrategyCtok {
		return nil, meta, fmt.Errorf("old claude gate counter selected ctok")
	}
	return counter, meta, nil
}

// claudeGateFullCount counts the whole request for slot through the semantic seam.
func claudeGateFullCount(ctx context.Context, set claudeFixtureSet, model string, slot claudeLedgerSlot, counter textCounter) (int64, error) {
	req, err := claudeGateRequest(model, slot, set)
	if err != nil {
		return 0, err
	}
	n, err := countRequestWithCounter(ctx, req, counter)
	if err != nil {
		return 0, fmt.Errorf("count claude gate request: %w", err)
	}
	return int64(n), nil
}

// claudeGateMeasure fills rep.Text, rep.Tool, and rep.Full from the recorded
// prompt totals and the two counters. Text and tool values are deltas from the
// baseline slot, so the CLI framing shared by every slot cancels. The full slot
// is compared whole. Text deltas count the raw payload, and the tool and full
// values count the request through countRequestWithCounter.
func claudeGateMeasure(ctx context.Context, set claudeFixtureSet, model string, recs map[claudeLedgerSlot]claudeLedgerRecord, rep *claudeGateReport) error {
	oldC, meta, err := claudeGateOldCounter(model)
	if err != nil {
		return err
	}
	newC, err := claudeFixtureCounter(model)
	if err != nil {
		return err
	}
	rep.OldStrategy = meta.strategy
	prompt := func(slot claudeLedgerSlot) int64 { return int64(recs[slot].Usage.PromptTotal) }
	baseline := claudeLedgerSlotBaseline
	oldBase, err := oldC.Count(claudeFixtureSystem)
	if err != nil {
		return fmt.Errorf("count old claude gate baseline: %w", err)
	}
	newBase, err := newC.Count(claudeFixtureSystem)
	if err != nil {
		return fmt.Errorf("count new claude gate baseline: %w", err)
	}
	for _, slot := range claudeGateTextSlots {
		p, err := claudeGatePayload(set, claudeGateSlotPayload[slot])
		if err != nil {
			return err
		}
		oldDelta, err := claudeFixtureDelta(oldC, oldBase, string(p.Bytes))
		if err != nil {
			return err
		}
		newDelta, err := claudeFixtureDelta(newC, newBase, string(p.Bytes))
		if err != nil {
			return err
		}
		rep.Text = append(rep.Text, claudeGateCompare(slot, prompt(slot)-prompt(baseline), int64(oldDelta), int64(newDelta)))
	}
	oldTool, err := claudeGateToolDelta(ctx, set, model, oldC)
	if err != nil {
		return err
	}
	newTool, err := claudeGateToolDelta(ctx, set, model, newC)
	if err != nil {
		return err
	}
	rep.Tool = claudeGateCompare(claudeLedgerSlotTools, prompt(claudeLedgerSlotTools)-prompt(baseline), oldTool, newTool)
	oldFull, err := claudeGateFullCount(ctx, set, model, claudeLedgerSlotMixedTools, oldC)
	if err != nil {
		return err
	}
	newFull, err := claudeGateFullCount(ctx, set, model, claudeLedgerSlotMixedTools, newC)
	if err != nil {
		return err
	}
	rep.Full = claudeGateCompare(claudeLedgerSlotMixedTools, prompt(claudeLedgerSlotMixedTools), oldFull, newFull)
	return nil
}

// claudeGateToolDelta returns the semantic request diff of the tools slot over the
// baseline slot, using counter.
func claudeGateToolDelta(ctx context.Context, set claudeFixtureSet, model string, counter textCounter) (int64, error) {
	tools, err := claudeGateFullCount(ctx, set, model, claudeLedgerSlotTools, counter)
	if err != nil {
		return 0, err
	}
	base, err := claudeGateFullCount(ctx, set, model, claudeLedgerSlotBaseline, counter)
	if err != nil {
		return 0, err
	}
	return tools - base, nil
}

// claudeGateCompare builds one comparison. Signed errors are predicted minus observed.
func claudeGateCompare(slot claudeLedgerSlot, observed, oldPred, newPred int64) claudeGateComparison {
	c := claudeGateComparison{
		Slot:         string(slot),
		Observed:     observed,
		OldPredicted: oldPred,
		NewPredicted: newPred,
		OldSigned:    oldPred - observed,
		NewSigned:    newPred - observed,
	}
	c.OldAbs = claudeGateAbs(c.OldSigned)
	c.NewAbs = claudeGateAbs(c.NewSigned)
	return c
}

// claudeGateAbs returns the absolute value of v. Callers pass in-bounds values, so
// it cannot overflow.
func claudeGateAbs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// claudeGateInBounds reports whether v is a token count the gate compares. Values
// outside the bound fail closed, so the integer arithmetic below cannot overflow.
func claudeGateInBounds(v int64) bool {
	return v >= -claudeGateMaxTokens && v <= claudeGateMaxTokens
}

// claudeGateTextRatioOK reports whether pred is within 10% of obs in absolute
// relative error and at most 5% below obs. obs must be positive and both values
// must be in bounds. The integer forms 10*|pred-obs| <= obs and
// 20*(obs-pred) <= obs are exact at the boundaries.
func claudeGateTextRatioOK(pred, obs int64) bool {
	if obs <= 0 || !claudeGateInBounds(pred) || !claudeGateInBounds(obs) {
		return false
	}
	return 10*claudeGateAbs(pred-obs) <= obs && 20*(obs-pred) <= obs
}

// claudeGateFail adds code to rep.Failures once.
func claudeGateFail(rep *claudeGateReport, code string) {
	if !slices.Contains(rep.Failures, code) {
		rep.Failures = append(rep.Failures, code)
	}
}

// claudeGateJudge sets rep.Failures from the comparisons in rep. It reads no
// counter, so the gates can be tested with fixed numbers. A comparison outside the
// bounds fails closed and no threshold is evaluated.
//
// Text: each delta is observed and positive, within 10% of the observation, at
// most 5% under it, and no further from it than the old prediction. The three
// absolute errors sum to at most half the old sum. Tool: observed and no further
// than the old prediction. Full: no further than the old prediction and at most
// 60% of it.
func claudeGateJudge(rep *claudeGateReport) {
	for _, c := range append([]claudeGateComparison{rep.Tool, rep.Full}, rep.Text...) {
		if !claudeGateInBounds(c.Observed) || !claudeGateInBounds(c.OldPredicted) || !claudeGateInBounds(c.NewPredicted) {
			claudeGateFail(rep, claudeGateFailBounds)
			return
		}
	}
	if len(rep.Text) != len(claudeGateTextSlots) {
		claudeGateFail(rep, claudeGateFailText)
	}
	var oldSum, newSum int64
	for _, c := range rep.Text {
		if c.Observed <= 0 {
			claudeGateFail(rep, claudeGateFailObserved)
			continue
		}
		if !claudeGateTextRatioOK(c.NewPredicted, c.Observed) || c.NewAbs > c.OldAbs {
			claudeGateFail(rep, claudeGateFailText)
		}
		oldSum += c.OldAbs
		newSum += c.NewAbs
	}
	rep.CategoryOldAbs, rep.CategoryNewAbs = oldSum, newSum
	if 2*newSum > oldSum {
		claudeGateFail(rep, claudeGateFailCategory)
	}
	if rep.Tool.Observed <= 0 {
		claudeGateFail(rep, claudeGateFailObserved)
	} else if rep.Tool.NewAbs > rep.Tool.OldAbs {
		claudeGateFail(rep, claudeGateFailTool)
	}
	if rep.Full.NewAbs > rep.Full.OldAbs || 5*rep.Full.NewAbs > 3*rep.Full.OldAbs {
		claudeGateFail(rep, claudeGateFailFull)
	}
}

// claudeGateEvaluate returns one report per candidate model, in claudeLedgerModels
// order. Evidence-wide faults mark every model inactive. Otherwise each model is
// judged on its own six slots. A counter error is returned, not reported as a
// verdict. A cancelled context returns its error before any work.
func claudeGateEvaluate(ctx context.Context, set claudeFixtureSet, ev claudeGateEvidence) ([]claudeGateReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	global := claudeGateEvidenceFailures(set, ev)
	reports := make([]claudeGateReport, 0, len(claudeLedgerModels))
	for _, model := range claudeLedgerModels {
		rep := claudeGateReport{Model: model, Residual: claudeGateResidual}
		if len(global) > 0 {
			rep.Failures = slices.Clone(global)
		} else {
			recs, fails := claudeGateModelRecords(set, ev, model)
			rep.Failures = fails
			if len(fails) == 0 {
				if err := claudeGateMeasure(ctx, set, model, recs, &rep); err != nil {
					return nil, fmt.Errorf("measure %s: %w", model, err)
				}
				claudeGateJudge(&rep)
			}
		}
		rep.Active = len(rep.Failures) == 0
		reports = append(reports, rep)
	}
	return reports, nil
}

// claudeGateActiveModels returns the models whose reports are active, in order.
func claudeGateActiveModels(reports []claudeGateReport) []string {
	var active []string
	for _, rep := range reports {
		if rep.Active {
			active = append(active, rep.Model)
		}
	}
	return active
}

// TestClaudeTokenReplay replays the committed evidence. With no attempts every
// candidate model must be inactive with a missing-slot failure, and the pins must
// match the fixtures. Production ctok IDs must be a subset of the active models.
func TestClaudeTokenReplay(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	ev, err := claudeGateLoadEvidence(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		t.Fatalf("load claude gate evidence: %v", err)
	}
	if len(ev.Attempts) != 0 {
		t.Fatalf("committed evidence has %d attempts, want 0 until the live run", len(ev.Attempts))
	}
	if !claudeGatePinsMatch(set, ev) {
		t.Fatal("committed evidence pins do not match the fixtures")
	}
	reports, err := claudeGateEvaluate(context.Background(), set, ev)
	if err != nil {
		t.Fatalf("evaluate claude gate: %v", err)
	}
	for _, rep := range reports {
		if rep.Active {
			t.Errorf("%s is active with empty evidence", rep.Model)
		}
		if !slices.Contains(rep.Failures, claudeGateFailMissing) {
			t.Errorf("%s failures %q lack %q", rep.Model, rep.Failures, claudeGateFailMissing)
		}
	}
	active := claudeGateActiveModels(reports)
	for id := range ctokActiveModelIDs {
		if !slices.Contains(active, id) {
			t.Errorf("production ctok model %q is not active in the gate", id)
		}
	}
}

// TestClaudeGateBuilderRequests checks every model and slot against the authored
// request contract, then renders it through the production CLI helpers that step 5
// uses. The system text is the fixture system text plus the raw payload with no
// separator. The CLI adds only the constant tool note.
func TestClaudeGateBuilderRequests(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	ctx := context.Background()
	for _, model := range claudeLedgerModels {
		counter, err := claudeFixtureCounter(model)
		if err != nil {
			t.Fatalf("select counter for %s: %v", model, err)
		}
		for _, slot := range claudeLedgerSlots {
			req, err := claudeGateRequest(model, slot, set)
			if err != nil {
				t.Fatalf("build %s %s: %v", model, slot, err)
			}
			wantSystem := claudeFixtureSystem
			if name := claudeGateSlotPayload[slot]; name != "" {
				p, err := claudeGatePayload(set, name)
				if err != nil {
					t.Fatalf("payload for %s: %v", slot, err)
				}
				wantSystem += string(p.Bytes)
			}
			if req.Model != model || len(req.Messages) != 2 ||
				req.Messages[0].Role != MessageRoleSystem || req.Messages[0].Content != wantSystem ||
				req.Messages[1].Role != MessageRoleUser || req.Messages[1].Content != claudeFixtureUser {
				t.Errorf("%s %s: request does not match the authored system and user messages", model, slot)
			}
			if req.MaxTokens == nil || *req.MaxTokens != claudeGateMaxOutputTokens {
				t.Errorf("%s %s: max tokens is not %d", model, slot, claudeGateMaxOutputTokens)
			}
			wantTools := 0
			if claudeGateHasTools(slot) {
				wantTools = len(set.Tools)
			}
			if len(req.Tools) != wantTools {
				t.Errorf("%s %s: %d tools, want %d", model, slot, len(req.Tools), wantTools)
			}
			if got, want := claudeSubSystemPrompt(req.Messages), wantSystem+"\n\n"+claudeSubToolNote; got != want {
				t.Errorf("%s %s: CLI system prompt is not the authored system text plus the tool note", model, slot)
			}
			if msgs := claudeSubMessages(req.Messages); len(msgs) != 1 || msgs[0].Content != claudeFixtureUser {
				t.Errorf("%s %s: CLI messages are not the user message alone", model, slot)
			}
			if _, err := countRequestWithCounter(ctx, req, counter); err != nil {
				t.Errorf("count %s %s: %v", model, slot, err)
			}
		}
	}
	if _, err := claudeGateRequest(claudeLedgerModels[0], claudeLedgerSlot("bogus"), set); err == nil {
		t.Error("unknown slot built a request")
	}
}

// TestClaudeGateTextRatioBounds checks the text ratio at its exact boundaries and
// at int64 extremes, where an unguarded product would overflow.
func TestClaudeGateTextRatioBounds(t *testing.T) {
	tests := []struct {
		name string
		pred int64
		obs  int64
		want bool
	}{
		{"exactly 10 percent over", 110, 100, true},
		{"just over 10 percent", 111, 100, false},
		{"exactly 5 percent under", 95, 100, true},
		{"just over 5 percent under", 94, 100, false},
		{"zero observation", 0, 0, false},
		{"negative observation", 100, -100, false},
		{"prediction at the bound", claudeGateMaxTokens, claudeGateMaxTokens, true},
		{"prediction above the bound", claudeGateMaxTokens + 1, claudeGateMaxTokens + 1, false},
		{"maximum int64 prediction", math.MaxInt64, 100, false},
		{"minimum int64 prediction", math.MinInt64, 100, false},
	}
	for _, tt := range tests {
		if got := claudeGateTextRatioOK(tt.pred, tt.obs); got != tt.want {
			t.Errorf("%s: claudeGateTextRatioOK(%d, %d) = %v, want %v", tt.name, tt.pred, tt.obs, got, tt.want)
		}
	}
}

// claudeGateForbiddenMarkers are substrings that evidence and reports must not
// contain: absolute home or temp paths and credential-like keys.
var claudeGateForbiddenMarkers = []string{"/home/", "/users/", "api_key", "apikey", "authorization", "password", "secret", "credential", "bearer "}

// claudeGateCheckClean fails t when data contains a forbidden marker.
func claudeGateCheckClean(t *testing.T, what string, data []byte) {
	t.Helper()
	lower := strings.ToLower(string(data))
	for _, marker := range claudeGateForbiddenMarkers {
		if strings.Contains(lower, marker) {
			t.Errorf("%s contains forbidden marker %q", what, marker)
		}
	}
}

// TestClaudeGateEvidenceSanitized checks the committed evidence, the authored
// requests, and the reports for absolute paths and credential keys. It also checks
// that the evidence holds exactly the documented top-level keys, so no CLI result
// usage can be stored, and that every failure code is a fixed gate code.
func TestClaudeGateEvidenceSanitized(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	path := filepath.Join(claudeFixtureDir, claudeGateEvidenceName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read claude gate evidence: %v", err)
	}
	claudeGateCheckClean(t, "evidence", raw)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("parse claude gate evidence keys: %v", err)
	}
	wantKeys := []string{"schema_version", "cli_version", "library_commit", "payload_sha256", "tools_sha256", "launch", "attempts"}
	if len(top) != len(wantKeys) {
		t.Errorf("evidence has %d top-level keys, want %d", len(top), len(wantKeys))
	}
	for _, key := range wantKeys {
		if _, ok := top[key]; !ok {
			t.Errorf("evidence lacks top-level key %q", key)
		}
	}
	for _, model := range claudeLedgerModels {
		for _, slot := range claudeLedgerSlots {
			req, err := claudeGateRequest(model, slot, set)
			if err != nil {
				t.Fatalf("build %s %s: %v", model, slot, err)
			}
			data, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("encode %s %s: %v", model, slot, err)
			}
			claudeGateCheckClean(t, fmt.Sprintf("%s %s request", model, slot), data)
		}
	}
	ev, err := claudeGateLoadEvidence(path)
	if err != nil {
		t.Fatalf("load claude gate evidence: %v", err)
	}
	reports, err := claudeGateEvaluate(context.Background(), set, ev)
	if err != nil {
		t.Fatalf("evaluate claude gate: %v", err)
	}
	allowed := []string{
		claudeGateFailRecord, claudeGateFailState, claudeGateFailMissing, claudeGateFailDuplicate,
		claudeGateFailPins, claudeGateFailRequest, claudeGateFailBudget, claudeGateFailBounds,
		claudeGateFailObserved, claudeGateFailText, claudeGateFailCategory, claudeGateFailTool, claudeGateFailFull,
	}
	for _, rep := range reports {
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("encode report %s: %v", rep.Model, err)
		}
		claudeGateCheckClean(t, "report "+rep.Model, data)
		for _, code := range rep.Failures {
			if !slices.Contains(allowed, code) {
				t.Errorf("%s failure %q is not a fixed gate code", rep.Model, code)
			}
		}
	}
}
