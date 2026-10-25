package provider

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// claudeCaptureCount is the number of succeeded attempts and of sanitized captures.
const claudeCaptureCount = 18

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

// claudeCaptureOutputOK reports whether an output count is present and within the output cap.
func claudeCaptureOutputOK(n *int) bool {
	return n != nil && *n <= claudeGateMaxOutputTokens
}

// claudeCaptureMatched is one committed capture replayed and matched to its record.
type claudeCaptureMatched struct {
	Name string
	Data []byte
	Obs  claudeLedgerObservation
	Rec  claudeLedgerRecord
}

// loadClaudeCaptures replays every committed capture and matches it to the observations
// record with the same model and prompt total. The file name must hold the matched
// attempt ID.
func loadClaudeCaptures(t *testing.T) []claudeCaptureMatched {
	t.Helper()
	ev, err := claudeGateLoadEvidence(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		t.Fatalf("load observations: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(claudeFixtureDir, "captures", "*.jsonl"))
	if err != nil || len(paths) != claudeCaptureCount {
		t.Fatalf("found %d captures, want %d (%v)", len(paths), claudeCaptureCount, err)
	}
	data := make([][]byte, len(paths))
	obs := make([]claudeLedgerObservation, len(paths))
	keys := make([]claudeCaptureKey, len(paths))
	for i, path := range paths {
		data[i] = claudeLedgerReadStream(t, path)
		o, err := claudeCaptureReplayAny(data[i])
		if err != nil {
			t.Fatalf("replay %s: %v", filepath.Base(path), err)
		}
		obs[i] = o
		keys[i] = claudeCaptureKey{Model: o.ObservedModel, PromptTotal: o.PromptTotal}
	}
	matched, err := claudeCaptureMatch(ev.Attempts, keys)
	if err != nil {
		t.Fatalf("match captures: %v", err)
	}
	out := make([]claudeCaptureMatched, len(paths))
	for i, path := range paths {
		rec := ev.Attempts[matched[i]]
		name := filepath.Base(path)
		if want := fmt.Sprintf("attempt-%02d.jsonl", rec.AttemptID); name != want {
			t.Errorf("%s matches attempt %d, want file %s", name, rec.AttemptID, want)
		}
		out[i] = claudeCaptureMatched{Name: name, Data: data[i], Obs: obs[i], Rec: rec}
	}
	return out
}

// TestClaudeTokenCaptureReplaysRecorded replays each sanitized capture first through
// the model it names and then through the recorded model. Both replays must match the
// recorded identity, full usage, and thinking flag.
func TestClaudeTokenCaptureReplaysRecorded(t *testing.T) {
	for _, c := range loadClaudeCaptures(t) {
		if err := claudeCaptureCheckRecord(c.Obs, c.Rec); err != nil {
			t.Errorf("%s replay: %v", c.Name, err)
		}
		o, err := claudeLedgerObserveStream(c.Data, c.Rec.RequestedModel)
		if err != nil {
			t.Errorf("%s replay as recorded model: %v", c.Name, err)
			continue
		}
		if err := claudeCaptureCheckRecord(o, c.Rec); err != nil {
			t.Errorf("%s recorded-model replay: %v", c.Name, err)
		}
	}
}

// TestClaudeTokenCaptureAuditBounds is the standalone audit of the captures. The
// recorded and the sanitized output count must be present and at most the output cap,
// and no capture may carry thinking. It reads the captures only and changes no verdict.
func TestClaudeTokenCaptureAuditBounds(t *testing.T) {
	for _, c := range loadClaudeCaptures(t) {
		if !claudeCaptureOutputOK(c.Rec.Usage.OutputTokens) {
			t.Errorf("%s recorded output count is absent or above %d", c.Name, claudeGateMaxOutputTokens)
		}
		if !claudeCaptureOutputOK(c.Obs.OutputTokens) {
			t.Errorf("%s sanitized output count is absent or above %d", c.Name, claudeGateMaxOutputTokens)
		}
		if c.Obs.ThinkingBlocksObserved || c.Rec.ThinkingBlocksObserved {
			t.Errorf("%s carries thinking", c.Name)
		}
	}
}

// TestClaudeTokenCaptureMapping checks the one-to-one mapping on synthetic keys. Each
// unknown, duplicate, ambiguous, or unmatched case must fail with the mapping error.
func TestClaudeTokenCaptureMapping(t *testing.T) {
	rec := func(id int, model string, prompt int, status claudeLedgerStatus) claudeLedgerRecord {
		return claudeLedgerRecord{AttemptID: id, RequestedModel: model, ObservedModel: model, Status: status, Usage: &claudeLedgerUsage{PromptTotal: prompt}}
	}
	ok, failed := claudeLedgerStatusSucceeded, claudeLedgerStatusFailed
	key := func(model string, prompt int) claudeCaptureKey {
		return claudeCaptureKey{Model: model, PromptTotal: prompt}
	}
	tests := []struct {
		name     string
		attempts []claudeLedgerRecord
		keys     []claudeCaptureKey
		want     []int
		wantErr  bool
	}{
		{"one to one", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok), rec(3, "n", 10, ok)}, []claudeCaptureKey{key("m", 20), key("m", 10), key("n", 10)}, []int{1, 0, 2}, false},
		{"unknown capture", []claudeLedgerRecord{rec(1, "m", 10, ok)}, []claudeCaptureKey{key("m", 11)}, nil, true},
		{"unknown model", []claudeLedgerRecord{rec(1, "m", 10, ok)}, []claudeCaptureKey{key("x", 10)}, nil, true},
		{"duplicate capture", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok)}, []claudeCaptureKey{key("m", 10), key("m", 10)}, nil, true},
		{"unmatched attempt", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, ok)}, []claudeCaptureKey{key("m", 10)}, nil, true},
		{"ambiguous attempts", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 10, ok)}, []claudeCaptureKey{key("m", 10)}, nil, true},
		{"failed attempt ignored", []claudeLedgerRecord{rec(1, "m", 10, ok), rec(2, "m", 20, failed)}, []claudeCaptureKey{key("m", 10)}, []int{0}, false},
		{"succeeded attempt without usage", []claudeLedgerRecord{{AttemptID: 1, Status: ok}}, nil, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := claudeCaptureMatch(tc.attempts, tc.keys)
			if tc.wantErr {
				if !errors.Is(err, errClaudeCaptureMap) {
					t.Errorf("error = %v, want a mapping error", err)
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
