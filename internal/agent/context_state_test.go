package agent

import "testing"

func TestContextStateCloneDoesNotShareSlices(t *testing.T) {
	state := ContextState{
		RecentToolCalls: []string{"read"},
		TurnCount:       4,
		CompactionCount: 1,
	}

	cloned := state.Clone()
	cloned.RecentToolCalls[0] = "mutate"

	if got, want := state.RecentToolCalls[0], "read"; got != want {
		t.Fatalf("original recent tool call = %q, want %q", got, want)
	}
}
