package main

import (
	"sync"

	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui"
)

// serverStatesSnapshot converts MCP manager server states into the output format.
// It applies mcpOutcomeLabel to tool outcomes and builds the servers map.
func serverStatesSnapshot(servers []tui.MCPServerStatus) map[string]output.MCPServerState {
	states := make(map[string]output.MCPServerState, len(servers))
	for _, s := range servers {
		tools := make([]output.MCPAdvertisedTool, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, output.MCPAdvertisedTool{Name: t.Name, Outcome: t.Outcome})
		}
		states[s.Name] = output.MCPServerState{
			State:     s.State,
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Error,
		}
	}
	return states
}

// emitMCPServerStatesSnapshot emits a snapshot of the current MCP server states
// without registry origins. This is used for pre-arm notifications while servers
// are connecting, before tool defs are registered.
func emitMCPServerStatesSnapshot(rt cliRuntime, sink output.EventSink) {
	if rt.mcpManager == nil {
		return
	}
	enabled := rt.cfg.MCP.Enabled
	states := rt.mcpManager.ServerStates()
	if !enabled {
		states = mcp.DeclaredStates(rt.cfg.MCP)
	}

	servers := make([]tui.MCPServerStatus, 0, len(states))
	for _, s := range states {
		tools := make([]tui.MCPToolStatus, 0, len(s.AdvertisedTools))
		for _, t := range s.AdvertisedTools {
			tools = append(tools, tui.MCPToolStatus{Name: t.Name, Outcome: mcpOutcomeLabel(t.Outcome)})
		}
		servers = append(servers, tui.MCPServerStatus{
			Name:      s.Name,
			State:     string(s.Status),
			Transport: s.Transport,
			Tools:     tools,
			Error:     s.Err,
		})
	}

	statesMap := serverStatesSnapshot(servers)
	sink.Emit(output.NewMCPStatusEvent(enabled, statesMap, nil))
}

// emitMCPStateSnapshot computes the current MCP display state and emits it as
// one immutable snapshot event on the given sink. The snapshot carries every
// server's live state plus the registry's MCP tool origins, so the TUI can
// rebuild its MCP surface from a single event without touching the manager or
// registry.
func emitMCPStateSnapshot(rt cliRuntime, sink output.EventSink) {
	if rt.mcpManager == nil {
		return
	}
	enabled, servers, origins := mcpTUIState(rt.cfg, rt.mcpManager, rt.registry)
	statesMap := serverStatesSnapshot(servers)
	toolOrigins := make(map[string]output.MCPToolOrigin, len(origins))
	for name, o := range origins {
		toolOrigins[name] = output.MCPToolOrigin{Server: o.Server, Tool: o.Tool}
	}
	sink.Emit(output.NewMCPStatusEvent(enabled, statesMap, toolOrigins))
}

// mcpStateProducer forwards MCP state-change notifications to different
// listeners depending on the armed status. Before arming, state changes invoke
// the preListener with states-only snapshots; after arming, they invoke the
// listener with full snapshots including registry origins. This allows the TUI
// to show connection progress before tool defs are registered, while ensuring
// it never observes a half-connected server set with incomplete tool origins.
type mcpStateProducer struct {
	mu          sync.Mutex
	armed       bool
	listener    func()
	preListener func()
}

// stateChanged is the onStateChange callback passed to mcp.Connect. It forwards
// to preListener when not armed, listener when armed.
func (p *mcpStateProducer) stateChanged() {
	p.mu.Lock()
	armed, listener, preListener := p.armed, p.listener, p.preListener
	p.mu.Unlock()
	if armed {
		if listener != nil {
			listener()
		}
	} else {
		if preListener != nil {
			preListener()
		}
	}
}

// setListener installs the full snapshot/transition listener. Safe to call
// before arm.
func (p *mcpStateProducer) setListener(listener func()) {
	p.mu.Lock()
	p.listener = listener
	p.mu.Unlock()
}

// setPreListener installs the states-only snapshot listener for pre-arm
// notifications. Safe to call before arm.
func (p *mcpStateProducer) setPreListener(listener func()) {
	p.mu.Lock()
	p.preListener = listener
	p.mu.Unlock()
}

// arm marks the producer armed and emits one consolidated snapshot of the
// current state. After arm, every manager state change forwards to the
// listener. Idempotent: arming twice emits one snapshot total.
func (p *mcpStateProducer) arm() {
	p.mu.Lock()
	if p.armed {
		p.mu.Unlock()
		return
	}
	p.armed = true
	listener := p.listener
	p.mu.Unlock()
	if listener != nil {
		listener()
	}
}
