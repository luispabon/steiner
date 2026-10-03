package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// addActiveRoster registers roster entries oldest first with the given statuses
// and one delegation card each; it returns their occurrence keys in that order.
func addActiveRoster(m *Model, statuses ...string) []occurrenceKey {
	keys := make([]occurrenceKey, 0, len(statuses))
	for i, st := range statuses {
		id := string(rune('a' + i))
		key := addJumpCard(m, i, "call-"+id, "")
		e := m.roster.upsert("agent-" + id)
		e.occurrence = key
		e.status = st
		e.startTime = int64(i + 1)
		keys = append(keys, key)
	}
	addJumpFiller(m, 60)
	m.syncViewport()
	return keys
}

func altKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModAlt} }

func TestAltKeyMatching(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{"alt", altKey('.'), true},
		{"alt with numlock", tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt | tea.ModNumLock}, true},
		{"plain", tea.KeyPressMsg{Code: '.', Text: "."}, false},
		{"alt shift", tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt | tea.ModShift}, false},
		{"ctrl alt", tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt | tea.ModCtrl}, false},
		{"other rune", altKey(','), false},
	}
	for _, tc := range cases {
		if got := isAlt(tc.msg, '.'); got != tc.want {
			t.Errorf("%s: isAlt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestActiveJumpOrder(t *testing.T) {
	m := newJumpTestModel(t)
	keys := addActiveRoster(m, rosterRunning, rosterDone, rosterQueued, rosterFailed, rosterRunning)
	got := m.activeJumpOrder()
	want := []occurrenceKey{keys[4], keys[2], keys[0]}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].occurrence != want[i] {
			t.Errorf("order[%d] = %v, want %v", i, got[i].occurrence, want[i])
		}
	}
}

func TestCycleSubAgent(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		keys     []rune // alt keys pressed in sequence
		want     []int  // expected target index (into roster order) after each press; -1 = unchanged/none
	}{
		{"next walks older and wraps", []string{rosterRunning, rosterRunning, rosterQueued}, []rune{'.', '.', '.', '.'}, []int{2, 1, 0, 2}},
		{"prev walks newer and wraps", []string{rosterRunning, rosterRunning, rosterQueued}, []rune{',', ',', ',', ','}, []int{0, 1, 2, 0}},
		{"mixed", []string{rosterRunning, rosterRunning, rosterRunning}, []rune{'.', ',', ','}, []int{2, 0, 1}},
		{"finished skipped", []string{rosterRunning, rosterDone, rosterRunning}, []rune{'.', '.', '.'}, []int{2, 0, 2}},
		{"none active", []string{rosterDone, rosterFailed}, []rune{'.', ','}, []int{-1, -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newJumpTestModel(t)
			keys := addActiveRoster(m, tc.statuses...)
			m.input.SetValue("draft")
			for i, r := range tc.keys {
				_, cmd := m.handleKeyMsg(altKey(r))
				if tc.want[i] < 0 {
					if cmd != nil || m.jumpTarget != (occurrenceKey{}) {
						t.Fatalf("press %d: expected no-op, got target %v", i, m.jumpTarget)
					}
				} else {
					if cmd == nil {
						t.Fatalf("press %d: nil cmd, flash tick not returned", i)
					}
					if m.jumpTarget != keys[tc.want[i]] {
						t.Fatalf("press %d: target = %v, want %v", i, m.jumpTarget, keys[tc.want[i]])
					}
				}
				if got := m.input.Value(); got != "draft" {
					t.Fatalf("press %d: composer = %q", i, got)
				}
			}
		})
	}
}

func TestCycleSubAgentTargetNoLongerActive(t *testing.T) {
	m := newJumpTestModel(t)
	keys := addActiveRoster(m, rosterRunning, rosterRunning, rosterRunning)
	m.cycleSubAgent(1) // newest (2)
	m.cycleSubAgent(1) // 1
	m.roster.entries["agent-b"].status = rosterDone
	m.cycleSubAgent(-1)
	if m.jumpTarget != keys[0] {
		t.Errorf("prev from inactive target = %v, want oldest %v", m.jumpTarget, keys[0])
	}
	m.jumpTarget = keys[1]
	m.cycleSubAgent(1)
	if m.jumpTarget != keys[2] {
		t.Errorf("next from inactive target = %v, want newest %v", m.jumpTarget, keys[2])
	}
}

func TestAltSlashToggle(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"within window", time.Second, true},
		{"after window", jumpExpandWindow + time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newJumpTestModel(t)
			keys := addActiveRoster(m, rosterRunning)
			m.input.SetValue("draft")
			m.cycleSubAgent(1)
			m.jumpAt = time.Now().Add(-tc.age)
			dd := m.content.delegations[keys[0]].dd
			before := dd.collapsed
			_, _ = m.handleKeyMsg(altKey('/'))
			if (dd.collapsed != before) != tc.want {
				t.Errorf("collapsed %v -> %v, want toggled=%v", before, dd.collapsed, tc.want)
			}
			if got := m.input.Value(); got != "draft" {
				t.Errorf("composer = %q", got)
			}
		})
	}
}

func TestAltKeysIgnoredWhileHelpVisible(t *testing.T) {
	m := newJumpTestModel(t)
	addActiveRoster(m, rosterRunning)
	m.helpVisible = true
	m.handleKeyMsg(altKey('.'))
	if m.jumpTarget != (occurrenceKey{}) {
		t.Errorf("jump happened while help visible: %v", m.jumpTarget)
	}
}

func TestAltKeysIgnoredWhileOverlayOpen(t *testing.T) {
	m := newJumpTestModel(t)
	addActiveRoster(m, rosterRunning)
	m.approval.active = true
	m.handleKeyMsg(altKey('.'))
	if m.jumpTarget != (occurrenceKey{}) {
		t.Errorf("jump happened during approval: %v", m.jumpTarget)
	}
}
