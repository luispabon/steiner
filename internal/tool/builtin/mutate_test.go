package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// newMutateTestTool builds a mutate tool whose FileObserved checker always
// reports true. Nearly all tests in this file exercise replace mechanics
// (diagnostics, hash verification, batch accounting) unrelated to the
// observation guard, which has its own dedicated tests
// (TestMutateReplaceObservationGuard); defaulting to "observed" here keeps
// them exercising what they actually test.
func newMutateTestTool(t *testing.T, root string) tool.ToolDef {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	return NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
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
	if len(got.Created) != 2 {
		t.Fatalf("Created = %v, want 2 items (created.txt, written.txt)", got.Created)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "note.txt" {
		t.Fatalf("Modified = %v, want [note.txt]", got.Modified)
	}
	if len(got.Deleted) != 1 || got.Deleted[0] != "written.txt" {
		t.Fatalf("Deleted = %v, want [written.txt]", got.Deleted)
	}
	if len(got.Moved) != 1 || got.Moved[0].From != "old.txt" || got.Moved[0].To != "new.txt" {
		t.Fatalf("Moved = %v, want [old.txt -> new.txt]", got.Moved)
	}
	if got.Output != "" {
		t.Fatalf("Output = %q, want empty string on success", got.Output)
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
	if got.FileHashes["note.txt"] != FileContentHash([]byte("alpha\nBETA\ncharlie\n")) {
		t.Fatalf("file hash = %q, want hash for final content", got.FileHashes["note.txt"])
	}
	// Single-match replaces are filtered from OperationResults on success.
	if len(got.OperationResults) != 0 {
		t.Fatalf("len(OperationResults) = %d, want 0 (single-match replaces filtered)", len(got.OperationResults))
	}
}

func TestMutateSuccessEnvelopeIsCompact(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ncharlie\ndelta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
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

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal success result: %v", err)
	}
	for _, field := range []string{"resolved_path", "file_hash", "assertions", "context"} {
		if strings.Contains(string(b), `"`+field+`"`) {
			t.Errorf("success JSON unexpectedly contains %q field: %s", field, b)
		}
	}
	if len(b) >= 500 {
		t.Fatalf("success result = %d bytes, want under 500 for a representative single-file replace: %s", len(b), b)
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
			name:      "unsupported type: chmod",
			input:     map[string]any{"operations": []any{map[string]any{"type": "chmod", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name:      "unsupported type: line_replace (removed)",
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name:      "unsupported type: delete_line (removed)",
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete_line", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name:      "unsupported type: insert_before (removed)",
			input:     map[string]any{"operations": []any{map[string]any{"type": "insert_before", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name:      "unsupported type: insert_after (removed)",
			input:     map[string]any{"operations": []any{map[string]any{"type": "insert_after", "path": "note.txt"}}},
			wantError: "unsupported type",
		},
		{
			name:      "unsupported type: delete (use delete_file)",
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete", "path": "note.txt"}}},
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

func TestMutateReplaceInsertionPatterns(t *testing.T) {
	t.Run("insert before anchor in middle", func(t *testing.T) {
		root := t.TempDir()
		content := "line1\nline2\nline3\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "line2", "new_string": "inserted\nline2"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "line1\ninserted\nline2\nline3\n")
	})

	t.Run("insert after anchor in middle", func(t *testing.T) {
		root := t.TempDir()
		content := "line1\nline2\nline3\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "line2", "new_string": "line2\ninserted"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "line1\nline2\ninserted\nline3\n")
	})

	t.Run("insert at end of file with no trailing newline", func(t *testing.T) {
		root := t.TempDir()
		content := "line1\nline2"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "line2", "new_string": "line2\nline3"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "line1\nline2\nline3")
	})
}

// TestMutateSuccessOutputOmitsDiff locks in that a successful mutate no
// longer echoes a unified diff back in Output — the model authored the
// change itself and doesn't need it repeated. unifiedTextDiff's hunk
// formatting is covered directly by mutate_diff_test.go.
func TestMutateSuccessOutputOmitsDiff(t *testing.T) {
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
	// On success, Output is empty and status is available via Modified array.
	if got.Output != "" {
		t.Fatalf("Output = %q, want empty on success", got.Output)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "note.txt" {
		t.Fatalf("Modified = %v, want [note.txt]", got.Modified)
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
	if !strings.Contains(got.Output, "retry with old_string set to the file text shown above") {
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
	// On success, Output is empty and status is available via status arrays.
	if got.Output != "" {
		t.Fatalf("Output = %q, want empty on success", got.Output)
	}
	if len(got.Modified) != 2 {
		t.Fatalf("Modified = %v, want 2 items (a.txt, b.txt)", got.Modified)
	}
	if len(got.Created) != 1 || got.Created[0] != "c.txt" {
		t.Fatalf("Created = %v, want [c.txt]", got.Created)
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

func TestMutateSuccessOutputStaysSmallForLargeWrite(t *testing.T) {
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
	// Success output omits diffs entirely, so it stays tiny regardless of
	// how large the underlying write was.
	if len(got.Output) > 200 {
		t.Fatalf("Output len = %d, want small status-only output: %q", len(got.Output), got.Output)
	}
	if strings.Contains(got.Output, "after\n") {
		t.Fatalf("Output unexpectedly contains file content: %q", got.Output)
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

	// A trailing failing operation forces the whole batch to fail, which
	// preserves the first operation's context in the failure envelope
	// (context is stripped on success, but left intact on failure).
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":       "replace",
				"path":       "big.txt",
				"old_string": "needle",
				"new_string": "NEEDLE",
			},
			map[string]any{
				"type":       "replace",
				"path":       "missing.txt",
				"old_string": "x",
				"new_string": "y",
			},
		},
	})
	if got.OperationsFailed == 0 {
		t.Fatalf("mutate succeeded, want failure: %#v", got)
	}
	if len(got.OperationResults) != 1 {
		t.Fatalf("len(OperationResults) = %d, want 1", len(got.OperationResults))
	}
	if got.OperationResults[0].ResolvedPath == "" {
		t.Fatal("ResolvedPath is empty, want populated on failure")
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
		hash := FileContentHash([]byte(content))
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
		hash := FileContentHash([]byte(content))

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
		hash := FileContentHash([]byte(content))

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
		modifiedHash := FileContentHash([]byte(modifiedContent))

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
		xxxHash := FileContentHash([]byte("xxx\n"))
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
		hash := FileContentHash([]byte(content))

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
		hash := FileContentHash([]byte(content))
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
		oldHash := FileContentHash([]byte(originalContent))

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
			name:    "delete with content",
			op:      map[string]any{"type": "delete_file", "path": "exist.txt", "content": "stuff"},
			wantErr: `field "content" is not valid`,
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
		{"delete_file missing path", MutateOperation{Type: "delete_file"}, "path is required"},
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
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "fresh.txt", "content": "fresh\n"},
			map[string]any{"type": "write", "path": "another.txt", "content": "written\n"},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 2 {
		t.Fatalf("result = %#v", got)
	}
	assertFile(t, filepath.Join(root, "fresh.txt"), "fresh\n")
	assertFile(t, filepath.Join(root, "another.txt"), "written\n")
}

func TestMutateWriteEmptyToExistingFileReturnsError(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("data\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "existing.txt", "content": ""},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("expected 1 failed operation, got %d", got.OperationsFailed)
	}
	if !strings.Contains(got.Output, "use delete_file") {
		t.Fatalf("output %q does not suggest delete_file", got.Output)
	}
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
	// Single-match replaces (MatchCount=1) are filtered from OperationResults on success.
	if len(got.OperationResults) != 0 {
		t.Fatalf("OperationResults count = %d, want 0 (single-match replaces filtered)", len(got.OperationResults))
	}
	if got.FileHashes["note.txt"] == "" {
		t.Fatal("FileHashes[note.txt] is empty, want non-empty on success")
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

func TestMutateSchemaOpTypes(t *testing.T) {
	schema := MutateSchema()
	props := schema["properties"].(map[string]any)
	operations := props["operations"].(map[string]any)
	items := operations["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	typeField := itemProps["type"].(map[string]any)
	enumVal := typeField["enum"].([]string)

	wantTypes := []string{"create", "delete_file", "move", "replace", "write"}
	if len(enumVal) != len(wantTypes) {
		t.Fatalf("schema enum has %d types, want exactly %d", len(enumVal), len(wantTypes))
	}

	typeMap := make(map[string]bool)
	for _, t := range enumVal {
		typeMap[t] = true
	}
	for _, want := range wantTypes {
		if !typeMap[want] {
			t.Errorf("schema missing type %q", want)
		}
	}
}

func TestMutateSuccessProjectionSingleCreate(t *testing.T) {
	// Test that a single create operation returns trimmed JSON with only
	// created, file_hashes, operations_applied and no paths, operation_results, or output.
	root := t.TempDir()
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "POEM.md", "content": "Roses are red\nViolets are blue\n"},
		},
	})

	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if got.OperationsApplied != 1 {
		t.Fatalf("OperationsApplied = %d, want 1", got.OperationsApplied)
	}

	// Verify the trimmed fields are present/correct.
	if len(got.Created) != 1 || got.Created[0] != "POEM.md" {
		t.Fatalf("Created = %v, want [POEM.md]", got.Created)
	}
	if len(got.FileHashes) != 1 {
		t.Fatalf("len(FileHashes) = %d, want 1", len(got.FileHashes))
	}

	// Verify the dropped fields are absent/empty.
	if got.Paths != nil {
		t.Fatalf("Paths = %v, want nil on success", got.Paths)
	}
	if got.OperationResults != nil {
		t.Fatalf("OperationResults = %v, want nil on success (no MatchCount > 1 entries)", got.OperationResults)
	}
	if got.Output != "" {
		t.Fatalf("Output = %q, want empty on success", got.Output)
	}

	// Marshal and verify JSON shape matches plan target.
	// Success envelope is trimmed: paths, operation_results, and output absent (omitempty).
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to check keys: %v", err)
	}
	if _, ok := m["paths"]; ok {
		t.Errorf("JSON unexpectedly contains 'paths' key")
	}
	if _, ok := m["operation_results"]; ok {
		t.Errorf("JSON unexpectedly contains 'operation_results' key")
	}
	if _, ok := m["output"]; ok {
		t.Errorf("JSON unexpectedly contains 'output' key")
	}
	if _, ok := m["created"]; !ok {
		t.Errorf("JSON missing 'created' key")
	}
	if _, ok := m["file_hashes"]; !ok {
		t.Errorf("JSON missing 'file_hashes' key")
	}
	if _, ok := m["operations_applied"]; !ok {
		t.Errorf("JSON missing 'operations_applied' key")
	}
}

func TestMutateMultiMatchReplaceKeepsOperationResult(t *testing.T) {
	// Test that a replace with MatchCount > 1 keeps the operation result
	// with only Index, Type, Path, MatchCount fields.
	root := t.TempDir()
	content := "foo\nfoo\nbar\nfoo\n"
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":        "replace",
				"path":        "file.txt",
				"old_string":  "foo",
				"new_string":  "FOO",
				"replace_all": true,
			},
		},
	})

	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if len(got.OperationResults) != 1 {
		t.Fatalf("len(OperationResults) = %d, want 1 (MatchCount > 1 entry kept)", len(got.OperationResults))
	}

	op := got.OperationResults[0]
	if op.Index != 1 || op.Type != "replace" || op.Path != "file.txt" {
		t.Fatalf("operation result = %#v, want Index=1, Type=replace, Path=file.txt", op)
	}
	if op.MatchCount != 3 {
		t.Fatalf("MatchCount = %d, want 3", op.MatchCount)
	}

	// Verify other fields are cleared.
	if op.ResolvedPath != "" {
		t.Errorf("ResolvedPath = %q, want empty", op.ResolvedPath)
	}
	if op.FileHash != "" {
		t.Errorf("FileHash = %q, want empty", op.FileHash)
	}
	if op.Assertions != nil {
		t.Errorf("Assertions = %v, want nil", op.Assertions)
	}
	if op.Context != nil {
		t.Errorf("Context = %v, want nil", op.Context)
	}
}

func TestMutateFailurePayloadUnchanged(t *testing.T) {
	// Test that a batch with a failure emits the full envelope unchanged:
	// paths, operation_results with resolved_path/file_hash/assertions intact, output.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists.txt"), []byte("content\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{
				"type":        "replace",
				"path":        "exists.txt",
				"old_string":  "content",
				"new_string":  "CONTENT",
				"replace_all": true,
			},
			map[string]any{
				"type":    "create",
				"path":    "missing_parent/new.txt",
				"content": "new\n",
			},
		},
	})

	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if got.OperationsApplied != 0 {
		t.Fatalf("OperationsApplied = %d, want 0 (atomic failure)", got.OperationsApplied)
	}

	// Failure payload should have empty Paths (set by clearCommittedMetadata).
	if len(got.Paths) != 0 {
		t.Fatalf("Paths = %v, want empty slice on failure", got.Paths)
	}

	// Output should be a prose diagnostic.
	if got.Output == "" {
		t.Fatal("Output is empty, want prose diagnostic on failure")
	}

	// Verify the JSON shape includes output on failure.
	// Note: Added json:"paths,omitempty" to Paths field for success-path trimming.
	// This causes empty Paths arrays to be omitted from failure-path JSON too
	// (acceptable edge case: zero-dirty-paths failure scenario).
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to check keys: %v", err)
	}
	if _, ok := m["paths"]; ok {
		t.Errorf("JSON unexpectedly contains 'paths' key on failure (omitted via omitempty on empty array)")
	}
	if _, ok := m["output"]; !ok {
		t.Errorf("JSON missing 'output' key on failure")
	}
}

func TestMutateMutatedPathsMethod(t *testing.T) {
	// Test that MutatedPaths() returns the union of all touched paths.
	// This guards the consumer-side interface added in tool_exec.go.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "modify.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "move_me.txt"), []byte("move\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "new\n"},
			map[string]any{"type": "write", "path": "written.txt", "content": "written\n"},
			map[string]any{"type": "replace", "path": "modify.txt", "old_string": "old", "new_string": "new"},
			map[string]any{"type": "delete_file", "path": "written.txt"},
			map[string]any{"type": "move", "from": "move_me.txt", "to": "moved.txt"},
		},
	})

	paths := got.MutatedPaths()

	// All touched paths should be present (both old and new names for moved).
	pathMap := make(map[string]bool)
	for _, p := range paths {
		pathMap[p] = true
	}

	wantPaths := []string{"created.txt", "written.txt", "modify.txt", "move_me.txt", "moved.txt"}
	for _, want := range wantPaths {
		if !pathMap[want] {
			t.Errorf("MutatedPaths() missing %q; got %v", want, paths)
		}
	}

	// No duplicates.
	seen := make(map[string]bool)
	for _, p := range paths {
		if seen[p] {
			t.Errorf("MutatedPaths() has duplicate path %q", p)
		}
		seen[p] = true
	}
}
