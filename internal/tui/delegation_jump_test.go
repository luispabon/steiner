package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/output"
)

func newJumpTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{Model: "m", ModelContexts: map[string]int{"m": 4096}}, nil)
	return updateModelDirect(m, tea.WindowSizeMsg{Width: 100, Height: 20})
}

func addJumpCard(m *Model, idx int, callID, group string) occurrenceKey {
	m.content.AppendEvent(output.NewToolCallQueuedEvent(idx, "sub_agent", callID, subAgentArgs(group)))
	m.content.AppendEvent(output.NewToolCallStartedEvent(idx, "sub_agent", callID, subAgentArgs(group)))
	m.content.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: callID, BatchID: "batch"}, group))
	return occurrenceKey{BatchID: "batch", CallID: callID}
}

func addJumpFiller(m *Model, n int) {
	for i := 0; i < n; i++ {
		m.content.AppendLine(fmt.Sprintf("filler %d", i))
	}
}

// viewportRow returns the visible viewport row (0 = first) as plain text.
func viewportRow(m *Model, row int) string {
	lines := m.viewport.Lines()
	idx := m.viewport.YOffset() + row
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return ansi.Strip(lines[idx])
}

func TestJumpToStandaloneCardPutsTopBorderFirst(t *testing.T) {
	m := newJumpTestModel(t)
	addJumpFiller(m, 10)
	key := addJumpCard(m, 1, "target", "")
	addJumpFiller(m, 80)
	m.autoScroll = true
	m.syncViewport()

	if cmd := m.jumpToDelegation(key); cmd == nil {
		t.Fatal("jumpToDelegation returned nil cmd for a resolvable card")
	}
	if m.autoScroll {
		t.Error("autoScroll still true after jump")
	}
	seg, row, _, ok := m.content.delegationTopRow(key, m.viewport.Width())
	if !ok || row != 0 {
		t.Fatalf("delegationTopRow = %d,%d,%v", seg, row, ok)
	}
	line, _ := m.content.contentLineForSegmentRow(seg, 0)
	if got := m.viewport.YOffset(); got != line+m.contentTopPad {
		t.Errorf("YOffset = %d, want %d", got, line+m.contentTopPad)
	}
	if top := viewportRow(m, 0); !strings.Contains(top, "╭") && !strings.Contains(top, "┌") {
		t.Errorf("first viewport row is not a top border: %q", top)
	}
	if m.jumpTarget != key || m.jumpAt.IsZero() {
		t.Errorf("jump target not recorded: %+v %v", m.jumpTarget, m.jumpAt)
	}
}

func TestJumpToGroupMembers(t *testing.T) {
	cases := []struct {
		name   string
		member int
	}{
		{"first member aligns group top border", 0},
		{"second member aligns divider", 1},
		{"third member aligns divider", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newJumpTestModel(t)
			addJumpFiller(m, 10)
			keys := []occurrenceKey{
				addJumpCard(m, 1, "a", "g"),
				addJumpCard(m, 2, "b", "g"),
				addJumpCard(m, 3, "c", "g"),
			}
			addJumpFiller(m, 80)
			m.syncViewport()

			key := keys[tc.member]
			if m.jumpToDelegation(key) == nil {
				t.Fatal("jump returned nil")
			}
			seg, row, dd, ok := m.content.delegationTopRow(key, m.viewport.Width())
			if !ok {
				t.Fatal("unresolved")
			}
			g := m.content.segments[seg].delegGroupData
			if g == nil || g.entries[tc.member] != dd {
				t.Fatalf("member %d not at expected group position", tc.member)
			}
			if tc.member == 0 && row != 0 {
				t.Errorf("member 0 row = %d, want 0", row)
			}
			if tc.member > 0 {
				// The row after the divider is the member's first content row, and
				// the divider must differ from content rows of the previous member.
				if row < 1 {
					t.Fatalf("divider row = %d", row)
				}
				prev := g.entries[tc.member-1]
				want := 1 + len(m.content.delegationContentRows(prev, m.viewport.Width()))
				if tc.member == 2 {
					want += 1 + len(m.content.delegationContentRows(g.entries[1], m.viewport.Width()))
				}
				if row != want {
					t.Errorf("divider row = %d, want %d", row, want)
				}
			}
			line, _ := m.content.contentLineForSegmentRow(seg, row)
			if got := m.viewport.YOffset(); got != line+m.contentTopPad {
				t.Errorf("YOffset = %d, want %d", got, line+m.contentTopPad)
			}
			if m.autoScroll {
				t.Error("autoScroll still true")
			}
		})
	}
}

func TestJumpClampsAtContentEnd(t *testing.T) {
	m := newJumpTestModel(t)
	addJumpFiller(m, 30)
	key := addJumpCard(m, 1, "last", "")
	m.syncViewport()
	if m.jumpToDelegation(key) == nil {
		t.Fatal("jump returned nil")
	}
	if !m.viewport.AtBottom() {
		t.Errorf("expected clamp at bottom, yOffset=%d", m.viewport.YOffset())
	}
}

func TestJumpUnresolvableChangesNothing(t *testing.T) {
	m := newJumpTestModel(t)
	addJumpFiller(m, 80)
	existing := addJumpCard(m, 1, "x", "")
	addJumpFiller(m, 80)
	m.syncViewport()
	m.viewport.SetYOffset(7)
	m.autoScroll = true
	// Establish an active flash on the existing card via a real jump, then
	// restore the state under test.
	m.jumpToDelegation(existing)
	m.autoScroll = true
	m.viewport.SetYOffset(7)
	flash, epoch, left, target := m.content.jumpFlash, m.jumpFlashEpoch, m.jumpFlashLeft, m.jumpTarget

	cases := []occurrenceKey{
		{BatchID: "batch", CallID: "missing"},
		{},
	}
	for _, key := range cases {
		if cmd := m.jumpToDelegation(key); cmd != nil {
			t.Errorf("key %+v: got cmd, want nil", key)
		}
		if !m.autoScroll || m.viewport.YOffset() != 7 {
			t.Errorf("key %+v: scroll state changed (auto=%v offset=%d)", key, m.autoScroll, m.viewport.YOffset())
		}
		if m.content.jumpFlash != flash || m.jumpFlashEpoch != epoch || m.jumpFlashLeft != left || m.jumpTarget != target {
			t.Errorf("key %+v: flash/jump state changed", key)
		}
	}
}

var elapsedRE = regexp.MustCompile(`[0-9.]+m?s`)

// headerRowText returns the header row with the live elapsed timer masked so
// two renders taken moments apart compare equal.
func headerRowText(m *Model, dd *delegationDisplayState) string {
	for _, r := range m.content.delegationRows(dd, m.viewport.Width()) {
		if r.kind == delegationRowHeader {
			return elapsedRE.ReplaceAllString(r.text, "T")
		}
	}
	return ""
}

func TestJumpFlashLifecycle(t *testing.T) {
	m := newJumpTestModel(t)
	addJumpFiller(m, 5)
	key := addJumpCard(m, 1, "t", "")
	addJumpFiller(m, 60)
	m.syncViewport()
	dd := m.content.delegations[key].dd
	plain := headerRowText(m, dd)

	cmd := m.jumpToDelegation(key)
	if cmd == nil || !m.content.jumpFlash.on || m.content.jumpFlash.dd != dd {
		t.Fatal("flash not started")
	}
	rowsBefore := len(m.content.delegationRows(dd, m.viewport.Width()))
	onText := headerRowText(m, dd)
	if onText == plain {
		t.Error("highlighted header identical to normal header")
	}
	if ansi.Strip(onText) != ansi.Strip(plain) && strings.TrimSpace(ansi.Strip(onText)) != strings.TrimSpace(ansi.Strip(plain)) {
		t.Errorf("highlight changed header text: %q vs %q", ansi.Strip(onText), ansi.Strip(plain))
	}

	// 7 more half-phases toggle, the 8th tick clears.
	ticks := 0
	for m.content.jumpFlash.dd != nil {
		offset := m.viewport.YOffset()
		_, next := m.handleJumpFlashTick(jumpFlashTickMsg{epoch: m.jumpFlashEpoch})
		ticks++
		if m.viewport.YOffset() != offset {
			t.Fatal("flash tick scrolled")
		}
		if ticks > 20 {
			t.Fatal("flash never ended")
		}
		if m.content.jumpFlash.dd == nil && next != nil {
			t.Error("final tick re-armed")
		}
		if m.content.jumpFlash.dd != nil && next == nil {
			t.Error("tick not re-armed mid-flash")
		}
		wantOn := ticks%2 == 0
		if m.content.jumpFlash.dd != nil && m.content.jumpFlash.on != wantOn {
			t.Errorf("tick %d: on=%v want %v", ticks, m.content.jumpFlash.on, wantOn)
		}
	}
	if ticks != jumpFlashPhases {
		t.Errorf("ticks = %d, want %d", ticks, jumpFlashPhases)
	}
	if got := headerRowText(m, dd); got != plain {
		t.Error("header not restored after flash")
	}
	if len(m.content.delegationRows(dd, m.viewport.Width())) != rowsBefore {
		t.Error("flash changed row count")
	}
	// Ticks after the end are no-ops.
	if _, next := m.handleJumpFlashTick(jumpFlashTickMsg{epoch: m.jumpFlashEpoch}); next != nil {
		t.Error("tick after end re-armed")
	}
}

func TestSecondJumpCancelsFirstFlash(t *testing.T) {
	m := newJumpTestModel(t)
	k1 := addJumpCard(m, 1, "one", "")
	addJumpFiller(m, 30)
	k2 := addJumpCard(m, 2, "two", "")
	addJumpFiller(m, 60)
	m.syncViewport()
	dd1, dd2 := m.content.delegations[k1].dd, m.content.delegations[k2].dd

	m.jumpToDelegation(k1)
	oldEpoch := m.jumpFlashEpoch
	m.jumpToDelegation(k2)
	if m.jumpFlashEpoch == oldEpoch {
		t.Error("epoch not bumped")
	}
	if m.content.jumpFlash.dd != dd2 {
		t.Error("flash not moved to second card")
	}
	if headerRowText(m, dd1) != headerRowTextNoFlash(m, dd1) {
		t.Error("old card still highlighted")
	}
	before := m.content.jumpFlash
	left := m.jumpFlashLeft
	if _, next := m.handleJumpFlashTick(jumpFlashTickMsg{epoch: oldEpoch}); next != nil {
		t.Error("stale tick re-armed")
	}
	if m.content.jumpFlash != before || m.jumpFlashLeft != left {
		t.Error("stale tick mutated flash state")
	}
}

func headerRowTextNoFlash(m *Model, dd *delegationDisplayState) string {
	saved := m.content.jumpFlash
	m.content.jumpFlash = jumpFlashState{}
	defer func() { m.content.jumpFlash = saved }()
	return headerRowText(m, dd)
}

func TestToggleJumpTargetCollapse(t *testing.T) {
	cases := []struct {
		name     string
		group    string
		age      time.Duration
		noTarget bool
		want     bool
	}{
		{"standalone within window", "", time.Second, false, true},
		{"group member within window", "g", time.Second, false, true},
		{"after window", "", jumpExpandWindow + time.Second, false, false},
		{"no jump yet", "", 0, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newJumpTestModel(t)
			addJumpCard(m, 0, "first", tc.group)
			key := addJumpCard(m, 1, "second", tc.group)
			addJumpFiller(m, 60)
			m.syncViewport()
			dd := m.content.delegations[key].dd
			if !tc.noTarget {
				m.jumpToDelegation(key)
				m.jumpAt = time.Now().Add(-tc.age)
			}
			before := dd.collapsed
			offset := m.viewport.YOffset()
			if got := m.toggleJumpTargetCollapse(); got != tc.want {
				t.Fatalf("toggle = %v, want %v", got, tc.want)
			}
			if (dd.collapsed != before) != tc.want {
				t.Errorf("collapsed %v -> %v", before, dd.collapsed)
			}
			if m.viewport.YOffset() != offset {
				t.Error("toggle re-scrolled")
			}
		})
	}
}

func TestToggleJumpTargetCollapseAfterClear(t *testing.T) {
	m := newJumpTestModel(t)
	key := addJumpCard(m, 1, "x", "")
	m.jumpToDelegation(key)
	m.resetConversationUI()
	if m.toggleJumpTargetCollapse() {
		t.Error("toggle succeeded after clear")
	}
	if m.jumpToDelegation(key) != nil {
		t.Error("jump resolved after clear")
	}
}
