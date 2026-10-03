package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	jumpFlashInterval = 250 * time.Millisecond // one on/off half-phase
	jumpFlashPhases   = 8                      // 8 half-phases = 4 flips over 2s
	jumpExpandWindow  = 10 * time.Second
)

// jumpFlashState marks one delegation card whose header row renders highlighted.
type jumpFlashState struct {
	dd *delegationDisplayState // nil when no flash is active
	on bool                    // true during the highlighted half-phase
}

// jumpFlashTickMsg advances the header flash. A tick whose epoch is stale (a
// newer jump or a clear happened since it was armed) is ignored.
type jumpFlashTickMsg struct{ epoch int }

// delegationTopRow returns the segment index and the row within that segment of the
// target card's top edge (D2): row 0 for a standalone segmentDelegation; for a
// segmentDelegationGroup member i, row 0 when i == 0, else the divider row directly
// above member i's header. ok is false when the occurrence has no card, the locator's
// segment index is out of range, or the segment does not hold dd.
func (b *contentBuffer) delegationTopRow(key occurrenceKey, width int) (seg, row int, dd *delegationDisplayState, ok bool) {
	loc, found := b.delegations[key]
	if !found || loc.dd == nil {
		return 0, 0, nil, false
	}
	dd = loc.dd
	seg = loc.seg
	if seg < 0 || seg >= len(b.segments) || !segmentHoldsDelegation(b.segments[seg], dd) {
		seg = findDelegationSegment(b.segments, dd)
		if seg < 0 {
			return 0, 0, nil, false
		}
	}
	s := b.segments[seg]
	if s.delegData == dd {
		return seg, 0, dd, true
	}
	if s.delegGroupData == nil {
		return 0, 0, nil, false
	}
	// Mirrors delegationGroupEntryAtRow: member 0 starts at segment row 1 and
	// each later member follows the previous one's rows plus a divider.
	header := 1
	for i, entry := range s.delegGroupData.entries {
		if entry == dd {
			if i == 0 {
				return seg, 0, dd, true
			}
			return seg, header - 1, dd, true
		}
		header += len(b.delegationContentRows(entry, width)) + 1
	}
	return 0, 0, nil, false
}

// dirtyDelegationCard re-renders the segment holding dd on the next sync.
func (b *contentBuffer) dirtyDelegationCard(dd *delegationDisplayState) {
	if dd == nil {
		return
	}
	if i := findDelegationSegment(b.segments, dd); i >= 0 {
		b.segments[i].renderDirty = true
	}
	b.gen++
}

// jumpToDelegation scrolls the target card's top edge to the first viewport row,
// disables follow-to-bottom, flashes its header, and arms the expand window.
// It returns nil and changes nothing when the card cannot be resolved (D9).
func (m *Model) jumpToDelegation(key occurrenceKey) tea.Cmd {
	seg, row, dd, ok := m.content.delegationTopRow(key, m.viewport.Width())
	if !ok {
		return nil
	}
	prevAuto, prevOffset := m.autoScroll, m.viewport.YOffset()
	m.autoScroll = false
	m.syncViewport() // segmentHeights must be current before mapping the row
	line, ok := m.content.contentLineForSegmentRow(seg, row)
	if !ok {
		m.autoScroll = prevAuto
		m.viewport.SetYOffset(prevOffset)
		return nil
	}
	m.viewport.SetYOffset(line + m.contentTopPad)

	m.content.dirtyDelegationCard(m.content.jumpFlash.dd)
	m.content.jumpFlash = jumpFlashState{dd: dd, on: true}
	m.content.dirtyDelegationCard(dd)
	m.syncViewport()

	m.jumpTarget = key
	m.jumpAt = time.Now()
	m.jumpFlashEpoch++
	m.jumpFlashLeft = jumpFlashPhases - 1
	return m.jumpFlashTick()
}

func (m *Model) jumpFlashTick() tea.Cmd {
	epoch := m.jumpFlashEpoch
	return tea.Tick(jumpFlashInterval, func(time.Time) tea.Msg { return jumpFlashTickMsg{epoch: epoch} })
}

func (m *Model) handleJumpFlashTick(msg jumpFlashTickMsg) (tea.Model, tea.Cmd) {
	if msg.epoch != m.jumpFlashEpoch || m.content.jumpFlash.dd == nil {
		return m, nil
	}
	dd := m.content.jumpFlash.dd
	if m.jumpFlashLeft <= 0 {
		m.content.jumpFlash = jumpFlashState{}
		m.content.dirtyDelegationCard(dd)
		m.syncViewport()
		return m, nil
	}
	m.content.jumpFlash.on = !m.content.jumpFlash.on
	m.jumpFlashLeft--
	m.content.dirtyDelegationCard(dd)
	m.syncViewport()
	return m, m.jumpFlashTick()
}

// toggleJumpTargetCollapse flips collapsed on the last jump target when within
// jumpExpandWindow of the jump. Reports whether it toggled.
func (m *Model) toggleJumpTargetCollapse() bool {
	if m.jumpTarget == (occurrenceKey{}) || time.Since(m.jumpAt) > jumpExpandWindow {
		return false
	}
	_, _, dd, ok := m.content.delegationTopRow(m.jumpTarget, m.viewport.Width())
	if !ok {
		return false
	}
	dd.collapsed = !dd.collapsed
	m.content.dirtyDelegationCard(dd)
	m.syncViewport()
	return true
}
