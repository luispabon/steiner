package tui

import (
	"fmt"
	"strings"
)

// recomputeLSPAggregate derives the scalar LSP fields from s.lspServers,
// deduping sessions by server name. A server counts as active if any of its
// sessions is "starting", "ready" or "failed"; "stopped" and anything else
// do not count. lspServers itself is excluded from sidebarStateComparable
// (it's a slice), so lspRow must read only these precomputed scalars —
// otherwise a change to lspServers with no scalar change would silently
// serve a stale cached sidebar frame.
func (s *sidebarState) recomputeLSPAggregate() {
	type agg struct {
		starting, ready, failed bool
	}
	byName := make(map[string]*agg)
	var order []string
	for _, sess := range s.lspServers {
		a, ok := byName[sess.Name]
		if !ok {
			a = &agg{}
			byName[sess.Name] = a
			order = append(order, sess.Name)
		}
		switch sess.Status {
		case "starting":
			a.starting = true
		case "ready":
			a.ready = true
		case "failed":
			a.failed = true
		}
	}

	var activeNames []string
	activeCount := 0 // N: servers with >=1 ready session
	totalKnown := 0  // M: servers with >=1 active session this poll
	anyStarting := false
	anyFailed := false
	for _, name := range order {
		a := byName[name]
		if !a.starting && !a.ready && !a.failed {
			continue
		}
		totalKnown++
		activeNames = append(activeNames, name)
		if a.failed {
			anyFailed = true
		}
		if a.starting {
			anyStarting = true
		}
		if a.ready {
			activeCount++
		}
	}

	s.lspActive = activeCount
	s.lspTotalKnown = totalKnown
	s.lspStarting = anyStarting
	s.lspFailed = anyFailed
	s.lspSingleName = strings.Join(activeNames, ",")
}

// lspRow renders the sidebar's LSP status as two unstyled pieces: a spinner
// frame (empty when settled, spinning frame when any active server is
// starting) and a text piece — either the comma-joined active server names
// (if they fit width) or an "N/M" fallback count. Returns ("", "") when no
// server has an active session. The caller applies styling to each piece.
func (s sidebarState) lspRow(width int) (spinner, text string) {
	if s.lspTotalKnown == 0 {
		return "", ""
	}
	if len(s.lspSingleName) <= width {
		text = s.lspSingleName
	} else {
		text = fmt.Sprintf("%d/%d", s.lspActive, s.lspTotalKnown)
	}
	if s.lspStarting {
		spinner = spinnerFrames[s.tickCount%len(spinnerFrames)]
	}
	return spinner, text
}
