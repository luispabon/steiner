package tui

import (
	"testing"
)

func TestRegistrySize(t *testing.T) {
	t.Parallel()
	if len(slashCommands) != 19 {
		t.Fatalf("registry length = %d, want 19", len(slashCommands))
	}
}

func TestRegistryAllowlist(t *testing.T) {
	t.Parallel()
	for _, sc := range slashCommands {
		switch sc.ID {
		case "/exit", "/thinking", "/accent":
			if !sc.Allowlist {
				t.Errorf("%s: Allowlist = false, want true", sc.ID)
			}
		case "/implement", "/review":
			// OverlayOnly, not allowlist
			if sc.Allowlist {
				t.Errorf("%s: Allowlist = true, want false", sc.ID)
			}
		default:
			if sc.Allowlist {
				t.Errorf("%s: Allowlist = true, want false", sc.ID)
			}
		}
	}
}

func TestRegistryOverlayOnly(t *testing.T) {
	t.Parallel()
	for _, sc := range slashCommands {
		switch sc.ID {
		case "/implement", "/review":
			if !sc.OverlayOnly {
				t.Errorf("%s: OverlayOnly = false, want true", sc.ID)
			}
		default:
			if sc.OverlayOnly {
				t.Errorf("%s: OverlayOnly = true, want false", sc.ID)
			}
		}
	}
}

func TestRegistryOrder(t *testing.T) {
	t.Parallel()
	want := []string{
		"/cache-stats",
		"/clear",
		"/compact",
		"/config",
		"/exit",
		"/fork",
		"/implement",
		"/ls",
		"/mcp",
		"/model",
		"/mode",
		"/oneshot",
		"/oneshot-resume",
		"/resume",
		"/review",
		"/skill",
		"/skills",
		"/thinking",
		"/accent",
	}
	if len(slashCommands) != len(want) {
		t.Fatalf("registry length = %d, want %d", len(slashCommands), len(want))
	}
	for i, sc := range slashCommands {
		if sc.ID != want[i] {
			t.Errorf("registry[%d] = %q, want %q", i, sc.ID, want[i])
		}
	}
}

func TestProjectPrefixesFalse(t *testing.T) {
	t.Parallel()
	got := projectPrefixes(false)
	// No overlay-only entries
	for _, p := range got {
		if p == "/implement" || p == "/review" {
			t.Errorf("projectPrefixes includes overlay-only entry %q", p)
		}
	}
	// Every non-overlay entry appears at least as bare
	nonOverlay := 0
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		nonOverlay++
	}
	// Each entry produces bare + (if takes arg) trailing-space
	// So total should be nonOverlay + argTakingCount
	argTaking := 0
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		if takesArg(sc) {
			argTaking++
		}
	}
	if len(got) != nonOverlay+argTaking {
		t.Errorf("projectPrefixes(false) length = %d, want %d (non-overlay=%d, arg-taking=%d)",
			len(got), nonOverlay+argTaking, nonOverlay, argTaking)
	}
	// Check /accent appears with trailing space
	foundBare := false
	foundSpace := false
	for _, p := range got {
		if p == "/accent" {
			foundBare = true
		}
		if p == "/accent " {
			foundSpace = true
		}
	}
	if !foundBare {
		t.Error("/accent bare not found in projectPrefixes(false)")
	}
	if !foundSpace {
		t.Error("/accent  (trailing space) not found in projectPrefixes(false)")
	}
	// Check /ls appears with trailing space
	foundLSBare := false
	foundLSSpace := false
	for _, p := range got {
		if p == "/ls" {
			foundLSBare = true
		}
		if p == "/ls " {
			foundLSSpace = true
		}
	}
	if !foundLSBare {
		t.Error("/ls bare not found in projectPrefixes(false)")
	}
	if !foundLSSpace {
		t.Error("/ls  (trailing space) not found in projectPrefixes(false)")
	}
	// Check non-arg commands don't have trailing space
	for _, p := range got {
		if p == "/clear " || p == "/exit " {
			t.Errorf("non-arg command %q should not have trailing space variant", p)
		}
	}
}

func TestProjectPrefixesTrue(t *testing.T) {
	t.Parallel()
	got := projectPrefixes(true)
	// Only allowlist entries
	wantAllowlist := map[string]bool{"/exit": true, "/thinking": true, "/accent": true}
	for _, p := range got {
		base := p
		if base[len(base)-1] == ' ' {
			base = base[:len(base)-1]
		}
		if !wantAllowlist[base] {
			t.Errorf("projectPrefixes(true) includes non-allowlist entry %q", p)
		}
	}
	// Must include /exit, /thinking, /accent
	for _, cmd := range []string{"/exit", "/thinking", "/accent"} {
		found := false
		for _, p := range got {
			if p == cmd {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("projectPrefixes(true) missing %q", cmd)
		}
	}
	// /accent should also have trailing space
	foundSpace := false
	for _, p := range got {
		if p == "/accent " {
			foundSpace = true
			break
		}
	}
	if !foundSpace {
		t.Error("/accent  (trailing space) not found in projectPrefixes(true)")
	}
}

func TestProjectCompletionCandidatesFalseNoSkills(t *testing.T) {
	t.Parallel()
	got := projectCompletionCandidates(false, nil)
	// Should contain all non-overlay entries
	for _, sc := range slashCommands {
		if sc.OverlayOnly {
			continue
		}
		found := false
		for _, c := range got {
			if c == sc.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("projectCompletionCandidates(false, nil) missing %q", sc.ID)
		}
	}
	// /accent variants should be present
	for _, v := range []string{"amber", "rose", "magenta", "violet", "cyan", "mint", "lime"} {
		candidate := "/accent " + v
		found := false
		for _, c := range got {
			if c == candidate {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("projectCompletionCandidates(false, nil) missing %q", candidate)
		}
	}
	// No skill variants (skillNames is nil)
	for _, c := range got {
		if c == "/skill +foo" || c == "/skill -foo" || c == "/skill foo" {
			t.Errorf("projectCompletionCandidates(false, nil) contains skill variant %q with nil skillNames", c)
		}
	}
	// OverlayOnly entries should not appear
	for _, c := range got {
		if c == "/implement" || c == "/review" {
			t.Errorf("projectCompletionCandidates includes overlay-only entry %q", c)
		}
	}
}

func TestProjectCompletionCandidatesFalseWithSkills(t *testing.T) {
	t.Parallel()
	got := projectCompletionCandidates(false, []string{"foo"})
	// Should include skill variants
	hasPlus := false
	hasMinus := false
	hasBare := false
	for _, c := range got {
		if c == "/skill +foo" {
			hasPlus = true
		}
		if c == "/skill -foo" {
			hasMinus = true
		}
		if c == "/skill foo" {
			hasBare = true
		}
	}
	if !hasPlus {
		t.Error("missing /skill +foo")
	}
	if !hasMinus {
		t.Error("missing /skill -foo")
	}
	if !hasBare {
		t.Error("missing /skill foo")
	}
}

func TestProjectCompletionCandidatesTrue(t *testing.T) {
	t.Parallel()
	got := projectCompletionCandidates(true, nil)
	// Only allowlist entries
	for _, c := range got {
		if c == "/oneshot" || c == "/clear" || c == "/cache-stats" {
			t.Errorf("projectCompletionCandidates(true) includes non-allowlist %q", c)
		}
	}
	// Must include allowlist entries
	for _, cmd := range []string{"/exit", "/thinking", "/accent"} {
		found := false
		for _, c := range got {
			if c == cmd {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("projectCompletionCandidates(true) missing %q", cmd)
		}
	}
	// Accent variants should be present
	for _, v := range []string{"amber", "rose"} {
		candidate := "/accent " + v
		found := false
		for _, c := range got {
			if c == candidate {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("projectCompletionCandidates(true) missing %q", candidate)
		}
	}
}

func TestProjectOverlayItemsFalse(t *testing.T) {
	t.Parallel()
	got := projectOverlayItems(false, nil, nil)
	if len(got) != 19 {
		t.Fatalf("projectOverlayItems(false) length = %d, want 19", len(got))
	}
	for i, sc := range slashCommands {
		item := got[i]
		if item.command != sc.ID {
			t.Errorf("projectOverlayItems[%d].command = %q, want %q", i, item.command, sc.ID)
		}
		if item.name != sc.Name {
			t.Errorf("projectOverlayItems[%d].name = %q, want %q (for %s)", i, item.name, sc.Name, sc.ID)
		}
		if item.desc != sc.Desc {
			t.Errorf("projectOverlayItems[%d].desc = %q, want %q (for %s)", i, item.desc, sc.Desc, sc.ID)
		}
	}
}

func TestProjectOverlayItemsTrue(t *testing.T) {
	t.Parallel()
	got := projectOverlayItems(true, nil, nil)
	if len(got) != 3 {
		t.Fatalf("projectOverlayItems(true) length = %d, want 3", len(got))
	}
	want := []string{"/exit", "/thinking", "/accent"}
	for i, cmd := range want {
		if got[i].command != cmd {
			t.Errorf("projectOverlayItems(true)[%d] = %q, want %q", i, got[i].command, cmd)
		}
	}
}

func TestProjectOverlayItemsWithSkills(t *testing.T) {
	t.Parallel()
	skillNames := []string{"foo"}
	skillDescs := map[string]string{"foo": "a useful skill"}
	got := projectOverlayItems(false, skillNames, skillDescs)
	// 19 commands + 1 skill = 20 items
	if len(got) != 20 {
		t.Fatalf("projectOverlayItems with skill length = %d, want 20", len(got))
	}
	// Last item should be the skill
	last := got[len(got)-1]
	if last.command != "/foo" {
		t.Errorf("last item command = %q, want /foo", last.command)
	}
	if last.desc != "a useful skill" {
		t.Errorf("skill desc = %q, want 'a useful skill'", last.desc)
	}
	if !last.isSkill {
		t.Error("skill item isSkill = false, want true")
	}
}

func TestProjectHelpLines(t *testing.T) {
	t.Parallel()
	got := projectHelpLines()
	want := []helpBinding{
		{key: "/clear", desc: "reset the current session"},
		{key: "/compact", desc: "trigger compaction with optional focus text"},
		{key: "/fork", desc: "fork current conversation into a new session"},
		{key: "/resume", desc: "load a previous session"},
		{key: "/accent [preset]", desc: "change accent color"},
		{key: "/thinking", desc: "show or hide thinking blocks"},
		{key: "shift+tab / /mode [plan|build]", desc: "toggle or set mode: plan (restricted edits, plan artifacts only) or build (normal workspace editing)"},
		{key: "/exit", desc: "quit steiner"},
	}
	if len(got) != len(want) {
		t.Fatalf("projectHelpLines() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].key != want[i].key {
			t.Errorf("projectHelpLines[%d] key = %q, want %q", i, got[i].key, want[i].key)
		}
		if got[i].desc != want[i].desc {
			t.Errorf("projectHelpLines[%d] desc = %q, want %q", i, got[i].desc, want[i].desc)
		}
	}
}
