package tui

import "testing"

func TestParseInputHandlesContextCommand(t *testing.T) {
	action := parseInput("/context", nil)
	if !action.handled {
		t.Fatal("handled = false, want true")
	}
	if !action.inspectContext {
		t.Fatal("inspectContext = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesContext(t *testing.T) {
	got := buildCompletionCandidates("/co", nil, nil)
	if len(got) != 1 || got[0] != "/context" {
		t.Fatalf("candidates = %#v, want /context", got)
	}
}
