package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

// allowApprover approves every request.
func allowApprover() tool.ApprovalResponder {
	return tool.ApprovalResponderFunc(func(_ context.Context, req tool.ApprovalRequest) error {
		req.Response <- tool.ApprovalResponse{Allow: true}
		return nil
	})
}

// TestCloseRuntimeTerminatesMCPServers proves closeRuntime invokes the MCP
// manager's Close: after teardown a connected server no longer serves calls.
func TestCloseRuntimeTerminatesMCPServers(t *testing.T) {
	mgr := mcpFixtureManager(t)
	// The manager is connected with a nil approver, which denies before the
	// session is reached. Wire an approving responder so the call actually
	// travels to the server and the assertion below can observe teardown.
	mgr.UpdateApprover(allowApprover())
	echo := findToolDef(t, mgr.ToolDefs(), "mcp__fixture__echo")

	// Sanity: the tool round-trips while the server is alive, so a failure
	// after closeRuntime is attributable to teardown and not to the fixture.
	env, err := echo.Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("echo before closeRuntime returned %v, want a live server", err)
	}
	envelope, isEnv := env.(tool.JSONEnvelope)
	if !isEnv || !envelope.OK {
		t.Fatalf("echo before closeRuntime = %#v, want an OK envelope", env)
	}

	rt := cliRuntime{
		mcpManager: mgr,
		events:     output.NoopSink{},
	}
	closeRuntime(&rt)

	env, err = echo.Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		return // transport failure: the server is gone, as required
	}
	envelope, isEnv = env.(tool.JSONEnvelope)
	if !isEnv {
		t.Fatalf("echo after closeRuntime returned %T with a nil error, want a transport failure or a non-OK envelope", env)
	}
	if envelope.OK {
		t.Fatal("MCP echo call succeeded after closeRuntime: manager Close was not invoked")
	}
}

// TestCloseRuntimeWithoutMCPIsSafe ensures teardown tolerates a nil manager and
// stays silent: with nothing to close, no close warning may be emitted.
func TestCloseRuntimeWithoutMCPIsSafe(t *testing.T) {
	var events []output.Event
	rt := cliRuntime{events: output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	})}

	closeRuntime(&rt)

	if len(events) != 0 {
		t.Fatalf("closeRuntime emitted %d events with no manager, want 0: %v", len(events), events)
	}
}

// TestBuildInteractiveRuntimeWiresMCPApprover pins the ordering inside
// buildInteractiveRuntime: the MCP approver must be installed BEFORE the
// registry is built. The registry copies ToolDef values (handler closures
// included) and the interactive runner later snapshots the runtime by value, so
// an approver wired afterwards never reaches the tools the agent actually runs.
// Before that fix the handler returned an approval_denied envelope immediately;
// with the approver wired it blocks on the interactive approval prompt instead.
func TestBuildInteractiveRuntimeWiresMCPApprover(t *testing.T) {
	sess, err := interactive.NewSession(interactive.Dependencies{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rt := cliRuntime{
		cfg:        registryTestConfig(),
		workDir:    t.TempDir(),
		events:     output.NoopSink{},
		mcpManager: mcpFixtureManager(t),
	}

	rt = buildInteractiveRuntime(rt, sess)

	def, ok := rt.registry.Get("mcp__fixture__echo")
	if !ok {
		t.Fatalf("registry missing mcp__fixture__echo; names: %v", rt.registry.Names())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		env any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		env, handlerErr := def.Handler(ctx, map[string]any{"text": "hi"})
		done <- outcome{env: env, err: handlerErr}
	}()

	select {
	case got := <-done:
		envelope, isEnv := got.env.(tool.JSONEnvelope)
		if isEnv && envelope.Error != nil && envelope.Error.Kind == "approval_denied" {
			t.Fatalf("MCP handler denied immediately (%s): no approver reached the registry's tool definitions", envelope.Error.Message)
		}
		t.Fatalf("MCP handler returned early: env = %#v, err = %v; want it pending on the approval prompt", got.env, got.err)
	case <-time.After(250 * time.Millisecond):
		// Still waiting on the interactive approval coordinator, which is only
		// possible when a non-nil approver was baked into the registered tool.
	}
}

// TestBuildInteractiveAppSeedsConfigWarnings pins the interactive delivery path
// for the project_context.max_tokens deprecation warning: buildInteractiveApp
// copies rt.configWarnings into the TUI config, and the TUI seeds those lines
// into the content view at startup.
func TestBuildInteractiveAppSeedsConfigWarnings(t *testing.T) {
	sess, err := interactive.NewSession(interactive.Dependencies{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	rt := cliRuntime{
		cfg:            testRuntimeConfig("test-model"),
		registry:       tool.NewRegistry(),
		workDir:        t.TempDir(),
		homeDir:        t.TempDir(),
		configWarnings: projectContextConfigWarnings(config.Config{ProjectContext: config.ProjectContextConfig{MaxTokens: 64}}),
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	app := buildInteractiveApp(cmd, &cliFlags{}, rt, sess)

	prog := app.NewProgram(
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithWindowSize(80, 24),
	)

	done := make(chan tea.Model, 1)
	go func() {
		final, _ := prog.Run()
		done <- final
	}()
	prog.Quit()

	select {
	case final := <-done:
		model, ok := final.(tui.Model)
		if !ok {
			t.Fatalf("final model type = %T, want tui.Model", final)
		}
		// Apply the window size directly so the render is deterministic
		// regardless of message ordering inside the program's event loop.
		sized, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		model, ok = sized.(tui.Model)
		if !ok {
			t.Fatalf("sized model type = %T, want tui.Model", sized)
		}
		if got := model.View().Content; !strings.Contains(got, "max_tokens is deprecated") {
			t.Fatalf("TUI view does not contain the config warning:\n%s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TUI program did not exit after Quit")
	}
}

// findToolDef returns the definition with the given registry name.
func findToolDef(t *testing.T, defs []tool.ToolDef, name string) tool.ToolDef {
	t.Helper()
	for _, d := range defs {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("tool %q not found in %v", name, defs)
	return tool.ToolDef{}
}
