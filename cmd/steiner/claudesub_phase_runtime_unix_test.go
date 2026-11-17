//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/provider"
)

// writeFakeClaudeCLI writes a shell script that answers the locator commands
// and replays one recorded turn when it receives the first user line. It keeps
// reading stdin afterwards, so the process stays reusable until teardown.
func writeFakeClaudeCLI(t *testing.T, dir string) {
	t.Helper()
	const script = `#!/bin/sh
case "$1" in
  --version) printf '%s\n' '2.1.300 (Claude Code)'; exit 0 ;;
  auth) printf '%s\n' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'; exit 0 ;;
esac
printf '%s\n' "$$" > "$STEINER_FAKE_CLAUDE_PID"
printf '%s\n' "$@" > "$STEINER_FAKE_CLAUDE_ARGS"
pwd > "$STEINER_FAKE_CLAUDE_CWD"
replied=0
while IFS= read -r line; do
  case "$line" in
    *'"type":"control_request"'*)
      case "$line" in
        *'"subtype":"get_usage"'*)
          printf '%s\n' get_usage >> "$STEINER_FAKE_CLAUDE_EVENTS"
          rid=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
          printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s","response":' "$rid"
          tr -d '\n' < "$STEINER_FAKE_CLAUDE_USAGE"
          printf '}}\n'
          ;;
        *)
          printf '%s\n' unexpected-control >> "$STEINER_FAKE_CLAUDE_EVENTS"
          ;;
      esac
      ;;
    *'"type":"user"'*)
      printf '%s\n' user >> "$STEINER_FAKE_CLAUDE_EVENTS"
      if [ "$replied" -eq 0 ]; then
        cat "$STEINER_FAKE_CLAUDE_TURN"
        replied=1
      fi
      ;;
  esac
done
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude CLI: %v", err)
	}
}

// TestPhaseRuntimeClaudeSubscriptionFakeCLITurn runs one phase-runtime provider
// turn through the real claude_subscription provider and a scripted CLI. It
// pins the phase runtime wiring: the resolved model, the worktree as the child
// working directory, and process teardown when the phase runtime closes.
func TestPhaseRuntimeClaudeSubscriptionFakeCLITurn(t *testing.T) {
	binDir := t.TempDir()
	writeFakeClaudeCLI(t, binDir)
	projectRoot := t.TempDir()
	home := t.TempDir()
	markers := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	pidPath := filepath.Join(markers, "pid")
	argsPath := filepath.Join(markers, "args")
	cwdPath := filepath.Join(markers, "cwd")
	eventsPath := filepath.Join(markers, "events")
	t.Setenv("STEINER_FAKE_CLAUDE_PID", pidPath)
	t.Setenv("STEINER_FAKE_CLAUDE_ARGS", argsPath)
	t.Setenv("STEINER_FAKE_CLAUDE_CWD", cwdPath)
	t.Setenv("STEINER_FAKE_CLAUDE_EVENTS", eventsPath)
	turnPath, err := filepath.Abs(filepath.Join("..", "..", "internal", "provider", "testdata", "claudesub", "turn_text.jsonl"))
	if err != nil {
		t.Fatalf("resolve turn fixture: %v", err)
	}
	t.Setenv("STEINER_FAKE_CLAUDE_TURN", turnPath)
	usagePath, err := filepath.Abs(filepath.Join("..", "..", "internal", "provider", "testdata", "claudesub", "get_usage_ok.json"))
	if err != nil {
		t.Fatalf("resolve usage fixture: %v", err)
	}
	t.Setenv("STEINER_FAKE_CLAUDE_USAGE", usagePath)

	configPath := filepath.Join(projectRoot, "config.yaml")
	writeFile(t, configPath, `providers:
  claude:
    type: claude_subscription
models:
  profiles:
    default:
      default_model: claude
  definitions:
    claude:
      provider: claude
      id: claude-sonnet
`)
	worktree := oneshot.RunIdentity{ID: "run-abc"}.WorktreePath(projectRoot)
	mustMkdirAll(t, worktree)

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	flags := &cliFlags{configPath: configPath, trustProjectConfig: true, exec: true}
	factory := phaseRunnerFactory{cmd: cmd, flags: flags, rootDir: projectRoot, identity: oneshot.RunIdentity{ID: "run-abc"}}
	runner, err := factory.NewPhaseRunner(context.Background(), oneshot.PhaseImplement, "claude", nil, config.AdvisorConfig{})
	if err != nil {
		t.Fatalf("NewPhaseRunner() error = %v", err)
	}
	phase := runner.(phaseRunner)
	rt := &phase.runner.runtime
	t.Cleanup(func() { closeRuntime(rt) })
	if got, want := rt.workDir, worktree; got != want {
		t.Fatalf("phase runtime workDir = %q, want worktree %q", got, want)
	}
	// Keep metadata resolution offline and deterministic: the transport for a
	// claude_subscription provider is fixed, so no catalog or models.dev facts
	// are needed.
	rt.modelResolver = provider.NewResolver(provider.ResolverOptions{
		ModelsDev: func(context.Context) metadata.LoadResult { return metadata.LoadResult{} },
	})
	rm, err := rt.resolveModel("claude")
	if err != nil {
		t.Fatalf("resolveModel(claude) error = %v", err)
	}
	if got, want := rm.EffectiveProviderType, config.ProviderTypeClaudeSubscription; got != want {
		t.Fatalf("resolved effective provider type = %q, want %q", got, want)
	}

	p, err := rt.providerFactory(rm, "run-abc")
	if err != nil {
		t.Fatalf("providerFactory() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := p.ChatCompletion(ctx, provider.ChatRequest{
		Model:            rm.BackendModelID,
		Messages:         []provider.Message{{Role: provider.MessageRoleUser, Content: "implement the plan"}},
		TransportSession: "run-abc",
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error = %v", err)
	}
	if got := resp.Message.Content; !strings.Contains(got, "Hello world") {
		t.Fatalf("response content = %q, want the scripted turn text", got)
	}

	cwd := canonicalPath(t, strings.TrimSpace(readFile(t, cwdPath)))
	if want := canonicalPath(t, worktree); cwd != want {
		t.Fatalf("claude CLI working directory = %q, want worktree %q", cwd, want)
	}
	assertUsageGateBeforeUserTurn(t, readFile(t, eventsPath))
	argv := strings.Fields(readFile(t, argsPath))
	if !containsAdjacent(argv, "--model", "claude-sonnet") {
		t.Fatalf("claude CLI argv = %v, want --model claude-sonnet", argv)
	}
	pid := readPID(t, pidPath)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("claude CLI pid %d not running during the turn: %v", pid, err)
	}

	closeRuntime(rt)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("claude CLI pid %d still running after phase runtime close: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// canonicalPath resolves symlinks so the worktree reported by the test (for
// example under /var on macOS) compares equal to the child's cwd.
func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	return resolved
}

// assertUsageGateBeforeUserTurn proves the CLI received the get_usage control
// request before any user turn, and no other control request.
func assertUsageGateBeforeUserTurn(t *testing.T, events string) {
	t.Helper()
	firstUsage, firstUser := -1, -1
	for i, line := range strings.Fields(events) {
		switch line {
		case "get_usage":
			if firstUsage < 0 {
				firstUsage = i
			}
		case "user":
			if firstUser < 0 {
				firstUser = i
			}
		default:
			t.Fatalf("fake claude CLI received unexpected line %q", line)
		}
	}
	if firstUsage < 0 {
		t.Fatal("fake claude CLI received no get_usage control request")
	}
	if firstUser < 0 {
		t.Fatal("fake claude CLI received no user turn")
	}
	if firstUsage > firstUser {
		t.Fatalf("get_usage at %d came after the user turn at %d, want the usage gate first", firstUsage, firstUser)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(readFile(t, path)), "%d", &pid); err != nil {
		t.Fatalf("parse pid from %s: %v", path, err)
	}
	return pid
}

func containsAdjacent(values []string, first, second string) bool {
	for i := 0; i+1 < len(values); i++ {
		if values[i] == first && values[i+1] == second {
			return true
		}
	}
	return false
}
