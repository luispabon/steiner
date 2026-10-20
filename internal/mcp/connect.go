package mcp

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/luispabon/steiner/internal/config"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectOptions holds the optional settings for Connect. Its zero value is
// valid: no release hook, plan mode off, nil callbacks discard their messages,
// and a nil Stderr stays nil.
type ConnectOptions struct {
	// Release frees resources owned by a wrapped server command. Optional.
	Release ReleaseFn
	// PlanMode marks the session as running in plan mode.
	PlanMode bool
	// WarnFn receives connection failures. Optional.
	WarnFn func(string)
	// InfoFn receives successful connects and their negotiated protocol
	// version. Optional.
	InfoFn func(string)
	// Stderr becomes each server process's stderr. Connect serialises writes
	// to it, so callers may pass an unsynchronised writer. Optional.
	Stderr io.Writer
	// OnStateChange is invoked after each recorded state transition. Optional.
	OnStateChange func()
}

// Connect starts a connection attempt for every enabled server in cfg and
// returns a Manager immediately. Each enabled server connects on its own
// goroutine, bounded by its per-server ConnectTimeout, so startup latency is
// bounded by the slowest server rather than the sum. Call WaitInit before
// consuming ToolDefs or ServerStates if the resolved (connected/failed) view
// is required; meanwhile ServerStates reports connecting entries.
//
// The reporting channels are distinct so callers can pick a severity per
// channel: warnFn receives connection failures, infoFn receives successful
// connects and their negotiated protocol version. Both are optional; a nil
// callback discards its messages. onStateChange, also optional, is invoked
// after each recorded state transition.
//
// stderr is not a reporting channel: it becomes each server process's own
// stderr, which exec copies into from a goroutine per process. Every connected
// server therefore writes to it concurrently for the whole session, so Connect
// serialises those writes and callers may pass an ordinary unsynchronised
// writer. Connect never writes to it directly.
//
// Connect takes no approver: tool definitions resolve the approver lazily via
// the Manager at call time, so definitions created before UpdateApprover still
// work once the approver is installed. limits is captured at connect time and
// bounds each MCP tool call's output and duration at call time (D19).
//
// Connect never returns an error for a server failure; a failed server is
// simply marked failed.
func Connect(ctx context.Context, cfg config.MCPConfig, limits config.LimitsConfig, wrap WrapFn, opts ConnectOptions) *Manager {
	release := opts.Release
	planMode := opts.PlanMode
	warnFn := opts.WarnFn
	infoFn := opts.InfoFn
	stderr := opts.Stderr
	onStateChange := opts.OnStateChange
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{
		planMode: planMode,
		ctx:      ctx,
		cancel:   cancel,
		initDone: make(chan struct{}),
	}

	if warnFn == nil {
		warnFn = func(string) {}
	}
	if infoFn == nil {
		infoFn = func(string) {}
	}
	if onStateChange == nil {
		onStateChange = func() {}
	}
	if stderr != nil {
		stderr = &syncWriter{w: stderr}
	}

	if !cfg.Enabled {
		m.states = DeclaredStates(cfg)
		return m
	}

	if len(cfg.Servers) == 0 {
		return m
	}

	// Iterate servers in sorted key order for determinism.
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	// Pre-record every configured server in sorted order so ServerStates is
	// stable: enabled servers start as connecting and their goroutine later
	// transitions the same entry to connected or failed in place.
	for _, name := range names {
		srv := cfg.Servers[name]
		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}
		if !srv.Enabled {
			m.setServerState(ServerState{Name: name, Status: ServerStatusDisabled, Transport: transport})
			continue
		}
		m.setServerState(ServerState{Name: name, Status: ServerStatusConnecting, Transport: transport})
	}

	for _, name := range names {
		srv := cfg.Servers[name]
		if !srv.Enabled {
			continue
		}
		transport := srv.Transport
		if transport == "" {
			transport = "stdio"
		}
		spec := ServerSpec{
			Name:      name,
			Transport: transport,
			Command:   srv.Command,
			Args:      srv.Args,
			Env:       srv.Env,
			URL:       srv.URL,
			Headers:   srv.Headers,
		}
		m.connectWG.Add(1)
		go m.connectServer(spec, srv, transport, limits, wrap, release, warnFn, infoFn, stderr, onStateChange)
	}

	return m
}

// sortedUnique returns names sorted lexically with duplicates removed, so
// unknown-reference warnings are emitted once each in a deterministic order
// (D11) regardless of the configured list's order.
func sortedUnique(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := append([]string(nil), names...)
	sort.Strings(out)
	return slices.Compact(out)
}

// filterTools applies allow-then-block filtering (D1, D2) to the advertised
// tools and returns, in advertised order, every tool with its outcome plus the
// passing tool pointers for def creation. A nil AllowedTools means no allowlist
// restriction; an explicitly configured empty one filters all tools (D12).
// Unknown allow/block references are non-fatal diagnostics reported once each
// in sorted order through warnFn (D3).
func filterTools(advertised []*mcpsdk.Tool, srv config.MCPServerConfig, serverName string, warnFn func(string)) (outcomes []AdvertisedTool, passing []*mcpsdk.Tool) {
	advertisedSet := make(map[string]struct{}, len(advertised))
	for _, t := range advertised {
		advertisedSet[t.Name] = struct{}{}
	}

	allowed := make(map[string]struct{}, len(srv.AllowedTools))
	for _, name := range srv.AllowedTools {
		allowed[name] = struct{}{}
	}
	blocked := make(map[string]struct{}, len(srv.BlockedTools))
	for _, name := range srv.BlockedTools {
		blocked[name] = struct{}{}
	}

	for _, t := range advertised {
		if srv.AllowedTools != nil {
			if _, ok := allowed[t.Name]; !ok {
				outcomes = append(outcomes, AdvertisedTool{Name: t.Name, Outcome: ToolFiltered})
				continue
			}
		}
		if _, ok := blocked[t.Name]; ok {
			outcomes = append(outcomes, AdvertisedTool{Name: t.Name, Outcome: ToolFiltered})
			continue
		}
		outcomes = append(outcomes, AdvertisedTool{Name: t.Name, Outcome: ToolRegistered})
		passing = append(passing, t)
	}

	for _, name := range sortedUnique(srv.AllowedTools) {
		if _, ok := advertisedSet[name]; !ok {
			warnFn(fmt.Sprintf("allowed_tools: tool %q not advertised by server %q", name, serverName))
		}
	}
	for _, name := range sortedUnique(srv.BlockedTools) {
		if _, ok := advertisedSet[name]; !ok {
			warnFn(fmt.Sprintf("blocked_tools: tool %q not advertised by server %q", name, serverName))
		}
	}
	return outcomes, passing
}
