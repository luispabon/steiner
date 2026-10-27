package tui

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// legacyRenderDelegationTranscript is the pre-E2a implementation, kept verbatim
// as the reference the tail-only renderer must match.
func (b *contentBuffer) legacyRenderDelegationTranscript(dd *delegationDisplayState, width int) []string {
	rows := make([]string, 0, len(dd.entries))
	for _, entry := range dd.entries {
		rows = append(rows, b.renderDelegationEntry(entry, width)...)
	}
	if len(rows) == 0 {
		return nil
	}
	if len(rows) > maxDelegationTranscriptRows {
		keep := maxDelegationTranscriptRows - 1
		if keep < 0 {
			keep = 0
		}
		rows = append([]string{b.styles.FgMute.Render("[old child events hidden]")}, rows[len(rows)-keep:]...)
	}
	if b.maxDelegationBodyLines > 0 && len(rows) > b.maxDelegationBodyLines {
		rows = rows[len(rows)-b.maxDelegationBodyLines:]
	}
	return rows
}

var (
	transcriptTestWidths = []int{20, 80, 200}
	transcriptTestCaps   = []int{0, 10, 39, 40, 41, 60}
	transcriptTestWords  = []string{"alpha", "beta", "ü", "é", "ñ", "naïve", "\x1b[1mbold\x1b[0m", "résumé", strings.Repeat("x", 90)}
)

func newTranscriptTestBuffer(maxBodyLines int) *contentBuffer {
	return &contentBuffer{styles: testStyles(theme.AccentAmber), maxDelegationBodyLines: maxBodyLines}
}

func randomTranscriptWords(rng *rand.Rand, n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = transcriptTestWords[rng.IntN(len(transcriptTestWords))]
	}
	return strings.Join(words, " ")
}

func randomDelegationTranscript(rng *rand.Rand) []delegationTranscriptEntry {
	entries := make([]delegationTranscriptEntry, rng.IntN(101))
	for i := range entries {
		switch rng.IntN(4) {
		case 0:
			entries[i] = delegationTranscriptEntry{
				kind: delegationTranscriptEntryThinking,
				body: randomTranscriptWords(rng, rng.IntN(4)),
			}
		case 1:
			entries[i] = delegationTranscriptEntry{
				kind:     delegationTranscriptEntryTool,
				tool:     "read",
				args:     randomTranscriptWords(rng, rng.IntN(5)),
				status:   []string{"", "running", "complete"}[rng.IntN(3)],
				hasError: rng.IntN(5) == 0,
			}
		default:
			lines := make([]string, rng.IntN(61))
			for l := range lines {
				lines[l] = randomTranscriptWords(rng, rng.IntN(20))
			}
			entries[i] = delegationTranscriptEntry{
				kind: delegationTranscriptEntryAssistant,
				body: strings.Join(lines, "\n"),
			}
		}
	}
	return entries
}

func TestRenderDelegationTranscriptMatchesLegacyRandomized(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := range 60 {
		entries := randomDelegationTranscript(rng)
		maxBodyLines := transcriptTestCaps[rng.IntN(len(transcriptTestCaps))]
		b := newTranscriptTestBuffer(maxBodyLines)
		dd := &delegationDisplayState{entries: entries}
		for _, width := range transcriptTestWidths {
			got := b.renderDelegationTranscript(dd, width)
			want := b.legacyRenderDelegationTranscript(dd, width)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("iter %d (entries=%d width=%d cap=%d):\ngot  %q\nwant %q", iter, len(entries), width, maxBodyLines, got, want)
			}
		}
	}
}

func assistantTranscriptEntry(prefix string, lines int) delegationTranscriptEntry {
	parts := make([]string, lines)
	for i := range parts {
		parts[i] = prefix + " " + strconv.Itoa(i)
	}
	return delegationTranscriptEntry{kind: delegationTranscriptEntryAssistant, body: strings.Join(parts, "\n")}
}

func TestRenderDelegationTranscriptBoundaries(t *testing.T) {
	t.Parallel()
	rowEntries := func(n int) []delegationTranscriptEntry {
		entries := make([]delegationTranscriptEntry, n)
		for i := range entries {
			entries[i] = assistantTranscriptEntry("row", 1)
		}
		return entries
	}
	zeroRows := []delegationTranscriptEntry{
		{kind: delegationTranscriptEntryAssistant, body: ""},
		{kind: delegationTranscriptEntryAssistant, body: " \n\t "},
		{kind: delegationTranscriptEntryThinking, body: ""},
		{kind: delegationTranscriptEntryKind(99), body: "unknown kind"},
	}
	mixed := append(append(rowEntries(20), zeroRows...), rowEntries(21)...)

	cases := []struct {
		name      string
		entries   []delegationTranscriptEntry
		totalRows int
	}{
		{name: "exactly 40 rows", entries: rowEntries(40), totalRows: 40},
		{name: "41 rows", entries: rowEntries(41), totalRows: 41},
		{name: "one entry longer than 40 rows", entries: []delegationTranscriptEntry{assistantTranscriptEntry("line", 60)}, totalRows: 60},
		{name: "all entries zero rows", entries: zeroRows, totalRows: 0},
		{name: "zero-row entries between 41 rows", entries: mixed, totalRows: 41},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dd := &delegationDisplayState{entries: tc.entries}
			counter := newTranscriptTestBuffer(0)
			count := 0
			for _, entry := range tc.entries {
				count += len(counter.renderDelegationEntry(entry, 80))
			}
			if count != tc.totalRows {
				t.Fatalf("test setup rendered %d rows, want %d", count, tc.totalRows)
			}
			for _, width := range transcriptTestWidths {
				for _, maxBodyLines := range transcriptTestCaps {
					b := newTranscriptTestBuffer(maxBodyLines)
					got := b.renderDelegationTranscript(dd, width)
					want := b.legacyRenderDelegationTranscript(dd, width)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("width=%d cap=%d:\ngot  %q\nwant %q", width, maxBodyLines, got, want)
					}
				}
			}
		})
	}
}

func TestDelegationPromptPreviewTextTracksPrompt(t *testing.T) {
	t.Parallel()
	assertPreview := func(t *testing.T, dd *delegationDisplayState, label string) {
		t.Helper()
		if got, want := dd.promptPreviewText(), previewDelegationText(dd.promptText); got != want {
			t.Errorf("%s: promptPreviewText() = %q, want %q", label, got, want)
		}
	}

	dd := &delegationDisplayState{promptText: "Investigate the delegation prompt cache " + strings.Repeat("and report back ", 8) + "\n  with   spacing"}
	assertPreview(t, dd, "initial")

	dd.promptText = "second prompt"
	assertPreview(t, dd, "reassigned")

	copied := *dd
	copied.promptText = "copied prompt, reassigned on the copy"
	assertPreview(t, &copied, "by-value copy reassigned")
	assertPreview(t, dd, "original after copy")

	dd.promptText = ""
	assertPreview(t, dd, "reassigned to empty")

	assertPreview(t, &delegationDisplayState{}, "zero value")
}
