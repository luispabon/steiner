package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestAppendEventReplayBoundariesAddNoSegments(t *testing.T) {
	t.Parallel()
	buffer := scopedChildBuffer()

	buffer.AppendEvent(output.NewReplayStartedEvent())
	buffer.AppendEvent(output.NewReplayFinishedEvent())

	if got := len(buffer.segments); got != 0 {
		t.Errorf("segments = %d, want 0 for replay boundary events", got)
	}
}
