package prompt

import (
	"regexp"
	"testing"
)

// canonRoster is the set of specialist names canon currently declares, taken
// from the specialists slice that renders the roster table. Consumer prose is
// checked against this set.
func canonRoster() map[string]struct{} {
	roster := make(map[string]struct{}, len(specialists))
	for _, name := range SpecialistNames() {
		roster[name] = struct{}{}
	}
	return roster
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
	roster := canonRoster()
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
