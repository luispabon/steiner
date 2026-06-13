package tui

import "testing"

func TestParseInputHandlesHumanizerCommand(t *testing.T) {
	action := parseInput("/humanizer")
	if !action.humanizerToggle {
		t.Fatal("humanizerToggle = false, want true")
	}
	if action.submit != "" {
		t.Fatalf("submit = %q, want empty", action.submit)
	}
}

func TestBuildCompletionCandidatesIncludesHumanizer(t *testing.T) {
	got := buildCompletionCandidates("/hum", nil, nil)
	found := false
	for _, c := range got {
		if c == "/humanizer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("candidates = %#v, want /humanizer included", got)
	}
}

func TestSlashOverlayItemsIncludesHumanizer(t *testing.T) {
	m := Model{}
	items := m.buildSlashOverlayItems()
	found := false
	for _, item := range items {
		if item.command == "/humanizer" {
			found = true
			if item.name != "Toggle humanizer mode" {
				t.Fatalf("humanizer overlay name = %q, want 'Toggle humanizer mode'", item.name)
			}
			if item.desc != "switch humanized prompting on/off" {
				t.Fatalf("humanizer overlay desc = %q, want 'switch humanized prompting on/off'", item.desc)
			}
			break
		}
	}
	if !found {
		t.Fatal("buildSlashOverlayItems() does not include /humanizer")
	}
}
