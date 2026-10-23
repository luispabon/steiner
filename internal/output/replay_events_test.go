package output

import "testing"

func TestReplayBoundaryEventsRenderNoText(t *testing.T) {
	t.Parallel()
	for _, event := range []Event{NewReplayStartedEvent(), NewReplayFinishedEvent()} {
		if got := FormatEvent(event); got != "" {
			t.Errorf("FormatEvent(%s) = %q, want empty", event.Type, got)
		}
	}
}
