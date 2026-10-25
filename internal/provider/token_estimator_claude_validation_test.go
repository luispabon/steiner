package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
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
// token_estimator_claude_gate_protocol_test.go. Negative and semantic cases are
// in token_estimator_claude_gate_cases_test.go. Stored report replay is in
// token_estimator_claude_gate_replay_test.go.

// claudeGateResidual is copied into every report. It states what the gates do
// not model.
const claudeGateResidual = "CLI framing (system prompt, tool note, CLI built-in tools) is not modeled. " +
	"Text and tool deltas cancel it only if it is the same in every slot. " +
	"The full slot includes it, so its error is not framing-free. " +
	"Tool and full slots have no underestimation limit."

// claudeGateComparison is one measured delta or full request. Signed errors are
// predicted minus observed. Errors are math/big values, so no pair of int64
// inputs can overflow them.
type claudeGateComparison struct {
	Slot         string   `json:"slot"`
	Observed     int64    `json:"observed"`
	OldPredicted int64    `json:"old_predicted"`
	NewPredicted int64    `json:"new_predicted"`
	OldSigned    *big.Int `json:"old_signed_error"`
	NewSigned    *big.Int `json:"new_signed_error"`
	OldAbs       *big.Int `json:"old_abs_error"`
	NewAbs       *big.Int `json:"new_abs_error"`
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
	CategoryOldAbs *big.Int               `json:"category_old_abs_sum"`
	CategoryNewAbs *big.Int               `json:"category_new_abs_sum"`
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
	}
	c.OldSigned, c.OldAbs = claudeGateError(oldPred, observed)
	c.NewSigned, c.NewAbs = claudeGateError(newPred, observed)
	return c
}

// claudeGateError returns pred minus obs and its absolute value. The subtraction
// runs in math/big, so it is exact for every pair of int64 values.
func claudeGateError(pred, obs int64) (signed, abs *big.Int) {
	signed = new(big.Int).Sub(big.NewInt(pred), big.NewInt(obs))
	return signed, new(big.Int).Abs(signed)
}

// claudeGateTextRatioOK reports whether pred is within 10% of obs in absolute
// relative error and at most 5% below obs. obs must be positive. The checks
// 10*|pred-obs| <= obs and 20*(obs-pred) <= obs are evaluated in math/big, so
// they are exact at the boundaries for every int64 input.
func claudeGateTextRatioOK(pred, obs int64) bool {
	if obs <= 0 {
		return false
	}
	signed, abs := claudeGateError(pred, obs)
	bigObs := big.NewInt(obs)
	over := new(big.Int).Mul(abs, big.NewInt(10))
	under := new(big.Int).Mul(new(big.Int).Neg(signed), big.NewInt(20))
	return over.Cmp(bigObs) <= 0 && under.Cmp(bigObs) <= 0
}

// claudeGateFail adds code to rep.Failures once.
func claudeGateFail(rep *claudeGateReport, code string) {
	if !slices.Contains(rep.Failures, code) {
		rep.Failures = append(rep.Failures, code)
	}
}

// claudeGateJudge sets rep.Failures from the comparisons in rep. It reads no
// counter, so the gates can be tested with fixed numbers. A negative prediction
// fails closed and stops the judge, because a token count cannot be negative.
// No other bound applies: every comparison is exact in math/big.
//
// Text: each delta is observed and positive, within 10% of the observation, at
// most 5% under it, and no further from it than the old prediction. The three
// absolute errors sum to at most half the old sum. Tool: observed and no further
// than the old prediction. Full: no further than the old prediction and at most
// 60% of it.
func claudeGateJudge(rep *claudeGateReport) {
	for _, c := range append([]claudeGateComparison{rep.Tool, rep.Full}, rep.Text...) {
		if c.OldPredicted < 0 || c.NewPredicted < 0 {
			claudeGateFail(rep, claudeGateFailNegative)
			return
		}
	}
	if len(rep.Text) != len(claudeGateTextSlots) {
		claudeGateFail(rep, claudeGateFailText)
	}
	oldSum, newSum := new(big.Int), new(big.Int)
	for _, c := range rep.Text {
		if c.Observed <= 0 {
			claudeGateFail(rep, claudeGateFailObserved)
			continue
		}
		if !claudeGateTextRatioOK(c.NewPredicted, c.Observed) || c.NewAbs.Cmp(c.OldAbs) > 0 {
			claudeGateFail(rep, claudeGateFailText)
		}
		oldSum.Add(oldSum, c.OldAbs)
		newSum.Add(newSum, c.NewAbs)
	}
	rep.CategoryOldAbs, rep.CategoryNewAbs = oldSum, newSum
	if new(big.Int).Mul(newSum, big.NewInt(2)).Cmp(oldSum) > 0 {
		claudeGateFail(rep, claudeGateFailCategory)
	}
	if rep.Tool.Observed <= 0 {
		claudeGateFail(rep, claudeGateFailObserved)
	} else if rep.Tool.NewAbs.Cmp(rep.Tool.OldAbs) > 0 {
		claudeGateFail(rep, claudeGateFailTool)
	}
	fiveNew := new(big.Int).Mul(rep.Full.NewAbs, big.NewInt(5))
	threeOld := new(big.Int).Mul(rep.Full.OldAbs, big.NewInt(3))
	if rep.Full.NewAbs.Cmp(rep.Full.OldAbs) > 0 || fiveNew.Cmp(threeOld) > 0 {
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

// TestClaudeTokenReplay replays the committed evidence. The pins must match the
// fixtures, and the stored report must equal the recomputed report. With no
// attempts every candidate model must be inactive with a missing-slot failure.
// Populated evidence is replayed by the synthetic tests in
// token_estimator_claude_gate_replay_test.go.
func TestClaudeTokenReplay(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	ev, err := claudeGateLoadEvidence(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		t.Fatalf("load claude gate evidence: %v", err)
	}
	if !claudeGatePinsMatch(set, ev) {
		t.Fatal("committed evidence pins do not match the fixtures")
	}
	reports, err := claudeGateEvaluate(context.Background(), set, ev)
	if err != nil {
		t.Fatalf("evaluate claude gate: %v", err)
	}
	if err := claudeGateStoredReportMatches(ev, reports); err != nil {
		t.Fatalf("committed evidence: %v", err)
	}
	if len(ev.Attempts) == 0 {
		for _, rep := range reports {
			if rep.Active {
				t.Errorf("%s is active with empty evidence", rep.Model)
			}
			if !slices.Contains(rep.Failures, claudeGateFailMissing) {
				t.Errorf("%s failures %q lack %q", rep.Model, rep.Failures, claudeGateFailMissing)
			}
		}
	}
	// Step 6 must make this set equal the exact passing set. Until then each
	// production ctok ID must be an active model.
	active := claudeGateActiveModels(reports)
	for id := range ctokActiveModelIDs {
		if !slices.Contains(active, id) {
			t.Errorf("production ctok model %q is not active in the gate", id)
		}
	}
}

// TestClaudeTokenGateBuilderRequests checks every model and slot against the authored
// request contract, then renders it through the production CLI helpers that step 5
// uses. The system text is the fixture system text plus the raw payload with no
// separator. The CLI adds only the constant tool note.
func TestClaudeTokenGateBuilderRequests(t *testing.T) {
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

// claudeGateForbiddenKeys is the fixed set of sensitive keys that evidence and
// reports must not hold, plus raw CLI output keys that the gate never stores. A
// key is compared exactly after claudeGateNormalizeKey, so a key that only
// contains one of these words is not rejected.
var claudeGateForbiddenKeys = []string{
	"apikey", "token", "secret", "password", "authorization", "cookie", "credential", "sessionkey",
	"bearer", "stderr", "resultusage",
}

// claudeGateViolations walks a decoded JSON value and appends one message to out
// for each forbidden object key and each path-like string value (see
// claudeGateIsPathValue). Authored
// relative paths, such as .steiner/tmp/fetched in a tool description, pass.
func claudeGateViolations(path string, v any, out []string) []string {
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			if slices.Contains(claudeGateForbiddenKeys, claudeGateNormalizeKey(key)) {
				out = append(out, fmt.Sprintf("%s has forbidden key %q", path, key))
			}
			out = claudeGateViolations(path+"."+key, child, out)
		}
	case []any:
		for i, child := range x {
			out = claudeGateViolations(fmt.Sprintf("%s[%d]", path, i), child, out)
		}
	case string:
		if claudeGateIsPathValue(x) {
			out = append(out, fmt.Sprintf("%s is a path value", path))
		}
	}
	return out
}

// claudeGateNormalizeKey lowercases key and removes '-', '_', and '.'.
func claudeGateNormalizeKey(key string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
}

// claudeGateIsPathValue reports whether s begins like a path: a Unix root "/", a
// Windows drive ("C:/" or "C:\"), a UNC share ("\\"), or a home path ("~/" or
// "~\"). A path-like sequence later in the text does not count.
func claudeGateIsPathValue(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		return true
	}
	letter := len(s) > 0 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z'))
	return len(s) >= 3 && letter && s[1] == ':' && (s[2] == '/' || s[2] == '\\')
}

// claudeGateCheckClean fails t for every violation in the JSON document data.
func claudeGateCheckClean(t *testing.T, what string, data []byte) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Errorf("%s is not JSON: %v", what, err)
		return
	}
	for _, msg := range claudeGateViolations(what, doc, nil) {
		t.Error(msg)
	}
}

// TestClaudeTokenGateEvidenceSanitized checks the committed evidence, the authored
// requests, and the reports for absolute paths and credential or CLI-output keys.
// It also checks that the evidence holds exactly the documented top-level keys, so
// no CLI result usage can be stored, and that every failure code is a fixed gate code.
func TestClaudeTokenGateEvidenceSanitized(t *testing.T) {
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
	wantKeys := []string{"schema_version", "cli_version", "library_commit", "payload_sha256", "tools_sha256", "launch", "evaluator_commit", "attempts", "report"}
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
		claudeGateFailPins, claudeGateFailRequest, claudeGateFailBudget, claudeGateFailNegative,
		claudeGateFailObserved, claudeGateFailText, claudeGateFailCategory, claudeGateFailTool, claudeGateFailFull,
		claudeGateFailEvaluator,
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
