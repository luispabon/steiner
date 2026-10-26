package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The provenance pins tie the sanitized captures to the retained originals and the
// frozen evidence. provenance.json holds digests, sizes, and attempt IDs only. It never
// holds stream content, and it does not hash itself. Pins that need the retained
// originals run only when STEINER_CLAUDE_CAPTURE_ORIGINALS names them.

const (
	// claudeCaptureOriginalsEnv names the retained raw capture directory.
	claudeCaptureOriginalsEnv = "STEINER_CLAUDE_CAPTURE_ORIGINALS"
	// claudeCaptureGenerateEnv names the scratch directory that generation writes to.
	claudeCaptureGenerateEnv = "STEINER_CLAUDE_CAPTURE_GENERATE"

	claudeProvenanceName = "provenance.json"
	// claudeObservationsFrozenSHA256 freezes observations.json. Its bytes must not change.
	claudeObservationsFrozenSHA256 = "61ce15a4ce298a017bf1c592d85e80b96c6304b8ac39370e9b0b151fcbb74fe3"
	// claudeManifestFrozenSHA256 freezes manifest.json. Its bytes must not change.
	claudeManifestFrozenSHA256 = "05bf4f28c41f72657645bd472fad628175b0e8651ccfc10c2e812decc3542238"
	// claudeHistoricalEvaluatorCommit is the evaluator commit recorded in the
	// observations. It is compared as a string and not checked against git history.
	claudeHistoricalEvaluatorCommit = "643bfe916d84ed8dc5083044d0d154dc346132f1"
	// claudeProvenanceControlSHA256 identifies the metadata-only preflight control
	// response among the originals. It has no capture and is excluded.
	claudeProvenanceControlSHA256 = "d30e4e3ec2c6679f7d280fabb4014138fce71294909425b35135436372639eff"
	claudeProvenanceControlBytes  = 43334
	// claudeNegativeStreamCount is the number of stream fixtures under streams/.
	claudeNegativeStreamCount = 7
)

// claudeProvenance is provenance.json.
type claudeProvenance struct {
	SchemaVersion   int                    `json:"schema_version"`
	Observations    claudeProvObservations `json:"observations"`
	Ledger          claudeProvLedger       `json:"ledger"`
	Originals       claudeProvOriginals    `json:"originals"`
	NegativeStreams []claudeProvFile       `json:"negative_streams"`
	Captures        []claudeProvCapture    `json:"captures"`
}

// claudeProvObservations pins observations.json and its historical evaluator commit.
type claudeProvObservations struct {
	Path            string `json:"path"`
	SHA256          string `json:"sha256"`
	SchemaVersion   int    `json:"schema_version"`
	EvaluatorCommit string `json:"evaluator_commit"`
}

// claudeProvLedger pins the attempt ledger that the originals were recorded in.
type claudeProvLedger struct {
	Location string `json:"location"`
	SHA256   string `json:"sha256"`
	Attempts int    `json:"attempts"`
}

// claudeProvOriginals pins the retained raw directory and its excluded control file.
type claudeProvOriginals struct {
	Location string             `json:"location"`
	Files    int                `json:"files"`
	Excluded claudeProvExcluded `json:"excluded"`
}

// claudeProvExcluded is the control file that no capture is derived from.
type claudeProvExcluded struct {
	Role   string `json:"role"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// claudeProvFile pins one fixture file by path, size, and digest.
type claudeProvFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// claudeProvCapture pins one sanitized capture and the original it was derived from.
// The source digest and size are taken before sanitizing.
type claudeProvCapture struct {
	AttemptID    int    `json:"attempt_id"`
	Path         string `json:"path"`
	Bytes        int    `json:"bytes"`
	SHA256       string `json:"sha256"`
	Model        string `json:"model"`
	Slot         string `json:"slot"`
	PromptTotal  int    `json:"prompt_total_tokens"`
	SourceBytes  int    `json:"source_bytes"`
	SourceSHA256 string `json:"source_sha256"`
}

// claudeCaptureFile is one generated fixture file, relative to the fixture directory.
type claudeCaptureFile struct {
	Path string
	Data []byte
}

// claudeProvenanceBuild regenerates every sanitized capture and provenance.json from the
// originals in originals. Its ledger is the sibling attempts.json. It returns the files
// keyed by path relative to the fixture directory.
func claudeProvenanceBuild(originals string) ([]claudeCaptureFile, error) {
	ledgerRaw, err := os.ReadFile(filepath.Join(originals, "..", "attempts.json"))
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	var ledger claudeLedgerFile
	if err := json.Unmarshal(ledgerRaw, &ledger); err != nil {
		return nil, fmt.Errorf("decode ledger: %w", err)
	}
	srcs, err := claudeCaptureSanitizeDir(originals, ledger.Attempts)
	if err != nil {
		return nil, err
	}
	obsRaw, err := os.ReadFile(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		return nil, fmt.Errorf("read observations: %w", err)
	}
	var obs struct {
		SchemaVersion   int    `json:"schema_version"`
		EvaluatorCommit string `json:"evaluator_commit"`
	}
	if err := json.Unmarshal(obsRaw, &obs); err != nil {
		return nil, fmt.Errorf("decode observations: %w", err)
	}
	prov := claudeProvenance{
		SchemaVersion: 1,
		Observations: claudeProvObservations{Path: claudeGateEvidenceName, SHA256: claudeGateSHA256(obsRaw),
			SchemaVersion: obs.SchemaVersion, EvaluatorCommit: obs.EvaluatorCommit},
		Ledger: claudeProvLedger{Location: ".steiner/tmp/claude-token-validation/attempts.json",
			SHA256: claudeGateSHA256(ledgerRaw), Attempts: len(ledger.Attempts)},
		Originals: claudeProvOriginals{
			Location: ".steiner/tmp/claude-token-validation/raw",
			Files:    len(srcs) + 1,
			Excluded: claudeProvExcluded{Role: "preflight control_response, metadata only",
				Bytes: claudeProvenanceControlBytes, SHA256: claudeProvenanceControlSHA256},
		},
	}
	files := make([]claudeCaptureFile, 0, len(srcs)+2)
	for _, s := range srcs {
		name := fmt.Sprintf("captures/attempt-%02d.jsonl", s.Attempt.AttemptID)
		files = append(files, claudeCaptureFile{Path: name, Data: s.Data})
		prov.Captures = append(prov.Captures, claudeProvCapture{
			AttemptID:    s.Attempt.AttemptID,
			Path:         name,
			Bytes:        len(s.Data),
			SHA256:       claudeGateSHA256(s.Data),
			Model:        s.Attempt.RequestedModel,
			Slot:         string(s.Attempt.Slot),
			PromptTotal:  s.Attempt.Usage.PromptTotal,
			SourceBytes:  s.SourceBytes,
			SourceSHA256: s.SourceSHA256,
		})
	}
	streams, err := filepath.Glob(filepath.Join(claudeFixtureDir, "streams", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("list streams: %w", err)
	}
	for _, p := range streams {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read stream: %w", err)
		}
		prov.NegativeStreams = append(prov.NegativeStreams, claudeProvFile{Path: "streams/" + filepath.Base(p), Bytes: len(data), SHA256: claudeGateSHA256(data)})
	}
	out, err := json.MarshalIndent(prov, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode provenance: %w", err)
	}
	files = append(files, claudeCaptureFile{Path: claudeProvenanceName, Data: append(out, '\n')})
	return files, nil
}

// claudeCaptureOriginalsDir returns the retained raw capture directory named by
// STEINER_CLAUDE_CAPTURE_ORIGINALS. The test skips when the variable is unset or the
// directory is absent, as in CI.
func claudeCaptureOriginalsDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(claudeCaptureOriginalsEnv)
	if dir == "" {
		t.Skipf("retained originals not configured: set %s to the raw capture directory", claudeCaptureOriginalsEnv)
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		t.Skipf("retained originals absent: %s does not exist", claudeCaptureOriginalsEnv)
	}
	return dir
}

// readClaudeFixtureFile returns one file under the fixture directory.
func readClaudeFixtureFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(claudeFixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// loadClaudeProvenance decodes provenance.json and rejects unknown fields.
func loadClaudeProvenance(t *testing.T) claudeProvenance {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(readClaudeFixtureFile(t, claudeProvenanceName)))
	dec.DisallowUnknownFields()
	var prov claudeProvenance
	if err := dec.Decode(&prov); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	return prov
}

// checkClaudeProvPinned checks that the *.jsonl files under sub are exactly the pinned
// files, and that each has the pinned size, digest, and LF-only line endings.
func checkClaudeProvPinned(t *testing.T, sub string, pins []claudeProvFile) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(claudeFixtureDir, sub, "*.jsonl"))
	if err != nil {
		t.Fatalf("list %s: %v", sub, err)
	}
	var have, want []string
	for _, p := range paths {
		have = append(have, sub+"/"+filepath.Base(p))
	}
	for _, pin := range pins {
		want = append(want, pin.Path)
	}
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Errorf("%s holds %q, pinned %q", sub, have, want)
	}
	for _, pin := range pins {
		data := readClaudeFixtureFile(t, pin.Path)
		if len(data) != pin.Bytes || claudeGateSHA256(data) != pin.SHA256 {
			t.Errorf("%s has %d bytes sha256 %s, pinned %d %s", pin.Path, len(data), claudeGateSHA256(data), pin.Bytes, pin.SHA256)
		}
		if bytes.Contains(data, []byte("\r")) || !bytes.HasSuffix(data, []byte("\n")) {
			t.Errorf("%s is not LF-terminated", pin.Path)
		}
	}
}

// TestClaudeTokenProvenancePins checks the committed fixtures against provenance.json
// and the frozen digests. It needs no retained originals.
func TestClaudeTokenProvenancePins(t *testing.T) {
	prov := loadClaudeProvenance(t)
	obsRaw := readClaudeFixtureFile(t, claudeGateEvidenceName)
	ev, err := claudeGateLoadEvidence(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		t.Fatalf("load observations: %v", err)
	}
	if got := claudeGateSHA256(obsRaw); got != claudeObservationsFrozenSHA256 || got != prov.Observations.SHA256 {
		t.Errorf("observations sha256 = %s, frozen %s, pinned %s", got, claudeObservationsFrozenSHA256, prov.Observations.SHA256)
	}
	var obs struct {
		SchemaVersion   int    `json:"schema_version"`
		EvaluatorCommit string `json:"evaluator_commit"`
	}
	if err := json.Unmarshal(obsRaw, &obs); err != nil {
		t.Fatalf("decode observations: %v", err)
	}
	if obs.SchemaVersion != prov.Observations.SchemaVersion || obs.EvaluatorCommit != prov.Observations.EvaluatorCommit || obs.EvaluatorCommit != claudeHistoricalEvaluatorCommit {
		t.Errorf("observations schema %d commit %s, pinned schema %d commit %s, historical %s",
			obs.SchemaVersion, obs.EvaluatorCommit, prov.Observations.SchemaVersion, prov.Observations.EvaluatorCommit, claudeHistoricalEvaluatorCommit)
	}
	if got := claudeGateSHA256(readClaudeFixtureFile(t, "manifest.json")); got != claudeManifestFrozenSHA256 {
		t.Errorf("manifest.json sha256 = %s, frozen %s", got, claudeManifestFrozenSHA256)
	}
	attr := readClaudeFixtureFile(t, ".gitattributes")
	for _, rule := range []string{"streams/*.jsonl text eol=lf\n", "captures/*.jsonl text eol=lf\n"} {
		if !bytes.Contains(attr, []byte(rule)) {
			t.Errorf(".gitattributes lacks rule %q", rule)
		}
	}
	if len(prov.NegativeStreams) != claudeNegativeStreamCount {
		t.Errorf("provenance pins %d negative streams, want %d", len(prov.NegativeStreams), claudeNegativeStreamCount)
	}
	checkClaudeProvPinned(t, "streams", prov.NegativeStreams)
	files := make([]claudeProvFile, 0, len(prov.Captures))
	for i, c := range prov.Captures {
		if c.AttemptID != i+1 || c.Path != fmt.Sprintf("captures/attempt-%02d.jsonl", i+1) {
			t.Errorf("capture %d pins attempt %d at %s", i+1, c.AttemptID, c.Path)
		}
		if err := claudeProvAttemptMismatch(c, ev.Attempts); err != nil {
			t.Error(err)
		}
		files = append(files, claudeProvFile{Path: c.Path, Bytes: c.Bytes, SHA256: c.SHA256})
	}
	if len(files) != claudeCaptureCount {
		t.Errorf("provenance pins %d captures, want %d", len(files), claudeCaptureCount)
	}
	checkClaudeProvPinned(t, "captures", files)
}

// claudeProvAttemptMismatch returns an error unless the pinned capture matches the
// succeeded attempt it names: requested model, slot, and terminal prompt total.
func claudeProvAttemptMismatch(c claudeProvCapture, attempts []claudeLedgerRecord) error {
	for _, rec := range attempts {
		if rec.AttemptID != c.AttemptID || rec.Status != claudeLedgerStatusSucceeded {
			continue
		}
		if rec.RequestedModel != c.Model || string(rec.Slot) != c.Slot {
			return fmt.Errorf("capture %s pins model %s slot %s, attempt %d has %s %s",
				c.Path, c.Model, c.Slot, c.AttemptID, rec.RequestedModel, rec.Slot)
		}
		if rec.Usage == nil || rec.Usage.PromptTotal != c.PromptTotal {
			return fmt.Errorf("capture %s pins prompt total %d, attempt %d does not match", c.Path, c.PromptTotal, c.AttemptID)
		}
		return nil
	}
	return fmt.Errorf("capture %s names no succeeded attempt %d", c.Path, c.AttemptID)
}

// TestClaudeTokenProvenanceAttemptCorruption corrupts one pinned capture field at a
// time. The pinned capture must pass, and each corrupted copy must fail against the
// immutable attempts.
func TestClaudeTokenProvenanceAttemptCorruption(t *testing.T) {
	ev, err := claudeGateLoadEvidence(filepath.Join(claudeFixtureDir, claudeGateEvidenceName))
	if err != nil {
		t.Fatalf("load observations: %v", err)
	}
	base := loadClaudeProvenance(t).Captures[0]
	if err := claudeProvAttemptMismatch(base, ev.Attempts); err != nil {
		t.Fatalf("pinned capture: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*claudeProvCapture)
	}{
		{"model", func(c *claudeProvCapture) { c.Model = "claude-other-5-5" }},
		{"slot", func(c *claudeProvCapture) { c.Slot = "tools" }},
		{"prompt total", func(c *claudeProvCapture) { c.PromptTotal++ }},
		{"attempt ID", func(c *claudeProvCapture) { c.AttemptID = 999 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			if err := claudeProvAttemptMismatch(c, ev.Attempts); err == nil {
				t.Error("corrupted capture passed against its attempt")
			}
		})
	}
}

// TestClaudeTokenProvenanceOriginals checks the retained originals against the pins: the
// ledger digest, the excluded control file, and each capture's source digest and size.
// It skips when the originals are not configured.
func TestClaudeTokenProvenanceOriginals(t *testing.T) {
	dir := claudeCaptureOriginalsDir(t)
	prov := loadClaudeProvenance(t)
	ledger, err := os.ReadFile(filepath.Join(dir, "..", "attempts.json"))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if got := claudeGateSHA256(ledger); got != prov.Ledger.SHA256 {
		t.Errorf("ledger sha256 = %s, pinned %s", got, prov.Ledger.SHA256)
	}
	var ledgerDoc claudeLedgerFile
	if err := json.Unmarshal(ledger, &ledgerDoc); err != nil || len(ledgerDoc.Attempts) != prov.Ledger.Attempts {
		t.Errorf("ledger holds %d attempts (%v), pinned %d", len(ledgerDoc.Attempts), err, prov.Ledger.Attempts)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read originals: %v", err)
	}
	bySource := map[string]claudeProvCapture{}
	for _, c := range prov.Captures {
		bySource[c.SourceSHA256] = c
	}
	seen := map[string]int{}
	files := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		files++
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read original: %v", err)
		}
		digest := claudeGateSHA256(raw)
		if digest == prov.Originals.Excluded.SHA256 {
			if len(raw) != prov.Originals.Excluded.Bytes {
				t.Errorf("excluded control has %d bytes, pinned %d", len(raw), prov.Originals.Excluded.Bytes)
			}
			seen[digest]++
			continue
		}
		pin, ok := bySource[digest]
		if !ok {
			t.Errorf("original %s is neither pinned nor the excluded control", digest)
			continue
		}
		if len(raw) != pin.SourceBytes {
			t.Errorf("original %s has %d bytes, pinned %d", digest, len(raw), pin.SourceBytes)
		}
		seen[digest]++
	}
	if files != prov.Originals.Files {
		t.Errorf("originals hold %d files, pinned %d", files, prov.Originals.Files)
	}
	for _, pin := range prov.Captures {
		if seen[pin.SourceSHA256] != 1 {
			t.Errorf("source of %s seen %d times, want 1", pin.Path, seen[pin.SourceSHA256])
		}
	}
}

// TestClaudeTokenCaptureGenerate regenerates the sanitized captures and provenance.json
// from the originals and writes them under the directory named by
// STEINER_CLAUDE_CAPTURE_GENERATE. It checks every destination before writing any file,
// and refuses a symbolic link or a path inside the fixture directory.
func TestClaudeTokenCaptureGenerate(t *testing.T) {
	out := os.Getenv(claudeCaptureGenerateEnv)
	if out == "" {
		t.Skipf("set %s to write regenerated fixtures", claudeCaptureGenerateEnv)
	}
	files, err := claudeProvenanceBuild(claudeCaptureOriginalsDir(t))
	if err != nil {
		t.Fatalf("build fixtures: %v", err)
	}
	if err := claudeGenerateCheckDests(out, claudeFixtureDir, files); err != nil {
		t.Fatalf("refuse to write: %v", err)
	}
	for _, f := range files {
		dst := filepath.Join(out, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("create output directory: %v", err)
		}
		if err := os.WriteFile(dst, f.Data, 0o644); err != nil {
			t.Fatalf("write %s: %v", f.Path, err)
		}
	}
}

// TestClaudeTokenCaptureOriginalsMatch regenerates every fixture from the originals and
// fails unless each committed capture and provenance.json is byte-equal to the result.
func TestClaudeTokenCaptureOriginalsMatch(t *testing.T) {
	files, err := claudeProvenanceBuild(claudeCaptureOriginalsDir(t))
	if err != nil {
		t.Fatalf("regenerate fixtures: %v", err)
	}
	for _, f := range files {
		committed, err := os.ReadFile(filepath.Join(claudeFixtureDir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("read committed %s: %v", f.Path, err)
			continue
		}
		if !bytes.Equal(committed, f.Data) {
			t.Errorf("committed %s differs from the regenerated bytes", f.Path)
		}
	}
}
