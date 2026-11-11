package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

// The offline gate and ledger tests read the authored payloads through this
// fixture contract. Everything here is synthetic: the payloads are hand-written
// text files, the library pin is a placeholder, and the tool schemas are the
// benchmark's synthetic specs. No recorded API traffic and no measured token
// counts are involved.
const (
	claudeFixtureDir    = claudeSyntheticDir
	claudeFixtureSystem = claudeSyntheticSystem
	claudeFixtureUser   = "Synthetic user turn."

	// claudeSyntheticLibraryCommit is the placeholder library pin every synthetic
	// attempt record carries.
	claudeSyntheticLibraryCommit = "synthetic-library-commit"
)

// claudeFixtureLibrary stands in for a recorded CLI library pin.
type claudeFixtureLibrary struct {
	Commit string
}

// claudeFixtureToolsPin pins the synthetic tool schema bytes.
type claudeFixtureToolsPin struct {
	SHA256 string
}

// claudeFixturePayload is one authored payload file with its computed digest.
type claudeFixturePayload struct {
	Name   string
	Bytes  []byte
	SHA256 string
}

// claudeFixtureManifest is the in-memory fixture contract.
type claudeFixtureManifest struct {
	Library  claudeFixtureLibrary
	Payloads []claudeFixturePayload
	Tools    claudeFixtureToolsPin
}

// claudeFixtureSet is the loaded fixture contract for the offline gate and ledger.
type claudeFixtureSet struct {
	Manifest claudeFixtureManifest
	Payloads []claudeFixturePayload
	Tools    []ToolSpec
}

// claudeFixtureCounter selects the ctok counter for model with a fresh, local
// counter. A tiktoken codec is never loaded.
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
// baseline. The payload is counted verbatim, with no trimming or normalization.
func claudeFixtureDelta(counter textCounter, baseline int, payload string) (int, error) {
	return claudeSyntheticDelta(counter, baseline, payload)
}

// claudeSyntheticFixtureSet builds the fixture contract from the authored payloads
// and the synthetic tool schemas.
func claudeSyntheticFixtureSet() (claudeFixtureSet, error) {
	var set claudeFixtureSet
	payloads, err := loadClaudeSyntheticPayloads(claudeSyntheticDir)
	if err != nil {
		return set, err
	}
	for _, name := range claudeSyntheticPayloadNames {
		body := []byte(payloads[name])
		set.Payloads = append(set.Payloads, claudeFixturePayload{
			Name:   name,
			Bytes:  body,
			SHA256: claudeGateSHA256(body),
		})
	}
	tools := claudeBenchToolSpecs()
	data, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return set, fmt.Errorf("encode synthetic fixture tools: %w", err)
	}
	set.Tools = tools
	set.Manifest = claudeFixtureManifest{
		Library:  claudeFixtureLibrary{Commit: claudeSyntheticLibraryCommit},
		Payloads: slices.Clone(set.Payloads),
		Tools:    claudeFixtureToolsPin{SHA256: claudeGateSHA256(append(data, '\n'))},
	}
	return set, nil
}

// loadClaudeFixtureForTest returns the synthetic fixture contract or stops the test.
func loadClaudeFixtureForTest(t *testing.T) claudeFixtureSet {
	t.Helper()
	set, err := claudeSyntheticFixtureSet()
	if err != nil {
		t.Fatalf("load claude fixture: %v", err)
	}
	return set
}

// claudeSyntheticPayloadSHA256 pins the authored payload files, and
// claudeSyntheticToolsSHA256 pins the canonical encoding of the synthetic tool schemas.
// Both are digests of checked-in synthetic inputs, not provider output: a changed byte
// fails until the pin is updated on purpose.
var claudeSyntheticPayloadSHA256 = map[string]string{
	"code_json":      "58335d20625eaa98800564587e94118ca0280c57fc2ba0ef2ae62e01b0de1cca",
	"markdown_prose": "8d26c7ab79ecb1da78d9e645f8e40fc9b12bece706b773517e9ab8d56a0ae1e8",
	"unicode":        "aa86bc5361f55e4a84079b6d1f4b599a9c80b155b30a9cfce6d3a8d4e7a648ac",
	"mixed":          "2ff3537b972a385ac6a3d7900b98f158668143d6dc0f4f5ce3808749e83499bd",
}

const claudeSyntheticToolsSHA256 = "7de6bee43fb150ee7c8ca8df903d9bf307300148a1876679f51398ad95f053d6"

// TestClaudeSyntheticFixtureDigests pins the authored payloads and the synthetic tool
// schema encoding by digest, so an edit to either fails until the pin changes.
func TestClaudeSyntheticFixtureDigests(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	if len(set.Payloads) != len(claudeSyntheticPayloadSHA256) {
		t.Fatalf("payloads = %d, want %d", len(set.Payloads), len(claudeSyntheticPayloadSHA256))
	}
	for _, p := range set.Payloads {
		want, ok := claudeSyntheticPayloadSHA256[p.Name]
		if !ok {
			t.Errorf("payload %q has no pinned digest", p.Name)
			continue
		}
		if p.SHA256 != want {
			t.Errorf("%s sha256 = %s, want %s", p.Name, p.SHA256, want)
		}
	}
	if got := set.Manifest.Tools.SHA256; got != claudeSyntheticToolsSHA256 {
		t.Errorf("synthetic tools sha256 = %s, want %s", got, claudeSyntheticToolsSHA256)
	}
}

// TestClaudeFixturePayloadsSurviveTrimSpace checks that trimming the counted
// system plus payload text changes nothing, so a verbatim delta stays exact.
func TestClaudeFixturePayloadsSurviveTrimSpace(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	for _, p := range set.Payloads {
		raw := claudeFixtureSystem + string(p.Bytes)
		if got := strings.TrimSpace(raw); got != raw {
			t.Errorf("%s: strings.TrimSpace changes system+payload", p.Name)
		}
	}
}
