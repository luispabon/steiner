package lsp

import (
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
	// LastUsed is when the server was last accessed (updated on every sessionFor hit).
	LastUsed time.Time
}
