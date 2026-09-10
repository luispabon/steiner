package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

// TestMutateMultiFailure_IndependentFiles asserts a batch with two
// independent no_match failures on different files reports both in one
// result, applies nothing.
func TestMutateMultiFailure_IndependentFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatalf("write fixture b.txt: %v", err)
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "NOPE", "new_string": "x"},
			map[string]any{"type": "replace", "path": "b.txt", "old_string": "NOPE", "new_string": "y"},
		},
	})

	if got.OperationsFailed != 2 {
		t.Fatalf("OperationsFailed = %d, want 2", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if !strings.Contains(got.Output, "2 of 2 operations failed") {
		t.Fatalf("Output = %q, want it to state 2 of 2 operations failed", got.Output)
	}
	if !strings.Contains(got.Output, "operation 1 replace") || !strings.Contains(got.Output, "operation 2 replace") {
		t.Fatalf("Output = %q, want both operation 1 and operation 2 detailed", got.Output)
	}
	assertFile(t, filepath.Join(root, "a.txt"), "hello\n")
	assertFile(t, filepath.Join(root, "b.txt"), "world\n")
}

// TestMutateMultiFailure_Cascading asserts a later failure on a path that
// already failed earlier in the same call is flagged as a probable
// consequence of that earlier failure, not an independent mistake.
func TestMutateMultiFailure_Cascading(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write fixture note.txt: %v", err)
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "NOPE-1", "new_string": "x"},
			map[string]any{"type": "create", "path": "other.txt", "content": "hi\n"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "NOPE-2", "new_string": "y"},
		},
	})

	if got.OperationsFailed != 2 {
		t.Fatalf("OperationsFailed = %d, want 2", got.OperationsFailed)
	}
	if got.OperationsRolledBack != 1 {
		t.Fatalf("OperationsRolledBack = %d, want 1 (operation 2 planned cleanly)", got.OperationsRolledBack)
	}
	if !strings.Contains(got.Output, "operation 3 replace") {
		t.Fatalf("Output = %q, want operation 3 detailed", got.Output)
	}
	if !strings.Contains(got.Output, "operation 1 on this file also failed") {
		t.Fatalf("Output = %q, want operation 3's failure flagged as a cascade of operation 1", got.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "other.txt")); !os.IsNotExist(err) {
		t.Fatalf("other.txt exists, want nothing applied")
	}
}

// TestMutateMultiFailure_SnapshotRestore is the regression test for the
// snapshot/restore requirement: an operation that mutates state.content
// before it fails its assert_present check must not leak that edit into a
// later operation's plan. Operation 1 replaces "alpha" -> "beta" (mutating
// content) and then fails an assert_present that never matches; operation 2
// replaces "alpha" -> "gamma" on the same file. If operation 1's edit
// leaked, operation 2 would see content "beta\n" and fail no_match too.
func TestMutateMultiFailure_SnapshotRestore(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write fixture note.txt: %v", err)
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type": "replace", "path": "note.txt",
				"old_string": "alpha", "new_string": "beta",
				"assert_present": []any{"nonexistent-marker"},
			},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "alpha", "new_string": "gamma"},
		},
	})

	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1 (operation 1's edit must not leak into operation 2's plan; output: %s)", got.OperationsFailed, got.Output)
	}
	if got.OperationsRolledBack != 1 {
		t.Fatalf("OperationsRolledBack = %d, want 1 (operation 2 should plan cleanly)", got.OperationsRolledBack)
	}
	if !strings.Contains(got.Output, "operation 1 replace") {
		t.Fatalf("Output = %q, want operation 1 detailed", got.Output)
	}
	assertFile(t, filepath.Join(root, "note.txt"), "alpha\n")
}

// TestMutateMultiFailure_BoundedOutput asserts full diagnostics are only
// emitted for the first maxDetailedMutateFailures failures, with the rest
// collapsed into one summary line each.
func TestMutateMultiFailure_BoundedOutput(t *testing.T) {
	root := t.TempDir()
	var operations []any
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte("content\n"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
		operations = append(operations, map[string]any{
			"type": "replace", "path": name, "old_string": "NOPE", "new_string": "x",
		})
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{"operations": operations})

	if got.OperationsFailed != 5 {
		t.Fatalf("OperationsFailed = %d, want 5", got.OperationsFailed)
	}

	detailBlocks := strings.Count(got.Output, "\noperation ")
	if detailBlocks != maxDetailedMutateFailures {
		t.Fatalf("full-detail blocks = %d, want %d (output: %s)", detailBlocks, maxDetailedMutateFailures, got.Output)
	}
	summaryLines := strings.Count(got.Output, "\n  operation ")
	wantSummary := 5 - maxDetailedMutateFailures
	if summaryLines != wantSummary {
		t.Fatalf("summary lines = %d, want %d (output: %s)", summaryLines, wantSummary, got.Output)
	}
	if !strings.Contains(got.Output, "2 more failures (summary only):") {
		t.Fatalf("Output = %q, want a summary-only total of 2 more failures", got.Output)
	}
}

// TestMutateMultiFailure_ValidOpBetweenFailures asserts a valid operation
// sandwiched between two failing operations still doesn't get applied, and
// counts as rolled back rather than skipped.
func TestMutateMultiFailure_ValidOpBetweenFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa\n"), 0o644); err != nil {
		t.Fatalf("write fixture a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c.txt"), []byte("ccc\n"), 0o644); err != nil {
		t.Fatalf("write fixture c.txt: %v", err)
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "NOPE", "new_string": "x"},
			map[string]any{"type": "create", "path": "b.txt", "content": "bbb\n"},
			map[string]any{"type": "replace", "path": "c.txt", "old_string": "NOPE", "new_string": "y"},
		},
	})

	if got.OperationsFailed != 2 {
		t.Fatalf("OperationsFailed = %d, want 2", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if got.OperationsRolledBack != 1 {
		t.Fatalf("OperationsRolledBack = %d, want 1", got.OperationsRolledBack)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatalf("b.txt exists, want nothing applied")
	}
	assertFile(t, filepath.Join(root, "a.txt"), "aaa\n")
	assertFile(t, filepath.Join(root, "c.txt"), "ccc\n")
}

// TestMutateMultiFailure_FatalAbortsImmediately asserts a genuine stateFor
// I/O failure aborts the whole call rather than accumulating: the second
// operation is never reached.
func TestMutateMultiFailure_FatalAbortsImmediately(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// A directory with no execute bit blocks resolving any name inside it,
	// so os.Stat(path) fails with a permission error at plan time — the
	// genuine I/O failure stateFor must treat as fatal, as opposed to the
	// commit-time write failure a 0o555 (still executable) dir would cause.
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod sub: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("cleanup chmod: %v", err)
		}
	})
	if _, err := os.Stat(path); err == nil {
		t.Skip("filesystem does not deny stat after chmod 000")
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "sub/a.txt", "content": "modified"},
			map[string]any{"type": "create", "path": "unreached.txt", "content": "x"},
		},
	})

	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1 (fatal I/O error aborts, doesn't accumulate)", got.OperationsFailed)
	}
	if len(got.FailedOps()) != 1 {
		t.Fatalf("FailedOps() = %v, want exactly one entry", got.FailedOps())
	}
	if _, err := os.Stat(filepath.Join(root, "unreached.txt")); !os.IsNotExist(err) {
		t.Fatalf("unreached.txt exists, want operation 2 never reached")
	}
}

// TestMutateMultiFailure_FatalAfterAccumulatedFailures asserts a fatal I/O
// error arriving after operations have already accumulated in the failures
// slice folds into the total instead of resetting OperationsFailed to 1 —
// the mismatch between OperationsFailed and the diagnostics failures slice
// that this change exists to avoid.
func TestMutateMultiFailure_FatalAfterAccumulatedFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture a.txt: %v", err)
	}
	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	blockedPath := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(blockedPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod sub: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("cleanup chmod: %v", err)
		}
	})
	if _, err := os.Stat(blockedPath); err == nil {
		t.Skip("filesystem does not deny stat after chmod 000")
	}
	toolDef := newMutateTestTool(t, root)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "NOPE", "new_string": "x"},
			map[string]any{"type": "write", "path": "sub/b.txt", "content": "modified"},
			map[string]any{"type": "create", "path": "unreached.txt", "content": "x"},
		},
	})

	if got.OperationsFailed != 2 {
		t.Fatalf("OperationsFailed = %d, want 2 (1 accumulated + 1 fatal)", got.OperationsFailed)
	}
	if len(got.FailedOps()) != 2 {
		t.Fatalf("FailedOps() = %v, want exactly two entries matching OperationsFailed", got.FailedOps())
	}
	if got.OperationsSkipped != 0 {
		t.Fatalf("OperationsSkipped = %d, want 0", got.OperationsSkipped)
	}
	if _, err := os.Stat(filepath.Join(root, "unreached.txt")); !os.IsNotExist(err) {
		t.Fatalf("unreached.txt exists, want operation 3 never reached")
	}
}

// TestMutateMultiFailure_DiagnosticsStream is the multi-failure companion to
// TestMutateToolStream_EndToEnd: it exercises the real tool.Executor seam
// and asserts ops_failed and failures[] both carry every failed operation,
// not just the first.
func TestMutateMultiFailure_DiagnosticsStream(t *testing.T) {
	root := t.TempDir()
	diagDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatalf("write fixture b.txt: %v", err)
	}

	writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
	registry := tool.NewRegistry(toolDef)
	executor := tool.NewExecutor(registry, config.Config{}, nil, root, "", tool.Unsandboxed{})
	executor.WithDiagnostics(writer)

	if _, err := executor.Execute(context.Background(), "mutate", "call-1", map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "NOPE", "new_string": "x"},
			map[string]any{"type": "replace", "path": "b.txt", "old_string": "NOPE", "new_string": "y"},
		},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
	if err != nil {
		t.Fatalf("read tool.jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("tool.jsonl has %d lines, want 1:\n%s", len(lines), raw)
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	payload, ok := rec["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %v, want an object", rec["payload"])
	}

	if got := payload["ops_failed"]; got != float64(2) {
		t.Errorf("payload[ops_failed] = %v, want 2", got)
	}
	failures, ok := payload["failures"].([]any)
	if !ok || len(failures) != 2 {
		t.Fatalf("payload[failures] = %v, want exactly two entries", payload["failures"])
	}
}
