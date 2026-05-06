package agent

import (
	"strings"
	"testing"
)

func TestContextStateRenderIncludesScaffoldSummaries(t *testing.T) {
	state := ContextState{
		TurnCount:       4,
		CompactionCount: 1,
	}

	rendered := state.Render()
	for _, want := range []string{
		"session state: turn=4 compactions=1",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Render() = %q, want substring %q", rendered, want)
		}
	}
}
