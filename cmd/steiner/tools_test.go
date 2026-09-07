package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/lsp"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// buildMCPFixture returns the path to the internal/mcp fixture server binary,
// compiled once per test process and shared by all callers.
func buildMCPFixture(t *testing.T) string {
	t.Helper()
	built := buildMCPFixtureBinaryOnce()
	if built.err != nil {
		t.Fatalf("%v", built.err)
	}
	return built.path
}

type builtMCPFixtureBinary struct {
	path string
	err  error
}

var buildMCPFixtureBinaryOnce = sync.OnceValue(func() builtMCPFixtureBinary {
	dir, err := os.MkdirTemp("", "steiner-mcp-fixture")
	if err != nil {
		return builtMCPFixtureBinary{err: fmt.Errorf("create fixture dir: %w", err)}
	}
	mcpFixtureBinaryDir = dir

	bin := filepath.Join(dir, "fixtureserver")
	cmd := exec.Command("go", "build", "-o", bin, "../../internal/mcp/testdata/fixtureserver") //nolint:noctx
	output, err := cmd.CombinedOutput()
	if err != nil {
		return builtMCPFixtureBinary{err: fmt.Errorf("build fixtureserver: %w: %s", err, strings.TrimSpace(string(output)))}
	}
	return builtMCPFixtureBinary{path: bin}
})

var mcpFixtureBinaryDir string

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
	enabled := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, mgr, nil)

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
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mcpFixtureManager(t), nil)

	for _, want := range []string{"bash", "read", "mcp__fixture__echo", "mcp__fixture__boom"} {
		if _, ok := registry.Get(want); !ok {
			t.Fatalf("registry missing %q; names: %v", want, registry.Names())
		}
	}
}

func TestSubAgentSubsetExcludesMCPToolsByDefault(t *testing.T) {
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mcpFixtureManager(t), nil)

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
	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr, nil)

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
		registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr, nil)

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
		registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, mgr, nil)

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
	registry := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, mgr, nil)

	exposure := BuildMCPExposure(registry.Definitions(), cfg.MCP.Servers)
	if want := []string{"mcp__fixture__big_output", "mcp__fixture__boom", "mcp__fixture__die", "mcp__fixture__echo", "mcp__fixture__readonly_echo", "mcp__fixture__sleep"}; !reflect.DeepEqual(exposure[delegation.AgentTypeResearch], want) {
		t.Fatalf("research exposure = %v, want %v", exposure[delegation.AgentTypeResearch], want)
	}

	runner := &capturingChildRunner{}
	def := delegation.SubAgentToolDef(delegation.SpecializedToolDeps{
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
	}, nil)
	if _, err := def.Handler(context.Background(), subAgentTask("research", "research the codebase", "initial context", "findings")); err != nil {
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

// lspHelperEnv is the environment variable that selects re-exec mode for the fake LSP server.
const lspHelperEnv = "STEINER_LSP_TEST_HELPER"

// TestLSPHelperProcess is not a test: it is the entry point of the child process
// spawned by lspFixtureManager, and does nothing in a normal test run.
func TestLSPHelperProcess(t *testing.T) {
	mode := os.Getenv(lspHelperEnv)
	switch mode {
	case "":
		// Normal test mode, do nothing.
	case "lsp":
		// Run as a fake LSP server.
		runLSPServerHelper()
	default:
		t.Fatalf("unknown lsp helper mode %q", mode)
	}
}

// runLSPServerHelper implements a minimal in-process LSP server for testing.
// It speaks the LSP protocol over stdin/stdout.
func runLSPServerHelper() {
	ctx := context.Background()
	stream := jsonrpc2.NewStream(&lspReadWriteCloser{r: os.Stdin, w: os.Stdout})
	fs := &minimalLSPServer{exited: make(chan struct{})}
	fs.onExit = func() {
		close(fs.exited)
	}
	_, serverConn, _ := protocol.NewServer(ctx, fs, stream)
	defer serverConn.Close()

	select {
	case <-fs.exited:
	}
}

// lspReadWriteCloser adapts stdin/stdout pipes to io.ReadWriteCloser.
type lspReadWriteCloser struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (rwc *lspReadWriteCloser) Read(b []byte) (int, error) {
	return rwc.r.Read(b)
}

func (rwc *lspReadWriteCloser) Write(b []byte) (int, error) {
	return rwc.w.Write(b)
}

func (rwc *lspReadWriteCloser) Close() error {
	_ = rwc.w.Close()
	return rwc.r.Close()
}

// minimalLSPServer is a minimal LSP server that implements just enough to complete
// the handshake and report its process ID.
type minimalLSPServer struct {
	protocol.UnimplementedServer
	exited chan struct{}
	onExit func()
}

func (s *minimalLSPServer) Initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	fmt.Fprintf(os.Stderr, "steiner-lsp-helper-pid=%d\n", os.Getpid())
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			DefinitionProvider: protocol.Boolean(true),
		},
	}, nil
}

func (s *minimalLSPServer) Initialized(context.Context, *protocol.InitializedParams) error {
	return nil
}

func (s *minimalLSPServer) Shutdown(context.Context) error {
	return nil
}

func (s *minimalLSPServer) Exit(context.Context) error {
	if s.onExit != nil {
		s.onExit()
	}
	return nil
}

// lspFixtureManagerWithPID spawns a fake LSP server process and returns a connected manager
// along with the server's process ID. It blocks until the server reaches ServerStatusReady.
func lspFixtureManagerWithPID(t *testing.T, cacheDir, workDir string) (*lsp.Manager, int) {
	t.Helper()
	stderrBuf := &bytes.Buffer{}
	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("2s"),
		CacheDir:     cacheDir,
		Servers: map[string]config.LSPServerConfig{
			"test": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".test"},
				RootMarkers:    []string{"root.marker"},
				Env:            map[string]string{lspHelperEnv: "lsp"},
			},
		},
	}

	mgr := lsp.NewManager(cfg, workDir, nil, func(string) {}, stderrBuf)
	t.Cleanup(func() { _ = mgr.Close() })

	// Trigger server startup by attempting a tool operation. This forces the
	// manager to spawn the server and complete the handshake.
	if err := os.WriteFile(filepath.Join(workDir, "test.test"), []byte("test"), 0o644); err != nil {
		t.Fatalf("create test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "root.marker"), []byte(""), 0o644); err != nil {
		t.Fatalf("create root marker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg := runtimeRegistryWithSinkAndMode(config.Config{
		Limits: config.LimitsConfig{ToolTimeoutDefault: config.MustDuration("30s")},
		LSP:    cfg,
		Tools:  map[string]config.ToolConfig{},
	}, workDir, nil, false, nil, nil, nil, mgr)

	def, ok := reg.Get("definitions")
	if !ok {
		t.Fatal("definitions tool not found in registry")
	}

	// Call the definitions tool to trigger server startup.
	_, _ = def.Handler(ctx, map[string]any{"file": filepath.Join(workDir, "test.test"), "line": float64(1), "column": float64(1)})

	// Verify the server reached ready status.
	states := mgr.ServerStates()
	if len(states) == 0 {
		t.Fatalf("no servers in manager after startup (stderr: %s)", stderrBuf.String())
	}
	if states[0].Status != lsp.ServerStatusReady {
		t.Logf("server stderr: %s", stderrBuf.String())
		t.Fatalf("server status = %v, want ServerStatusReady", states[0].Status)
	}

	// Extract PID from stderr output.
	pid, err := extractLSPHelperPID(stderrBuf.String())
	if err != nil {
		t.Fatalf("extract LSP helper PID: %v (stderr: %s)", err, stderrBuf.String())
	}

	return mgr, pid
}

// extractLSPHelperPID parses the PID from the stderr output of the LSP helper.
func extractLSPHelperPID(stderr string) (int, error) {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "steiner-lsp-helper-pid=") {
			pidStr := strings.TrimPrefix(line, "steiner-lsp-helper-pid=")
			var pid int
			_, err := fmt.Sscanf(pidStr, "%d", &pid)
			if err != nil {
				return 0, fmt.Errorf("parse PID: %w", err)
			}
			return pid, nil
		}
	}
	return 0, fmt.Errorf("PID not found in stderr")
}

// TestLSPToolsRegisteredWhenEnabledWithUnavailableServer verifies that LSP tools
// register unconditionally based on config, not server state. When lsp.enabled=true
// and a server's binary does not exist, all three LSP tools still register in the
// registry, and calling them returns unavailability messages without errors.
func TestLSPToolsRegisteredWhenEnabledWithUnavailableServer(t *testing.T) {
	cfg := registryTestConfig()
	cfg.LSP = config.LSPConfig{
		Enabled: true,
		Servers: map[string]config.LSPServerConfig{
			"nonexistent": {
				Enabled:        true,
				Command:        "/path/to/nonexistent/binary",
				FileExtensions: []string{".go"},
			},
		},
	}

	lspMgr := lsp.NewManager(cfg.LSP, t.TempDir(), nil, func(string) {}, io.Discard)
	defer lspMgr.Close()

	registry := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, lspMgr)

	// All three LSP tools must be registered despite server being unavailable.
	toolNames := registry.Names()
	for _, name := range []string{"definitions", "references", "diagnostics"} {
		def, ok := registry.Get(name)
		if !ok {
			t.Fatalf("LSP tool %q not registered; names: %v", name, toolNames)
		}
		// Verify that calling the tool returns an unavailability message, not an error.
		result, err := def.Handler(context.Background(), map[string]any{"file": "test.go", "line": float64(1), "column": float64(1)})
		if err != nil {
			t.Fatalf("tool %q returned error: %v", name, err)
		}
		if result == nil {
			t.Fatalf("tool %q returned nil result", name)
		}
		// The result should be a string message indicating unavailability.
		if _, ok := result.(string); !ok {
			t.Fatalf("tool %q returned non-string result: %T", name, result)
		}
	}
}

// TestLSPToolsNotRegisteredWhenDisabled verifies that when lsp.enabled=false,
// none of the three LSP tools appear in the registry.
func TestLSPToolsNotRegisteredWhenDisabled(t *testing.T) {
	cfg := registryTestConfig()
	cfg.LSP = config.LSPConfig{Enabled: false}

	registry := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, nil)

	toolNames := registry.Names()
	for _, name := range []string{"definitions", "references", "diagnostics"} {
		if _, ok := registry.Get(name); ok {
			t.Fatalf("LSP tool %q registered despite lsp.enabled=false; names: %v", name, toolNames)
		}
	}
}

// TestLSPRegistryOrderingDeterministic verifies that repeated registry constructions
// from identical config always yield identical tool-name ordering. This guards the
// prompt-cache invariant that tool defs must not churn per-turn.
func TestLSPRegistryOrderingDeterministic(t *testing.T) {
	cfg := registryTestConfig()
	cfg.LSP = config.LSPConfig{
		Enabled: true,
		Servers: map[string]config.LSPServerConfig{
			"dummy": {
				Enabled:        true,
				Command:        "/nonexistent",
				FileExtensions: []string{".go"},
			},
		},
	}

	// Build two registries from identical config.
	lspMgr1 := lsp.NewManager(cfg.LSP, t.TempDir(), nil, func(string) {}, io.Discard)
	defer lspMgr1.Close()
	reg1 := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, lspMgr1)
	names1 := reg1.Names()

	lspMgr2 := lsp.NewManager(cfg.LSP, t.TempDir(), nil, func(string) {}, io.Discard)
	defer lspMgr2.Close()
	reg2 := runtimeRegistryWithSinkAndMode(cfg, t.TempDir(), nil, false, nil, nil, nil, lspMgr2)
	names2 := reg2.Names()

	// Tool ordering must be identical (Registry.Names() sorts, so this tests
	// that the LSP tools always appear in the same position in the sorted list).
	if !slices.Equal(names1, names2) {
		t.Fatalf("registry ordering not deterministic:\n  first:  %v\n  second: %v", names1, names2)
	}

	// Additionally, verify that LSP tools appear in their expected order within
	// the full sorted list (definitions, references, diagnostics alphabetically).
	lspToolsInRegistry := make([]string, 0)
	for _, name := range names1 {
		if name == "definitions" || name == "references" || name == "diagnostics" {
			lspToolsInRegistry = append(lspToolsInRegistry, name)
		}
	}
	if want := []string{"definitions", "diagnostics", "references"}; !slices.Equal(lspToolsInRegistry, want) {
		t.Fatalf("LSP tools in wrong order: got %v, want %v", lspToolsInRegistry, want)
	}
}
