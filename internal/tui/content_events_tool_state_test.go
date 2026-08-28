package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestAppendAdjacentToolCallGroupsAdjacentTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		firstTool  string
		secondTool string
		wantMixed  bool
	}{
		{name: "different tools", firstTool: "read", secondTool: "bash", wantMixed: true},
		{name: "same tool", firstTool: "read", secondTool: "read", wantMixed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			first := &toolCallSegment{tool: tt.firstTool}
			second := &toolCallSegment{tool: tt.secondTool}
			b := &contentBuffer{
				segments: []contentSegment{{kind: segmentToolCall, toolData: first}},
			}

			if !b.appendAdjacentToolCall(second) {
				t.Fatal("appendAdjacentToolCall() = false, want true")
			}
			if len(b.segments) != 1 {
				t.Fatalf("segments = %d, want 1", len(b.segments))
			}
			group := b.segments[0].toolGroupData
			if b.segments[0].kind != segmentToolCallGroup || group == nil {
				t.Fatalf("segment = %#v, want tool call group", b.segments[0])
			}
			if group.mixed != tt.wantMixed {
				t.Fatalf("group.mixed = %v, want %v", group.mixed, tt.wantMixed)
			}
			if len(group.entries) != 2 || group.entries[0] != first || group.entries[1] != second {
				t.Fatalf("group.entries = %#v, want both calls in order", group.entries)
			}
		})
	}
}

func TestAppendAdjacentToolCallKeepsMixedGroupMixed(t *testing.T) {
	t.Parallel()

	first := &toolCallSegment{tool: "read"}
	second := &toolCallSegment{tool: "bash"}
	third := &toolCallSegment{tool: "mutate"}
	b := &contentBuffer{
		segments: []contentSegment{{kind: segmentToolCall, toolData: first}},
	}

	if !b.appendAdjacentToolCall(second) || !b.appendAdjacentToolCall(third) {
		t.Fatal("appendAdjacentToolCall() = false, want both calls grouped")
	}
	group := b.segments[0].toolGroupData
	if group == nil || !group.mixed {
		t.Fatalf("group = %#v, want mixed group", group)
	}
	if len(group.entries) != 3 {
		t.Fatalf("group entries = %d, want 3", len(group.entries))
	}
}

func TestRegularToolCallLifecycleTracksElapsedAndClears(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(10_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call-1", map[string]any{"path": "main.go"}))
	loc, ok := b.activeToolCalls["call-1"]
	if !ok || loc.td == nil {
		t.Fatalf("activeToolCalls[call-1] = %#v, want locator", loc)
	}
	if !loc.td.active || loc.td.startTime != now {
		t.Fatalf("tool state = %#v, want active start time %d", loc.td, now)
	}

	active := stripANSI(b.renderToolCallFrame(loc.td, 100))
	if !strings.Contains(active, spinnerFrames[0]) || !strings.Contains(active, "0ms") {
		t.Fatalf("active frame = %q, want spinner and immediate 0ms", active)
	}
	if strings.Index(active, spinnerFrames[0]) > strings.Index(active, "0ms") {
		t.Fatalf("active frame = %q, want spinner before duration", active)
	}

	beforeAdvance := b.String(100)
	now += 125_000_000
	b.AdvanceToolCallSpinners()
	if loc.td.spinnerFrame != 1 || !b.segments[loc.seg].renderDirty {
		t.Fatalf("after advance state = frame %d, dirty %v, want frame 1 and dirty", loc.td.spinnerFrame, b.segments[loc.seg].renderDirty)
	}
	updated := stripANSI(b.renderToolCallFrame(loc.td, 100))
	afterAdvance := b.String(100)
	if beforeAdvance == afterAdvance {
		t.Fatal("cached tool-call render did not change after spinner advancement")
	}
	if !strings.Contains(updated, spinnerFrames[1]) || !strings.Contains(updated, "125ms") {
		t.Fatalf("updated frame = %q, want next spinner and 125ms", updated)
	}

	now += 2_000_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call-1", "done", nil))
	if b.HasActiveToolCalls() || loc.td.active {
		t.Fatalf("active calls after finish = %v, tool active = %v, want false", b.HasActiveToolCalls(), loc.td.active)
	}
	if loc.td.meta != "✓" || loc.td.elapsed != "2s" {
		t.Fatalf("finished state = meta %q elapsed %q, want ✓ and 2s", loc.td.meta, loc.td.elapsed)
	}
	finished := stripANSI(b.renderToolCallFrame(loc.td, 100))
	if !strings.Contains(finished, "✓") || !strings.Contains(finished, "2s") {
		t.Fatalf("finished frame = %q, want icon and fixed duration", finished)
	}
	if strings.Index(finished, "✓") > strings.Index(finished, "2s") {
		t.Fatalf("finished frame = %q, want icon before duration", finished)
	}
}

func TestRegularToolCallErrorKeepsIconBeforeElapsed(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(40_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-error", map[string]any{"command": "false"}))
	now += 250_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call-error", "", errTestToolCall))

	td := b.segments[0].toolData
	if td == nil || td.meta != "✗" || td.elapsed != "250ms" {
		t.Fatalf("error state = %#v, want ✗ and 250ms", td)
	}
	got := stripANSI(b.renderToolCallFrame(td, 100))
	if !strings.Contains(got, "✗") || !strings.Contains(got, "250ms") || strings.Index(got, "✗") > strings.Index(got, "250ms") {
		t.Fatalf("error frame = %q, want ✗ before 250ms", got)
	}
}

var errTestToolCall = errors.New("tool failed")

func TestRegularToolCallGroupFinishesCallsIndependently(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(20_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-1", map[string]any{"command": "one"}))
	now += 50_000_000
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-2", map[string]any{"command": "two"}))
	group := b.segments[0].toolGroupData
	if group == nil || len(group.entries) != 2 {
		t.Fatalf("group = %#v, want two regular calls", group)
	}
	if b.activeToolCalls["call-1"].seg != 0 || b.activeToolCalls["call-2"].seg != 0 {
		t.Fatalf("active locators = %#v, want shared segment 0", b.activeToolCalls)
	}

	now += 950_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call-2", "two", nil))
	if _, ok := b.activeToolCalls["call-2"]; ok {
		t.Fatal("call-2 remains active after finish")
	}
	if _, ok := b.activeToolCalls["call-1"]; !ok {
		t.Fatal("call-1 removed when call-2 finished")
	}
	if group.entries[1].elapsed != "950ms" || !group.entries[0].active {
		t.Fatalf("group entries after call-2 finish = %#v, want only call-2 finished", group.entries)
	}

	b.Clear()
	if b.HasActiveToolCalls() || b.activeToolCalls != nil {
		t.Fatalf("activeToolCalls after Clear = %#v, want empty", b.activeToolCalls)
	}
}

func TestRegularToolCallEmptyCallIDUsesCompletionFallback(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(50_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "", map[string]any{"path": "main.go"}))
	if b.HasActiveToolCalls() {
		t.Fatal("empty CallID entered active-tool tracking")
	}
	now += 125_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "", "done", nil))

	td := b.segments[0].toolData
	if td == nil || td.meta != "✓" || td.elapsed != "125ms" || td.active {
		t.Fatalf("empty CallID completion state = %#v, want finished ✓, 125ms, inactive", td)
	}
}

func TestRegularToolCallFinishRecoversFromStaleGroupedLocator(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(60_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-1", map[string]any{"command": "one"}))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "call-2", map[string]any{"command": "two"}))
	group := b.segments[0].toolGroupData
	if group == nil {
		t.Fatal("tool calls did not form a group")
	}
	b.activeToolCalls["call-2"] = toolCallLocator{seg: 99, td: group.entries[1]}
	now += 300_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "bash", "call-2", "two", nil))

	if group.entries[1].meta != "✓" || group.entries[1].elapsed != "300ms" {
		t.Fatalf("grouped stale-locator state = %#v, want ✓ and 300ms", group.entries[1])
	}
	if group.entries[0].meta != "" || !group.entries[0].active {
		t.Fatalf("unrelated grouped call state = %#v, want active and unfinished", group.entries[0])
	}
	if _, ok := b.activeToolCalls["call-1"]; !ok {
		t.Fatal("stale-locator recovery removed unrelated active grouped call")
	}
}

func TestRegularToolCallFinishRecoversFromStaleLocator(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(30_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	td := &toolCallSegment{tool: "read", callID: "call-1", active: true, startTime: now}
	b := &contentBuffer{
		styles:          testStyles(theme.AccentAmber),
		segments:        []contentSegment{{kind: segmentToolCall, toolData: td}},
		activeToolCalls: map[string]toolCallLocator{"call-1": {seg: 99, td: td}},
	}
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call-1", "done", nil))
	if td.meta != "✓" || td.active || b.HasActiveToolCalls() {
		t.Fatalf("stale locator finish state = %#v, active calls %v, want finished and empty", td, b.activeToolCalls)
	}
}

func TestEmptyToolCallFinishSkipsTrailingDisplayFilePlaceholder(t *testing.T) {
	originalNanoNow := nanoNow
	now := int64(80_000_000_000)
	nanoNow = func() int64 { return now }
	defer func() { nanoNow = originalNanoNow }()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "", map[string]any{"path": "main.go"}))
	b.AppendEvent(output.NewDisplayFileEvent(output.DisplayFilePayload{Path: "preview.go"}))
	now += 175_000_000
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "", "read result", nil))

	read := b.segments[0].toolData
	display := b.segments[1].toolData
	if read == nil || read.body != "read result" || read.meta != "✓" || read.elapsed != "175ms" {
		t.Fatalf("read state = %#v, want result, ✓, and 175ms", read)
	}
	if display == nil || display.body != "" || display.meta != "" || display.elapsed != "" {
		t.Fatalf("display_file placeholder state = %#v, want untouched", display)
	}
}
