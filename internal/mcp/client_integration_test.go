//go:build linux

package mcp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/mcp"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
)

// TestStdio exercises a long-lived MCP stdio server across the whole connect,
// handshake, tool list, and tool call path, with and without the sandbox.
func TestStdio(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())

	t.Run("sandboxed_end_to_end", func(t *testing.T) {
		skipIfBwrapUnavailable(t)

		repoRoot := t.TempDir()
		sandboxTmp := t.TempDir()
		copyFile(t, fixtureBin, filepath.Join(sandboxTmp, "fixtureserver"))

		s := newSandbox(t, repoRoot, sandboxTmp)
		wrap := func(c *exec.Cmd) *exec.Cmd { return s.WrapCommandMode(c, true) }

		var stderr bytes.Buffer
		sess, err := mcp.ConnectSession(context.Background(), mcp.ServerSpec{
			Name:    "fixture",
			Command: "/tmp/fixtureserver", // sandboxTmp is bound at /tmp inside the sandbox
		}, wrap, &stderr, 30*time.Second)
		if err != nil {
			t.Fatalf("connect: %v\nstderr:\n%s", err, stderr.String())
		}
		defer sess.Close() //nolint:errcheck

		if got := sess.ProtocolVersion(); got != "2025-11-25" {
			t.Errorf("ProtocolVersion() = %q, want %q", got, "2025-11-25")
		}
		if tools := sess.Tools(); len(tools) != 6 {
			t.Fatalf("Tools() has %d entries, want 6", len(tools))
		}

		res, err := sess.Call(context.Background(), "echo", map[string]any{"text": "hi"})
		if err != nil {
			t.Fatalf("Call(echo): %v", err)
		}
		if res.IsError {
			t.Error("echo IsError = true, want false")
		}
		if got := textContent(t, res); got != "hi" {
			t.Errorf("echo text = %q, want %q", got, "hi")
		}

		res, err = sess.Call(context.Background(), "boom", nil)
		if err != nil {
			t.Fatalf("Call(boom) returned Go error %v, want nil with IsError=true", err)
		}
		if !res.IsError {
			t.Error("boom IsError = false, want true")
		}

		if err := sess.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	t.Run("sandboxed_project_is_read_only", func(t *testing.T) {
		skipIfBwrapUnavailable(t)

		repoRoot := t.TempDir()
		sandboxTmp := t.TempDir()
		copyFile(t, fixtureBin, filepath.Join(sandboxTmp, "fixtureserver"))
		// recordPath is the HOST path used to read the record back; the
		// fixture itself is given the SANDBOX-side path below, since
		// sandboxTmp is bind-mounted read-write at /tmp inside the sandbox
		// (internal/sandbox/mounts.go) while its host path resolves through
		// the read-only "--ro-bind / /" mount.
		recordPath := filepath.Join(sandboxTmp, "record.txt")

		s := newSandbox(t, repoRoot, sandboxTmp)
		wrap := func(c *exec.Cmd) *exec.Cmd {
			w := s.WrapCommandMode(c, true)
			// The sandbox env allowlist (sandbox.FilterEnv) strips arbitrary
			// vars, so the fixture's env vars are injected on the wrapped
			// command, which bwrap passes on to the fixture unchanged.
			// STEINER_FIXTURE_TOUCH deliberately stays on the host repoRoot
			// path: it is the read-only target being probed. STEINER_FIXTURE_RECORD
			// must be the sandbox-side /tmp path so the fixture can actually
			// write it.
			w.Env = append(w.Env,
				"STEINER_FIXTURE_TOUCH="+filepath.Join(repoRoot, "probe.txt"),
				"STEINER_FIXTURE_RECORD=/tmp/record.txt",
			)
			return w
		}

		var stderr bytes.Buffer
		sess, err := mcp.ConnectSession(context.Background(), mcp.ServerSpec{
			Name:    "fixture",
			Command: "/tmp/fixtureserver",
		}, wrap, &stderr, 30*time.Second)
		if err != nil {
			t.Fatalf("connect: %v\nstderr:\n%s", err, stderr.String())
		}
		defer sess.Close() //nolint:errcheck

		// The fixture records touch_error at startup, before the handshake
		// completes, so the record exists by the time Connect returns.
		record, err := os.ReadFile(recordPath)
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		if got := recordValue(t, string(record), "touch_error"); got == "ok" {
			t.Fatalf("touch_error = %q, want a read-only filesystem error", got)
		}
	})

	t.Run("unsandboxed_reaps_process_group", func(t *testing.T) {
		// This test MUST run unsandboxed (wrap == nil): bwrap's --unshare-all
		// implies --unshare-pid, so under the sandbox the namespace teardown
		// kills the grandchild regardless of whether applyProcessGroup works.
		// Only an unwrapped process proves that cancelling the session kills
		// the process group.
		childPIDFile := filepath.Join(t.TempDir(), "child.pid")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sess, err := mcp.ConnectSession(ctx, mcp.ServerSpec{
			Name:    "fixture",
			Command: fixtureBin,
			Env: map[string]string{
				"STEINER_FIXTURE_SPAWN_CHILD":    "1",
				"STEINER_FIXTURE_CHILD_PID_FILE": childPIDFile,
			},
		}, nil, io.Discard, 30*time.Second)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer sess.Close() //nolint:errcheck

		pidData, err := os.ReadFile(childPIDFile)
		if err != nil {
			t.Fatalf("read child pid file: %v", err)
		}
		childPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
		if err != nil {
			t.Fatalf("parse child pid %q: %v", pidData, err)
		}

		cancel()

		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH) {
				return // grandchild killed and reaped
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("grandchild %d still alive after cancelling the session", childPID)
	})

	t.Run("connect_timeout", func(t *testing.T) {
		// The deadline ctx also binds the transport, so the hung process is
		// killed at the deadline and teardown is fast; the timeout param is the
		// per-server connect_timeout under test.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		var stderr bytes.Buffer
		_, err := mcp.ConnectSession(ctx, mcp.ServerSpec{
			Name:    "fixture",
			Command: fixtureBin,
			Env: map[string]string{
				"STEINER_FIXTURE_STALL_HANDSHAKE": "1",
			},
		}, nil, &stderr, 500*time.Millisecond)
		if err == nil {
			t.Fatal("Connect succeeded, want error from the handshake timeout")
		}
	})
}

// TestIntegrationAnnotationAutoAllow drives the full Connect path against the
// fixture server with trust_annotations: true (step 9, D6): a readOnlyHint tool
// auto-allows with no approval prompt, while an unannotated tool still prompts.
func TestIntegrationAnnotationAutoAllow(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())
	approver := &recordingApprover{allow: true}
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: fixtureBin, Approval: "ask", TrustAnnotations: true},
		},
	}

	m := mcp.Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer m.Close() //nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}
	m.UpdateApprover(approver)

	// readonly_echo carries readOnlyHint, so trust_annotations auto-allows it:
	// the call reaches the server and returns the echoed text with no prompt.
	env, err := findToolDef(t, m.ToolDefs(), "mcp__fixture__readonly_echo").Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("readonly_echo returned Go error %v, want nil", err)
	}
	echo, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("readonly_echo result type = %T, want tool.JSONEnvelope", env)
	}
	if !echo.OK {
		t.Errorf("readonly_echo OK = false, want true (error: %+v)", echo.Error)
	}
	if got := echo.Result; got != "hi" {
		t.Errorf("readonly_echo result = %v, want %q", got, "hi")
	}
	if got := len(approver.reqs); got != 0 {
		t.Errorf("approver recorded %d requests, want 0 (readOnlyHint auto-allows)", got)
	}

	// boom has no hints (destructiveHint and openWorldHint default to true), so
	// it still prompts. The approving responder lets it through; the server's
	// deliberate failure proves the call was dispatched after approval.
	env, err = findToolDef(t, m.ToolDefs(), "mcp__fixture__boom").Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("boom returned Go error %v, want nil with an mcp_tool_error envelope", err)
	}
	boom, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("boom result type = %T, want tool.JSONEnvelope", env)
	}
	if boom.OK {
		t.Error("boom OK = true, want false")
	}
	if boom.Error == nil || boom.Error.Kind != "mcp_tool_error" {
		t.Errorf("boom error = %+v, want kind mcp_tool_error", boom.Error)
	}
	if got := len(approver.reqs); got != 1 {
		t.Errorf("approver recorded %d requests, want 1 (boom prompts)", got)
	}
	if got := approver.reqs[0].MCP.ToolName; got != "boom" {
		t.Errorf("approval request tool = %q, want %q", got, "boom")
	}
}

// TestIntegrationTrustAnnotationsFalse verifies that trust_annotations: false
// ignores readOnlyHint end to end: readonly_echo still prompts for approval.
func TestIntegrationTrustAnnotationsFalse(t *testing.T) {
	fixtureBin := buildFixture(t, t.TempDir())
	approver := &recordingApprover{allow: true}
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: fixtureBin, Approval: "ask", TrustAnnotations: false},
		},
	}

	m := mcp.Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer m.Close() //nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.WaitInit(ctx); err != nil {
		t.Fatalf("WaitInit: %v", err)
	}
	m.UpdateApprover(approver)

	// The prompt is answered with allow, so the call succeeds but the recorded
	// request proves approval was requested despite the readOnlyHint.
	env, err := findToolDef(t, m.ToolDefs(), "mcp__fixture__readonly_echo").Handler(context.Background(), map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("readonly_echo returned Go error %v, want nil", err)
	}
	echo, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("readonly_echo result type = %T, want tool.JSONEnvelope", env)
	}
	if !echo.OK {
		t.Errorf("readonly_echo OK = false, want true (error: %+v)", echo.Error)
	}
	if got := echo.Result; got != "hi" {
		t.Errorf("readonly_echo result = %v, want %q", got, "hi")
	}
	if got := len(approver.reqs); got != 1 {
		t.Errorf("approver recorded %d requests, want 1 (readOnlyHint ignored without trust_annotations)", got)
	}
	if got := approver.reqs[0].MCP.ToolName; got != "readonly_echo" {
		t.Errorf("approval request tool = %q, want %q", got, "readonly_echo")
	}
}

// recordingApprover records every approval request and answers with a fixed
// decision.
type recordingApprover struct {
	allow bool
	reqs  []tool.ApprovalRequest
}

func (r *recordingApprover) RequestApproval(_ context.Context, req tool.ApprovalRequest) error {
	r.reqs = append(r.reqs, req)
	req.Response <- tool.ApprovalResponse{Allow: r.allow}
	return nil
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

// buildFixture compiles the fixtureserver binary once per test run.
func buildFixture(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "fixtureserver")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fixtureserver") //nolint:noctx
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixtureserver: %v\n%s", err, out)
	}
	return bin
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// skipIfBwrapUnavailable mirrors the skip convention at
// internal/sandbox/sandbox_test.go:325.
func skipIfBwrapUnavailable(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("bwrap integration only runs on Linux")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
}

func newSandbox(t *testing.T, repoRoot, sandboxTmp string) *sandbox.Sandbox {
	t.Helper()
	cfg := config.SandboxConfig{Enabled: true}
	s := sandbox.New(cfg, config.PermissionsConfig{}, nil, repoRoot, repoRoot, t.TempDir(), sandboxTmp)
	if err := s.EnsureHome(); err != nil {
		t.Fatalf("ensure sandbox home: %v", err)
	}
	return s
}

// textContent returns the text of the first content entry of a tool result.
func textContent(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("content has %d entries, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content[0] type = %T, want *mcpsdk.TextContent", res.Content[0])
	}
	return tc.Text
}

// recordValue extracts the value of a key=value line from the fixture record.
func recordValue(t *testing.T, record, key string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(record), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && k == key {
			return v
		}
	}
	t.Fatalf("record %q has no %s key", record, key)
	return ""
}
