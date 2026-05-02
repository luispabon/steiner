package agent

import (
	"strings"
	"testing"
)

func TestContextStateRenderIncludesScaffoldSummaries(t *testing.T) {
	state := ContextState{
		FileTrackerSummary: []string{"README.md lines 1-40/120"},
		RecentToolCalls:    []string{"read path=README.md"},
		TurnCount:          4,
		CompactionCount:    1,
	}

	rendered := state.Render()
	for _, want := range []string{
		"session state: turn=4 compactions=1",
		"tracked files: README.md lines 1-40/120",
		"recent tool calls: read path=README.md",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() = %q, want substring %q", rendered, want)
		}
	}
}
