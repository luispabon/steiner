package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func newQueuedTestBuffer() *contentBuffer {
	return &contentBuffer{
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		activeDelegations: make(map[string]delegationLocator),
		styles:            testStyles(theme.AccentAmber),
	}
}

func TestToolCallQueuedBindsOnStartWithoutDuplicate(t *testing.T) {
	t.Parallel()
	buffer := newQueuedTestBuffer()
	args := map[string]any{"type": "code", "task": "inspect"}

	buffer.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "call_1", args))
	if len(buffer.segments) != 1 || buffer.segments[0].delegData == nil {
		t.Fatalf("segments = %d, want one delegation box", len(buffer.segments))
	}
	dd := buffer.segments[0].delegData
	if !dd.queuedForSlot || dd.status != "active" {
		t.Fatalf("queued box state = queuedForSlot %v status %q, want true/active", dd.queuedForSlot, dd.status)
	}
	if _, ok := buffer.queuedDelegations["call_1"]; !ok {
		t.Fatal("call_1 not recorded as queued")
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call_1", args))
	if len(buffer.segments) != 1 {
		t.Fatalf("segments = %d after start, want 1 (no duplicate box)", len(buffer.segments))
	}
	if dd.queuedForSlot {
		t.Fatal("queuedForSlot = true after start, want false")
	}
	if dd.startTime == 0 {
		t.Fatal("startTime = 0 after start, want set")
	}
	if _, ok := buffer.queuedDelegations["call_1"]; ok {
		t.Fatal("call_1 still queued after start")
	}
}

func TestToolCallQueuedCancellationFinishesBox(t *testing.T) {
	t.Parallel()
	buffer := newQueuedTestBuffer()
	args := map[string]any{"type": "code", "task": "inspect"}

	buffer.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "call_1", args))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "sub_agent", "call_1", "", errors.New("tool not dispatched")))

	dd := buffer.segments[0].delegData
	if dd.status != "failed" {
		t.Fatalf("status = %q, want failed after undispatched finish", dd.status)
	}
	if _, ok := buffer.queuedDelegations["call_1"]; ok {
		t.Fatal("call_1 still queued after finished event, want entry removed")
	}
	if op := buffer.renderDelegationHeaderOperation(dd, 80); strings.Contains(op, "Waiting for slot") {
		t.Fatalf("operation = %q, want no waiting text once failed", op)
	}
}

func TestQueuedDelegationHeaderRendering(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	dd := &delegationDisplayState{status: "active", queuedForSlot: true}

	status, width := buffer.renderDelegationHeaderStatus(dd)
	if !strings.Contains(status, "⧖") {
		t.Fatalf("status = %q, want hourglass", status)
	}
	if width != 1 {
		t.Fatalf("status width = %d, want 1", width)
	}
	if op := buffer.renderDelegationHeaderOperation(dd, 80); !strings.Contains(op, "Waiting for slot") {
		t.Fatalf("operation = %q, want Waiting for slot", op)
	}
}

func TestToolCallQueuedClearResetsState(t *testing.T) {
	t.Parallel()
	buffer := newQueuedTestBuffer()
	buffer.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "call_1", map[string]any{"type": "code"}))
	if len(buffer.queuedDelegations) == 0 {
		t.Fatal("queuedDelegations empty after queued event, want one entry")
	}
	buffer.Clear()
	if buffer.queuedDelegations != nil {
		t.Fatalf("queuedDelegations = %#v after Clear, want nil", buffer.queuedDelegations)
	}
}

func TestToolCallQueuedEmptyCallIDIgnored(t *testing.T) {
	t.Parallel()
	buffer := newQueuedTestBuffer()
	buffer.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "", map[string]any{"type": "code"}))
	if len(buffer.segments) != 0 {
		t.Fatalf("segments = %d, want 0 (empty call ID must not create a box)", len(buffer.segments))
	}
	if len(buffer.queuedDelegations) != 0 {
		t.Fatalf("queuedDelegations = %#v, want empty", buffer.queuedDelegations)
	}
}
