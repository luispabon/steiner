package provider

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// errClaudeCaptureMap marks a capture that does not map one-to-one to its attempt, or
// a replayed capture that does not match its recorded attempt.
var errClaudeCaptureMap = errors.New("claude capture mapping rejected")

// claudeCaptureKey is the mapping key of a capture: the observed model and the
// terminal prompt total.
type claudeCaptureKey struct {
	Model       string
	PromptTotal int
}

// claudeCaptureMatch maps each capture key to one succeeded attempt with the same
// observed model and prompt total. It returns the attempt index for each key, in key
// order. It fails on a capture that matches no attempt (unknown), two captures for one
// attempt (duplicate), two attempts sharing a key (ambiguous), and a succeeded attempt
// that no capture matches (unmatched).
func claudeCaptureMatch(attempts []claudeLedgerRecord, keys []claudeCaptureKey) ([]int, error) {
	byKey := map[claudeCaptureKey][]int{}
	for i, rec := range attempts {
		if rec.Status != claudeLedgerStatusSucceeded {
			continue
		}
		if rec.Usage == nil {
			return nil, fmt.Errorf("%w: attempt %d has no usage", errClaudeCaptureMap, rec.AttemptID)
		}
		k := claudeCaptureKey{Model: rec.ObservedModel, PromptTotal: rec.Usage.PromptTotal}
		byKey[k] = append(byKey[k], i)
		if len(byKey[k]) > 1 {
			return nil, fmt.Errorf("%w: attempt %d repeats model %s prompt total %d", errClaudeCaptureMap, rec.AttemptID, k.Model, k.PromptTotal)
		}
	}
	matched := make([]int, len(keys))
	used := map[int]int{}
	for c, k := range keys {
		idx, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("%w: capture %d (model %s, prompt total %d) matches no attempt", errClaudeCaptureMap, c, k.Model, k.PromptTotal)
		}
		if prev, dup := used[idx[0]]; dup {
			return nil, fmt.Errorf("%w: captures %d and %d match attempt %d", errClaudeCaptureMap, prev, c, attempts[idx[0]].AttemptID)
		}
		used[idx[0]] = c
		matched[c] = idx[0]
	}
	for i, rec := range attempts {
		if _, ok := used[i]; rec.Status == claudeLedgerStatusSucceeded && !ok {
			return nil, fmt.Errorf("%w: attempt %d has no capture", errClaudeCaptureMap, rec.AttemptID)
		}
	}
	return matched, nil
}

// claudeCaptureReplayAny replays one capture through claudeLedgerObserveStream for each
// candidate model and returns the observation of the model it names. A capture that no
// candidate replays is an error.
func claudeCaptureReplayAny(data []byte) (claudeLedgerObservation, error) {
	var last error
	for _, model := range claudeLedgerModels {
		obs, err := claudeLedgerObserveStream(data, model)
		if err == nil {
			return obs, nil
		}
		last = err
	}
	return claudeLedgerObservation{}, fmt.Errorf("%w: no candidate model replays: %w", errClaudeCaptureSanitize, last)
}

// claudeCaptureCheckRecord returns an error unless the observation has the recorded
// identity, the recorded full usage, and the recorded thinking flag. The immutable
// records omit the thinking flag, which reads as false.
func claudeCaptureCheckRecord(obs claudeLedgerObservation, rec claudeLedgerRecord) error {
	if obs.ObservedModel != rec.RequestedModel || obs.ObservedModel != rec.ObservedModel {
		return fmt.Errorf("%w: observed model %q, recorded %q", errClaudeCaptureMap, obs.ObservedModel, rec.ObservedModel)
	}
	if rec.Usage == nil || !reflect.DeepEqual(obs.claudeLedgerUsage, *rec.Usage) {
		return fmt.Errorf("%w: usage differs from the recorded usage", errClaudeCaptureMap)
	}
	if obs.ThinkingBlocksObserved != rec.ThinkingBlocksObserved {
		return fmt.Errorf("%w: thinking %t, recorded %t", errClaudeCaptureMap, obs.ThinkingBlocksObserved, rec.ThinkingBlocksObserved)
	}
	return nil
}

// claudeCaptureOutputOK reports whether an output count is present and within 0 to the
// output cap, inclusive.
func claudeCaptureOutputOK(n *int) bool {
	return n != nil && *n >= 0 && *n <= claudeGateMaxOutputTokens
}

// TestClaudeTokenCaptureMapping checks the one-to-one mapping on synthetic keys. Each
// unknown, duplicate, ambiguous, or unmatched case must fail with the mapping error and
// name its cause.
func TestClaudeTokenCaptureMapping(t *testing.T) {
	t.Parallel()
	rec := func(id int, model string, prompt int, status claudeLedgerStatus) claudeLedgerRecord {
		return claudeLedgerRecord{AttemptID: id, RequestedModel: model, ObservedModel: model, Status: status, Usage: &claudeLedgerUsage{PromptTotal: prompt}}
	}
	ok, failed := claudeLedgerStatusSucceeded, claudeLedgerStatusFailed
	key := func(model string, prompt int) claudeCaptureKey {
		return claudeCaptureKey{Model: model, PromptTotal: prompt}
	}
	tests := []struct {
		name        string
		attempts    []claudeLedgerRecord
		keys        []claudeCaptureKey
		want        []int
		wantErr     bool
		wantErrText string
	}{
		{"one to one", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok), rec(3, "n", 10, ok)}, []claudeCaptureKey{key("m", 20), key("m", 10), key("n", 10)}, []int{1, 0, 2}, false, ""},
		{"unknown capture", []claudeLedgerRecord{rec(1, "m", 10, ok)}, []claudeCaptureKey{key("m", 11)}, nil, true, "matches no attempt"},
		{"unknown model", []claudeLedgerRecord{rec(1, "m", 10, ok)}, []claudeCaptureKey{key("x", 10)}, nil, true, "matches no attempt"},
		{"duplicate capture", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok)}, []claudeCaptureKey{key("m", 10), key("m", 10)}, nil, true, "match attempt"},
		{"unmatched attempt", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok)}, []claudeCaptureKey{key("m", 10)}, nil, true, "has no capture"},
		{"ambiguous attempts", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 10, ok)}, []claudeCaptureKey{key("m", 10)}, nil, true, "repeats model m prompt total 10"},
		{"failed attempt ignored", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, failed)}, []claudeCaptureKey{key("m", 10)}, []int{0}, false, ""},
		{"succeeded attempt without usage", []claudeLedgerRecord{{AttemptID: 1, Status: ok}}, nil, nil, true, "has no usage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := claudeCaptureMatch(tc.attempts, tc.keys)
			if tc.wantErr {
				if !errors.Is(err, errClaudeCaptureMap) {
					t.Errorf("error = %v, want a mapping error", err)
					return
				}
				if !strings.Contains(err.Error(), tc.wantErrText) {
					t.Errorf("error = %v, want it to contain %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("match: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("matched = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClaudeTokenCaptureCheckRecord checks each identity, usage, and thinking mismatch
// between an observation and its recorded attempt. The matching pair must pass. Each
// mismatch must fail with the mapping error.
func TestClaudeTokenCaptureCheckRecord(t *testing.T) {
	t.Parallel()
	intp := func(n int) *int { return &n }
	base := func() (claudeLedgerObservation, claudeLedgerRecord) {
		u := claudeLedgerUsage{Input: intp(2), CacheCreation: intp(5), CacheRead: intp(0), OutputTokens: intp(4), PromptTotal: 7}
		return claudeLedgerObservation{ObservedModel: "m", claudeLedgerUsage: u},
			claudeLedgerRecord{RequestedModel: "m", ObservedModel: "m", Usage: &u}
	}
	tests := []struct {
		name    string
		mutate  func(*claudeLedgerObservation, *claudeLedgerRecord)
		wantErr bool
	}{
		{"matching pair", func(*claudeLedgerObservation, *claudeLedgerRecord) {}, false},
		{"observed model differs from requested", func(o *claudeLedgerObservation, _ *claudeLedgerRecord) { o.ObservedModel = "n" }, true},
		{"requested model differs", func(_ *claudeLedgerObservation, r *claudeLedgerRecord) { r.RequestedModel = "n" }, true},
		{"recorded observed model differs", func(_ *claudeLedgerObservation, r *claudeLedgerRecord) { r.ObservedModel = "n" }, true},
		{"nil recorded usage", func(_ *claudeLedgerObservation, r *claudeLedgerRecord) { r.Usage = nil }, true},
		{"prompt total differs", func(o *claudeLedgerObservation, _ *claudeLedgerRecord) { o.PromptTotal = 8 }, true},
		{"output count differs", func(o *claudeLedgerObservation, _ *claudeLedgerRecord) { o.OutputTokens = intp(3) }, true},
		{"absent input count", func(o *claudeLedgerObservation, _ *claudeLedgerRecord) { o.Input = nil }, true},
		{"thinking observed but not recorded", func(o *claudeLedgerObservation, _ *claudeLedgerRecord) { o.ThinkingBlocksObserved = true }, true},
		{"thinking recorded but not observed", func(_ *claudeLedgerObservation, r *claudeLedgerRecord) { r.ThinkingBlocksObserved = true }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obs, rec := base()
			tc.mutate(&obs, &rec)
			err := claudeCaptureCheckRecord(obs, rec)
			if tc.wantErr {
				if !errors.Is(err, errClaudeCaptureMap) {
					t.Errorf("error = %v, want %v", err, errClaudeCaptureMap)
				}
				return
			}
			if err != nil {
				t.Errorf("matching pair: %v", err)
			}
		})
	}
}

// TestClaudeTokenCaptureOutputOK checks the output count bound. An absent or negative
// count fails, zero passes, the cap passes, and a count above the cap fails.
func TestClaudeTokenCaptureOutputOK(t *testing.T) {
	t.Parallel()
	intp := func(n int) *int { return &n }
	tests := []struct {
		name string
		n    *int
		want bool
	}{
		{"absent", nil, false},
		{"negative", intp(-1), false},
		{"zero", intp(0), true},
		{"cap", intp(claudeGateMaxOutputTokens), true},
		{"above cap", intp(claudeGateMaxOutputTokens + 1), false},
	}
	for _, tc := range tests {
		if got := claudeCaptureOutputOK(tc.n); got != tc.want {
			t.Errorf("%s: claudeCaptureOutputOK = %t, want %t", tc.name, got, tc.want)
		}
	}
}
