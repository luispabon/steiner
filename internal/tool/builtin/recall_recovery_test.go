package builtin_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// recallSessionID is the session the real recall store writes under.
const recallSessionID = "recall-recovery"

// writeRecallEntry archives body through the real session store and returns the
// absolute recall file path.
func writeRecallEntry(t *testing.T, storeDir, body string) string {
	t.Helper()
	store, err := session.NewStore(storeDir)
	if err != nil {
		t.Fatalf("session.NewStore: %v", err)
	}
	entry := agent.RecallEntry{Kind: "tool", Tool: "grep", CallID: "call-1", Body: body}
	path, err := store.WriteRecall(context.Background(), recallSessionID, entry)
	if err != nil {
		t.Fatalf("WriteRecall: %v", err)
	}
	return path
}

// recallEnv returns a tool Env whose project root is disjoint from the session
// store, plus the absolute recall path of body.
func recallEnv(t *testing.T, body string) (builtin.Env, string) {
	t.Helper()
	projectRoot := t.TempDir()
	policy := tool.NewPathPolicy(projectRoot, config.PathsConfig{})
	return builtin.Env{WorkDir: projectRoot, PathPolicy: &policy}, writeRecallEntry(t, t.TempDir(), body)
}

func TestRecallResolveReadPathOutsideProjectRoot(t *testing.T) {
	env, recallPath := recallEnv(t, "body\n")
	if tool.PathWithinRoot(env.WorkDir, recallPath) {
		t.Fatalf("recall path %q is inside project root %q; the test needs it outside", recallPath, env.WorkDir)
	}
	resolved, err := env.PathPolicy.ResolveReadPath(recallPath)
	if err != nil {
		t.Fatalf("ResolveReadPath(%q): %v", recallPath, err)
	}
	if resolved != recallPath {
		t.Fatalf("ResolveReadPath = %q, want %q", resolved, recallPath)
	}
}

func TestRecallReadToolReturnsExactBody(t *testing.T) {
	body := "recall needle ALPHA-7\nsecond line\nthird line\n"
	env, recallPath := recallEnv(t, body)
	want, err := os.ReadFile(recallPath)
	if err != nil {
		t.Fatalf("read recall file: %v", err)
	}

	resultI, err := builtin.NewReadTool(env).Handler(context.Background(), map[string]any{"path": recallPath})
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	result, ok := resultI.(builtin.ReadResult)
	if !ok {
		t.Fatalf("read result type = %T, want builtin.ReadResult", resultI)
	}
	// The file fits one page and has no over-long line, so read returns it byte
	// for byte.
	if result.Output != string(want) {
		t.Fatalf("read output = %q, want exact file %q", result.Output, want)
	}
	_, recovered, ok := strings.Cut(result.Output, "---\n")
	if !ok {
		t.Fatalf("read output has no header separator: %q", result.Output)
	}
	if recovered != body {
		t.Fatalf("recovered body = %q, want %q", recovered, body)
	}
}

func TestRecallGrepToolMatchesBody(t *testing.T) {
	body := "first line\nrecall needle ALPHA-7\nthird line\n"
	env, recallPath := recallEnv(t, body)

	resultI, err := builtin.NewGrepTool(env).Handler(context.Background(), map[string]any{
		"pattern": "ALPHA-7",
		"path":    recallPath,
	})
	if err != nil {
		t.Fatalf("grep handler: %v", err)
	}
	result, ok := resultI.(builtin.GrepResult)
	if !ok {
		t.Fatalf("grep result type = %T, want builtin.GrepResult", resultI)
	}
	if result.Shown != 1 || result.FilesSearched != 1 {
		t.Fatalf("grep Shown=%d FilesSearched=%d, want 1 and 1; output: %q", result.Shown, result.FilesSearched, result.Output)
	}
	if !strings.Contains(result.Output, "## "+recallPath) {
		t.Fatalf("grep output = %q, want the recall path header", result.Output)
	}
	if !strings.Contains(result.Output, "recall needle ALPHA-7") {
		t.Fatalf("grep output = %q, want the matching body line", result.Output)
	}
}

// TestRecallLongLineRecovery pins the documented fallback for a body line over
// read's 2000-rune clip: read cannot return it, and bash head -c/tail -c
// reconstructs the file exactly.
func TestRecallLongLineRecovery(t *testing.T) {
	longLine := strings.Repeat("z", 6000) + "-TAIL-MARKER"
	body := "prefix line\n" + longLine + "\nsuffix line\n"
	env, recallPath := recallEnv(t, body)
	want, err := os.ReadFile(recallPath)
	if err != nil {
		t.Fatalf("read recall file: %v", err)
	}

	readResultI, err := builtin.NewReadTool(env).Handler(context.Background(), map[string]any{"path": recallPath})
	if err != nil {
		t.Fatalf("read handler: %v", err)
	}
	readResult, ok := readResultI.(builtin.ReadResult)
	if !ok {
		t.Fatalf("read result type = %T, want builtin.ReadResult", readResultI)
	}
	if !strings.Contains(readResult.Output, builtin.LineTruncationMarker) {
		t.Fatalf("read output has no truncation marker, so the long-line fallback is not covered")
	}
	if strings.Contains(readResult.Output, longLine) {
		t.Fatalf("read output contains the %d-rune line intact, want it clipped", len([]rune(longLine)))
	}

	half := len(want) / 2
	quoted := shellQuotePath(recallPath)
	command := "head -c " + strconv.Itoa(half) + " " + quoted +
		"; tail -c " + strconv.Itoa(len(want)-half) + " " + quoted

	ctx := context.WithValue(context.Background(), tool.SandboxWrapperKey{}, tool.ResolvedSandbox{Wrapper: tool.Unsandboxed{}})
	t.Run("bash tool", func(t *testing.T) {
		result := runRecallBash(ctx, t, env, command)
		if result.Output != string(want) {
			t.Fatalf("bash recovered %d bytes, want %d", len(result.Output), len(want))
		}
	})

	t.Run("bwrap sandbox", func(t *testing.T) {
		if err := sandbox.PrereqCheck(); err != nil {
			t.Skipf("bubblewrap unavailable, sandbox recovery not exercised: %v", err)
		}
		// The store lives under the host temp dir, which the sandbox replaces
		// with a tmpfs. A read-only host mount stands in for the session
		// directory that is visible under the user's home in production.
		sb := sandbox.New(config.SandboxConfig{
			Enabled:    true,
			HostMounts: []config.HostMount{{Path: filepath.Dir(recallPath), Mode: "ro"}},
		}, config.PermissionsConfig{}, env.WorkDir, env.WorkDir, t.TempDir(), "")
		if err := sb.EnsureHome(); err != nil {
			t.Fatalf("sandbox EnsureHome: %v", err)
		}
		sandboxCtx := context.WithValue(context.Background(), tool.SandboxWrapperKey{}, tool.ResolvedSandbox{Wrapper: sb})
		if probe := runRecallBash(sandboxCtx, t, env, "command -v head"); strings.TrimSpace(probe.Output) == "" {
			t.Skip("head is not available inside the bubblewrap sandbox")
		}
		result := runRecallBash(sandboxCtx, t, env, command)
		if result.Output != string(want) {
			t.Fatalf("sandboxed bash recovered %d bytes, want %d", len(result.Output), len(want))
		}
	})
}

func runRecallBash(ctx context.Context, t *testing.T, env builtin.Env, command string) *builtin.BashResult {
	t.Helper()
	resultI, err := builtin.NewBashTool(env).Handler(ctx, map[string]any{
		"command":    command,
		"raw_output": true,
	})
	if err != nil {
		t.Fatalf("bash handler: %v", err)
	}
	result, ok := resultI.(*builtin.BashResult)
	if !ok {
		t.Fatalf("bash result type = %T, want *builtin.BashResult", resultI)
	}
	if result.ExitCode != 0 {
		t.Fatalf("bash exit code = %d, output: %.200q", result.ExitCode, result.Output)
	}
	if result.Truncated {
		t.Fatalf("bash output truncated, so the chunked recovery is incomplete")
	}
	return result
}

// shellQuotePath quotes path as one POSIX shell word.
func shellQuotePath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
