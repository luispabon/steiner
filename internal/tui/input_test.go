package tui

import "testing"

func TestParseInputHandlesContextCommand(t *testing.T) {
	action := parseInput("/context", nil)
	if !action.inspectContext {
		t.Fatal("inspectContext = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesContext(t *testing.T) {
	got := buildCompletionCandidates("/co", nil, nil)
	if len(got) != 2 {
		t.Fatalf("candidates = %#v, want 2 candidates", got)
	}
	if got[0] != "/compact" || got[1] != "/context" {
		t.Fatalf("candidates = %#v, want [/compact, /context]", got)
	}
}
