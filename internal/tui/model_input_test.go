package tui

import "testing"

func TestParseInputHandlesCaveHumanCommand(t *testing.T) {
	action := parseInput("/cave-human")
	if !action.caveHumanToggle {
		t.Fatal("caveHumanToggle = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesCaveHuman(t *testing.T) {
	got := buildCompletionCandidates("/cave", nil, nil)
	found := false
	for _, c := range got {
		if c == "/cave-human" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("candidates = %#v, want /cave-human included", got)
	}
}

func TestSlashOverlayItemsIncludesCaveHuman(t *testing.T) {
	m := Model{}
	items := m.buildSlashOverlayItems()
	found := false
	for _, item := range items {
		if item.command == "/cave-human" {
			found = true
			if item.name != "Toggle cave_human mode" {
				t.Fatalf("cave_human overlay name = %q, want 'Toggle cave_human mode'", item.name)
			}
			if item.desc != "terse, human voice on/off" {
				t.Fatalf("cave_human overlay desc = %q, want 'terse, human voice on/off'", item.desc)
			}
			break
		}
	}
	if !found {
		t.Fatal("buildSlashOverlayItems() does not include /cave-human")
	}
}
