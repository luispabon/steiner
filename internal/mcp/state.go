package mcp

import (
	"sort"

	"github.com/luispabon/steiner/internal/config"
)

// ServerStatus is the connection outcome for one configured MCP server.
type ServerStatus string

const (
	// ServerStatusConnecting means the server's connection attempt is in flight.
	ServerStatusConnecting ServerStatus = "connecting"
	// ServerStatusConnected means the server's session was established.
	ServerStatusConnected ServerStatus = "connected"
	// ServerStatusFailed means the server was enabled but the connection attempt failed.
	ServerStatusFailed ServerStatus = "failed"
	// ServerStatusReconnecting means the session dropped and a reconnect is in flight.
	ServerStatusReconnecting ServerStatus = "reconnecting"
	// ServerStatusUnavailable means the server is not reachable for the current attempt.
	ServerStatusUnavailable ServerStatus = "unavailable"
	// ServerStatusDisabled means the server (or MCP as a whole) is configured off.
	ServerStatusDisabled ServerStatus = "disabled"
)

// ToolOutcome classifies one advertised MCP tool's fate after filtering.
type ToolOutcome int

const (
	// ToolRegistered means the tool passed filtering and a definition was registered.
	ToolRegistered ToolOutcome = iota
	// ToolFiltered means the tool was excluded by allowed_tools or blocked_tools.
	ToolFiltered
	// ToolDenied means the server's approval mode is "deny", so no tools register.
	ToolDenied
)

// AdvertisedTool records one tool a connected server advertised and its outcome
// after allow/block filtering or approval denial.
type AdvertisedTool struct {
	Name    string      // MCP-native advertised name
	Outcome ToolOutcome // registered, filtered, or denied
}

// ServerState describes one configured MCP server's live lifecycle state.
// Status is a snapshot: it starts as connecting and resolves to connected or
// failed once the connection attempt settles.
type ServerState struct {
	Name            string // config key
	Status          ServerStatus
	Transport       string           // "stdio" when the config leaves it empty
	Tools           []string         // registered MCP-side tool names; connected only, may be empty
	AdvertisedTools []AdvertisedTool // every advertised tool and its outcome; connected only
	Err             string           // failure text; failed only
	ProtocolVersion string           // negotiated version; connected only
}

// DeclaredStates returns a disabled ServerState for every server declared in cfg,
// in sorted name order. Used when MCP is switched off wholesale and no session exists.
func DeclaredStates(cfg config.MCPConfig) []ServerState {
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var states []ServerState
	for _, name := range names {
		transport := cfg.Servers[name].Transport
		if transport == "" {
			transport = "stdio"
		}
		states = append(states, ServerState{
			Name:      name,
			Status:    ServerStatusDisabled,
			Transport: transport,
		})
	}
	return states
}
