package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Pinned matcher parameters (D13). These must not be relaxed to make tests
// pass: shingleWidth and overlapThreshold define what counts as duplication,
// and lowering either would silently widen the gap the check is meant to
// close. The only sanctioned adjustment if the real-tree test produces a
// false positive on legitimate shared engineering vocabulary is raising
// minUnitWords — never lowering overlapThreshold, never changing
// shingleWidth.
const (
	shingleWidth     = 5
	overlapThreshold = 0.6
	minUnitWords     = 12
)

// deferralMarkers identify paragraphs that deliberately point back at canon
// instead of restating it. Introduced by #447 (system preamble orchestrator
// role) to distinguish intentional deferral from prose drift.
var deferralMarkers = []string{
	"in your system prompt",
	"(Workflow-specific:",
}

var consumerFilePaths = []string{
	"../../skills/implement/SKILL.md.src",
	"../../skills/review/SKILL.md.src",
	"../../skills/simplify/SKILL.md.src",
	"../../skills/plan/SKILL.md",
	"../../skills/pull-request/SKILL.md",
}

var consumerGlobs = []string{
	"../../skills/partials/*.md",
	"../oneshot/prompts/*.md",
}

type consumerParagraph struct {
	Path      string // repo-relative, e.g. "skills/implement/SKILL.md.src"
	StartLine int    // 1-indexed line number of the paragraph's first line
	Text      string // raw paragraph text, internal newlines preserved
	Exempt    bool   // a deferral marker occurs somewhere in Text
}

type finding struct {
	Path      string
	StartLine int
	Detail    string // human-readable: what matched what, with both excerpts
}

type waiver struct {
	Consumer    string `json:"consumer"`    // repo-relative consumer path
	Fingerprint string `json:"fingerprint"` // first 12 hex chars of sha256 over the space-joined normalizeWords of the matched unit
	Excerpt     string `json:"excerpt"`     // human-readable, not used for matching
	Reason      string `json:"reason"`      // required, non-empty
}

// findingCandidate pairs a finding with the fingerprint of the unit that
// triggered it, so waiver matching can be applied uniformly regardless of
// which direction produced the finding.
type findingCandidate struct {
	finding
	fingerprint string
}

// consumerPaths returns the repo-relative paths of every consumer file,
// sorted, resolved relative to the internal/prompt package directory.
func consumerPaths(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, p := range consumerFilePaths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("consumer path missing: %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	for _, g := range consumerGlobs {
		matches, err := filepath.Glob(g)
		if err != nil {
			t.Fatalf("glob %s: %v", g, err)
		}
		if len(matches) == 0 {
			t.Fatalf("glob %s matched no files", g)
		}
		paths = append(paths, matches...)
	}

	sort.Strings(paths)
	return paths
}

// loadConsumers reads consumerPaths and splits each into paragraphs.
func loadConsumers(t *testing.T) []consumerParagraph {
	t.Helper()

	var out []consumerParagraph
	for _, p := range consumerPaths(t) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read consumer %s: %v", p, err)
		}
		out = append(out, splitParagraphs(p, string(data))...)
	}
	return out
}

// splitParagraphs splits content into blank-line-delimited blocks,
// tagging each with its 1-indexed start line and Exempt flag.
func splitParagraphs(path, content string) []consumerParagraph {
	lines := strings.Split(content, "\n")

	var out []consumerParagraph
	var block []string
	blockStart := 0

	flush := func() {
		if len(block) == 0 {
			return
		}
		text := strings.Join(block, "\n")
		out = append(out, consumerParagraph{
			Path:      path,
			StartLine: blockStart,
			Text:      text,
			Exempt:    containsDeferralMarker(text),
		})
		block = nil
	}

	for i, line := range lines {
		lineNum := i + 1
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if len(block) == 0 {
			blockStart = lineNum
		}
		block = append(block, line)
	}
	flush()

	return out
}

func containsDeferralMarker(text string) bool {
	for _, m := range deferralMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

var normalizePunctuation = strings.NewReplacer(
	"`", " ", "|", " ", "*", " ", "_", " ", "#", " ", ">", " ",
	"(", " ", ")", " ", "[", " ", "]", " ", ",", " ", ":", " ",
	";", " ", ".", " ", "!", " ", "?", " ", "\"", " ", "'", " ",
	"—", " ", "–", " ", "-", " ", "/", " ",
)

// normalizeWords lowercases, replaces markdown punctuation with spaces,
// collapses whitespace, and returns the resulting content words.
func normalizeWords(s string) []string {
	lowered := strings.ToLower(s)
	replaced := normalizePunctuation.Replace(lowered)
	return strings.Fields(replaced)
}

// shingleSet returns the set of k-word shingles over words, joined by " ".
// Returns an empty set when len(words) < k.
//
//nolint:unparam // k is a deliberately general parameter of this helper; all current call sites pass the pinned shingleWidth constant, but the signature is not hardcoded to it by design.
func shingleSet(words []string, k int) map[string]struct{} {
	set := make(map[string]struct{})
	if len(words) < k {
		return set
	}
	for i := 0; i+k <= len(words); i++ {
		set[strings.Join(words[i:i+k], " ")] = struct{}{}
	}
	return set
}

// canonUnits splits canon into comparable units: headings and table
// separator rows are dropped, each table row becomes one unit, and each
// remaining line is split into sentences on ". " boundaries. Units whose
// normalized word count is below minUnitWords are discarded.
func canonUnits(canon string) []string {
	var units []string

	for _, line := range strings.Split(canon, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if isTableSeparatorRow(trimmed) {
			continue
		}
		if isTableRow(trimmed) {
			units = appendUnit(units, trimmed)
			continue
		}
		for _, sentence := range strings.Split(trimmed, ". ") {
			units = appendUnit(units, strings.TrimSpace(sentence))
		}
	}

	return units
}

func appendUnit(units []string, candidate string) []string {
	if candidate == "" {
		return units
	}
	if len(normalizeWords(candidate)) < minUnitWords {
		return units
	}
	return append(units, candidate)
}

func isTableRow(line string) bool {
	return strings.Contains(line, "|")
}

func isTableSeparatorRow(line string) bool {
	hasDash := false
	for _, r := range line {
		switch r {
		case '|', '-', ':', ' ', '\t':
			if r == '-' {
				hasDash = true
			}
		default:
			return false
		}
	}
	return hasDash
}

func overlapRatio(a, b map[string]struct{}) float64 {
	if len(a) == 0 {
		return 0
	}
	matched := 0
	for k := range a {
		if _, ok := b[k]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(a))
}

func fingerprintUnit(words []string) string {
	sum := sha256.Sum256([]byte(strings.Join(words, " ")))
	return hex.EncodeToString(sum[:])[:12]
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// duplicationCandidates compares canon against consumers in both directions.
func duplicationCandidates(canon string, consumers []consumerParagraph) []findingCandidate {
	var candidates []findingCandidate

	for _, u := range canonUnits(canon) {
		uWords := normalizeWords(u)
		uShingles := shingleSet(uWords, shingleWidth)
		if len(uShingles) == 0 {
			continue
		}
		fp := fingerprintUnit(uWords)
		for _, p := range consumers {
			if p.Exempt {
				continue
			}
			pShingles := shingleSet(normalizeWords(p.Text), shingleWidth)
			if len(pShingles) == 0 {
				continue
			}
			if overlapRatio(uShingles, pShingles) >= overlapThreshold {
				candidates = append(candidates, findingCandidate{
					finding: finding{
						Path:      p.Path,
						StartLine: p.StartLine,
						Detail:    fmt.Sprintf("canon unit %q duplicated in consumer paragraph %q", excerpt(u), excerpt(p.Text)),
					},
					fingerprint: fp,
				})
			}
		}
	}

	// Reverse direction: split each consumer paragraph into canon-style
	// units and check them against the whole-canon shingle set. This is
	// what makes #451's "Remainder B" (workflow prose copied up into canon)
	// covered rather than merely asserted: the forward direction alone only
	// catches canon prose restated downstream, not consumer prose that was
	// later pasted into canon.
	canonShingles := shingleSet(normalizeWords(canon), shingleWidth)
	for _, p := range consumers {
		if p.Exempt {
			continue
		}
		for _, cu := range canonUnits(p.Text) {
			cuWords := normalizeWords(cu)
			cuShingles := shingleSet(cuWords, shingleWidth)
			if len(cuShingles) == 0 {
				continue
			}
			if overlapRatio(cuShingles, canonShingles) >= overlapThreshold {
				candidates = append(candidates, findingCandidate{
					finding: finding{
						Path:      p.Path,
						StartLine: p.StartLine,
						Detail:    fmt.Sprintf("consumer unit %q duplicated in canon %q", excerpt(cu), excerpt(canon)),
					},
					fingerprint: fingerprintUnit(cuWords),
				})
			}
		}
	}

	return candidates
}

// duplicationFindingsWithUsage runs duplicationCandidates and applies
// waivers, additionally reporting which waivers matched at least one
// candidate so callers can detect stale waivers.
func duplicationFindingsWithUsage(canon string, consumers []consumerParagraph, waivers []waiver) ([]finding, []bool) {
	candidates := duplicationCandidates(canon, consumers)

	used := make([]bool, len(waivers))
	var findings []finding
	for _, c := range candidates {
		waived := false
		for i, w := range waivers {
			if w.Consumer == c.Path && w.Fingerprint == c.fingerprint {
				used[i] = true
				waived = true
			}
		}
		if !waived {
			findings = append(findings, c.finding)
		}
	}

	return findings, used
}

// duplicationFindings compares canon against consumers in both directions
// and returns findings not covered by an applicable waiver.
func duplicationFindings(canon string, consumers []consumerParagraph, waivers []waiver) []finding {
	findings, _ := duplicationFindingsWithUsage(canon, consumers, waivers)
	return findings
}

func invalidWaivers(waivers []waiver) []waiver {
	var out []waiver
	for _, w := range waivers {
		if strings.TrimSpace(w.Reason) == "" {
			out = append(out, w)
		}
	}
	return out
}

func staleWaivers(waivers []waiver, used []bool) []waiver {
	var out []waiver
	for i, w := range waivers {
		if !used[i] {
			out = append(out, w)
		}
	}
	return out
}

func loadWaivers(t *testing.T) []waiver {
	t.Helper()

	data, err := os.ReadFile("testdata/canon_drift_waivers.json")
	if err != nil {
		t.Fatalf("read waivers file: %v", err)
	}
	var waivers []waiver
	if err := json.Unmarshal(data, &waivers); err != nil {
		t.Fatalf("unmarshal waivers file: %v", err)
	}
	return waivers
}

func TestCanonNotDuplicatedInConsumers(t *testing.T) {
	consumers := loadConsumers(t)
	waivers := loadWaivers(t)

	for _, w := range invalidWaivers(waivers) {
		t.Errorf("waiver for %s (%s) has an empty reason", w.Consumer, w.Fingerprint)
	}

	findings, used := duplicationFindingsWithUsage(delegationInstructions, consumers, waivers)
	for _, f := range findings {
		t.Errorf("canon drift: %s:%d: %s", f.Path, f.StartLine, f.Detail)
	}

	for _, w := range staleWaivers(waivers, used) {
		t.Errorf("stale waiver: consumer=%s fingerprint=%s reason=%q matched nothing this run", w.Consumer, w.Fingerprint, w.Reason)
	}
}

func TestDuplicationFindingsSeeded(t *testing.T) {
	const fakeCanon = `## Fake Canon

Always delegate the implementation work to a specialist sub agent whenever a task touches more than one file in the repository, per policy.
`

	const canonSentence = "Always delegate the implementation work to a specialist sub agent whenever a task touches more than one file in the repository, per policy."

	pathParaphrase := "fake/consumer_paraphrase.md"
	paraphraseParagraph := consumerParagraph{
		Path:      pathParaphrase,
		StartLine: 1,
		Text:      canonSentence,
	}

	pathExempt := "fake/consumer_exempt.md"
	exemptText := canonSentence + " (in your system prompt already.)"
	exemptParagraph := consumerParagraph{
		Path:      pathExempt,
		StartLine: 1,
		Text:      exemptText,
		Exempt:    containsDeferralMarker(exemptText),
	}

	pathUnrelated := "fake/consumer_unrelated.md"
	unrelatedParagraph := consumerParagraph{
		Path:      pathUnrelated,
		StartLine: 1,
		Text:      "Run the test suite before you open the file and check that the review team approved the changes to the config.",
	}

	const pastedSentence = "The executor must never mutate implementation scoped files directly and must always dispatch a dedicated code sub agent instead."
	pathVerbatim := "fake/consumer_verbatim.md"
	verbatimParagraph := consumerParagraph{
		Path:      pathVerbatim,
		StartLine: 1,
		Text:      pastedSentence,
	}
	canonWithPastedText := "## Other Canon\n\n" + pastedSentence + "\n"

	t.Run("paraphrase is flagged", func(t *testing.T) {
		findings := duplicationFindings(fakeCanon, []consumerParagraph{paraphraseParagraph}, nil)
		if len(findings) == 0 {
			t.Fatalf("expected paraphrase paragraph to be flagged, got no findings")
		}
	})

	t.Run("deferral marker exempts paragraph", func(t *testing.T) {
		findings := duplicationFindings(fakeCanon, []consumerParagraph{exemptParagraph}, nil)
		if len(findings) != 0 {
			t.Errorf("expected exempt paragraph to produce no findings, got %v", findings)
		}
	})

	t.Run("verbatim paste flagged in reverse direction", func(t *testing.T) {
		findings := duplicationFindings(canonWithPastedText, []consumerParagraph{verbatimParagraph}, nil)
		if len(findings) == 0 {
			t.Fatalf("expected verbatim-pasted consumer text to be flagged via reverse direction, got no findings")
		}
	})

	t.Run("unrelated common vocabulary is not flagged", func(t *testing.T) {
		findings := duplicationFindings(fakeCanon, []consumerParagraph{unrelatedParagraph}, nil)
		if len(findings) != 0 {
			t.Errorf("expected unrelated paragraph to produce no findings, got %v", findings)
		}
	})

	t.Run("waiver suppresses matching finding", func(t *testing.T) {
		candidates := duplicationCandidates(fakeCanon, []consumerParagraph{paraphraseParagraph})
		if len(candidates) == 0 {
			t.Fatalf("setup failure: expected at least one candidate to waive")
		}
		w := waiver{
			Consumer:    candidates[0].Path,
			Fingerprint: candidates[0].fingerprint,
			Reason:      "test waiver covering seeded paraphrase fixture",
		}
		findings := duplicationFindings(fakeCanon, []consumerParagraph{paraphraseParagraph}, []waiver{w})
		if len(findings) != 0 {
			t.Errorf("expected waiver to suppress the matching finding, got %v", findings)
		}
	})

	t.Run("waiver with empty reason is rejected", func(t *testing.T) {
		bad := waiver{Consumer: pathParaphrase, Fingerprint: "deadbeefcafe", Reason: ""}
		invalid := invalidWaivers([]waiver{bad})
		if len(invalid) != 1 {
			t.Errorf("expected empty-reason waiver to be rejected, got %d invalid waivers", len(invalid))
		}
	})

	t.Run("waiver matching nothing is reported stale", func(t *testing.T) {
		stray := waiver{
			Consumer:    pathParaphrase,
			Fingerprint: "000000000000",
			Reason:      "does not match anything",
		}
		_, used := duplicationFindingsWithUsage(fakeCanon, []consumerParagraph{paraphraseParagraph}, []waiver{stray})
		stale := staleWaivers([]waiver{stray}, used)
		if len(stale) != 1 {
			t.Errorf("expected stray waiver to be reported stale, got %d stale waivers", len(stale))
		}
	})
}
