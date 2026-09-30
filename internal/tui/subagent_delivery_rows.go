package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// deliveredLookup is what the transcript knows about one delivered sub-agent
// beyond the delivery event itself.
type deliveredLookup struct {
	group     string // sub_agent "group" label, "" when ungrouped
	groupSize int    // delegations in the transcript sharing that label
	reason    string // failure text captured from DelegationFailed, "" when unknown
	elapsed   string // formatted duration from the delegate box, used when the event carries none
}

// deliveredMember is one sub-agent line in a "sub-agent finished" row.
type deliveredMember struct {
	label    string // "<type> <id>"
	outcome  string // "✓ complete", "✗ failed", "? lost (session restarted)"
	bad      bool   // failed or lost: outcome renders in the error colour
	duration string
	reason   string // first line of the failure text, unclipped
}

// deliveredRows is the pure content of a "sub-agent finished" row.
type deliveredRows struct {
	tag     string // "sub-agent finished" or "sub-agents finished"
	header  string // one-line summary; empty for a single item
	members []deliveredMember
}

// buildDeliveredRows turns a delivery event into row content. lookup is
// consulted per item and may return the zero value.
func buildDeliveredRows(items []output.DeliveredSubAgent, lookup func(output.DeliveredSubAgent) deliveredLookup) deliveredRows {
	rows := deliveredRows{tag: "sub-agent finished"}
	if len(items) == 0 {
		return rows
	}
	shared := ""
	sharedSize := 0
	for i, item := range items {
		info := deliveredLookup{}
		if lookup != nil {
			info = lookup(item)
		}
		rows.members = append(rows.members, newDeliveredMember(item, info))
		switch {
		case i == 0:
			shared, sharedSize = info.group, info.groupSize
		case info.group != shared:
			shared = ""
		}
	}
	if len(items) == 1 {
		return rows
	}
	rows.tag = "sub-agents finished"
	if shared != "" {
		size := max(sharedSize, len(items))
		rows.header = fmt.Sprintf("group %s (%d of %d)", shared, len(items), size)
	} else {
		rows.header = fmt.Sprintf("%d of %d", len(items), len(items))
	}
	return rows
}

func newDeliveredMember(item output.DeliveredSubAgent, info deliveredLookup) deliveredMember {
	m := deliveredMember{label: strings.TrimSpace(item.AgentType + " " + item.AgentID)}
	switch strings.TrimSpace(item.Status) {
	case "", "complete":
		m.outcome = "✓ complete"
	case "lost":
		m.outcome, m.bad = "? lost (session restarted)", true
	case "failed":
		m.outcome, m.bad = "✗ failed", true
	default:
		m.outcome, m.bad = "✗ "+strings.TrimSpace(item.Status), true
	}
	if item.DurationMs > 0 {
		m.duration = formatElapsed(0, item.DurationMs*1_000_000)
	} else {
		m.duration = info.elapsed
	}
	if m.bad {
		m.reason = firstLine(info.reason)
	}
	return m
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
