package tui

import (
	"fmt"
	"strings"
	"testing"
)

type pickerCard struct {
	id      string
	group   string
	typ     string
	status  string
	start   int64
	advisor bool
	task    string
}

// buildPickerBuffer appends one card per entry; a filler line between differing
// groups (or standalone cards) keeps them in separate segments.
func buildPickerBuffer(t *testing.T, cards []pickerCard) (*contentBuffer, map[string]occurrenceKey) {
	t.Helper()
	m := newJumpTestModel(t)
	keys := map[string]occurrenceKey{}
	for i, c := range cards {
		if i > 0 && (c.group == "" || c.group != cards[i-1].group) {
			m.content.AppendLine(fmt.Sprintf("filler %d", i))
		}
		key := addJumpCard(m, i+1, c.id, c.group)
		keys[c.id] = key
		dd := m.content.delegations[key].dd
		dd.agentID, dd.agentType, dd.toolLabel, dd.status = c.id, c.typ, c.typ, c.status
		dd.startTime, dd.isAdvisor, dd.taskPreview = c.start, c.advisor, c.task
	}
	return &m.content, keys
}

func rowIDs(rows []subAgentPickerRow) []string {
	var out []string
	for _, r := range rows {
		switch {
		case r.section:
			out = append(out, "#"+r.heading)
		case r.groupHdr:
			out = append(out, "┌"+r.heading)
		default:
			out = append(out, r.dd.agentID)
		}
	}
	return out
}

func TestSubAgentPickerRows(t *testing.T) {
	cards := []pickerCard{
		{id: "old", typ: "plan", status: "complete", start: 10, task: "draft plan"},
		{id: "g1", group: "fan", typ: "explore", status: "complete", start: 20, task: "map tui"},
		{id: "g2", group: "fan", typ: "explore", status: "failed", start: 30, task: "map tool"},
		{id: "mix1", group: "batch-a", typ: "review", status: "complete", start: 40, task: "review auth"},
		{id: "mix2", group: "batch-a", typ: "review", status: "active", start: 50, task: "review db"},
		{id: "run", typ: "research", status: "active", start: 60, task: "find spec"},
		{id: "queued", typ: "simplify", status: "active", start: 0, task: "simplify parser"},
		{id: "adv", typ: "advisor", status: "active", start: 70, advisor: true, task: "hidden"},
	}
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"all", "", []string{"#RUNNING", "queued", "run", "┌batch-a", "mix1", "mix2", "#COMPLETED", "┌fan", "g1", "g2", "old"}},
		{"group name lists whole group", "FAN", []string{"#COMPLETED", "┌fan", "g1", "g2"}},
		{"type lists all of type", "explore", []string{"#COMPLETED", "┌fan", "g1", "g2"}},
		{"id matches single member keeps group in running", "mix1", []string{"#RUNNING", "┌batch-a", "mix1"}},
		{"task word", "parser", []string{"#RUNNING", "queued"}},
		{"no match", "zzz", nil},
		{"advisor never shown", "hidden", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := buildPickerBuffer(t, cards)
			got := rowIDs(b.subAgentPickerRows(tc.query))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("rows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubAgentPickerRowsCarryOwnKeys(t *testing.T) {
	b, keys := buildPickerBuffer(t, []pickerCard{
		{id: "a", group: "g", typ: "explore", status: "complete", start: 1},
		{id: "b", group: "g", typ: "explore", status: "complete", start: 2},
	})
	for _, r := range b.subAgentPickerRows("") {
		if r.selectable() && r.key != keys[r.dd.agentID] {
			t.Errorf("row %s key = %+v, want %+v", r.dd.agentID, r.key, keys[r.dd.agentID])
		}
		if r.selectable() && !r.inGroup {
			t.Errorf("row %s should be inGroup", r.dd.agentID)
		}
	}
}

func TestSubAgentPickerRowsFollowUpIsOwnRow(t *testing.T) {
	m := newJumpTestModel(t)
	k1 := addJumpCard(m, 1, "c1", "")
	m.content.AppendLine("filler")
	k2 := addJumpCard(m, 2, "c2", "")
	for _, k := range []occurrenceKey{k1, k2} {
		dd := m.content.delegations[k].dd
		dd.agentID, dd.status = "same", "complete"
	}
	m.content.delegations[k2].dd.isFollowUp = true
	n := 0
	for _, r := range m.content.subAgentPickerRows("") {
		if r.selectable() {
			n++
		}
	}
	if n != 2 {
		t.Errorf("card rows = %d, want 2", n)
	}
}
