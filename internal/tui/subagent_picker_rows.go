package tui

import (
	"strings"
)

// subAgentPickerRow is one display line of the sub-agent picker. Only card rows
// (neither section nor groupHdr) are selectable.
type subAgentPickerRow struct {
	section  bool          // "RUNNING"/"COMPLETED" heading row
	heading  string        // section or group heading text
	groupHdr bool          // "┌ name" group header row
	inGroup  bool          // member row rendered with the "│ " prefix
	key      occurrenceKey // jump target for card rows
	dd       *delegationDisplayState
}

func (r subAgentPickerRow) selectable() bool { return !r.section && !r.groupHdr }

type pickerMember struct {
	key occurrenceKey
	dd  *delegationDisplayState
}

type pickerUnit struct {
	seg     int
	group   string // empty for standalone cards
	grouped bool
	members []pickerMember
	running bool
	age     int64
}

// searchText is the lowercase haystack a query is matched against.
func (u pickerUnit) searchText(dd *delegationDisplayState) string {
	return strings.ToLower(strings.Join([]string{dd.effectiveTypeLabel(), dd.agentType, dd.agentID, u.group, dd.taskPreview}, " "))
}

// subAgentPickerRows builds layout-A rows from the conversation's occurrence index (D7).
func (b *contentBuffer) subAgentPickerRows(query string) []subAgentPickerRow {
	units := b.subAgentPickerUnits()
	q := strings.ToLower(strings.TrimSpace(query))

	var running, done []pickerUnit
	for _, u := range units {
		matched := u.members
		if q != "" {
			matched = nil
			for _, mem := range u.members {
				if strings.Contains(u.searchText(mem.dd), q) {
					matched = append(matched, mem)
				}
			}
			if len(matched) == 0 {
				continue
			}
		}
		// running was computed over all members so filtering never moves a group.
		u.members = matched
		if u.running {
			running = append(running, u)
		} else {
			done = append(done, u)
		}
	}

	var rows []subAgentPickerRow
	emit := func(heading string, list []pickerUnit) {
		if len(list) == 0 {
			return
		}
		rows = append(rows, subAgentPickerRow{section: true, heading: heading})
		for _, u := range list {
			if u.grouped {
				rows = append(rows, subAgentPickerRow{groupHdr: true, heading: u.group})
			}
			for _, mem := range u.members {
				rows = append(rows, subAgentPickerRow{inGroup: u.grouped, key: mem.key, dd: mem.dd})
			}
		}
	}
	emit("RUNNING", running)
	emit("COMPLETED", done)
	return rows
}
