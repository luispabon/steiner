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
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "two", "new_string": "TWO", "replace_all": true},
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "old_string": "TWO", "new_string": "line two"},
			map[string]any{"type": "delete", "path": "written.txt"},
			map[string]any{"type": "move", "from": "old.txt", "to": "new.txt"},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 7 {
		t.Fatalf("mutate result = %#v", got)
	}
	if !got.WasMutated() {
		t.Fatal("mutate WasMutated() = false, want true")
	}
	assertFile(t, filepath.Join(root, "created.txt"), "created\n")
	assertFile(t, filepath.Join(root, "note.txt"), "ONE\nline two\nTWO\n")
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

func TestMutateLineReplaceRepeatedSubstitutions(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "patch_woes.md")
	content := "alpha\nfoo = 1\nbeta\nfoo = 1\ngamma\nfoo = 1\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "line_replace", "path": "patch_woes.md", "line": float64(2), "old_string": "foo = 1", "new_string": "foo = 2"},
			map[string]any{"type": "line_replace", "path": "patch_woes.md", "line": float64(4), "old_string": "foo = 1", "new_string": "foo = 3"},
			map[string]any{"type": "line_replace", "path": "patch_woes.md", "line": float64(6), "old_string": "foo = 1", "new_string": "foo = 4"},
		},
	})
	if got.OperationsFailed != 0 || got.OperationsApplied != 3 {
		t.Fatalf("mutate result = %#v", got)
	}
	assertFile(t, path, "alpha\nfoo = 2\nbeta\nfoo = 3\ngamma\nfoo = 4\n")
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
	assertFile(t, path, "one\n")
}

func TestMutateDryRunDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateTestTool(t, root)
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, toolDef, map[string]any{
		"dry_run": true,
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
		},
	})
	if got.WasMutated() {
		t.Fatal("WasMutated() = true for dry run")
	}
	assertFile(t, path, "one\n")
}

func TestMutateRejectsInvalidOperations(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, root string)
		input     map[string]any
		wantError string
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
			name: "wrong line",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "old_string": "x", "new_string": "y"}}},
			wantError: "outside file",
		},
		{
			name: "wrong old string on correct line",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("target line\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "missing", "new_string": "new"}}},
			wantError: "contains old_string 0 times",
		},
		{
			name: "line replace ambiguous within one line",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "x", "new_string": "y"}}},
			wantError: "contains old_string 2 times",
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
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete", "path": "dir"}}},
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
			input:     map[string]any{"operations": []any{map[string]any{"type": "delete", "path": "missing.txt"}}},
			wantError: "does not exist",
		},
		{
			name:      "missing parent directory",
			input:     map[string]any{"operations": []any{map[string]any{"type": "write", "path": "missing/note.txt", "content": "x"}}},
			wantError: "parent directory",
		},
		{
			name: "line_replace old_string with trailing newline",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "hello\n", "new_string": "hi"}}},
			wantError: "old_string contains newline",
		},
		{
			name: "line_replace old_string with embedded newline",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "hello\nworld", "new_string": "hi"}}},
			wantError: "old_string contains newline",
		},
		{
			name: "line_replace on empty file",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(""), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "", "new_string": "x"}}},
			wantError: "outside file",
		},
		{
			name: "line_replace line zero",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(0), "old_string": "hello", "new_string": "world"}}},
			wantError: "line must be >= 1",
		},
		{
			name: "line_count exceeds file bounds",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "line_count": float64(3), "new_string": ""}}},
			wantError: "exceeds file length",
		},
		{
			name: "line_count with old_string",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			input:     map[string]any{"operations": []any{map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "line_count": float64(1), "old_string": "a", "new_string": "x"}}},
			wantError: "old_string cannot be used with line_count",
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
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "A b", "new_string": "AB"},
				map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "AB c", "new_string": "done"},
			},
			want: "done\n",
		},
		{
			name:    "delete then create same path acts on in-memory state",
			initial: "old\n",
			operations: []any{
				map[string]any{"type": "delete", "path": "note.txt"},
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

func TestMutateLineReplaceEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		line    any
		old     string
		new     string
		want    string
	}{
		{
			name:    "last line without trailing newline",
			initial: "first\nsecond",
			line:    float64(2),
			old:     "second",
			new:     "SECOND",
			want:    "first\nSECOND",
		},
		{
			name:    "preserves crlf line endings",
			initial: "first\r\nsecond\r\n",
			line:    float64(2),
			old:     "second",
			new:     "SECOND",
			want:    "first\r\nSECOND\r\n",
		},
		{
			name:    "line number supplied as string",
			initial: "first\nsecond\n",
			line:    "2",
			old:     "second",
			new:     "SECOND",
			want:    "first\nSECOND\n",
		},
		{
			name:    "old string appears elsewhere but once on target line",
			initial: "needle\nneedle here\nneedle\n",
			line:    float64(2),
			old:     "needle here",
			new:     "target",
			want:    "needle\ntarget\nneedle\n",
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
					map[string]any{"type": "line_replace", "path": "note.txt", "line": tt.line, "old_string": tt.old, "new_string": tt.new},
				},
			})
			if got.OperationsFailed != 0 {
				t.Fatalf("mutate failed: %#v", got)
			}
			assertFile(t, path, tt.want)
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
	if !strings.Contains(got.Output, "line_replace") {
		t.Fatalf("Output missing line_replace suggestion:\n%s", got.Output)
	}
}

func TestMutateDryRunIncludesDiff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("aaa\nbbb\nccc\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"dry_run": true,
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "bbb", "new_string": "BBB"},
		},
	})
	if got.WasMutated() {
		t.Fatal("WasMutated() = true for dry run")
	}
	if !strings.Contains(got.Output, "-bbb") || !strings.Contains(got.Output, "+BBB") {
		t.Fatalf("dry run Output missing diff:\n%s", got.Output)
	}
	assertFile(t, filepath.Join(root, "note.txt"), "aaa\nbbb\nccc\n")
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

func TestMutateLineReplaceOnFirstLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "first", "new_string": "FIRST"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, path, "FIRST\nsecond\nthird\n")
}

func TestMutateLineReplaceOnSingleLineFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("only\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(1), "old_string": "only", "new_string": "replaced"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, path, "replaced\n")
}

func TestMutatePlanningFailureDoesNotCreateIntermediateFiles(t *testing.T) {
	root := t.TempDir()
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
			map[string]any{"type": "delete", "path": "missing.txt"},
		},
	})
	if got.OperationsFailed != 1 {
		t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("created.txt exists after planning failure, err=%v", err)
	}
}

func TestMutateOutputIsBounded(t *testing.T) {
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
	if !strings.Contains(got.Output, "<truncated>") {
		t.Fatalf("Output missing truncation marker")
	}
}

func TestMutateLineReplaceWholeLineReplacement(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		line    int
		newStr  string
		want    string
	}{
		{name: "LF terminated", initial: "alpha\nbeta\ngamma\n", line: 2, newStr: "BETA", want: "alpha\nBETA\ngamma\n"},
		{name: "CRLF terminated", initial: "alpha\r\nbeta\r\ngamma\r\n", line: 2, newStr: "BETA", want: "alpha\r\nBETA\r\ngamma\r\n"},
		{name: "last line no trailing newline", initial: "alpha\nbeta", line: 2, newStr: "BETA", want: "alpha\nBETA"},
		{name: "first line", initial: "alpha\nbeta\ngamma\n", line: 1, newStr: "ALPHA", want: "ALPHA\nbeta\ngamma\n"},
		{name: "single line file", initial: "only\n", line: 1, newStr: "replaced", want: "replaced\n"},
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
					map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(tt.line), "old_string": "", "new_string": tt.newStr},
				},
			})
			if got.OperationsFailed != 0 {
				t.Fatalf("mutate failed: %#v", got)
			}
			assertFile(t, path, tt.want)
		})
	}
}

func TestMutateLineReplaceWholeLinePreservesOtherLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	initial := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(3), "old_string": "", "new_string": "LINE3"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, path, "line1\nline2\nLINE3\nline4\nline5\n")
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

func TestMutateLineReplaceLineCount(t *testing.T) {
	tests := []struct {
		name      string
		initial   string
		line      int
		lineCount int
		oldStr    string
		newStr    string
		want      string
	}{
		{
			name:      "delete single line",
			initial:   "a\nb\nc\n",
			line:      2,
			lineCount: 1,
			newStr:    "",
			want:      "a\nc\n",
		},
		{
			name:      "delete range at start",
			initial:   "a\nb\nc\nd\n",
			line:      1,
			lineCount: 2,
			newStr:    "",
			want:      "c\nd\n",
		},
		{
			name:      "delete range in middle",
			initial:   "a\nb\nc\nd\ne\n",
			line:      2,
			lineCount: 3,
			newStr:    "",
			want:      "a\ne\n",
		},
		{
			name:      "delete range at end",
			initial:   "a\nb\nc\nd\n",
			line:      3,
			lineCount: 2,
			newStr:    "",
			want:      "a\nb\n",
		},
		{
			name:      "delete all lines",
			initial:   "a\nb\nc\n",
			line:      1,
			lineCount: 3,
			newStr:    "",
			want:      "",
		},
		{
			name:      "replace range with single line",
			initial:   "a\nb\nc\nd\n",
			line:      2,
			lineCount: 2,
			newStr:    "X",
			want:      "a\nX\nd\n",
		},
		{
			name:      "replace range with multi-line block",
			initial:   "a\nb\nc\nd\n",
			line:      2,
			lineCount: 2,
			newStr:    "X\nY\nZ",
			want:      "a\nX\nY\nZ\nd\n",
		},
		{
			name:      "replace range new_string already ends with newline",
			initial:   "a\nb\nc\nd\n",
			line:      2,
			lineCount: 2,
			newStr:    "X\n",
			want:      "a\nX\nd\n",
		},
		{
			name:      "CRLF preservation",
			initial:   "a\r\nb\r\nc\r\n",
			line:      1,
			lineCount: 2,
			newStr:    "X",
			want:      "X\r\nc\r\n",
		},
		{
			name:      "last line without trailing newline",
			initial:   "a\nb\nc",
			line:      2,
			lineCount: 2,
			newStr:    "",
			want:      "a\n",
		},
		{
			name:      "line_count 0 defaults to 1",
			initial:   "a\nb\nc\n",
			line:      2,
			lineCount: 0,
			oldStr:    "b",
			newStr:    "B",
			want:      "a\nB\nc\n",
		},
		{
			name:      "line_count 1 replaces whole line",
			initial:   "a\nhello world\nc\n",
			line:      2,
			lineCount: 1,
			newStr:    "goodbye",
			want:      "a\ngoodbye\nc\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "note.txt")
			if err := os.WriteFile(path, []byte(tt.initial), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			op := map[string]any{
				"type":       "line_replace",
				"path":       "note.txt",
				"line":       float64(tt.line),
				"new_string": tt.newStr,
			}
			if tt.lineCount != 0 {
				op["line_count"] = float64(tt.lineCount)
			}
			if tt.oldStr != "" {
				op["old_string"] = tt.oldStr
			}
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{
				"operations": []any{op},
			})
			if got.OperationsFailed != 0 {
				t.Fatalf("mutate failed: %#v", got)
			}
			assertFile(t, path, tt.want)
		})
	}
}

func TestMutateLineReplaceLineCountBatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\nf\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{
		"operations": []any{
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "line_count": float64(2), "new_string": ""},
			map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(3), "old_string": "e", "new_string": "E"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("mutate failed: %#v", got)
	}
	assertFile(t, path, "a\nd\nE\nf\n")
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

	t.Run("valid hash on line_replace succeeds", func(t *testing.T) {
		root := t.TempDir()
		content := "alpha\nbeta\ngamma\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		hash := fileContentHash([]byte(content))
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "old_string": "beta", "new_string": "BETA", "file_hash": hash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "alpha\nBETA\ngamma\n")
	})

	t.Run("stale hash on line_replace fails", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "line_replace", "path": "note.txt", "line": float64(2), "old_string": "beta", "new_string": "BETA", "file_hash": "DEAD"},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
	})

	t.Run("hash on create new file ignored", func(t *testing.T) {
		root := t.TempDir()
		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "create", "path": "new.txt", "content": "new content\n", "file_hash": "BEEF"},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed: %#v", got)
		}
		assertFile(t, filepath.Join(root, "new.txt"), "new content\n")
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
				map[string]any{"type": "delete", "path": "note.txt", "file_hash": hash},
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
				map[string]any{"type": "delete", "path": "note2.txt", "file_hash": "DEAD"},
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

	t.Run("hash after prior operation modified same file", func(t *testing.T) {
		root := t.TempDir()
		content := "aaa\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		originalHash := fileContentHash([]byte(content))

		// First op modifies the file; second op provides hash of post-modification content
		modifiedContent := "AAA\n"
		modifiedHash := fileContentHash([]byte(modifiedContent))

		got := runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "aaa", "new_string": "AAA"},
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "AAA", "new_string": "BBB", "file_hash": modifiedHash},
			},
		})
		if got.OperationsFailed != 0 {
			t.Fatalf("mutate failed with post-mod hash: %#v", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "BBB\n")

		// Now test with original (now stale) hash — must fail
		if err := os.WriteFile(filepath.Join(root, "note2.txt"), []byte("xxx\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		got = runMutate(t, newMutateTestTool(t, root), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note2.txt", "old_string": "xxx", "new_string": "XXX"},
				map[string]any{"type": "replace", "path": "note2.txt", "old_string": "XXX", "new_string": "YYY", "file_hash": originalHash},
			},
		})
		if got.OperationsFailed != 1 {
			t.Fatalf("OperationsFailed = %d, want 1 for stale hash after prior op", got.OperationsFailed)
		}
		if !strings.Contains(got.Output, "file_hash mismatch") {
			t.Fatalf("Output = %q, want file_hash mismatch", got.Output)
		}
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
