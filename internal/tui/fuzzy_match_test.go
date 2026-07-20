package tui

import (
	"testing"
)

func TestScoreStringMatch_ExactSubstringBoost(t *testing.T) {
	// A weak fuzzy match in a long string vs an exact substring match
	t.Parallel()
	weak := scoreStringMatch("some very long description text", "plan", []int{5, 10, 15, 20})
	exact := scoreStringMatch("/plan", "plan", []int{1, 2, 3, 4})
	if exact <= weak {
		t.Fatalf("expected exact substring match to score higher than weak fuzzy match, got weak=%d exact=%d", weak, exact)
	}
}

func TestScoreStringMatch_PrefixBoost(t *testing.T) {
	t.Parallel()
	prefix := scoreStringMatch("planning.md", "plan", []int{0, 1, 2, 3})
	middle := scoreStringMatch("my-planning.md", "plan", []int{3, 4, 5, 6})
	if prefix <= middle {
		t.Fatalf("expected prefix match to score higher than middle match, got prefix=%d middle=%d", prefix, middle)
	}
}

func TestScoreStringMatch_ConsecutiveRunBonus(t *testing.T) {
	// Consecutive match "plan" should score higher than scattered match
	t.Parallel()
	consecutive := scoreStringMatch("plan", "plan", []int{0, 1, 2, 3})
	scattered := scoreStringMatch("p___l__a___n", "plan", []int{0, 4, 7, 11})
	if consecutive <= scattered {
		t.Fatalf("expected consecutive match to score higher than scattered match, got consecutive=%d scattered=%d", consecutive, scattered)
	}
}

func TestScoreStringMatch_WordBoundaryBonus(t *testing.T) {
	// Match at word boundary should score higher than match in middle of word
	t.Parallel()
	boundary := scoreStringMatch("plan file", "plan", []int{0, 1, 2, 3})
	midWord := scoreStringMatch("template", "plan", []int{2, 3, 4, 5})
	if boundary <= midWord {
		t.Fatalf("expected word-boundary match to score higher than mid-word match, got boundary=%d midWord=%d", boundary, midWord)
	}
}

func TestScoreStringMatch_EarlyMatchBonus(t *testing.T) {
	t.Parallel()
	early := scoreStringMatch("plan something", "plan", []int{0, 1, 2, 3})
	late := scoreStringMatch("something plan", "plan", []int{10, 11, 12, 13})
	if early <= late {
		t.Fatalf("expected early match to score higher than late match, got early=%d late=%d", early, late)
	}
}

func TestConsecutiveScore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		indexes []int
		want    int
	}{
		{"empty", []int{}, 0},
		{"single", []int{0}, 1},
		{"two consecutive", []int{0, 1}, 4},
		{"three consecutive", []int{0, 1, 2}, 9},
		{"two separate singles", []int{0, 2}, 2},
		{"run then single", []int{0, 1, 3}, 5},
		{"two runs", []int{0, 1, 3, 4}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := consecutiveScore(tt.indexes)
			if got != tt.want {
				t.Fatalf("consecutiveScore(%v) = %d, want %d", tt.indexes, got, tt.want)
			}
		})
	}
}

func TestFuzzyMatchStrings_ReordersByCustomScore(t *testing.T) {
	t.Parallel()
	entries := []string{
		"planning",
		"some-plan",
		"plan",
	}
	results, indexes := fuzzyMatchStrings(entries, "plan")
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// "plan" should be first because it's an exact prefix match
	if results[0] != "plan" {
		t.Fatalf("expected first result to be %q, got %q", "plan", results[0])
	}
	// "planning" should be second because it's a prefix match
	if results[1] != "planning" {
		t.Fatalf("expected second result to be %q, got %q", "planning", results[1])
	}
	// Verify matched indexes are preserved and in correct order
	if len(indexes[0]) != 4 {
		t.Fatalf("expected 4 matched indexes for exact match, got %d", len(indexes[0]))
	}
}

func TestFuzzyMatchStrings_EmptyQuery(t *testing.T) {
	t.Parallel()
	entries := []string{"alpha", "beta", "gamma"}
	results, indexes := fuzzyMatchStrings(entries, "")
	if len(results) != 3 {
		t.Fatalf("expected 3 results for empty query, got %d", len(results))
	}
	for i := range indexes {
		if len(indexes[i]) != 0 {
			t.Fatalf("expected empty indexes for empty query at %d, got %v", i, indexes[i])
		}
	}
}

func TestFuzzyMatchStrings_NoMatches(t *testing.T) {
	t.Parallel()
	entries := []string{"alpha", "beta"}
	results, _ := fuzzyMatchStrings(entries, "zzz")
	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-matching query, got %d", len(results))
	}
}

func TestScoreSlashMatch_CommandBeatsDescription(t *testing.T) {
	// A weak command match should still outrank a strong description match
	t.Parallel()
	itemCmd := slashOverlayItem{command: "/plan", name: "Plan", desc: "plan things"}
	matchCmd := slashOverlayMatch{commandIndexes: []int{1, 2, 3, 4}}

	itemDesc := slashOverlayItem{command: "/other", name: "Other", desc: "plan your work"}
	matchDesc := slashOverlayMatch{descIndexes: []int{0, 1, 2, 3}}

	scoreCmd := scoreSlashMatch(itemCmd, "plan", matchCmd)
	scoreDesc := scoreSlashMatch(itemDesc, "plan", matchDesc)

	if scoreCmd <= scoreDesc {
		t.Fatalf("expected command match to outrank description match, got cmd=%d desc=%d", scoreCmd, scoreDesc)
	}
}

func TestScoreSlashMatch_PrefixSlashCommand(t *testing.T) {
	t.Parallel()
	item := slashOverlayItem{command: "/plan", name: "Plan", desc: "plan things"}
	match := slashOverlayMatch{commandIndexes: []int{1, 2, 3, 4}}
	score := scoreSlashMatch(item, "plan", match)
	if score < 200000 {
		t.Fatalf("expected /plan prefix match to score at least 200k, got %d", score)
	}
}

func TestScoreSlashMatch_NameBeatsDescription(t *testing.T) {
	t.Parallel()
	itemName := slashOverlayItem{command: "/other", name: "Planner", desc: "something"}
	matchName := slashOverlayMatch{nameIndexes: []int{0, 1, 2, 3}}

	itemDesc := slashOverlayItem{command: "/other", name: "Other", desc: "plan your work"}
	matchDesc := slashOverlayMatch{descIndexes: []int{0, 1, 2, 3}}

	scoreName := scoreSlashMatch(itemName, "plan", matchName)
	scoreDesc := scoreSlashMatch(itemDesc, "plan", matchDesc)

	if scoreName <= scoreDesc {
		t.Fatalf("expected name match to outrank description match, got name=%d desc=%d", scoreName, scoreDesc)
	}
}

func TestFuzzyMatchSlashItems_ReordersByField(t *testing.T) {
	t.Parallel()
	items := []slashOverlayItem{
		{command: "/other", name: "Other", desc: "plan your work"},
		{command: "/plan", name: "Plan", desc: "do things"},
	}
	results, matchData := fuzzyMatchSlashItems(items, "/plan")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// /plan should rank first because it's a command match
	if results[0].command != "/plan" {
		t.Fatalf("expected first result to be /plan, got %s", results[0].command)
	}
	// Verify command indexes are populated for the top result
	if len(matchData[0].commandIndexes) == 0 {
		t.Fatalf("expected command indexes for /plan match")
	}
}

func TestFuzzyMatchSlashItems_EmptyQuery(t *testing.T) {
	t.Parallel()
	items := []slashOverlayItem{
		{command: "/a", name: "A", desc: "a"},
		{command: "/b", name: "B", desc: "b"},
	}
	results, matchData := fuzzyMatchSlashItems(items, "/")
	if len(results) != 2 {
		t.Fatalf("expected 2 results for empty query, got %d", len(results))
	}
	for i := range matchData {
		if len(matchData[i].commandIndexes) != 0 || len(matchData[i].nameIndexes) != 0 || len(matchData[i].descIndexes) != 0 {
			t.Fatalf("expected empty match data for empty query at %d", i)
		}
	}
}

func TestFuzzyMatchSlashItems_NoMatches(t *testing.T) {
	t.Parallel()
	items := []slashOverlayItem{
		{command: "/a", name: "A", desc: "a"},
	}
	results, _ := fuzzyMatchSlashItems(items, "/zzz")
	if len(results) != 0 {
		t.Fatalf("expected 0 results for non-matching query, got %d", len(results))
	}
}
