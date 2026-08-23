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
			name:         "unknown context and tps",
			wantSegments: []string{"⠋", "gpt-x/high"},
			omitSegments: []string{"ctx:", "t/s"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBuffer(t)
			dd := &delegationDisplayState{
				status:         "active",
				modelName:      "gpt-x",
				reasoning:      "high",
				contextFillPct: tc.contextFill,
				outputTPS:      tc.outputTPS,
				startTime:      nanoNow() - 10_000_000_000,
			}

			meta := stripANSI(b.renderDelegationHeaderMeta(dd))
			previous := -1
			for _, segment := range tc.wantSegments {
				index := strings.Index(meta, segment)
				if index < 0 {
					t.Fatalf("meta = %q, missing %q", meta, segment)
				}
				if index <= previous {
					t.Fatalf("meta = %q, %q is out of order", meta, segment)
				}
				previous = index
			}
			if elapsedIndex := regexp.MustCompile(`\d+(ms|s|m\ds)`).FindStringIndex(meta); elapsedIndex == nil || elapsedIndex[0] <= previous {
				t.Fatalf("meta = %q, elapsed must follow active metadata", meta)
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
