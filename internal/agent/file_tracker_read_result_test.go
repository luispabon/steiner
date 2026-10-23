package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// A failed read (for example, a file over the size cap) is returned with no
// path, so the tracker must not record it as observed.
func TestFileTrackerIgnoresFailedReadResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed, err := json.Marshal(readResult{Output: "Error: file too large (max 1MB, got 2.0MB)"})
	if err != nil {
		t.Fatal(err)
	}
	var tracker FileTracker
	tracker.RecordRead(1, string(failed))
	if tracker.WasObserved(path) {
		t.Fatal("failed read recorded as observed")
	}
}

// A successful read of an empty file keeps its path and counts as observed.
func TestFileTrackerRecordsEmptyFileRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := json.Marshal(readResult{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var tracker FileTracker
	tracker.RecordRead(1, string(ok))
	if !tracker.WasObserved(path) {
		t.Fatal("successful empty-file read not recorded as observed")
	}
}

// writeNumberedFile writes numbered lines until the file holds at least size
// bytes. Line 3000 always exists when size is at least 200KB.
func writeNumberedFile(t *testing.T, path string, size int) {
	t.Helper()
	var b strings.Builder
	for i := 1; b.Len() < size; i++ {
		fmt.Fprintf(&b, "line-%05d %s\n", i, strings.Repeat("x", 40))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readMutateTools builds the real read and mutate tools for root.
func readMutateTools(t *testing.T, root string) (tool.ToolDef, tool.ToolDef) {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true})
	env := builtin.Env{WorkDir: root, PathPolicy: &policy}
	return builtin.NewReadTool(env), builtin.NewMutateTool(env)
}

// recordReadResult runs the read tool and feeds its JSON result to tracker,
// as the agent does for every read tool result.
func recordReadResult(t *testing.T, tracker *FileTracker, readDef tool.ToolDef, input map[string]any) {
	t.Helper()
	got, err := readDef.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("read Handler() error = %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}
	tracker.RecordRead(1, string(raw))
}

// replaceLine3000 runs mutate replace on line 3000 of path without file_hash,
// with the tracker's WasObserved wired in as the observation checker.
func replaceLine3000(t *testing.T, tracker *FileTracker, mutateDef tool.ToolDef, path string) *builtin.MutateResult {
	t.Helper()
	ctx := tool.WithFileObservedChecker(context.Background(), tracker.WasObserved)
	got, err := mutateDef.Handler(ctx, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": path, "old_string": "line-03000 ", "new_string": "LINE-03000 "},
		},
	})
	if err != nil {
		t.Fatalf("mutate Handler() error = %v", err)
	}
	result, ok := got.(*builtin.MutateResult)
	if !ok {
		t.Fatalf("mutate Handler() result = %T, want *builtin.MutateResult", got)
	}
	return result
}

// A paged read of a large text file (offset in the middle, small limit)
// records the file as observed, so mutate replace passes its read guard
// without file_hash.
func TestPagedReadOfLargeFileAllowsMutateReplace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.txt")
	writeNumberedFile(t, path, 200*1024)
	readDef, mutateDef := readMutateTools(t, root)
	var tracker FileTracker
	recordReadResult(t, &tracker, readDef, map[string]any{"path": "big.txt", "offset": 3000, "limit": 5})

	got := replaceLine3000(t, &tracker, mutateDef, "big.txt")
	if got.OperationsApplied != 1 || got.OperationsFailed != 0 {
		t.Fatalf("mutate result = %#v, want the replace applied after a paged read", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "LINE-03000 ") {
		t.Fatal("replace not applied to large file")
	}
}

// A read rejected for size (over 1MB) returns no path, so the tracker does not
// observe the file and mutate replace without file_hash fails.
func TestOversizedReadDoesNotAllowMutateReplace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "huge.txt")
	writeNumberedFile(t, path, 1024*1024+1)
	readDef, mutateDef := readMutateTools(t, root)
	var tracker FileTracker
	recordReadResult(t, &tracker, readDef, map[string]any{"path": "huge.txt", "offset": 3000, "limit": 5})
	if tracker.WasObserved(path) {
		t.Fatal("failed oversized read recorded as observed")
	}

	got := replaceLine3000(t, &tracker, mutateDef, "huge.txt")
	if got.OperationsApplied != 0 || got.OperationsFailed != 1 {
		t.Fatalf("mutate result = %#v, want the replace rejected", got)
	}
	if !strings.Contains(got.Output, "not read this session") {
		t.Fatalf("mutate output = %q, want the not-read-this-session error", got.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "line-03000 ") {
		t.Fatal("rejected replace modified the file")
	}
}
