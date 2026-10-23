package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/tiktoken-go/tokenizer"
)

// The Claude token fixture is authored text plus pinned hashes and token deltas.
// Loaders and helpers are per call and hold no package state.
const (
	claudeFixtureDir    = "testdata/claude_token_estimation"
	claudeFixtureSystem = "Reply only OK."
	claudeFixtureUser   = "Reply OK."
	claudeFixtureModel  = "claude-token-fixture"
)

// claudeFixtureManifest mirrors manifest.json.
type claudeFixtureManifest struct {
	Library        claudeFixtureLibrary      `json:"library"`
	Model          string                    `json:"model"`
	System         string                    `json:"system"`
	User           string                    `json:"user"`
	DeltaFormula   string                    `json:"delta_formula"`
	BaselineTokens int                       `json:"baseline_tokens"`
	Payloads       []claudeFixturePayloadPin `json:"payloads"`
	Tools          claudeFixtureToolsPin     `json:"tools"`
}

// claudeFixtureLibrary pins the ctok-go module version and commit.
type claudeFixtureLibrary struct {
	Module  string `json:"module"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// claudeFixturePayloadPin pins one payload file and its token delta.
type claudeFixturePayloadPin struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
	DeltaTokens int    `json:"delta_tokens"`
}

// claudeFixtureToolsPin pins the tools.json bytes, hash, and scope.
type claudeFixtureToolsPin struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Scope  string `json:"scope"`
}

// claudeFixturePayload is one hash-verified payload. DeltaTokens is
// CountContent(claudeFixtureSystem+payload) minus CountContent(claudeFixtureSystem).
type claudeFixturePayload struct {
	Name        string
	Path        string
	Bytes       []byte
	SHA256      string
	DeltaTokens int
}

// claudeFixtureSet is the loaded fixture contract for the offline evaluator and benchmark.
type claudeFixtureSet struct {
	Manifest       claudeFixtureManifest
	BaselineTokens int
	Payloads       []claudeFixturePayload
	Tools          []ToolSpec
	ToolsBytes     []byte
}

// claudeFixtureCounter selects the ctok counter for model with a fresh, local
// counter. A tiktoken codec is never loaded for the fixture model.
func claudeFixtureCounter(model string) (textCounter, error) {
	counter, meta, err := selectTextCounter(model, counterSelectOptions{
		ids: map[string]struct{}{model: {}},
		codecLoader: func(string) (tokenizer.Codec, error) {
			return nil, errors.New("claude fixture model must not load a tiktoken codec")
		},
		candidate: &claudeTextCounter{},
	})
	if err != nil {
		return nil, fmt.Errorf("select claude fixture counter: %w", err)
	}
	if meta.strategy != TokenizerStrategyCtok {
		return nil, fmt.Errorf("claude fixture strategy = %q, want %q", meta.strategy, TokenizerStrategyCtok)
	}
	return counter, nil
}

// claudeFixtureDelta returns CountContent(claudeFixtureSystem+payload) minus
// baseline. The payload is counted verbatim, with no trimming, separator, or
// line-ending normalization.
func claudeFixtureDelta(counter textCounter, baseline int, payload string) (int, error) {
	total, err := counter.Count(claudeFixtureSystem + payload)
	if err != nil {
		return 0, fmt.Errorf("count claude fixture payload: %w", err)
	}
	return total - baseline, nil
}

// loadClaudeFixture reads the manifest, verifies every payload and tools.json
// against their pinned length and SHA-256, and measures each payload's token delta.
func loadClaudeFixture(dir string) (claudeFixtureSet, error) {
	var set claudeFixtureSet
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return set, fmt.Errorf("read claude fixture manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &set.Manifest); err != nil {
		return set, fmt.Errorf("parse claude fixture manifest: %w", err)
	}
	counter, err := claudeFixtureCounter(set.Manifest.Model)
	if err != nil {
		return set, err
	}
	baseline, err := counter.Count(claudeFixtureSystem)
	if err != nil {
		return set, fmt.Errorf("count claude fixture baseline: %w", err)
	}
	set.BaselineTokens = baseline
	for _, pin := range set.Manifest.Payloads {
		data, err := claudeFixtureReadVerified(dir, pin.Path, pin.Bytes, pin.SHA256)
		if err != nil {
			return set, err
		}
		delta, err := claudeFixtureDelta(counter, baseline, string(data))
		if err != nil {
			return set, fmt.Errorf("claude fixture payload %s: %w", pin.Name, err)
		}
		set.Payloads = append(set.Payloads, claudeFixturePayload{
			Name:        pin.Name,
			Path:        pin.Path,
			Bytes:       data,
			SHA256:      pin.SHA256,
			DeltaTokens: delta,
		})
	}
	tools := set.Manifest.Tools
	data, err := claudeFixtureReadVerified(dir, tools.Path, tools.Bytes, tools.SHA256)
	if err != nil {
		return set, err
	}
	if err := json.Unmarshal(data, &set.Tools); err != nil {
		return set, fmt.Errorf("parse claude fixture tools: %w", err)
	}
	set.ToolsBytes = data
	return set, nil
}

// claudeFixtureReadVerified reads dir/rel and checks its length and SHA-256.
func claudeFixtureReadVerified(dir, rel string, wantBytes int, wantSHA256 string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return nil, fmt.Errorf("read claude fixture %s: %w", rel, err)
	}
	if len(data) != wantBytes {
		return nil, fmt.Errorf("claude fixture %s has %d bytes, want %d", rel, len(data), wantBytes)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return nil, fmt.Errorf("claude fixture %s sha256 = %s, want %s", rel, got, wantSHA256)
	}
	return data, nil
}

func loadClaudeFixtureForTest(t *testing.T) claudeFixtureSet {
	t.Helper()
	set, err := loadClaudeFixture(claudeFixtureDir)
	if err != nil {
		t.Fatalf("load claude fixture: %v", err)
	}
	return set
}

// claudeFixtureGoModVersion returns the version go.mod requires for module.
func claudeFixtureGoModVersion(t *testing.T, module string) string {
	t.Helper()
	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == module {
			return fields[1]
		}
	}
	t.Fatalf("go.mod has no requirement for %s", module)
	return ""
}

func TestClaudeTokenFixtureManifestPinsLibraryAndBaseline(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	m := set.Manifest
	const commit = "7969144b7665d73ae1e20daee102cf1c5e0947a1"
	if m.Library.Module != "github.com/luispabon/ctok-go" {
		t.Errorf("library module = %q", m.Library.Module)
	}
	if m.Library.Commit != commit {
		t.Errorf("library commit = %q, want %q", m.Library.Commit, commit)
	}
	if !strings.HasSuffix(m.Library.Version, "-"+commit[:12]) {
		t.Errorf("library version = %q does not end with commit %s", m.Library.Version, commit[:12])
	}
	if got := claudeFixtureGoModVersion(t, m.Library.Module); got != m.Library.Version {
		t.Errorf("go.mod requires %s, manifest pins %s", got, m.Library.Version)
	}
	if m.Model != claudeFixtureModel {
		t.Errorf("manifest model = %q, want %q", m.Model, claudeFixtureModel)
	}
	if m.System != claudeFixtureSystem {
		t.Errorf("manifest system = %q, want %q", m.System, claudeFixtureSystem)
	}
	if m.User != claudeFixtureUser {
		t.Errorf("manifest user = %q, want %q", m.User, claudeFixtureUser)
	}
	const deltaFormula = "CountContent(system + payload) - CountContent(system)"
	if m.DeltaFormula != deltaFormula {
		t.Errorf("manifest delta_formula = %q, want %q", m.DeltaFormula, deltaFormula)
	}
	if set.BaselineTokens != m.BaselineTokens {
		t.Errorf("baseline tokens = %d, manifest pins %d", set.BaselineTokens, m.BaselineTokens)
	}
}

func TestClaudeTokenFixturePayloadsAreOrderedAndPinned(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	want := []string{"code_json", "markdown_prose", "unicode", "mixed"}
	if len(set.Payloads) != len(want) {
		t.Fatalf("payloads = %d, want %d", len(set.Payloads), len(want))
	}
	for i, name := range want {
		p := set.Payloads[i]
		if p.Name != name || p.Path != "payloads/"+name+".txt" {
			t.Errorf("payload %d = %q at %q, want %q at payloads/%s.txt", i, p.Name, p.Path, name, name)
		}
		pin := set.Manifest.Payloads[i]
		sum := sha256.Sum256(p.Bytes)
		if got := hex.EncodeToString(sum[:]); p.SHA256 != pin.SHA256 || got != pin.SHA256 {
			t.Errorf("%s sha256 = %s (loaded %s), manifest pins %s", p.Name, got, p.SHA256, pin.SHA256)
		}
		if len(p.Bytes) != pin.Bytes {
			t.Errorf("%s has %d bytes, manifest pins %d", p.Name, len(p.Bytes), pin.Bytes)
		}
	}
}

func TestClaudeTokenFixturePayloadDeltasAreInRangeAndPinned(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	for i, p := range set.Payloads {
		if p.DeltaTokens < 1000 || p.DeltaTokens > 2000 {
			t.Errorf("%s delta = %d tokens, want 1000 to 2000", p.Name, p.DeltaTokens)
		}
		if want := set.Manifest.Payloads[i].DeltaTokens; p.DeltaTokens != want {
			t.Errorf("%s delta = %d tokens, manifest pins %d", p.Name, p.DeltaTokens, want)
		}
	}
}

func TestClaudeTokenFixturePayloadsSurviveTrimSpace(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	for _, p := range set.Payloads {
		raw := claudeFixtureSystem + string(p.Bytes)
		if got := strings.TrimSpace(raw); got != raw {
			t.Errorf("%s: strings.TrimSpace changes system+payload", p.Name)
		}
	}
}

// claudeFixtureRecordingCounter counts text as its byte length and records each input.
type claudeFixtureRecordingCounter struct {
	inputs []string
}

func (c *claudeFixtureRecordingCounter) Count(text string) (int, error) {
	c.inputs = append(c.inputs, text)
	return len(text), nil
}

func TestClaudeTokenFixtureDeltaCountsRawPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"leading tab", "\tindented line\n"},
		{"leading and trailing spaces", "  padded  \n\n"},
		{"crlf line endings", "first\r\nsecond\r\n"},
		{"tab and crlf mix", "col\tvalue\r\n\t\r\n"},
		{"no final newline", "tail without newline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &claudeFixtureRecordingCounter{}
			delta, err := claudeFixtureDelta(counter, len(claudeFixtureSystem), tt.payload)
			if err != nil {
				t.Fatal(err)
			}
			want := claudeFixtureSystem + tt.payload
			if len(counter.inputs) != 1 || counter.inputs[0] != want {
				t.Fatalf("counted %q, want verbatim %q", counter.inputs, want)
			}
			if delta != len(tt.payload) {
				t.Errorf("delta = %d, want %d raw payload bytes", delta, len(tt.payload))
			}
		})
	}
}

func TestClaudeTokenFixturePayloadContentKinds(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	texts := make(map[string]string, len(set.Payloads))
	for _, p := range set.Payloads {
		texts[p.Name] = string(p.Bytes)
	}
	needles := []struct {
		payload string
		want    []string
	}{
		{"code_json", []string{"func (l *Ledger) Add", `"sku": "BLT-204"`}},
		{"markdown_prose", []string{"\n## ", "\n| ", "```sh"}},
		{"mixed", []string{`{"batch":`, "def summarize(", "\u2705"}},
	}
	for _, n := range needles {
		for _, sub := range n.want {
			if !strings.Contains(texts[n.payload], sub) {
				t.Errorf("%s payload lacks %q", n.payload, sub)
			}
		}
	}
	runes := []rune(texts["unicode"])
	for _, check := range []struct {
		name  string
		match func(rune) bool
	}{
		{"combining mark", func(r rune) bool { return unicode.Is(unicode.Mn, r) }},
		{"Han ideograph", func(r rune) bool { return unicode.Is(unicode.Han, r) }},
		{"zero width joiner", func(r rune) bool { return r == 0x200d }},
		{"emoji variation selector", func(r rune) bool { return r == 0xfe0f }},
		{"emoji skin tone modifier", func(r rune) bool { return r >= 0x1f3fb && r <= 0x1f3ff }},
	} {
		if !slices.ContainsFunc(runes, check.match) {
			t.Errorf("unicode payload has no %s", check.name)
		}
	}
}

func TestClaudeTokenFixtureToolsAreCoreBuiltinSet(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	names := make([]string, 0, len(set.Tools))
	for _, spec := range set.Tools {
		if spec.Type != "function" {
			t.Errorf("tool %q type = %q, want function", spec.Function.Name, spec.Type)
		}
		names = append(names, spec.Function.Name)
	}
	want := []string{"bash", "display_file", "fetch_url", "glob", "grep", "ls", "mutate", "read", "workflow_handoff"}
	if !slices.Equal(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}
	canonical, err := json.MarshalIndent(set.Tools, "", "  ")
	if err != nil {
		t.Fatalf("encode tools: %v", err)
	}
	if !bytes.Equal(append(canonical, '\n'), set.ToolsBytes) {
		t.Error("tools.json is not in canonical indented form")
	}
}

func TestClaudeTokenFixtureReadVerifiedRejectsWrongPins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("abc"), 0o600); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	sum := sha256.Sum256([]byte("abc"))
	goodSHA := hex.EncodeToString(sum[:])
	if _, err := claudeFixtureReadVerified(dir, "sample.txt", 3, goodSHA); err != nil {
		t.Fatalf("matching pins rejected: %v", err)
	}
	tests := []struct {
		name  string
		bytes int
		sha   string
	}{
		{"wrong length", 4, goodSHA},
		{"wrong hash", 3, strings.Repeat("0", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := claudeFixtureReadVerified(dir, "sample.txt", tt.bytes, tt.sha); err == nil {
				t.Error("mismatched pins accepted")
			}
		})
	}
}
