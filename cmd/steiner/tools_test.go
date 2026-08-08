package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// buildMCPFixture compiles the internal/mcp fixture server once per test.
func buildMCPFixture(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fixtureserver")
	cmd := exec.Command("go", "build", "-o", bin, "../../internal/mcp/testdata/fixtureserver") //nolint:noctx
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixtureserver: %v\n%s", err, out)
	}
	return bin
}

// mcpFixtureManager connects the fixture server and returns the manager.
func mcpFixtureManager(t *testing.T) *mcp.Manager {
	t.Helper()
	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: buildMCPFixture(t)},
		},
	}, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// registryTestConfig returns a minimal config with the default tool timeout set.
func registryTestConfig() config.Config {
	return config.Config{
		Limits: config.LimitsConfig{ToolTimeoutDefault: config.MustDuration("30s")},
		Tools:  map[string]config.ToolConfig{},
	}
}

func TestRuntimeRegistryWithNilManagerRegistersNoMCPTools(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, nil)
	for _, name := range registry.Names() {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("nil manager produced MCP tool %q", name)
		}
	}
}

// TestRuntimeRegistryMCPEnabledWithoutServersIsInert proves an enabled-but-empty
// MCP block registers no tools, and the always-run registry rebuild (runtime_build.go
// rebuilds whenever the manager is non-nil, and Connect returns a non-nil manager
// even with zero servers) yields the same tool set as MCP disabled.
func TestRuntimeRegistryMCPEnabledWithoutServersIsInert(t *testing.T) {
	cfg := registryTestConfig()
	cfg.MCP = config.MCPConfig{Enabled: true}
	mgr := mcp.Connect(context.Background(), cfg.MCP, cfg.Limits, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	t.Cleanup(func() { _ = mgr.Close() })
	enabled := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, mgr)

	disabledCfg := registryTestConfig()
	disabledCfg.MCP = config.MCPConfig{Enabled: false}
	disabled := runtimeRegistryWithSinkAndMode(disabledCfg, t.TempDir(), nil, false, nil, nil, nil, nil)

	for _, name := range enabled.Names() {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("enabled-but-empty MCP produced tool %q", name)
		}
	}
	if !slices.Equal(enabled.Names(), disabled.Names()) {
		t.Fatalf("tool names differ: enabled-but-empty = %v, disabled = %v", enabled.Names(), disabled.Names())
	}
}

func TestRuntimeRegistryRegistersMCPToolsAlongsideBuiltins(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mcpFixtureManager(t))

	for _, want := range []string{"bash", "read", "mcp__fixture__echo", "mcp__fixture__boom"} {
		if _, ok := registry.Get(want); !ok {
			t.Fatalf("registry missing %q; names: %v", want, registry.Names())
		}
	}
}

func TestSubAgentSubsetExcludesMCPToolsByDefault(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mcpFixtureManager(t))

	// Missing or empty sub_agents grants no MCP tools to any child (D6).
	exposure := BuildMCPExposure(registry.Definitions(), nil)
	if len(exposure) != 0 {
		t.Fatalf("exposure = %v, want empty", exposure)
	}
	for _, agentType := range delegation.AllAgentTypes() {
		subset := registry.Subset(delegation.AgentAllowedTools(agentType), nil)
		for _, name := range subset.Names() {
			if strings.HasPrefix(name, "mcp__") {
				t.Fatalf("%s subset contains MCP tool %q without sub_agents", agentType, name)
			}
		}
	}
}

func TestSubAgentSubsetResearchOnlyExposesMCPTools(t *testing.T) {
	srv := config.MCPServerConfig{Enabled: true, Approval: "ask", SubAgents: []string{"research"}}
	mgr := mcpFixtureManagerWithCfg(t, srv)
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mgr)

	cfg := registryTestConfig()
	cfg.MCP = config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": srv}}
	exposure := BuildMCPExposure(registry.Definitions(), cfg.MCP.Servers)

	// The research child merges its allowlist with the exposure; the code child
	// keeps its allowlist unchanged.
	research := slices.Concat(delegation.AgentAllowedTools(delegation.AgentTypeResearch), exposure[delegation.AgentTypeResearch])
	researchSubset := registry.Subset(research, nil)
	if _, ok := researchSubset.Get("mcp__fixture__echo"); !ok {
		t.Fatalf("research subset missing mcp__fixture__echo; names: %v", researchSubset.Names())
	}
	if _, ok := researchSubset.Get("mcp__fixture__boom"); !ok {
		t.Fatalf("research subset missing mcp__fixture__boom; names: %v", researchSubset.Names())
	}

	codeSubset := registry.Subset(delegation.AgentAllowedTools(delegation.AgentTypeCode), nil)
	if _, ok := codeSubset.Get("mcp__fixture__echo"); ok {
		t.Fatal("code subset unexpectedly contains mcp__fixture__echo")
	}
}

func TestSubAgentSubsetFilteredAndDeniedToolsAbsent(t *testing.T) {
	t.Run("filtered tools are absent from every child", func(t *testing.T) {
		srv := config.MCPServerConfig{Enabled: true, Approval: "ask", AllowedTools: []string{"echo"}, SubAgents: []string{"research"}}
		mgr := mcpFixtureManagerWithCfg(t, srv)
		registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mgr)

		cfg := registryTestConfig()
		cfg.MCP = config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": srv}}
		exposure := BuildMCPExposure(registry.Definitions(), cfg.MCP.Servers)
		if want := []string{"mcp__fixture__echo"}; !reflect.DeepEqual(exposure[delegation.AgentTypeResearch], want) {
			t.Fatalf("research exposure = %v, want only the filtered-in tool %v", exposure[delegation.AgentTypeResearch], want)
		}
		research := slices.Concat(delegation.AgentAllowedTools(delegation.AgentTypeResearch), exposure[delegation.AgentTypeResearch])
		if _, ok := registry.Subset(research, nil).Get("mcp__fixture__boom"); ok {
			t.Fatal("research subset contains filtered tool mcp__fixture__boom")
		}
	})

	t.Run("denied tools are absent from every child", func(t *testing.T) {
		srv := config.MCPServerConfig{Enabled: true, Approval: "deny", SubAgents: []string{"research"}}
		mgr := mcpFixtureManagerWithCfg(t, srv)
		registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, mgr)

		cfg := registryTestConfig()
		cfg.MCP = config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": srv}}
		exposure := BuildMCPExposure(registry.Definitions(), cfg.MCP.Servers)
		if len(exposure) != 0 {
			t.Fatalf("exposure = %v, want empty for denied server", exposure)
		}
	})
}

// capturingChildRunner records every child RunRequest and returns a completed
// state, so a delegate tool call can be triggered without a real provider.
type capturingChildRunner struct {
	reqs []agent.RunRequest
}

func (r *capturingChildRunner) Run(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
	r.reqs = append(r.reqs, req)
	return agent.RunState{
		Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "task result"}},
		TurnCount:    1,
		StopReason:   agent.StopReasonComplete,
	}, nil
}

// TestMCPToolExposedToChildStillRequiresApproval proves D10: an MCP tool
// exposed to a child via sub_agents is still gated by MCP approval. The child
// call goes through the same MCP handler, so the approval request is raised
// with MCPApprovalDetails and the emitted event carries the child agent scope.
func TestMCPToolExposedToChildStillRequiresApproval(t *testing.T) {
	srv := config.MCPServerConfig{Enabled: true, Approval: "ask", SubAgents: []string{"research"}}
	mgr := mcpFixtureManagerWithCfg(t, srv)

	// Production-equivalent approver wiring: an eventing approver over a
	// capturing sink, delegating to a recording inner responder.
	var requests []tool.ApprovalRequest
	var events []output.Event
	mgr.UpdateApprover(agent.NewEventingApprover(
		output.SinkFunc(func(e output.Event) { events = append(events, e) }),
		tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
			requests = append(requests, req)
			req.Response <- tool.ApprovalResponse{Allow: false}
			return nil
		}),
	))

	cfg := registryTestConfig()
	cfg.MCP = config.MCPConfig{Enabled: true, Servers: map[string]config.MCPServerConfig{"fixture": srv}}
	registry := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, mgr)

	exposure := BuildMCPExposure(registry.Definitions(), cfg.MCP.Servers)
	if want := []string{"mcp__fixture__big_output", "mcp__fixture__boom", "mcp__fixture__die", "mcp__fixture__echo", "mcp__fixture__readonly_echo", "mcp__fixture__sleep"}; !reflect.DeepEqual(exposure[delegation.AgentTypeResearch], want) {
		t.Fatalf("research exposure = %v, want %v", exposure[delegation.AgentTypeResearch], want)
	}

	runner := &capturingChildRunner{}
	def := delegation.SpecializedToolDef(delegation.AgentTypeResearch, delegation.SpecializedToolDeps{
		SubAgentHandlerDeps: delegation.SubAgentHandlerDeps{
			SubAgentCfg:       config.SubAgentConfig{},
			Provider:          stubProvider{},
			ParentReg:         registry,
			Runner:            runner,
			Events:            noopSink{},
			WorkDir:           t.TempDir(),
			HomeDir:           t.TempDir(),
			SessionStore:      delegation.NewSessionStore(),
			ExtraAllowedTools: exposure,
		},
	})
	if _, err := def.Handler(context.Background(), map[string]any{"task": "research the codebase"}); err != nil {
		t.Fatalf("research handler: %v", err)
	}
	if len(runner.reqs) == 0 {
		t.Fatal("research child run was never triggered")
	}
	childReq := runner.reqs[0]

	found := false
	for _, ts := range childReq.Tools {
		if ts.Function.Name == "mcp__fixture__echo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("child tools %v missing exposed mcp__fixture__echo", childReq.Tools)
	}

	// The child executes the exposed MCP tool through its executor. The MCP
	// handler must raise an approval request rather than auto-approving.
	if _, err := childReq.Executor.Execute(context.Background(), "mcp__fixture__echo", "call_1", map[string]any{"text": "hi"}); err != nil {
		t.Fatalf("child MCP tool execution: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("approval requests = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Kind != tool.ApprovalKindMCP {
		t.Errorf("approval kind = %q, want %q", req.Kind, tool.ApprovalKindMCP)
	}
	if req.MCP == nil || req.MCP.Server != "fixture" || req.MCP.ToolName != "echo" {
		t.Errorf("approval MCP details = %+v, want server=fixture tool=echo", req.MCP)
	}

	var requested *output.ApprovalEvent
	var scopeAgentID string
	for _, e := range events {
		if e.Type != output.EventTypeApprovalRequested {
			continue
		}
		if payload, ok := e.Payload.(output.ApprovalEvent); ok {
			requested = &payload
			scopeAgentID = e.Scope.AgentID
		}
	}
	if requested == nil {
		t.Fatal("no approval_requested event emitted")
	}
	if requested.Server != "fixture" || requested.ToolName != "echo" || requested.Kind != string(tool.ApprovalKindMCP) {
		t.Errorf("approval event = %+v, want server=fixture tool=echo kind=mcp", requested)
	}
	if scopeAgentID == "" {
		t.Error("approval event lacks child agent scope")
	}
}

// mcpFixtureManagerWithCfg connects the fixture server with the given server
// config (approval mode, filters, sub_agents) and returns the manager.
func mcpFixtureManagerWithCfg(t *testing.T, srv config.MCPServerConfig) *mcp.Manager {
	t.Helper()
	if srv.Command == "" {
		srv.Command = buildMCPFixture(t)
	}
	mgr := mcp.Connect(context.Background(), config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{"fixture": srv},
	}, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := mgr.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}
