package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

// newTestBuffer returns a minimal contentBuffer suitable for rendering tests.
func newTestBuffer(t *testing.T) *contentBuffer {
	t.Helper()
	styles := testStyles("#5599ff")
	return &contentBuffer{
		styles:   styles,
		segments: make([]contentSegment, 0),
	}
}

func TestCompactionBoxCollapsedInProgress(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		subtitle:     "summarizing context",
		finished:     false,
		startTime:    0, // no wall clock in test
		spinnerFrame: 3,
	}

	rows := b.compactionBoxRows(cd, 80)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one collapsed header row: %q", len(rows), rows)
	}
	out := b.renderCompactionBanner(cd, 80)

	// Must end with newline.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output does not end with newline")
	}
	// Must contain the tag with the collapsed disclosure triangle.
	plain := stripANSI(out)
	if !strings.Contains(plain, "▸ compaction") {
		t.Errorf("output missing '▸ compaction' header: %q", plain)
	}
	if strings.Contains(plain, "▾") {
		t.Errorf("collapsed output should not contain the expanded triangle: %q", plain)
	}
	// Must contain a spinner character (braille).
	hasSpinner := false
	for _, frame := range spinnerFrames {
		if strings.Contains(plain, frame) {
			hasSpinner = true
			break
		}
	}
	if !hasSpinner {
		t.Errorf("collapsed in-progress output missing spinner: %q", plain)
	}
}

func TestCompactionBoxCollapsedFinished(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		subtitle:        "summarizing context",
		finished:        true,
		elapsed:         "3s",
		compactionCount: 2,
	}

	rows := b.compactionBoxRows(cd, 80)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one collapsed header row: %q", len(rows), rows)
	}
	out := stripANSI(b.renderCompactionBanner(cd, 80))

	if !strings.Contains(out, "▸ compaction") {
		t.Errorf("output missing '▸ compaction' header: %q", out)
	}
	if strings.Contains(out, "▾") {
		t.Errorf("collapsed output should not contain the expanded triangle: %q", out)
	}
	// Must show success checkmark.
	if !strings.Contains(out, "✓") {
		t.Errorf("finished collapsed output missing '✓': %q", out)
	}
	// Must show elapsed.
	if !strings.Contains(out, "3s") {
		t.Errorf("finished collapsed output missing elapsed '3s': %q", out)
	}
	// Must show count.
	if !strings.Contains(out, "#2") {
		t.Errorf("finished collapsed output missing '#2': %q", out)
	}
}

func TestCompactionFinishedHeaderShowsCacheRateBeforeElapsed(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:          true,
		elapsed:           "3s",
		compactionCount:   2,
		cacheReadTokens:   950,
		inputTokens:       50,
		cacheCreateTokens: 0,
	}

	out := b.renderCompactionBanner(cd, 80)
	cacheIndex := strings.Index(out, "cache 95.0%")
	glyphIndex := strings.Index(out, "✓")
	elapsedIndex := strings.Index(out, "3s")
	countIndex := strings.Index(out, "#2")
	if cacheIndex < 0 {
		t.Fatalf("output missing cache rate: %q", out)
	}
	if glyphIndex < 0 || elapsedIndex < 0 || countIndex < 0 {
		t.Fatalf("output missing metadata: %q", out)
	}
	if cacheIndex >= elapsedIndex || elapsedIndex >= countIndex || countIndex >= glyphIndex {
		t.Errorf("metadata order = cache:%d elapsed:%d count:%d glyph:%d, want cache < elapsed < count < glyph", cacheIndex, elapsedIndex, countIndex, glyphIndex)
	}
}

func TestCompactionFinishedHeaderShowsZeroCacheRate(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:        true,
		elapsed:         "3s",
		compactionCount: 2,
		inputTokens:     100,
	}

	out := b.renderCompactionBanner(cd, 80)
	cacheIndex := strings.Index(out, "cache 0.0%")
	glyphIndex := strings.Index(out, "✓")
	elapsedIndex := strings.Index(out, "3s")
	countIndex := strings.Index(out, "#2")
	if cacheIndex < 0 {
		t.Fatalf("output missing zero cache rate: %q", out)
	}
	if glyphIndex < 0 || elapsedIndex < 0 || countIndex < 0 {
		t.Fatalf("output missing metadata: %q", out)
	}
	if cacheIndex >= elapsedIndex || elapsedIndex >= countIndex || countIndex >= glyphIndex {
		t.Errorf("metadata order = cache:%d elapsed:%d count:%d glyph:%d, want cache < elapsed < count < glyph", cacheIndex, elapsedIndex, countIndex, glyphIndex)
	}
}

func TestCompactionHeaderOmitsCacheRateWithoutUsage(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:        true,
		elapsed:         "3s",
		compactionCount: 2,
	}

	out := b.renderCompactionBanner(cd, 80)
	if strings.Contains(out, "cache") {
		t.Fatalf("output should omit cache rate: %q", out)
	}
	glyphIndex := strings.Index(out, "✓")
	elapsedIndex := strings.Index(out, "3s")
	countIndex := strings.Index(out, "#2")
	if glyphIndex < 0 || elapsedIndex < 0 || countIndex < 0 {
		t.Fatalf("output missing metadata: %q", out)
	}
	if elapsedIndex >= countIndex || countIndex >= glyphIndex {
		t.Errorf("metadata order = elapsed:%d count:%d glyph:%d, want elapsed < count < glyph", elapsedIndex, countIndex, glyphIndex)
	}
}

func TestCompactionDiagnosticsWiresBannerUsage(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:          "ok",
		CompactionCount:   1,
		SummaryTitle:      "ctx",
		CacheReadTokens:   30,
		InputTokens:       50,
		CacheCreateTokens: 20,
	})

	if len(b.segments) == 0 {
		t.Fatal("finished compaction diagnostics created no segments")
	}
	seg := b.segments[len(b.segments)-1]
	if seg.kind != segmentCompactionBanner || seg.compactionData == nil {
		t.Fatalf("last segment = %#v, want compaction banner with data", seg)
	}
	data := seg.compactionData
	if !data.finished {
		t.Fatal("finished compaction banner has finished = false")
	}
	if data.cacheReadTokens != 30 || data.inputTokens != 50 || data.cacheCreateTokens != 20 {
		t.Fatalf("banner usage = %d/%d/%d, want 30/50/20", data.cacheReadTokens, data.inputTokens, data.cacheCreateTokens)
	}
	if rendered := b.renderCompactionBanner(data, 80); !strings.Contains(rendered, "cache ") {
		t.Fatalf("rendered banner = %q, want cache rate", rendered)
	}

	b = newTestBuffer(t)
	b.handleCompactionDiagnostics(output.ContextCompactionEvent{
		Severity:        "compacting",
		CompactionCount: 1,
	})
	if len(b.segments) == 0 {
		t.Fatal("in-progress compaction diagnostics created no segments")
	}
	seg = b.segments[len(b.segments)-1]
	if seg.kind != segmentCompactionBanner || seg.compactionData == nil {
		t.Fatalf("last in-progress segment = %#v, want compaction banner with data", seg)
	}
	data = seg.compactionData
	if data.cacheReadTokens != 0 || data.inputTokens != 0 || data.cacheCreateTokens != 0 {
		t.Fatalf("in-progress banner usage = %d/%d/%d, want 0/0/0", data.cacheReadTokens, data.inputTokens, data.cacheCreateTokens)
	}
}

func TestCompactionInProgressHeaderOmitsCacheRate(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:          false,
		elapsed:           "3s",
		compactionCount:   2,
		cacheReadTokens:   950,
		inputTokens:       50,
		cacheCreateTokens: 0,
	}

	out := b.renderCompactionBanner(cd, 80)
	if strings.Contains(out, "cache") {
		t.Fatalf("in-progress output should omit cache rate: %q", out)
	}
	if !strings.Contains(stripANSI(out), "3s #2 "+spinnerFrames[0]) {
		t.Fatalf("in-progress output = %q, want elapsed, count, then spinner glyph", stripANSI(out))
	}
}

func TestCompactionSkippedHeaderOmitsGlyph(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:        true,
		skipped:         true,
		elapsed:         "3s",
		compactionCount: 2,
	}

	out := stripANSI(b.renderCompactionBanner(cd, 80))
	if strings.Contains(out, "✓") {
		t.Fatalf("skipped compaction header should omit the check glyph: %q", out)
	}
	if !strings.Contains(out, "3s #2") {
		t.Fatalf("skipped compaction header missing elapsed and count: %q", out)
	}
}

// TestCompactionBoxCollapsedIsSingleRow checks the fallback model sits in the
// one-row header meta cluster instead of a second note row, and that the
// collapsed header fits the box's inner width.
func TestCompactionBoxCollapsedIsSingleRow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		width     int
		wantModel bool
	}{
		{name: "very narrow drops the model label", width: 20},
		{name: "narrow drops the model label", width: 40},
		{name: "wide shows the model label", width: 80, wantModel: true},
		{name: "widest shows the model label", width: 120, wantModel: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBuffer(t)
			cd := &compactionBannerData{
				label:                "Context compacted",
				subtitle:             "summarizing context",
				finished:             true,
				compactionModel:      "anthropic/claude-haiku-5-5",
				compactionModelAlias: "haiku",
				fallbackStatus:       output.CompactionFallbackUsed,
				fallbackReason:       "primary over budget",
			}

			rows := b.compactionBoxRows(cd, tt.width)
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want one collapsed header row: %q", len(rows), rows)
			}
			header := stripANSI(rows[0])
			if innerWidth := max(1, tt.width-4); lipgloss.Width(header) > innerWidth {
				t.Errorf("header width = %d, want <= %d: %q", lipgloss.Width(header), innerWidth, header)
			}
			if !strings.HasPrefix(header, "▸ compaction") {
				t.Errorf("header = %q, want it to start with the collapsed triangle and tag", header)
			}
			if strings.Contains(header, "via ") || strings.Contains(header, "primary over budget") {
				t.Errorf("header = %q, want no fallback note row", header)
			}
			if got := strings.Contains(header, "haiku"); got != tt.wantModel {
				t.Errorf("header = %q, model label present = %v, want %v", header, got, tt.wantModel)
			}
		})
	}
}

func TestCompactionBoxExpandedShowsDetailRows(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		subtitle:             "summarizing context",
		finished:             true,
		elapsed:              "3s",
		compactionCount:      2,
		compactionModel:      "anthropic/claude-haiku-5-5",
		compactionModelAlias: "haiku",
		fallbackStatus:       output.CompactionFallbackUsed,
		fallbackReason:       "primary over budget",
		beforeTokens:         120000,
		beforePct:            80,
		afterTokens:          48000,
		afterPct:             32,
		compactedTurns:       12,
		compactedMessages:    34,
		retainedTurns:        4,
		retainedMessages:     9,
		retainedRawTurns:     3,
		mode:                 "stage",
		summaryBytes:         2048,
		summaryTokenBudget:   4000,
		contextWindow:        200000,
		thresholdTokens:      140000,
		compactionThreshold:  70,
		thresholdAchieved:    true,
		cacheReadTokens:      900,
		inputTokens:          100,
		cacheCreateTokens:    50,
	}

	collapsed := *cd
	if rows := b.compactionBoxRows(&collapsed, 120); len(rows) != 1 {
		t.Fatalf("collapsed rows = %d, want one header row: %q", len(rows), rows)
	}

	expanded := *cd
	expanded.expanded = true
	rows := b.compactionBoxRows(&expanded, 120)
	if len(rows) != 9 {
		t.Fatalf("expanded rows = %d, want header, blank, and seven detail rows: %q", len(rows), rows)
	}
	if rows[1] != "" {
		t.Errorf("row 1 = %q, want the blank separator row", rows[1])
	}

	details := []struct{ key, want string }{
		{"model", "haiku (anthropic/claude-haiku-5-5)"},
		{"fallback", "used · primary over budget"},
		{"tokens", "120k → 48k prompt"},
		{"window", "200k context · threshold 70% (140k) · achieved"},
		{"history", "12 turns / 34 messages compacted · 4 turns / 9 messages retained · 3 raw turns retained"},
		{"summary", "stage mode · 2KB · budget 4000 tokens"},
		{"cache", "read 900 · input 100 · create 50"},
	}
	for i, detail := range details {
		row := stripANSI(rows[i+2])
		if !strings.HasPrefix(row, detail.key) {
			t.Errorf("row %d = %q, want it to start with the %q key", i+2, row, detail.key)
		}
		if !strings.Contains(row, detail.want) {
			t.Errorf("%q row = %q, want it to contain %q", detail.key, row, detail.want)
		}
	}
	if tokens := stripANSI(rows[4]); !strings.Contains(tokens, "80% → 32% context") {
		t.Errorf("tokens row = %q, want the context percentages", tokens)
	}

	for _, width := range []int{20, 120} {
		innerWidth := max(1, width-4)
		// The detail rows carry free-form values (a long error reason), so each
		// one is flattened and truncated to the inner width.
		for _, row := range b.compactionBoxRows(&expanded, width)[2:] {
			plain := stripANSI(row)
			if got := lipgloss.Width(plain); got > innerWidth {
				t.Errorf("width %d: detail row = %q is %d cells, want <= %d", width, plain, got, innerWidth)
			}
			if strings.ContainsAny(plain, "\n\r\t") {
				t.Errorf("width %d: detail row = %q contains a line break or tab", width, plain)
			}
		}
	}
}

func TestCompactionModelLabelPrefersAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cd      *compactionBannerData
		want    string
		notWant string
	}{
		{
			name:    "alias preferred over canonical",
			cd:      &compactionBannerData{finished: true, compactionModel: "anthropic/claude-haiku-5-5", compactionModelAlias: "haiku"},
			want:    "haiku",
			notWant: "anthropic/claude-haiku-5-5",
		},
		{
			name: "raw canonical without alias",
			cd:   &compactionBannerData{finished: true, compactionModel: "anthropic/claude-haiku-5-5"},
			want: "anthropic/claude-haiku-5-5",
		},
		{
			name:    "skipped fallback reports the skip",
			cd:      &compactionBannerData{finished: true, skipped: true, compactionModel: "anthropic/claude-haiku-5-5", compactionModelAlias: "haiku", fallbackStatus: output.CompactionFallbackSkipped, fallbackReason: "fallback failed: boom"},
			want:    "fallback skipped",
			notWant: "haiku",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBuffer(t)
			header := stripANSI(b.compactionBoxRows(tt.cd, 120)[0])
			if !strings.Contains(header, tt.want) {
				t.Errorf("header = %q, want it to contain %q", header, tt.want)
			}
			if tt.notWant != "" && strings.Contains(header, tt.notWant) {
				t.Errorf("header = %q, want it to omit %q", header, tt.notWant)
			}
		})
	}
}

func TestCompactionBannerClickTogglesExpanded(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "test-model", ModelContexts: map[string]int{"test-model": 100000}}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m.content.segments = append(m.content.segments, contentSegment{
		kind:           segmentCompactionBanner,
		compactionData: &compactionBannerData{finished: true, elapsed: "3s"},
		renderDirty:    true,
	})
	m.syncViewport()

	last := func() *contentSegment { return &m.content.segments[len(m.content.segments)-1] }
	if last().compactionData.expanded {
		t.Fatal("compaction banner starts expanded, want collapsed")
	}
	m.handleSegmentClick(last(), 0)
	if !last().compactionData.expanded {
		t.Error("click did not expand the compaction banner")
	}
	m.handleSegmentClick(last(), 0)
	if last().compactionData.expanded {
		t.Error("second click did not collapse the compaction banner")
	}
}

// TestCompactionHeaderGlyphFlushToRightBorder pins the header's right-aligned
// meta against the box padding: the content area is the box width minus the 2
// border cells and the 1-cell padding each side, so the glyph must sit directly
// against the right padding with no dead gap.
func TestCompactionHeaderGlyphFlushToRightBorder(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cd := &compactionBannerData{
		finished:        true,
		elapsed:         "3s",
		compactionCount: 2,
		inputTokens:     100,
		cacheReadTokens: 90,
	}

	const width = 80
	lines := strings.Split(stripANSI(b.renderCompactionBanner(cd, width)), "\n")
	if len(lines) < 2 {
		t.Fatalf("banner has %d lines, want a header row: %q", len(lines), lines)
	}
	header := lines[1]
	if got := lipgloss.Width(header); got != width {
		t.Errorf("header width = %d, want %d: %q", got, width, header)
	}
	if !strings.HasSuffix(header, "✓ │") {
		t.Errorf("header = %q, want the glyph flush against the right padding", header)
	}
}

func TestCompactionBoxUsesNilGuard(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	seg := contentSegment{compactionData: nil}
	out := b.renderCompactionBannerSegment(seg, 80)
	if out != "" {
		t.Errorf("nil compactionData should produce empty string, got %q", out)
	}
}

func TestRenderDelegationHeaderAdvisor(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	header := b.renderDelegationHeader(&delegationDisplayState{
		isAdvisor:      true,
		toolLabel:      "advisor",
		status:         "budget_exhausted",
		modelName:      "advisor-model",
		advisorUse:     2,
		advisorMaxUses: 2,
		collapsed:      true,
	}, 80)

	if !strings.Contains(header, "advisor") {
		t.Fatalf("header = %q, want advisor label", header)
	}
	if strings.Contains(header, "pending") {
		t.Fatalf("header = %q, must not show pending agent id", header)
	}
	if !strings.Contains(header, "budget exhausted") {
		t.Fatalf("header = %q, want budget status", header)
	}
}

func TestDelegationRowsStylePromptBodyDifferentlyAndInsertSeparator(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	useTrueColor(t)
	dd := &delegationDisplayState{
		promptText:      "same text",
		promptCollapsed: false,
		entries: []delegationTranscriptEntry{
			{kind: delegationTranscriptEntryAssistant, body: "same text"},
		},
	}

	rows := b.delegationRows(dd, 80)
	promptIndex := -1
	transcriptIndex := -1
	separatorIndex := -1
	for i, row := range rows {
		switch row.kind {
		case delegationRowPromptBody:
			if promptIndex < 0 {
				promptIndex = i
			}
		case delegationRowTranscript:
			if transcriptIndex < 0 {
				transcriptIndex = i
			}
		case delegationRowSeparator:
			separatorIndex = i
		}
	}

	if promptIndex < 0 {
		t.Fatalf("rows = %#v, want prompt body row", rows)
	}
	if transcriptIndex < 0 {
		t.Fatalf("rows = %#v, want transcript row", rows)
	}
	if separatorIndex < 0 {
		t.Fatalf("rows = %#v, want separator row", rows)
	}
	if promptIndex >= separatorIndex || separatorIndex >= transcriptIndex {
		t.Fatalf("row order = prompt:%d separator:%d transcript:%d, want prompt < separator < transcript", promptIndex, separatorIndex, transcriptIndex)
	}
	if got, want := stripANSI(rows[promptIndex].text), stripANSI(rows[transcriptIndex].text); got != want {
		t.Fatalf("prompt body text = %q, transcript text = %q, want same plain text", got, want)
	}
	if !strings.Contains(rows[promptIndex].text, "\x1b[3;") {
		t.Fatalf("prompt body row = %q, want italic ANSI", rows[promptIndex].text)
	}
	if strings.Contains(rows[transcriptIndex].text, "\x1b[3;") {
		t.Fatalf("transcript row = %q, want non-italic ANSI", rows[transcriptIndex].text)
	}
}

func TestDelegationRowsOmitSeparatorWhenCollapsed(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	cases := []struct {
		name string
		dd   *delegationDisplayState
	}{
		{
			name: "prompt collapsed",
			dd: &delegationDisplayState{
				promptText:      "same text",
				promptCollapsed: true,
				entries: []delegationTranscriptEntry{
					{kind: delegationTranscriptEntryAssistant, body: "same text"},
				},
			},
		},
		{
			name: "delegation collapsed",
			dd: &delegationDisplayState{
				promptText:      "same text",
				promptCollapsed: false,
				collapsed:       true,
				entries: []delegationTranscriptEntry{
					{kind: delegationTranscriptEntryAssistant, body: "same text"},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := b.delegationRows(tc.dd, 80)
			for _, row := range rows {
				if row.kind == delegationRowSeparator {
					t.Fatalf("rows = %#v, want no separator row", rows)
				}
			}
		})
	}
}

func TestDelegationRowsAdvisorQuestionAndFiles(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	dd := &delegationDisplayState{
		isAdvisor:       true,
		advisorQuestion: "split the switch?",
		advisorFiles:    []string{"a.go", "b.go"},
		collapsed:       false,
	}

	rows := b.delegationRows(dd, 80)

	headerIdx, qHeaderIdx, qBodyIdx, fHeaderIdx, fBodyIdx := -1, -1, -1, -1, -1
	for i, row := range rows {
		switch row.kind {
		case delegationRowHeader:
			headerIdx = i
		case delegationRowQuestionHeader:
			qHeaderIdx = i
		case delegationRowQuestionBody:
			qBodyIdx = i
		case delegationRowFilesHeader:
			fHeaderIdx = i
		case delegationRowFilesBody:
			fBodyIdx = i
		}
	}
	if headerIdx < 0 || qHeaderIdx < 0 || qBodyIdx < 0 || fHeaderIdx < 0 || fBodyIdx < 0 {
		t.Fatalf("rows = %#v, want header, question header/body, files header/body rows", rows)
	}
	if headerIdx >= qHeaderIdx || qHeaderIdx >= qBodyIdx || qBodyIdx >= fHeaderIdx || fHeaderIdx >= fBodyIdx {
		t.Fatalf("row order = header:%d questionHeader:%d questionBody:%d filesHeader:%d filesBody:%d, want header < question < files", headerIdx, qHeaderIdx, qBodyIdx, fHeaderIdx, fBodyIdx)
	}

	var plain []string
	for _, row := range rows {
		plain = append(plain, stripANSI(row.text))
	}
	joined := strings.Join(plain, "\n")
	if !strings.Contains(joined, "split the switch?") {
		t.Fatalf("rows missing question text:\n%s", joined)
	}
	for _, path := range []string{"a.go", "b.go"} {
		if !strings.Contains(joined, "• "+path) {
			t.Fatalf("rows missing file path %q:\n%s", path, joined)
		}
	}
}

func TestDelegationRowsAdvisorCollapsedHeaderShowsQuestionAndFileCount(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	dd := &delegationDisplayState{
		isAdvisor:       true,
		advisorQuestion: "split the switch?",
		advisorFiles:    []string{"a.go", "b.go"},
		collapsed:       true,
	}

	rows := b.delegationRows(dd, 80)
	var header string
	for _, row := range rows {
		if row.kind == delegationRowHeader {
			header = stripANSI(row.text)
		}
		switch row.kind {
		case delegationRowQuestionHeader, delegationRowQuestionBody, delegationRowFilesHeader, delegationRowFilesBody:
			t.Fatalf("collapsed rows = %#v, want no question/files body rows", rows)
		}
	}
	if !strings.Contains(header, "split the switch?") {
		t.Fatalf("header = %q, want question text", header)
	}
	if !strings.Contains(header, "· 2 files") {
		t.Fatalf("header = %q, want file count", header)
	}
}

func TestRenderClosingSeparatorHasBlankLineMargin(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.appendLabeledBlock("Compaction", "summary text")

	out := b.String(80)
	plain := stripANSI(out)

	// The closing delimiter should be preceded by a blank line.
	lines := strings.Split(plain, "\n")
	var bodyIndex int
	for i, line := range lines {
		if strings.TrimSpace(line) == "summary text" {
			bodyIndex = i
			break
		}
	}
	if bodyIndex == 0 {
		t.Fatalf("body line not found in output:\n%s", plain)
	}
	if bodyIndex+1 >= len(lines) || strings.TrimSpace(lines[bodyIndex+1]) != "" {
		t.Errorf("expected blank line immediately after body, got %q:\n%s", lines[bodyIndex+1], plain)
	}
	closingIndex := -1
	for i := bodyIndex + 2; i < len(lines); i++ {
		if strings.Contains(lines[i], "End of Compaction") {
			closingIndex = i
			break
		}
	}
	if closingIndex != bodyIndex+2 {
		t.Errorf("closing delimiter index = %d, want %d:\n%s", closingIndex, bodyIndex+2, plain)
	}
}
