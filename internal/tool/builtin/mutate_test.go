package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func newMutateTestTool(t *testing.T, root string) tool.ToolDef {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	return NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})
}

func runMutate(t *testing.T, toolDef tool.ToolDef, input map[string]any) *MutateResult {
	t.Helper()
	result, err := toolDef.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("mutate Handler() error = %v", err)
	}
	got, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
	}
	return got
}

func TestMutateOperations(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("one\ntwo\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("move me\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
			map[string]any{"type": "write", "path": "written.txt", "content": "written\n"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "two\ntwo", "new_string": "TWO\nTWO"},
			map[string]any{"type": "delete_file", "path": "written.txt"},
			map[string]any{"type": "move", "from": "old.txt", "to": "new.txt"},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 6 {
		t.Fatalf("mutate result = %#v", got)
	}
	if !got.WasMutated() {
		t.Fatal("mutate WasMutated() = false, want true")
	}
	assertFile(t, filepath.Join(root, "created.txt"), "created\n")
	assertFile(t, filepath.Join(root, "note.txt"), "ONE\nTWO\nTWO\n")
	assertFile(t, filepath.Join(root, "new.txt"), "move me\n")
	if _, err := os.Stat(filepath.Join(root, "written.txt")); !os.IsNotExist(err) {
		t.Fatalf("written.txt exists after delete, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old.txt exists after move, err=%v", err)
	}
	if !strings.Contains(got.Output, "--- note.txt") {
		t.Fatalf("Output = %q, want diff", got.Output)
	}
}

func TestMutateFileHashRequiresExistingTarget(t *testing.T) {
	tests := []struct {
		name string
		op   map[string]any
	}{
		{
			name: "write on missing target",
			op: map[string]any{
				"type":      "write",
				"path":      "new.txt",
				"content":   "new\n",
				"file_hash": "BEEF",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{
				"operations": []any{tt.op},
			})
			if got.OperationsFailed != 1 {
				t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
			}
			if !strings.Contains(got.Output, "file_hash requires an existing file") {
				t.Fatalf("Output = %q, want file_hash missing-target diagnostic", got.Output)
			}
			if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("new.txt exists after failed hash-guarded op, err=%v", err)
			}
		})
	}
}

func TestMutateFailuresAreAtomic(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "missing", "new_string": "MISSING"},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if len(got.Paths) != 0 || len(got.Created) != 0 || len(got.Modified) != 0 || len(got.Deleted) != 0 || len(got.Moved) != 0 || len(got.FileHashes) != 0 {
		t.Fatalf("mutate result metadata = %#v, want no committed outputs", got)
	}
	assertFile(t, path, "one\n")
}

func TestMutateFailedAtomicBatchDoesNotReportCommittedMetadata(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	notePath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(notePath, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "missing", "new_string": "MISSING"},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if got.WasMutated() {
		t.Fatal("WasMutated() = true, want false")
	}
	if len(got.Paths) != 0 || len(got.Created) != 0 || len(got.Modified) != 0 || len(got.Deleted) != 0 || len(got.Moved) != 0 || len(got.FileHashes) != 0 {
		t.Fatalf("mutate result metadata = %#v, want no committed outputs", got)
	}
	assertFile(t, notePath, "one\n")
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt exists after failed batch, err=%v", err)
	}
}

func TestMutateReturnsStructuredVerificationData(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ncharlie\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":           "replace",
				"path":           "note.txt",
				"old_string":     "beta",
				"new_string":     "BETA",
				"assert_present": []any{"BETA"},
				"assert_absent":  []any{"beta\n"},
			},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	if got.FileHashes["note.txt"] != fileContentHash([]byte("alpha\nBETA\ncharlie\n")) {
		t.Fatalf("file hash = %q, want hash for final content", got.FileHashes["note.txt"])
	}
	if len(got.OperationResults) != 1 {
		t.Fatalf("len(OperationResults) = %d, want 1", len(got.OperationResults))
	}
	op := got.OperationResults[0]
	if op.Index != 1 || op.Type != "replace" || op.Path != "note.txt" {
		t.Fatalf("operation result = %#v", op)
	}
	if op.ResolvedPath != path {
		t.Errorf("ResolvedPath = %q, want absolute path %q", op.ResolvedPath, path)
	}
	if !filepath.IsAbs(op.ResolvedPath) {
		t.Errorf("ResolvedPath = %q, want absolute", op.ResolvedPath)
	}
	if op.MatchCount != 1 {
		t.Fatalf("MatchCount = %d, want 1", op.MatchCount)
	}
	if op.FileHash != got.FileHashes["note.txt"] {
		t.Fatalf("operation file hash = %q, want %q", op.FileHash, got.FileHashes["note.txt"])
	}
	if len(op.Assertions) != 2 {
		t.Fatalf("len(Assertions) = %d, want 2", len(op.Assertions))
	}
	if op.Assertions[0].Kind != "present" || op.Assertions[0].Text != "BETA" || op.Assertions[0].Matches != 1 {
		t.Fatalf("present assertion = %#v", op.Assertions[0])
	}
	if op.Assertions[1].Kind != "absent" || op.Assertions[1].Matches != 0 {
		t.Fatalf("absent assertion = %#v", op.Assertions[1])
	}
	if op.Context == nil {
		t.Fatal("Context = nil, want bounded excerpt")
	}
	if op.Context.StartLine != 1 || op.Context.EndLine != 3 || op.Context.TotalLines != 3 {
		t.Fatalf("context lines = %#v, want full 3-line excerpt", op.Context)
	}
	if op.Context.Content != "alpha\nBETA\ncharlie\n" {
		t.Fatalf("context content = %q", op.Context.Content)
	}
}

func TestMutateAssertionsFailAtomically(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":           "replace",
				"path":           "note.txt",
				"old_string":     "beta",
				"new_string":     "BETA",
				"assert_present": []any{"missing"},
			},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if len(got.OperationResults) != 0 || len(got.FileHashes) != 0 {
		t.Fatalf("verification metadata leaked on failed batch: %#v", got)
	}
	if !strings.Contains(got.Output, "assert_present failed") {
		t.Fatalf("Output = %q, want assert_present failure", got.Output)
	}
	assertFile(t, path, "alpha\nbeta\n")
}

func TestMutateRejectsInvalidOperations(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, root string)
		input     map[string]any
		wantError string
		wantAlso  string
	}{
		{
			name: "ambiguous replace",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x\nx\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "replace", "path": "note.txt", "old_string": "x", "new_string": "y"}}},
			wantError: "ambiguous match",
		},
		{
			name: "binary edit",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{'a', 0, 'b'}, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "replace", "path": "bin.dat", "old_string": "a", "new_string": "b"}}},
			wantError: "binary",
		},
		{
			name:      "missing move source",
			input:     map[string]any{"operations": []any{map[string]any{"type": "move", "from": "missing.txt", "to": "dest.txt"}}},
			wantError: "does not exist",
		},
		{
			name: "existing move destination",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "dest.txt"), []byte("dest"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "move", "from": "src.txt", "to": "dest.txt"}}},
			wantError: "already exists",
		},
		{
			name: "directory deletion",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete_file", "path": "dir"}}},
			wantError: "is a directory",
		},
		{
			name:      "invalid path",
			input:     map[string]any{"operations": []any{map[string]any{"type": "write", "path": "../outside.txt", "content": "x"}}},
			wantError: "outside project root",
		},
		{
			name:      "empty operations",
			input:     map[string]any{"operations": []any{}},
			wantError: "operations is required",
		},
		{
			name:      "unsupported type",
			input:     map[string]any{"operations": []any{map[string]any{"type": "chmod", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name: "create existing file",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "create", "path": "note.txt", "content": "y"}}},
			wantError: "already exists",
		},
		{
			name:      "delete missing file",
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete_file", "path": "missing.txt"}}},
			wantError: "does not exist",
		},
		{
			name:      "missing parent directory",
			input:     map[string]any{"operations": []any{map[string]any{"type": "write", "path": "missing/note.txt", "content": "x"}}},
			wantError: "parent directory",
		},
		{
			name:      "missing parent directory names the remedy",
			input:     map[string]any{"operations": []any{map[string]any{"type": "write", "path": "missing/note.txt", "content": "x"}}},
			wantError: "create it first (e.g. with bash mkdir -p), then retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, root)
			}
			got := runMutate(t, newMutateTestTool(t, root), tt.input)
			if got.OperationsFailed != 1 {
				t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
			}
			if !strings.Contains(got.Output, tt.wantError) {
				t.Fatalf("Output = %q, want substring %q", got.Output, tt.wantError)
			}
			if tt.wantAlso != "" && !strings.Contains(got.Output, tt.wantAlso) {
				t.Fatalf("Output = %q, want substring %q", got.Output, tt.wantAlso)
			}
		})
	}
}

func TestMutateReplaceEdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		initial    string
		operations []any
		want       string
		wantOutput string
	}{
		{
			name:    "single exact match among similar substrings",
			initial: "alpha\nalphabet\nalpha-beta\n",
			operations: []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "alpha\nalphabet", "new_string": "ALPHA\nALPHABET"},
			},
			want: "ALPHA\nALPHABET\nalpha-beta\n",
		},
		{
			name:    "replace all adjacent non-overlapping matches",
			initial: "aaaa",
			operations: []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "aa", "new_string": "b", "replace_all": true},
			},
			want: "bb",
		},
		{
			name:    "replacement may contain old string without looping",
			initial: "token\n",
			operations: []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "token", "new_string": "token token", "replace_all": true},
			},
			want: "token token\n",
		},
		{
			name:    "ordered operations see prior same-file edits",
			initial: "a b c\n",
			operations: []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "a", "new_string": "A"},
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "A b c", "new_string": "done"},
			},
			want: "done\n",
		},
		{
			name:    "delete then create same path acts on in-memory state",
			initial: "old\n",
			operations: []any{
				map[string]any{"type": "delete_file", "path": "note.txt"},
				map[string]any{"type": "create", "path": "note.txt", "content": "new\n"},
			},
			want: "new\n",
		},
		{
			name:    "write then replace created file in same call",
			initial: "original untouched\n",
			operations: []any{
				map[string]any{"type": "write", "path": "created.txt", "content": "hello world\n"},
				map[string]any{"type": "replace", "path": "created.txt", "old_string": "world", "new_string": "steiner"},
			},
			want: "original untouched\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": tt.operations})
			if got.OperationsFailed != 0 {
				t.Fatalf("mutate failed: %#v", got)
			}
			assertFile(t, filepath.Join(root, "note.txt"), tt.want)
			if _, err := os.Stat(filepath.Join(root, "created.txt")); err == nil {
				assertFile(t, filepath.Join(root, "created.txt"), "hello steiner\n")
			}
		})
	}
}

func TestMutateMoveEdgeCases(t *testing.T) {
	t.Run("move then edit destination", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("hello old\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "move", "from": "old.txt", "to": "new.txt"},
				map[string]any{"type": "replace", "path": "new.txt", "old_string": "old", "new_string": "new"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "new.txt"), "hello new\n")
		if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
			t.Fatalf("old.txt exists after move, err=%v", err)
		}
	})

	t.Run("destination collision does not overwrite", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte("src\n"), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "dest.txt"), []byte("dest\n"), 0o644); err != nil {
			t.Fatalf("write dest: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "move", "from": "src.txt", "to": "dest.txt"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
		}
		if !strings.Contains(got.Output, "already exists") {
			t.Fatalf("Output = %q, want destination collision", got.Output)
		}
		assertFile(t, filepath.Join(root, "src.txt"), "src\n")
		assertFile(t, filepath.Join(root, "dest.txt"), "dest\n")
	})

	t.Run("write then move created file", func(t *testing.T) {
		root := t.TempDir()
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "write", "path": "draft.txt", "content": "draft\n"},
				map[string]any{"type": "move", "from": "draft.txt", "to": "final.txt"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "final.txt"), "draft\n")
		if _, err := os.Stat(filepath.Join(root, "draft.txt")); !os.IsNotExist(err) {
			t.Fatalf("draft.txt exists after move, err=%v", err)
		}
	})

	t.Run("swap via temporary file", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("A\n"), 0o644); err != nil {
			t.Fatalf("write a: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("B\n"), 0o644); err != nil {
			t.Fatalf("write b: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "move", "from": "a.txt", "to": "tmp.txt"},
				map[string]any{"type": "move", "from": "b.txt", "to": "a.txt"},
				map[string]any{"type": "move", "from": "tmp.txt", "to": "b.txt"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "a.txt"), "B\n")
		assertFile(t, filepath.Join(root, "b.txt"), "A\n")
	})
}

func TestMutateDiffOutputContainsHunks(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "context line"
	}
	lines[25] = "old target"
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "old target", "new_string": "new target"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	if !strings.Contains(got.Output, "@@ -") {
		t.Fatalf("Output missing hunk header:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "-old target") {
		t.Fatalf("Output missing deleted line:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "+new target") {
		t.Fatalf("Output missing inserted line:\n%s", got.Output)
	}
	hunkCount := strings.Count(got.Output, "@@ -")
	if hunkCount != 1 {
		t.Fatalf("hunk count = %d, want 1", hunkCount)
	}
}

func TestMutateWhitespaceMismatchDiagnostic(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("check:\n\tgo test ./...\n\tgo vet ./...\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":       "replace",
				"path":       "Makefile",
				"old_string": "check:\n    go test ./...\n    go vet ./...\n",
				"new_string": "check:\n\tgo test -race ./...\n",
			},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if !strings.Contains(got.Output, "normalized whitespace match exists") {
		t.Fatalf("Output missing whitespace diagnostic:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "reread") {
		t.Fatalf("Output missing suggestion:\n%s", got.Output)
	}
}

// TestMutateDiagnosticsIncludeAbsolutePath guards against a class of "wrong
// file" confusion: when a relative path resolves to a different physical file
// (e.g. across a git worktree boundary), the no-match/ambiguous
// error must name the absolute path the planner actually touched so the
// divergence is immediately diagnosable from the error text.
func TestMutateDiagnosticsIncludeAbsolutePath(t *testing.T) {
	root := t.TempDir()
	absPath := filepath.Join(root, "note.txt")
	if err := os.WriteFile(absPath, []byte("aaa\nbbb\nccc\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	t.Run("replace no-match", func(t *testing.T) {
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "NOTFOUND", "new_string": "x"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
		}
		if !strings.Contains(got.Output, "no match for old_string in "+absPath) {
			t.Fatalf("Output must name the abs path %q in the no-match header:\n%s", absPath, got.Output)
		}
	})

	t.Run("replace ambiguous", func(t *testing.T) {
		// Duplicate 'aaa' so a non-replace_all match is ambiguous.
		if err := os.WriteFile(absPath, []byte("aaa\naaa\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "aaa", "new_string": "x"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
		}
		if !strings.Contains(got.Output, "ambiguous match for old_string in "+absPath) {
			t.Fatalf("Output must name the abs path %q in the ambiguous header:\n%s", absPath, got.Output)
		}
	})
}

func TestMutateMultiFileOperations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "alpha", "new_string": "ALPHA"},
			map[string]any{"type": "replace", "path": "b.txt", "old_string": "beta", "new_string": "BETA"},
			map[string]any{"type": "create", "path": "c.txt", "content": "gamma\n"},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 3 {
		t.Fatalf("mutate result = %#v", got)
	}
	assertFile(t, filepath.Join(root, "a.txt"), "ALPHA\n")
	assertFile(t, filepath.Join(root, "b.txt"), "BETA\n")
	assertFile(t, filepath.Join(root, "c.txt"), "gamma\n")
	if !strings.Contains(got.Output, "--- a.txt") || !strings.Contains(got.Output, "--- b.txt") {
		t.Fatalf("Output missing per-file diffs:\n%s", got.Output)
	}
}

func TestMutateReplaceWithEmptyNewString(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("keep\nremove this\nkeep\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "remove this\n", "new_string": ""},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, filepath.Join(root, "note.txt"), "keep\nkeep\n")
}

func TestMutateReplaceMultiLineBlockWithSmaller(t *testing.T) {
	root := t.TempDir()
	content := "header\n\nquick-check:\n\tgo vet ./...\n\ncheck:\n\tgo test ./...\n\tgo vet ./...\n\nci-check:\n\tgo test -race ./...\n\tgo vet ./...\n\tgolangci-lint run\n\nfooter\n"
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":       "replace",
				"path":       "Makefile",
				"old_string": "quick-check:\n\tgo vet ./...\n\ncheck:\n\tgo test ./...\n\tgo vet ./...\n\nci-check:\n\tgo test -race ./...\n\tgo vet ./...\n\tgolangci-lint run\n",
				"new_string": "check:\n\tgo test -race ./...\n\tgo vet ./...\n\tgolangci-lint run\n",
			},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, filepath.Join(root, "Makefile"), "header\n\ncheck:\n\tgo test -race ./...\n\tgo vet ./...\n\tgolangci-lint run\n\nfooter\n")
}

func TestMutateReplaceAtFileStartAndEnd(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		old     string
		new     string
		want    string
	}{
		{
			name:    "replace at very start of file",
			initial: "HEADER\nmiddle\nend\n",
			old:     "HEADER",
			new:     "header",
			want:    "header\nmiddle\nend\n",
		},
		{
			name:    "replace at very end of file",
			initial: "start\nmiddle\nFOOTER\n",
			old:     "FOOTER\n",
			new:     "footer\n",
			want:    "start\nmiddle\nfooter\n",
		},
		{
			name:    "replace entire single line file",
			initial: "only\n",
			old:     "only\n",
			new:     "replaced\n",
			want:    "replaced\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note.txt")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "note.txt", "old_string": tt.old, "new_string": tt.new},
				},
			})
			if got.OperationsFailed != 0 {
				t.Fatalf("mutate failed: %#v", got)
			}
			assertFile(t, path, tt.want)
		})
	}
}

func TestMutatePlanningFailureDoesNotCreateIntermediateFiles(t *testing.T) {
	root := t.TempDir()
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
			map[string]any{"type": "delete_file", "path": "missing.txt"},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt exists after planning failure, err=%v", err)
	}
}

func TestMutateOutputIsBoundedForLargeWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("before\n", 8000)), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "big.txt", "content": strings.Repeat("after\n", 8000)},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	if len(got.Output) > maxMutateOutputChars+len("\n<truncated>") {
		t.Fatalf("Output len = %d, want bounded", len(got.Output))
	}
	if !strings.Contains(got.Output, "<diff omitted: change too large") {
		t.Fatalf("Output missing omitted diff marker: %q", got.Output)
	}
}

func TestMutateReturnsBoundedPostEditContext(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.txt")
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	lines[9] = "needle"
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":       "replace",
				"path":       "big.txt",
				"old_string": "needle",
				"new_string": "NEEDLE",
			},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	if len(got.OperationResults) != 1 {
		t.Fatalf("len(OperationResults) = %d, want 1", len(got.OperationResults))
	}
	ctx := got.OperationResults[0].Context
	if ctx == nil {
		t.Fatal("Context = nil, want excerpt")
	}
	if !ctx.Truncated {
		t.Fatalf("Truncated = false, want true for large file excerpt: %#v", ctx)
	}
	if ctx.StartLine != 8 || ctx.EndLine != 12 || ctx.TotalLines != 20 {
		t.Fatalf("context = %#v, want lines 8-12 of 20", ctx)
	}
	if strings.Count(strings.TrimSuffix(ctx.Content, "\n"), "\n")+1 != 5 {
		t.Fatalf("context line count = %d, want 5; content=%q", strings.Count(strings.TrimSuffix(ctx.Content, "\n"), "\n")+1, ctx.Content)
	}
	if !strings.Contains(ctx.Content, "NEEDLE\n") {
		t.Fatalf("context content = %q, want edited line", ctx.Content)
	}
}

func TestMutateWriteRejectsEmptyContentOnExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "note.txt", "content": ""},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
	}
	if !strings.Contains(got.Output, "content is empty") {
		t.Fatalf("Output = %q, want content is empty diagnostic", got.Output)
	}
	assertFile(t, path, "hello\n")
}

func TestMutateWriteEmptyContentOnNewFileSucceeds(t *testing.T) {
	root := t.TempDir()

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "new.txt", "content": ""},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0; output=%q", got.OperationsFailed, got.Output)
	}
	assertFile(t, filepath.Join(root, "new.txt"), "")
}

func TestMutateWriteEmptyContentOnEmptyFileSucceeds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "empty.txt", "content": ""},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0; output=%q", got.OperationsFailed, got.Output)
	}
}

func TestMutateTabVsSpaceGoFileScenario(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.go")
	content := "type Config struct {\n\tworkDir    string\n\tlogLevel   int\n\tverbose    bool\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":       "replace",
				"path":       "config.go",
				"old_string": "    workDir    string",
				"new_string": "\tworkDir    string",
			},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
	}
	if !strings.Contains(got.Output, "normalized whitespace match exists") {
		t.Fatalf("Output missing whitespace diagnostic:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "nearest anchor at line 2") {
		t.Fatalf("Output missing anchor at line 2:\n%s", got.Output)
	}
}

func TestMutateFileHashVerification(t *testing.T) {
	t.Run("valid hash on replace succeeds", func(t *testing.T) {
		root := t.TempDir()
		content := "hello world\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "hello", "new_string": "HELLO", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "HELLO world\n")
	})

	t.Run("stale hash on replace fails", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello world\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "hello", "new_string": "HELLO", "file_hash": "DEAD"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
	})

	t.Run("missing hash backward compat", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello world\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "hello", "new_string": "HELLO"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "HELLO world\n")
	})

	t.Run("hash on create new file is rejected", func(t *testing.T) {
		root := t.TempDir()
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "create", "path": "new.txt", "content": "new content\n", "file_hash": "BEEF"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1; output=%q", got.OperationsFailed, got.Output)
		}
		if !strings.Contains(got.Output, "field \"file_hash\" is not valid") {
			t.Fatalf("Output = %q, want field not valid diagnostic", got.Output)
		}
		if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
			t.Fatalf("new.txt exists after rejected hash-guarded create, err=%v", err)
		}
	})

	t.Run("hash on delete verified", func(t *testing.T) {
		root := t.TempDir()
		content := "delete me\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))

		// Valid hash succeeds
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "delete_file", "path": "note.txt", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
			t.Fatalf("note.txt exists after delete, err=%v", err)
		}

		// Stale hash fails
		if err := os.WriteFile(filepath.Join(root, "note2.txt"), []byte("other content\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got = runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "delete_file", "path": "note2.txt", "file_hash": "DEAD"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
	})

	t.Run("hash on write overwrite verified", func(t *testing.T) {
		root := t.TempDir()
		content := "original\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))

		// Valid hash succeeds
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "write", "path": "note.txt", "content": "overwritten\n", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "overwritten\n")

		// Stale hash fails
		if err := os.WriteFile(filepath.Join(root, "note2.txt"), []byte("original2\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got = runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "write", "path": "note2.txt", "content": "overwritten\n", "file_hash": "DEAD"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
	})

	t.Run("hash after prior operation modified same file fails with stale hash", func(t *testing.T) {
		root := t.TempDir()
		content := "aaa\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		// First op modifies the file; second op provides hash of post-modification content
		// Since verifyFileHash checks state.original (disk snapshot), the hash of the modified
		// content is now stale and should fail
		modifiedContent := "AAA\n"
		modifiedHash := fileContentHash([]byte(modifiedContent))

		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "aaa", "new_string": "AAA"},
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "AAA", "new_string": "BBB", "file_hash": modifiedHash},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1 (stale hash after prior op)", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
		// File should remain unchanged (atomic)
		assertFile(t, filepath.Join(root, "note.txt"), "aaa\n")

		// Now test with original hash — must succeed since verifyFileHash checks state.original
		if err := os.WriteFile(filepath.Join(root, "note2.txt"), []byte("xxx\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		xxxHash := fileContentHash([]byte("xxx\n"))
		got = runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note2.txt", "old_string": "xxx", "new_string": "XXX", "file_hash": xxxHash},
				map[string]any{"type": "replace", "path": "note2.txt", "old_string": "XXX", "new_string": "YYY"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("OperationsFailed = %d, want 0 with matching original hash; output=%q", got.OperationsFailed, got.Output)
		}
		assertFile(t, filepath.Join(root, "note2.txt"), "YYY\n")
	})

	t.Run("hash on move verified against source", func(t *testing.T) {
		root := t.TempDir()
		content := "move me\n"
		if err := os.WriteFile(filepath.Join(root, "src.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))

		// Valid hash succeeds
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "move", "from": "src.txt", "to": "dst.txt", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "dst.txt"), "move me\n")
		if _, err := os.Stat(filepath.Join(root, "src.txt")); !os.IsNotExist(err) {
			t.Fatalf("src.txt exists after move, err=%v", err)
		}

		// Stale hash fails
		if err := os.WriteFile(filepath.Join(root, "src2.txt"), []byte("other\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got = runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "move", "from": "src2.txt", "to": "dst2.txt", "file_hash": "DEAD"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
	})
}

func TestMutateHashBatchSemantics(t *testing.T) {
	t.Run("multi-op same file succeeds with consistent hash", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "file.txt")
		content := "hello\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "hello", "new_string": "world", "file_hash": hash},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "world", "new_string": "final", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("OperationsFailed = %d, want 0; output=%q", got.OperationsFailed, got.Output)
		}
		if got.OperationsApplied != 2 {
			t.Fatalf("OperationsApplied = %d, want 2", got.OperationsApplied)
		}
		assertFile(t, path, "final\n")
	})

	t.Run("genuine stale hash fails", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "file.txt")
		originalContent := "original\n"
		if err := os.WriteFile(path, []byte(originalContent), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		oldHash := fileContentHash([]byte(originalContent))

		// Externally modify the file
		modifiedContent := "modified\n"
		if err := os.WriteFile(path, []byte(modifiedContent), 0o644); err != nil {
			t.Fatalf("modify fixture: %v", err)
		}

		// Try to mutate with the old hash
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "modified", "new_string": "changed", "file_hash": oldHash},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want substring 'file_hash mismatch'", got.Output)
		}
		// File should remain unchanged
		assertFile(t, path, "modified\n")
	})

	t.Run("operation counts on partial failure", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "file.txt")
		if err := os.WriteFile(path, []byte("aaa\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "aaa", "new_string": "bbb"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "bbb", "new_string": "ccc"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "NONEXISTENT", "new_string": "x"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "ccc", "new_string": "ddd"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "ddd", "new_string": "eee"},
			},
		})
		if got.OperationsApplied != 0 {
			t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
		}
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if got.OperationsSkipped != 2 {
			t.Fatalf("OperationsSkipped = %d, want 2", got.OperationsSkipped)
		}
		// File should remain unchanged (atomic)
		assertFile(t, path, "aaa\n")
	})

	t.Run("full success, skipped is 0", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "file.txt")
		if err := os.WriteFile(path, []byte("aaa\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "aaa", "new_string": "bbb"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "bbb", "new_string": "ccc"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
		}
		if got.OperationsSkipped != 0 {
			t.Fatalf("OperationsSkipped = %d, want 0", got.OperationsSkipped)
		}
		if got.OperationsApplied != 2 {
			t.Fatalf("OperationsApplied = %d, want 2", got.OperationsApplied)
		}
		assertFile(t, path, "ccc\n")
	})

	t.Run("failure at first op", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "file.txt")
		if err := os.WriteFile(path, []byte("aaa\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}

		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "NOTFOUND", "new_string": "bbb"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "aaa", "new_string": "ccc"},
				map[string]any{"type": "replace", "path": "file.txt", "old_string": "ccc", "new_string": "ddd"},
			},
		})
		if got.OperationsApplied != 0 {
			t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
		}
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if got.OperationsSkipped != 2 {
			t.Fatalf("OperationsSkipped = %d, want 2", got.OperationsSkipped)
		}
		// File should remain unchanged
		assertFile(t, path, "aaa\n")
	})
}

func TestMutateRejectsInapplicableFields(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "exist.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name    string
		op      map[string]any
		wantErr string
	}{
		{
			name:    "delete_line with content",
			op:      map[string]any{"type": "delete_line", "path": "exist.txt", "line": float64(1), "content": "stuff"},
			wantErr: `field "content" is not valid`,
		},
		{
			name:    "delete_line with old_string",
			op:      map[string]any{"type": "delete_line", "path": "exist.txt", "line": float64(1), "old_string": "hello"},
			wantErr: `field "old_string" is not valid`,
		},
		{
			name:    "delete_line with new_string",
			op:      map[string]any{"type": "delete_line", "path": "exist.txt", "line": float64(1), "new_string": "hello"},
			wantErr: `field "new_string" is not valid`,
		},
		{
			name:    "delete_line with from",
			op:      map[string]any{"type": "delete_line", "path": "exist.txt", "line": float64(1), "from": "a.txt"},
			wantErr: `field "from" is not valid`,
		},
		{
			name:    "delete with line",
			op:      map[string]any{"type": "delete_file", "path": "exist.txt", "line": float64(1)},
			wantErr: `field "line" is not valid`,
		},
		{
			name:    "delete with line_count",
			op:      map[string]any{"type": "delete_file", "path": "exist.txt", "line_count": float64(2)},
			wantErr: `field "line_count" is not valid`,
		},
		{
			name:    "delete with content",
			op:      map[string]any{"type": "delete_file", "path": "exist.txt", "content": "stuff"},
			wantErr: `field "content" is not valid`,
		},
		{
			name:    "create with line",
			op:      map[string]any{"type": "create", "path": "new.txt", "content": "x", "line": float64(1)},
			wantErr: `field "line" is not valid`,
		},
		{
			name:    "create with old_string",
			op:      map[string]any{"type": "create", "path": "new.txt", "content": "x", "old_string": "y"},
			wantErr: `field "old_string" is not valid`,
		},
		{
			name:    "create with from",
			op:      map[string]any{"type": "create", "path": "new.txt", "content": "x", "from": "a.txt"},
			wantErr: `field "from" is not valid`,
		},
		{
			name:    "move with content",
			op:      map[string]any{"type": "move", "from": "exist.txt", "to": "moved.txt", "content": "x"},
			wantErr: `field "content" is not valid`,
		},
		{
			name:    "move with line",
			op:      map[string]any{"type": "move", "from": "exist.txt", "to": "moved.txt", "line": float64(1)},
			wantErr: `field "line" is not valid`,
		},
		{
			name:    "replace with line",
			op:      map[string]any{"type": "replace", "path": "exist.txt", "old_string": "hello", "new_string": "hi", "line": float64(1)},
			wantErr: `field "line" is not valid`,
		},
		{
			name:    "replace with from",
			op:      map[string]any{"type": "replace", "path": "exist.txt", "old_string": "hello", "new_string": "hi", "from": "a.txt"},
			wantErr: `field "from" is not valid`,
		},
		{
			name:    "replace with content",
			op:      map[string]any{"type": "replace", "path": "exist.txt", "old_string": "hello", "new_string": "hi", "content": "x"},
			wantErr: `field "content" is not valid`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runMutate(t, toolDef, map[string]any{
				"operations": []any{tt.op},
			})
			if got.OperationsFailed != 1 {
				t.Fatalf("expected 1 failed operation, got %d; output: %s", got.OperationsFailed, got.Output)
			}
			if !strings.Contains(got.Output, tt.wantErr) {
				t.Fatalf("output %q does not contain %q", got.Output, tt.wantErr)
			}
		})
	}
}

func TestMutateDescriptionMatchesEnumSpelling(t *testing.T) {
	def := newMutateTestTool(t, t.TempDir())
	for _, want := range []string{"replace", "delete_file", "move", "create or overwrite"} {
		if !strings.Contains(def.Description, want) {
			t.Errorf("description missing %q", want)
		}
	}
	for _, bad := range []string{"line-replace", "insert-before", "insert-after"} {
		if strings.Contains(def.Description, bad) {
			t.Errorf("description contains hyphenated %q", bad)
		}
	}
}

func TestMutateDescriptionDocumentsParentDirectoryRequirement(t *testing.T) {
	def := newMutateTestTool(t, t.TempDir())
	if !strings.Contains(def.Description, "parent directories must exist for workspace paths") {
		t.Errorf("description missing parent-directory requirement clause: %q", def.Description)
	}
	if !strings.Contains(def.Description, "sandbox tmpdir") {
		t.Errorf("description missing sandbox-tmpdir auto-create carve-out: %q", def.Description)
	}
}

func TestMutateSchemaTypeDescriptionHasCheatSheet(t *testing.T) {
	schema := MutateSchema()
	props := schema["properties"].(map[string]any)
	operations := props["operations"].(map[string]any)
	items := operations["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	desc := itemProps["type"].(map[string]any)["description"].(string)
	for _, want := range []string{
		"move: from, to",

		"replace: path, old_string, new_string",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("type description missing %q; got: %s", want, desc)
		}
	}
}

func TestMutateRejectsDeadCreateFields(t *testing.T) {
	tests := []struct {
		name string
		op   MutateOperation
	}{
		{"file_hash", MutateOperation{Type: "create", Path: "x.txt", FileHash: "abcd"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateFields(1, tt.op); err == nil {
				t.Fatalf("create with %s: want error, got nil", tt.name)
			}
		})
	}
}

func TestMutateValidateRequired(t *testing.T) {
	tests := []struct {
		name string
		op   MutateOperation
		want string
	}{
		{"move missing from", MutateOperation{Type: "move", To: "b"}, "from is required"},
		{"move missing to", MutateOperation{Type: "move", From: "a"}, "to is required"},
		{"replace missing path", MutateOperation{Type: "replace", OldString: "a", NewString: "b"}, "path is required"},
		{"delete missing path", MutateOperation{Type: "delete"}, "path is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequired(1, tt.op)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateRequired = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestMutateValidCreateAndWriteStillSucceed(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "fresh.txt", "content": "fresh\n"},
			map[string]any{"type": "write", "path": "existing.txt", "content": "", "allow_empty": true},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 2 {
		t.Fatalf("result = %#v", got)
	}
	assertFile(t, filepath.Join(root, "fresh.txt"), "fresh\n")
	assertFile(t, filepath.Join(root, "existing.txt"), "")
}

func TestMutatePlanPhaseFailureAccounting(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\nf\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "a", "new_string": "A"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "b", "new_string": "B"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "c", "new_string": "C"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "d", "new_string": "D"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "missing", "new_string": "MISSING"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "f", "new_string": "F"},
		},
	})

	total := 6
	if got.OperationsApplied+got.OperationsFailed+got.OperationsRolledBack+got.OperationsSkipped != total {
		t.Fatalf("accounting sum = %d + %d + %d + %d = %d, want %d",
			got.OperationsApplied, got.OperationsFailed, got.OperationsRolledBack, got.OperationsSkipped,
			got.OperationsApplied+got.OperationsFailed+got.OperationsRolledBack+got.OperationsSkipped, total)
	}
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if got.OperationsRolledBack != 4 {
		t.Fatalf("OperationsRolledBack = %d, want 4", got.OperationsRolledBack)
	}
	if got.OperationsSkipped != 1 {
		t.Fatalf("OperationsSkipped = %d, want 1", got.OperationsSkipped)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if len(got.OperationResults) != 4 {
		t.Fatalf("OperationResults count = %d, want 4", len(got.OperationResults))
	}
	for i, opResult := range got.OperationResults {
		if opResult.Applied != false {
			t.Fatalf("OperationResults[%d].Applied = %v, want false", i, opResult.Applied)
		}
		if opResult.FileHash != "" {
			t.Fatalf("OperationResults[%d].FileHash = %q, want empty", i, opResult.FileHash)
		}
	}
	assertFile(t, path, "a\nb\nc\nd\ne\nf\n")
}

func TestMutateCommitPhaseFailureAccounting(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	readonlyDir := filepath.Join(root, "readonly")
	if err := os.Mkdir(readonlyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(readonlyDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(readonlyDir, 0o755); err != nil {
			t.Errorf("cleanup chmod %q: %v", readonlyDir, err)
		}
	})

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "readonly/file1.txt", "content": "one\n"},
			map[string]any{"type": "create", "path": "readonly/file2.txt", "content": "two\n"},
			map[string]any{"type": "create", "path": "readonly/file3.txt", "content": "three\n"},
		},
	})

	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1 (commit operation failed)", got.OperationsFailed)
	}
	if got.OperationsRolledBack != 3 {
		t.Fatalf("OperationsRolledBack = %d, want 3 (all input ops rolled back)", got.OperationsRolledBack)
	}
	if got.OperationsSkipped != 0 {
		t.Fatalf("OperationsSkipped = %d, want 0", got.OperationsSkipped)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0", got.OperationsApplied)
	}
	if len(got.OperationResults) != 3 {
		t.Fatalf("OperationResults count = %d, want 3", len(got.OperationResults))
	}
	for i, opResult := range got.OperationResults {
		if opResult.Applied != false {
			t.Fatalf("OperationResults[%d].Applied = %v, want false", i, opResult.Applied)
		}
		if opResult.FileHash != "" {
			t.Fatalf("OperationResults[%d].FileHash = %q, want empty", i, opResult.FileHash)
		}
	}
	if _, err := os.Stat(filepath.Join(readonlyDir, "file1.txt")); !os.IsNotExist(err) {
		t.Fatalf("file1.txt exists after failed commit, err=%v", err)
	}
}

func TestMutateSuccessfulBatchMarksApplied(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "a", "new_string": "A"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "b", "new_string": "B"},
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "c", "new_string": "C"},
		},
	})

	total := 3
	if got.OperationsApplied+got.OperationsFailed+got.OperationsRolledBack+got.OperationsSkipped != total {
		t.Fatalf("accounting sum = %d + %d + %d + %d = %d, want %d",
			got.OperationsApplied, got.OperationsFailed, got.OperationsRolledBack, got.OperationsSkipped,
			got.OperationsApplied+got.OperationsFailed+got.OperationsRolledBack+got.OperationsSkipped, total)
	}
	if got.OperationsApplied != 3 {
		t.Fatalf("OperationsApplied = %d, want 3", got.OperationsApplied)
	}
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if got.OperationsRolledBack != 0 {
		t.Fatalf("OperationsRolledBack = %d, want 0", got.OperationsRolledBack)
	}
	if len(got.OperationResults) != 3 {
		t.Fatalf("OperationResults count = %d, want 3", len(got.OperationResults))
	}
	for i, opResult := range got.OperationResults {
		if opResult.Applied != true {
			t.Fatalf("OperationResults[%d].Applied = %v, want true", i, opResult.Applied)
		}
		if opResult.FileHash == "" {
			t.Fatalf("OperationResults[%d].FileHash is empty, want non-empty", i)
		}
	}
	assertFile(t, path, "A\nB\nC\n")
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
