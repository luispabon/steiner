package lsp

import (
	"sync"
	"time"
)

// ServerStatus describes the state of a language server process.
type ServerStatus string

const (
	// ServerStatusDeclared means the server is configured but not yet attempted.
	ServerStatusDeclared ServerStatus = "declared"
	// ServerStatusStarting means a spawn attempt is in flight.
	ServerStatusStarting ServerStatus = "starting"
	// ServerStatusReady means the server is connected and ready for requests.
	ServerStatusReady ServerStatus = "ready"
	// ServerStatusFailed means the spawn attempt failed.
	ServerStatusFailed ServerStatus = "failed"
	// ServerStatusStopped means the server was shut down (idle or Close).
	ServerStatusStopped ServerStatus = "stopped"
)

// ServerState describes one language server's live state.
type ServerState struct {
	// Name is the configured server name (the map key).
	Name string
	// Root is the workspace root resolved for this server.
	Root string
	// Status describes the current lifecycle state.
	Status ServerStatus
	// Err is the last failure reason (failed status only).
	Err error
	// StartedAt is when the spawn attempt started.
	StartedAt time.Time
	// LastUsed is when the server was last accessed: refreshed when a lookup
	// finds the entry and, again, when a document cycle releases its lease.
	LastUsed time.Time
}

// entryLease pins an entry's live session against the idle reaper for the
// duration of one document cycle. Callers get one from Manager.acquireEntry or
// Manager.acquireEntryForKey, hold it across readiness waiting, the cycle queue
// and the open, request and close cycle, and Release it once that whole cycle
// finishes. The reaper only claims entries with no outstanding leases.
// Manager.Close overrides leases: shutdown does not wait for them.
//
// The lease records the readiness tracker installed with the session it pinned,
// so readiness waiting observes that session and not a later respawn's tracker.
type entryLease struct {
	ent       *entry
	readiness *readiness
	once      sync.Once
}

// Release drops the lease. It is safe to call on a nil lease and more than
// once, so callers can defer it before checking the acquire error. Release
// refreshes LastUsed so a cycle that outlasted IdleTimeout is not reaped on the
// next tick.
func (l *entryLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.ent.mu.Lock()
		l.ent.leases--
		l.ent.state.LastUsed = time.Now()
		l.ent.mu.Unlock()
	})
}

// acquireLease pins the entry's session and its readiness tracker against the
// idle reaper. The caller must hold ent.mu, so the lease cannot race the
// reaper's zero-lease claim or a respawn replacing either field.
func (e *entry) acquireLease() *entryLease {
	e.leases++
	return &entryLease{ent: e, readiness: e.readiness}
}
