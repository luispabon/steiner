package agent

import "testing"

func TestContextStateCloneRetainsSummariesWithoutSharingSlices(t *testing.T) {
	state := ContextState{
		RetainedSummaries: []RetainedSummary{
			{Title: "summary", Text: "keep this", Source: "compactor", Turn: 4},
		},
		FileTrackerSummary: []string{"tracked file"},
		RecentToolCalls:    []string{"read"},
		TurnCount:          4,
		CompactionCount:    1,
	}

	cloned := state.Clone()
	if got, want := len(cloned.RetainedSummaries), 1; got != want {
		t.Fatalf("cloned retained summaries = %d, want %d", got, want)
	}
	if got, want := cloned.RetainedSummaries[0].Text, "keep this"; got != want {
		t.Fatalf("cloned retained summary text = %q, want %q", got, want)
	}

	cloned.RetainedSummaries[0].Text = "changed"
	cloned.FileTrackerSummary[0] = "changed"
	cloned.RecentToolCalls[0] = "mutate"

	if got, want := state.RetainedSummaries[0].Text, "keep this"; got != want {
		t.Fatalf("original retained summary text = %q, want %q", got, want)
	}
	if got, want := state.FileTrackerSummary[0], "tracked file"; got != want {
		t.Fatalf("original file tracker summary = %q, want %q", got, want)
	}
	if got, want := state.RecentToolCalls[0], "read"; got != want {
		t.Fatalf("original recent tool call = %q, want %q", got, want)
	}
}
