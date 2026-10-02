package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

// appendSubAgentsDeliveredEvent records the delivery of sub-agent results as a
// "sub-agent finished" row.
func (b *contentBuffer) appendSubAgentsDeliveredEvent(event output.Event) {
	payload, ok := event.Payload.(output.SubAgentsDeliveredEvent)
	if !ok || len(payload.Items) == 0 {
		return
	}
	b.finishStreaming()
	for _, item := range payload.Items {
		b.settleLostDelivery(item)
	}
	rows := buildDeliveredRows(payload.Items, b.deliveredLookup)
	b.segments = append(b.segments, contentSegment{
		kind:          segmentSubAgentsFinished,
		timestamp:     timeNow(),
		deliveredData: &rows,
		renderDirty:   true,
	})
}

// deliveredLookup resolves the group label, group size, failure reason and
// elapsed text for a delivered item from the delegation boxes in the buffer.
func (b *contentBuffer) deliveredLookup(item output.DeliveredSubAgent) deliveredLookup {
	target, found := b.deliveredTarget(item)
	if !found {
		return deliveredLookup{}
	}
	dd := target.dd
	info := deliveredLookup{group: dd.acceptedGroup(), reason: dd.failureReason, elapsed: dd.elapsed}
	if key, valid := acceptedMembership(dd); valid {
		b.forEachDelegationReverse(func(loc delegationLocator) bool {
			if otherKey, ok := acceptedMembership(loc.dd); ok && otherKey == key {
				info.groupSize++
			}
			return false
		})
	}
	return info
}

// deliveredTarget resolves the card a delivered item reports on: its
// occurrence's card, or for an item without a parent call ID the agent's
// newest card.
func (b *contentBuffer) deliveredTarget(item output.DeliveredSubAgent) (delegationLocator, bool) {
	if item.ParentCallID != "" {
		return b.lookupOccurrence(occurrenceKey{BatchID: item.BatchID, CallID: item.ParentCallID})
	}
	if item.AgentID == "" {
		return delegationLocator{}, false
	}
	if loc, active := b.activeDelegations[item.AgentID]; active && loc.dd != nil {
		return loc, true
	}
	return b.findDelegation(item.AgentID)
}

func (b *contentBuffer) settleLostDelivery(item output.DeliveredSubAgent) {
	if strings.TrimSpace(item.Status) != "lost" {
		return
	}
	target, found := b.deliveredTarget(item)
	if !found || target.dd.status != "active" {
		return
	}
	markLostDelivery(target.dd, item)
	b.forgetDelegation(target.dd)
	b.markDelegationDirty(target.seg)
}

// markLostDelivery applies the terminal lost state for a settled delivery.
func markLostDelivery(dd *delegationDisplayState, item output.DeliveredSubAgent) {
	dd.status = "failed"
	dd.resultStatus = "lost"
	if dd.failureReason == "" {
		dd.failureReason = "session restarted"
	}
	dd.queuedForSlot = false
	dd.cacheWaiting = false
	if dd.elapsed == "" && dd.startTime > 0 {
		dd.elapsed = formatElapsed(dd.startTime, nanoNow())
	}
	dd.fillFromTerminalEvent(item.AgentType, item.DurationMs)
}

// renderSubAgentsFinishedSegment renders the delivery row: a bullet, an
// accent-coloured tag, the summary and a right-aligned timestamp, with one
// indented line per member when several results arrived together.
func (b *contentBuffer) renderSubAgentsFinishedSegment(segment contentSegment, width int) string {
	rows := segment.deliveredData
	if rows == nil || len(rows.members) == 0 {
		return ""
	}
	_, border := b.delegationStyles("")
	tag := lipgloss.NewStyle().Foreground(border.GetForeground()).Bold(true).Render(rows.tag)
	sep := b.styles.FgMute.Render("·")
	head := b.styles.FgMute.Render("○") + "  " + tag + " " + sep + " "

	timestamp := b.renderStatusTimestamp(segment, width)
	topMargin := ""
	if timestamp != "" {
		topMargin = "\n"
	}

	var sb strings.Builder
	sb.WriteString(topMargin)
	if rows.header == "" {
		first := head + b.renderDeliveredMember(rows.members[0], width-lipgloss.Width(head))
		sb.WriteString(b.appendRightAlignedTimestamp(first, timestamp, width) + "\n")
		return sb.String()
	}
	sb.WriteString(b.appendRightAlignedTimestamp(head+b.baseTextStyle().Render(rows.header), timestamp, width) + "\n")
	const indent = "      "
	for _, m := range rows.members {
		sb.WriteString(indent + b.renderDeliveredMember(m, width-len(indent)) + "\n")
	}
	return sb.String()
}

func (b *contentBuffer) renderDeliveredMember(m deliveredMember, avail int) string {
	outcomeStyle := b.styles.SuccessStyle
	if m.bad {
		outcomeStyle = b.styles.ErrorStyle
	}
	sep := " " + b.styles.FgMute.Render("·") + " "
	line := b.baseTextStyle().Render(m.label) + " " + outcomeStyle.Render(m.outcome)
	if m.duration != "" {
		line += sep + b.styles.FgDim.Render(m.duration)
	}
	if m.reason != "" {
		const dash = " — "
		room := avail - lipgloss.Width(line) - len(dash) - 12 // leave room for a right-aligned timestamp on single rows
		if room >= 8 {
			line += b.styles.FgDim.Render(dash + truncateRunes(m.reason, room))
		}
	}
	return line
}
