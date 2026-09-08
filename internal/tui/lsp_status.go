package tui

import (
	"cmp"
	"slices"
	"time"
)

// LSPServerStatus is the TUI's display-only view of one LSP server session
// (a server+root pair). Unlike MCP, an LSP server can have multiple sessions
// active at once (one per workspace root), so the sidebar and overlay key on
// Name for aggregation but the overlay lists sessions individually by
// (Name, Root).
type LSPServerStatus struct {
	// Name is the configured server name (the config map key).
	Name string
	// Root is the resolved workspace root for this session.
	Root string
	// Status is the session's lifecycle state, e.g. "declared", "starting",
	// "ready", "failed", "stopped", or "not started". Held as a plain string
	// so this package need not import internal/lsp.
	Status string
	// Error is the failure text; failed only.
	Error string
	// StartedAt is when the session was started.
	StartedAt time.Time
	// LastUsed is when the session was last used to serve a request.
	LastUsed time.Time
}

// sortLSPServerStatuses returns a new slice sorted by (Name, Root). The
// manager's ServerStates() iterates a map internally, so callers must sort
// before use to keep the sidebar aggregation and /lsp overlay ordering
// stable across polls.
func sortLSPServerStatuses(states []LSPServerStatus) []LSPServerStatus {
	sorted := slices.Clone(states)
	slices.SortFunc(sorted, func(a, b LSPServerStatus) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Root, b.Root))
	})
	return sorted
}
