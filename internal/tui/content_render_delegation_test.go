package tui

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDelegationCompleteMetaIncludesOnlyStatusDurationAndCache(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus:   "complete",
		turnCount:      4,
		toolCallCount:  12,
		tokenCount:     8123,
		elapsed:        "12.4s",
		cacheHitRate:   0.952,
		cacheHitOK:     true,
		advisorUse:     1,
		advisorMaxUses: 2,
	}

	got := delegationCompleteMeta(dd)
	want := []string{"complete", "cache 95.2%", "12.4s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delegationCompleteMeta() = %v, want %v", got, want)
	}
}

func TestDelegationCacheWaitingHeaderRendering(t *testing.T) {
	buffer := &contentBuffer{styles: testStyles("#5599ff")}
	dd := &delegationDisplayState{
		status:            "active",
		cacheWaiting:      true,
		cacheWaitDeadline: nanoNow() + 9_600_000_000,
	}

	status, width := buffer.renderDelegationHeaderStatus(dd)
	if !strings.Contains(status, "⧖") {
		t.Fatalf("status = %q, want hourglass", status)
	}
	if width != 1 {
		t.Fatalf("status width = %d, want 1", width)
	}
	meta := buffer.renderDelegationHeaderMeta(dd)
	if !regexp.MustCompile(`\d+\.\ds`).MatchString(meta) {
		t.Fatalf("meta = %q, want countdown", meta)
	}
	if operation := buffer.renderDelegationHeaderOperation(dd, 80); !strings.Contains(operation, "waiting for cache warm-up…") {
		t.Fatalf("operation = %q, want cache warm-up text", operation)
	}

	dd.cacheWaiting = false
	dd.startTime = time.Now().Add(-2 * time.Second).UnixNano()
	status, _ = buffer.renderDelegationHeaderStatus(dd)
	if strings.Contains(status, "⧖") {
		t.Fatalf("status = %q after clear, still contains hourglass", status)
	}
	if !regexp.MustCompile(`\d+(ms|s|m\ds)`).MatchString(buffer.renderDelegationHeaderMeta(dd)) {
		t.Fatalf("meta after clear = %q, want elapsed", buffer.renderDelegationHeaderMeta(dd))
	}
}

func TestDelegationHeaderOperationNormalizesWhitespace(t *testing.T) {

	tests := []struct {
		name       string
		delegation delegationDisplayState
		width      int
		want       string
	}{
		{
			name: "advisor question",
			delegation: delegationDisplayState{
				isAdvisor:       true,
				advisorQuestion: "review\nthese\r\nfiles\rplease",
			},
			width: 80,
			want:  "review these files please",
		},
		{
			name: "specialist task preview fallback",
			delegation: delegationDisplayState{
				status:      "active",
				taskPreview: "inspect\nthis\tmodule",
			},
			width: 80,
			want:  "inspect this module",
		},
		{
			name: "literal backslash n preserved",
			delegation: delegationDisplayState{
				isAdvisor:       true,
				advisorQuestion: `keep literal\ntext`,
			},
			width: 80,
			want:  `keep literal\ntext`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBuffer(t)
			got := stripANSI(b.renderDelegationHeaderOperation(&tt.delegation, tt.width))
			if got != tt.want {
				t.Fatalf("header operation = %q, want %q", got, tt.want)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("header operation = %q, contains a real line break", got)
			}
		})
	}
}

func TestDelegationHeaderOperationTruncatesAfterWhitespaceNormalization(t *testing.T) {

	b := newTestBuffer(t)
	dd := &delegationDisplayState{
		isAdvisor:       true,
		advisorQuestion: "alpha\nbeta gamma",
	}
	got := stripANSI(b.renderDelegationHeaderOperation(dd, 12))
	if got != "alpha beta …" {
		t.Fatalf("header operation = %q, want %q", got, "alpha beta …")
	}
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header operation = %q, contains a real line break", got)
	}
}

func TestDelegationRowsAdvisorMultilineQuestionKeepsHeaderSingleLine(t *testing.T) {
	b := newTestBuffer(t)
	dd := &delegationDisplayState{
		isAdvisor:       true,
		advisorQuestion: "check\nthese\r\nchanges",
		collapsed:       true,
	}

	rows := b.delegationRows(dd, 80)
	var header string
	for _, row := range rows {
		if row.kind == delegationRowHeader {
			header = stripANSI(row.text)
			break
		}
	}
	if header == "" {
		t.Fatal("delegation rows contain no header")
	}
	if strings.ContainsAny(header, "\r\n") {
		t.Fatalf("header = %q, contains a real line break", header)
	}
	if !strings.Contains(header, "check these changes") {
		t.Fatalf("header = %q, want normalized advisor question", header)
	}
}

func TestDelegationCompleteMetaOmitsCacheHitRateWhenNotOK(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus: "complete",
		elapsed:      "12.4s",
		cacheHitOK:   false,
	}

	got := delegationCompleteMeta(dd)
	want := []string{"complete", "12.4s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delegationCompleteMeta() = %v, want %v", got, want)
	}
}

func TestDelegationActiveHeaderMetaPlacesModelBeforeElapsed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		contextFill  float64
		outputTPS    float64
		wantSegments []string
		omitSegments []string
	}{
		{
			name:         "all active metadata",
			contextFill:  5.4,
			outputTPS:    42.1,
			wantSegments: []string{"⠋", "ctx: 5%", "42.1 t/s", "gpt-x/high"},
		},
		{
			name:         "context without tps",
			contextFill:  5.4,
			wantSegments: []string{"⠋", "ctx: 5%", "gpt-x/high"},
			omitSegments: []string{"t/s"},
		},
		{
			name:         "tps without context",
			outputTPS:    42.1,
			wantSegments: []string{"⠋", "42.1 t/s", "gpt-x/high"},
			omitSegments: []string{"ctx:"},
		},
		{
			name:         "unknown context and tps",
			wantSegments: []string{"⠋", "gpt-x/high"},
			omitSegments: []string{"ctx:", "t/s"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBuffer(t)
			now := nanoNow()
			dd := &delegationDisplayState{
				status:         "active",
				modelName:      "gpt-x",
				reasoning:      "high",
				contextFillPct: tc.contextFill,
				outputTPS:      tc.outputTPS,
				startTime:      now - 10_900_000_000,
			}

			wantParts := append([]string(nil), tc.wantSegments...)
			wantParts = append(wantParts, formatElapsed(dd.startTime, now))
			want := wantParts[0] + " " + strings.Join(wantParts[1:], " · ")
			meta := stripANSI(b.renderDelegationHeaderMeta(dd))
			if meta != want {
				t.Fatalf("meta = %q, want %q", meta, want)
			}
			modelIndex := strings.Index(meta, "gpt-x/high")
			elapsedIndex := strings.Index(meta, wantParts[len(wantParts)-1])
			if modelIndex < 0 || elapsedIndex <= modelIndex {
				t.Fatalf("meta = %q, model must precede elapsed", meta)
			}
			for _, segment := range tc.omitSegments {
				if strings.Contains(meta, segment) {
					t.Errorf("meta = %q, contains omitted segment %q", meta, segment)
				}
			}
		})
	}
}

func TestDelegationCompleteMetaOrdersModelCacheAndElapsed(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus: "complete",
		modelName:    "gpt-x",
		reasoning:    "high",
		cacheHitRate: 0.952,
		cacheHitOK:   true,
		elapsed:      "12.4s",
	}

	got := delegationCompleteMeta(dd)
	want := []string{"complete", "gpt-x/high", "cache 95.2%", "12.4s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delegationCompleteMeta() = %v, want %v", got, want)
	}
}

func TestDelegationBudgetMetaOrdersModelBeforeAdvisorUse(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		isAdvisor:      true,
		modelName:      "gpt-x",
		reasoning:      "high",
		advisorUse:     1,
		advisorMaxUses: 2,
	}

	got := delegationBudgetMeta(dd)
	want := []string{"budget exhausted", "gpt-x/high", "1/2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delegationBudgetMeta() = %v, want %v", got, want)
	}
}

func TestDelegationFailedMetaOrdersModelBeforeElapsed(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		modelName: "gpt-x",
		reasoning: "high",
		elapsed:   "12.4s",
	}

	got := delegationFailedMeta(dd)
	want := []string{"failed", "gpt-x/high", "12.4s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delegationFailedMeta() = %v, want %v", got, want)
	}
}

func TestDelegationHeaderMetaOmitsModelWhenModelNameEmpty(t *testing.T) {
	b := newTestBuffer(t)
	dd := &delegationDisplayState{
		status:    "active",
		reasoning: "high",
		startTime: nanoNow() - 10_000_000_000,
	}

	meta := b.renderDelegationHeaderMeta(dd)
	if strings.Contains(meta, "high") || strings.Contains(meta, "/high") {
		t.Fatalf("meta = %q, contains model/effort without model", meta)
	}
}

func TestFormatTokenPair(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		input, output int
		want          string
	}{
		{name: "zero", input: 0, output: 0, want: "0 in / 0 out"},
		{name: "zero input", input: 0, output: 15000, want: "0 in / 15k out"},
		{name: "compact thousands", input: 15000, output: 15900, want: "15k in / 16k out"},
		{name: "compact millions", input: 2_000_000, output: 1234, want: "2.0m in / 1.2k out"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatTokenPair(tc.input, tc.output); got != tc.want {
				t.Errorf("formatTokenPair(%d, %d) = %q, want %q", tc.input, tc.output, got, tc.want)
			}
		})
	}
}

func TestRenderDelegationGroupSegmentRendersBothEntriesWithDivider(t *testing.T) {
	useTrueColor(t)
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles("#5599ff"),
	}

	// Create a group segment with 2 entries
	dd1 := &delegationDisplayState{
		agentID:       "child-1",
		toolLabel:     "code",
		taskPreview:   "first task",
		status:        "complete",
		collapsed:     true,
		resultStatus:  "complete",
		turnCount:     1,
		tokenCount:    100,
		toolCallCount: 0,
		elapsed:       "1.0s",
	}
	dd2 := &delegationDisplayState{
		agentID:       "child-2",
		toolLabel:     "code",
		taskPreview:   "second task",
		status:        "complete",
		collapsed:     true,
		resultStatus:  "complete",
		turnCount:     1,
		tokenCount:    100,
		toolCallCount: 0,
		elapsed:       "1.0s",
	}

	group := &delegationGroupSegment{
		entries: []*delegationDisplayState{dd1, dd2},
	}

	seg := contentSegment{
		kind:           segmentDelegationGroup,
		delegGroupData: group,
	}

	rendered := buffer.renderDelegationGroupSegment(seg, 50)
	if rendered == "" {
		t.Fatalf("rendered output is empty")
	}

	// Should contain both agent IDs
	if !strings.Contains(rendered, "child-1") {
		t.Errorf("rendered output missing first agent: %q", rendered)
	}
	if !strings.Contains(rendered, "child-2") {
		t.Errorf("rendered output missing second agent: %q", rendered)
	}

	// Should have exactly one top border and one bottom border (wrapped once)
	lines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(lines) < 4 {
		t.Errorf("rendered lines = %d, want at least 4 (top, entry1, divider, entry2, bottom)", len(lines))
	}
}

func TestRenderDelegationGroupSegmentWithMixedLabelUsesDefaultBorder(t *testing.T) {
	useTrueColor(t)
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
		styles:        testStyles("#5599ff"),
	}

	// Create entries with different toolLabels
	dd1 := &delegationDisplayState{
		agentID:     "child-1",
		toolLabel:   "explore",
		taskPreview: "first",
		status:      "complete",
		collapsed:   true,
	}
	dd2 := &delegationDisplayState{
		agentID:     "child-2",
		toolLabel:   "implement",
		taskPreview: "second",
		status:      "complete",
		collapsed:   true,
	}

	group := &delegationGroupSegment{
		entries: []*delegationDisplayState{dd1, dd2},
	}

	seg := contentSegment{
		kind:           segmentDelegationGroup,
		delegGroupData: group,
	}

	// The delegationGroupBorderLabel function should return "" for mixed labels
	label := delegationGroupBorderLabel(group)
	if label != "" {
		t.Fatalf("mixed label group returned %q, want empty string", label)
	}

	// Render and verify it uses the default border style
	rendered := buffer.renderDelegationGroupSegment(seg, 50)
	if rendered == "" {
		t.Fatalf("rendered output is empty")
	}
	// Just verify it renders without error and contains entries
	if !strings.Contains(rendered, "child-1") || !strings.Contains(rendered, "child-2") {
		t.Errorf("rendered output missing entries: %q", rendered)
	}
}

func TestRenderDelegationBriefBodyIncludesAllFields(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)
	dd := &delegationDisplayState{
		briefObjective:       "implement feature",
		briefContext:         "background info",
		briefDeliverable:     "working code",
		briefConstraints:     []string{"no breaking changes", "use Go 1.25"},
		briefSuccessCriteria: []string{"tests pass"},
		briefChecks:          []string{"go test ./...", "go vet ./..."},
	}

	lines := buffer.renderDelegationBriefBody(dd, 80)
	if len(lines) == 0 {
		t.Fatal("renderDelegationBriefBody returned no lines")
	}

	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "objective") {
		t.Error("brief body missing 'objective' label")
	}
	if !strings.Contains(text, "context") {
		t.Error("brief body missing 'context' label")
	}
	if !strings.Contains(text, "deliverable") {
		t.Error("brief body missing 'deliverable' label")
	}
	if !strings.Contains(text, "constraints") {
		t.Error("brief body missing 'constraints' label")
	}
	if !strings.Contains(text, "success criteria") {
		t.Error("brief body missing 'success criteria' label")
	}
	if !strings.Contains(text, "checks") {
		t.Error("brief body missing 'checks' label")
	}
	if !strings.Contains(text, "no breaking changes") {
		t.Error("brief body missing constraint text")
	}
	if !strings.Contains(text, "tests pass") {
		t.Error("brief body missing success criteria text")
	}
}

func TestRenderDelegationBriefBodyOmitsEmptyFields(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)
	dd := &delegationDisplayState{
		briefObjective:   "task",
		briefContext:     "",
		briefDeliverable: "result",
		// empty slices are omitted
	}

	lines := buffer.renderDelegationBriefBody(dd, 80)
	text := strings.Join(lines, "\n")

	if strings.Contains(text, "context") {
		t.Error("brief body should not include empty context label")
	}
	if !strings.Contains(text, "objective") {
		t.Error("brief body should include objective")
	}
	if !strings.Contains(text, "deliverable") {
		t.Error("brief body should include deliverable")
	}
}

func TestAdvisorStatsOnlyInFooterWhenTerminal(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)

	// Completed non-advisor delegation with advisor budget
	dd := &delegationDisplayState{
		isAdvisor:       false,
		status:          "complete",
		advisorBudget:   2,
		advisorUses:     1,
		advisorDenied:   0,
		modelName:       "gpt-x",
		turnCount:       1,
	}

	// Check header meta does NOT contain "advisor"
	headerMeta := buffer.renderDelegationHeaderMeta(dd)
	if strings.Contains(headerMeta, "advisor") {
		t.Errorf("header meta should not contain 'advisor', got %q", headerMeta)
	}

	// Check footer stats DO contain "Advisor:"
	stats := delegationStatsParts(buffer, dd)
	found := false
	for _, stat := range stats {
		if strings.Contains(stat, "Advisor:") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("footer stats should contain 'Advisor:', got %v", stats)
	}
}

func TestAdvisorStatsNotInFooterWhenActive(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)

	// Active non-advisor delegation with advisor budget
	dd := &delegationDisplayState{
		isAdvisor:     false,
		status:        "active",
		advisorBudget: 2,
		advisorUses:   1,
		advisorDenied: 0,
	}

	// Check footer stats do NOT yet contain "Advisor:"
	stats := delegationStatsParts(buffer, dd)
	for _, stat := range stats {
		if strings.Contains(stat, "Advisor:") {
			t.Errorf("active delegation footer should not contain 'Advisor:', got %v", stats)
		}
	}
}

func TestNonAdvisorBoxDoesNotRenderQuestionFiles(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)

	// Non-advisor delegation with advisor question/files (should be ignored)
	dd := &delegationDisplayState{
		isAdvisor:         false,
		collapsed:         false,
		advisorBudget:     2,
		advisorQuestion:   "should not render",
		advisorFiles:      []string{"file1.go", "file2.go"},
	}

	rows, _ := buffer.delegationSectionRows(dd, 80)

	// Check that no question/files rows are rendered
	for _, row := range rows {
		if strings.Contains(row.text, "question") {
			t.Errorf("non-advisor box should not render question header")
		}
		if strings.Contains(row.text, "files") {
			t.Errorf("non-advisor box should not render files header")
		}
		if strings.Contains(row.text, "should not render") {
			t.Errorf("non-advisor box should not render advisor question text")
		}
		if strings.Contains(row.text, "file1.go") {
			t.Errorf("non-advisor box should not render advisor files")
		}
	}
}

func TestAdvisorBoxRendersQuestionFiles(t *testing.T) {
	t.Parallel()
	buffer := newTestBuffer(t)

	// Advisor delegation (should render question/files)
	dd := &delegationDisplayState{
		isAdvisor:       true,
		collapsed:       false,
		advisorQuestion: "what to check?",
		advisorFiles:    []string{"file1.go"},
	}

	rows, _ := buffer.delegationSectionRows(dd, 80)

	// Check that question/files rows ARE rendered for advisor
	questionFound := false
	filesFound := false
	for _, row := range rows {
		if strings.Contains(row.text, "question") {
			questionFound = true
		}
		if strings.Contains(row.text, "files") {
			filesFound = true
		}
	}
	if !questionFound {
		t.Error("advisor box should render question header")
	}
	if !filesFound {
		t.Error("advisor box should render files header")
	}
}
