package mcp

import (
	"sort"

	"github.com/luispabon/steiner/internal/config"
)

// ServerStatus is the connection outcome for one configured MCP server.
type ServerStatus string

const (
	// ServerStatusConnected means the server's session was established.
	ServerStatusConnected ServerStatus = "connected"
	// ServerStatusFailed means the server was enabled but the connection attempt failed.
	ServerStatusFailed ServerStatus = "failed"
	// ServerStatusDisabled means the server (or MCP as a whole) is configured off.
	ServerStatusDisabled ServerStatus = "disabled"
)

// ServerState describes one configured MCP server after Connect processed it.
type ServerState struct {
	Name            string // config key
	Status          ServerStatus
	Transport       string   // "stdio" when the config leaves it empty
	Tools           []string // MCP-side tool names; connected only, may be empty
	Err             string   // failure text; failed only
	ProtocolVersion string   // negotiated version; connected only
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
