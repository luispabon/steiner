package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDelegationCompleteMetaIncludesCacheHitRateWhenOK(t *testing.T) {
	t.Parallel()
	dd := &delegationDisplayState{
		resultStatus:  "complete",
		turnCount:     4,
		toolCallCount: 12,
		tokenCount:    8123,
		elapsed:       "12.4s",
		cacheHitRate:  0.952,
		cacheHitOK:    true,
	}

	meta := delegationCompleteMeta(dd)

	if len(meta) == 0 || meta[len(meta)-1] != "cache 95.2%" {
		t.Errorf("delegationCompleteMeta() = %v, want last entry %q", meta, "cache 95.2%")
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
		resultStatus:  "complete",
		turnCount:     4,
		toolCallCount: 12,
		tokenCount:    8123,
		elapsed:       "12.4s",
		cacheHitOK:    false,
	}

	meta := delegationCompleteMeta(dd)

	for _, part := range meta {
		if part == "cache 95.2%" || part == "cache 0.0%" {
			t.Errorf("delegationCompleteMeta() = %v, want no cache entry when cacheHitOK is false", meta)
		}
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
