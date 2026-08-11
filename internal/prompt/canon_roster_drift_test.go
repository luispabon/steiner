package prompt

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// canonRoster locates the "## Your specialists" heading in canon, then reads
// markdown table rows until the next "##" heading, skipping the header row
// (the one containing all of "Agent", "Lane", "Do not use for") and
// separator rows, taking the first cell (backticks and whitespace trimmed)
// of each remaining row.
//
// This deliberately duplicates the walking logic of
// internal/delegation/preamble_roster_test.go's parseSpecialistRoster
// (heading detection via strings.Contains(line, "## Your specialists"),
// table rows starting with "| ", stopping at the next "##") because
// importing internal/delegation here would be an import cycle:
// internal/delegation/bootstrap.go imports internal/prompt. That upstream
// test, TestPreambleSpecialistRosterMatchesAgentTypes, is what proves the
// parsed roster equals AllAgentTypes() — that link is what makes a Go
// rename of an AgentType eventually propagate into a failure there. This
// function only needs the raw roster names to compare against, so it
// panics (rather than taking a *testing.T) on a missing heading or an
// empty roster; callers in tests should treat a panic as a t.Fatalf.
func canonRoster(canon string) map[string]struct{} {
	lines := strings.Split(canon, "\n")

	var headingIndex int
	var foundHeading bool
	for i, line := range lines {
		if strings.Contains(line, "## Your specialists") {
			foundHeading = true
			headingIndex = i
			break
		}
	}
	if !foundHeading {
		panic("canonRoster: \"## Your specialists\" heading not found in canon")
	}

	roster := make(map[string]struct{})
	for i := headingIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "##") {
			break
		}
		if !strings.HasPrefix(line, "| ") {
			if len(roster) > 0 {
				break
			}
			continue
		}
		if strings.Contains(line, "Agent") && strings.Contains(line, "Lane") && strings.Contains(line, "Do not use for") {
			continue
		}
		if isRosterSeparatorRow(line) {
			continue
		}

		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}
		firstCell := strings.TrimSpace(cells[1])
		firstCell = strings.Trim(firstCell, "`")
		if firstCell != "" {
			roster[firstCell] = struct{}{}
		}
	}

	if len(roster) == 0 {
		panic("canonRoster: heading found but no roster rows parsed")
	}

	return roster
}

func isRosterSeparatorRow(line string) bool {
	cells := strings.Split(line, "|")
	for _, cell := range cells[1 : len(cells)-1] {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		allDashes := true
		for _, ch := range trimmed {
			if ch != '-' {
				allDashes = false
				break
			}
		}
		if !allDashes {
			return false
		}
	}
	return true
}

// Roster-reference frames: each captures a backticked token in submatch
// group 1, applied to RAW (not normalized) paragraph text since backticks
// and word boundaries carry the signal here.
//
// Deliberately absent: a bare "`X` tool" frame. Skills legitimately name
// built-in tools like `mutate`, `read`, `grep` throughout their prose, and
// none of those are roster members — adding that frame would produce
// constant false positives on correct text. This is a deliberate
// false-negative tradeoff (a stale tool-name inside "the `X` tool" phrasing
// would not be caught), not an oversight.
var (
	frameSubAgent   = regexp.MustCompile("`([a-z_]+)`\\s+sub-agents?\\b")
	frameDelegated  = regexp.MustCompile("delegated\\s+`([a-z_]+)`")
	frameDelegation = regexp.MustCompile("`([a-z_]+)`\\s+delegation\\b")
	frameToolCall   = regexp.MustCompile("`([a-z_]+)\\(")
)

var rosterFrames = []struct {
	name string
	re   *regexp.Regexp
}{
	{"`X` sub-agent(s)", frameSubAgent},
	{"delegated `X`", frameDelegated},
	{"`X` delegation", frameDelegation},
	{"`X(` tool call", frameToolCall},
}

// rosterFindings reports every framed token in consumers that is not in
// roster.
//
// This runs over every paragraph from loadConsumers with no exemptions. The
// historical #445 §3 bug (stale tool names like `verify`, `plan`, `delegate`
// left behind by a Go rename) lived inside exactly the kind of labelled,
// workflow-specific instruction a caller might otherwise think to skip.
func rosterFindings(roster map[string]struct{}, consumers []consumerParagraph) []finding {
	type key struct {
		path  string
		line  int
		token string
	}
	seen := make(map[key]bool)

	var findings []finding
	for _, p := range consumers {
		for _, frame := range rosterFrames {
			for _, m := range frame.re.FindAllStringSubmatch(p.Text, -1) {
				token := m[1]
				if _, ok := roster[token]; ok {
					continue
				}
				k := key{path: p.Path, line: p.StartLine, token: token}
				if seen[k] {
					continue
				}
				seen[k] = true
				findings = append(findings, finding{
					Path:      p.Path,
					StartLine: p.StartLine,
					Detail:    "stale roster token `" + token + "` matched by frame \"" + frame.name + "\" but not present in the current canon roster",
				})
			}
		}
	}

	return findings
}

func TestConsumersNameOnlyCurrentSpecialists(t *testing.T) {
	roster := canonRoster(delegationInstructions)
	consumers := loadConsumers(t)

	findings := rosterFindings(roster, consumers)
	for _, f := range findings {
		t.Errorf("roster drift: %s:%d: %s", f.Path, f.StartLine, f.Detail)
	}
}

func TestRosterFindingsSeeded(t *testing.T) {
	fakeRoster := map[string]struct{}{"code": {}, "evaluate": {}, "review": {}}

	tests := []struct {
		name    string
		para    consumerParagraph
		wantHit bool
	}{
		{
			name: "stale sub-agent name is flagged",
			para: consumerParagraph{
				Path:      "fake/a.md",
				StartLine: 1,
				Text:      "dispatch a delegated `verify` sub-agent",
			},
			wantHit: true,
		},
		{
			name: "current sub-agent name is not flagged",
			para: consumerParagraph{
				Path:      "fake/b.md",
				StartLine: 1,
				Text:      "dispatch a delegated `code` sub-agent",
			},
			wantHit: false,
		},
		{
			name: "stale tool-call token is flagged",
			para: consumerParagraph{
				Path:      "fake/c.md",
				StartLine: 1,
				Text:      "`plan({...})`",
			},
			wantHit: true,
		},
		{
			name: "current tool-call token is not flagged",
			para: consumerParagraph{
				Path:      "fake/d.md",
				StartLine: 1,
				Text:      "`evaluate({...})`",
			},
			wantHit: false,
		},
		{
			name: "stale token inside a workflow-specific aside is still flagged",
			para: consumerParagraph{
				Path:      "fake/e.md",
				StartLine: 1,
				Text:      "This workflow dispatches a delegated `verify` sub-agent instead.",
			},
			wantHit: true,
		},
		{
			name: "bare tool phrasing is never flagged",
			para: consumerParagraph{
				Path:      "fake/f.md",
				StartLine: 1,
				Text:      "the `mutate` tool",
			},
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			findings := rosterFindings(fakeRoster, []consumerParagraph{tc.para})
			got := len(findings) > 0
			if got != tc.wantHit {
				t.Errorf("rosterFindings() hit=%v, want hit=%v (findings=%v)", got, tc.wantHit, findings)
			}
		})
	}
}

func TestCanonRosterParses(t *testing.T) {
	roster := canonRoster(delegationInstructions)

	want := map[string]struct{}{
		"explore":      {},
		"research":     {},
		"code":         {},
		"evaluate":     {},
		"sanity_check": {},
		"review":       {},
	}

	if len(roster) != len(want) {
		t.Errorf("canonRoster() returned %d entries, want %d (roster=%v)", len(roster), len(want), sortedKeys(roster))
	}
	for k := range want {
		if _, ok := roster[k]; !ok {
			t.Errorf("canonRoster() missing expected entry %q (roster=%v)", k, sortedKeys(roster))
		}
	}
	for k := range roster {
		if _, ok := want[k]; !ok {
			t.Errorf("canonRoster() has unexpected entry %q (roster=%v)", k, sortedKeys(roster))
		}
	}

	if _, ok := roster["vision"]; ok {
		t.Errorf("canonRoster() should not contain \"vision\" (internal-only, never routed by the orchestrator)")
	}
	if _, ok := roster["follow_up"]; ok {
		t.Errorf("canonRoster() should not contain \"follow_up\" (not an AgentType, a continuation of an existing sub-agent)")
	}
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
