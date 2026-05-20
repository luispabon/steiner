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

func TestEditTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewEditTool(env)
	ctx := context.Background()

	run := func(t *testing.T, fileName string, content []byte, input map[string]any) (*MutationResult, []byte) {
		t.Helper()
		fullPath := filepath.Join(tmpDir, fileName)
		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("read test file: %v", err)
		}
		return result, data
	}

	t.Run("single exact replacement", func(t *testing.T) {
		result, data := run(t, "single.txt", []byte("hello world\nfoo bar\nbaz qux\n"), map[string]any{
			"path":       "single.txt",
			"old_string": "foo bar",
			"new_string": "replaced",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if result.Path != "single.txt" {
			t.Fatalf("Path = %q, want %q", result.Path, "single.txt")
		}
		if got, want := string(data), "hello world\nreplaced\nbaz qux\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: replaced 1 occurrence") {
			t.Fatalf("Output = %q, want replaced prefix", result.Output)
		}
	})

	t.Run("no match", func(t *testing.T) {
		result, data := run(t, "nomatch.txt", []byte("hello world\n"), map[string]any{
			"path":       "nomatch.txt",
			"old_string": "nonexistent text",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "hello world\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: no match for old_string") {
			t.Fatalf("Output = %q, want no-match prefix", result.Output)
		}
		if !strings.Contains(result.Output, "no nearby exact anchor found") {
			t.Fatalf("Output = %q, want anchor diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "suggestion: reread a slightly wider region") {
			t.Fatalf("Output = %q, want reread suggestion", result.Output)
		}
	})

	t.Run("whitespace_only_mismatch", func(t *testing.T) {
		result, data := run(t, "whitespace.txt", []byte("alpha beta\n"), map[string]any{
			"path":       "whitespace.txt",
			"old_string": "alpha   beta",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "alpha beta\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "normalized whitespace match exists") {
			t.Fatalf("Output = %q, want whitespace diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "nearest anchor at line 1") {
			t.Fatalf("Output = %q, want anchor diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "context:") {
			t.Fatalf("Output = %q, want context preview", result.Output)
		}
		if !strings.Contains(result.Output, "suggestion: use line_replace with a line number for whitespace-sensitive edits") {
			t.Fatalf("Output = %q, want whitespace-specific suggestion", result.Output)
		}
	})

	t.Run("ambiguous match with replace_all false", func(t *testing.T) {
		result, data := run(t, "ambiguous.txt", []byte("hello\nhello\nworld\n"), map[string]any{
			"path":       "ambiguous.txt",
			"old_string": "hello",
			"new_string": "hi",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "hello\nhello\nworld\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "ambiguous match for old_string") {
			t.Fatalf("Output = %q, want ambiguous match message", result.Output)
		}
		if !strings.Contains(result.Output, "2 occurrences") {
			t.Fatalf("Output = %q, want occurrence count", result.Output)
		}
	})

	t.Run("ambiguous match with replace_all true", func(t *testing.T) {
		result, data := run(t, "replace_all.txt", []byte("hello\nhello\nworld\n"), map[string]any{
			"path":        "replace_all.txt",
			"old_string":  "hello",
			"new_string":  "hi",
			"replace_all": true,
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "hi\nhi\nworld\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: replaced 2 occurrences") {
			t.Fatalf("Output = %q, want replaced prefix", result.Output)
		}
	})

	t.Run("trailing eof hunk ending in braces", func(t *testing.T) {
		result, data := run(t, "trailing.txt", []byte("func main() {\n\tprintln(\"hi\")\n}\n"), map[string]any{
			"path":       "trailing.txt",
			"old_string": "}\n",
			"new_string": "}\n\n// done\n",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "func main() {\n\tprintln(\"hi\")\n}\n\n// done\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("no final newline file", func(t *testing.T) {
		result, data := run(t, "nonewline.txt", []byte("alpha\nbeta"), map[string]any{
			"path":       "nonewline.txt",
			"old_string": "beta",
			"new_string": "gamma",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "alpha\ngamma"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("binary file rejection", func(t *testing.T) {
		result, data := run(t, "binary.bin", []byte{'a', 0, 'b', 'c'}, map[string]any{
			"path":       "binary.bin",
			"old_string": "a",
			"new_string": "z",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if !strings.Contains(result.Output, "binary") {
			t.Fatalf("Output = %q, want binary rejection", result.Output)
		}
		if got, want := string(data), string([]byte{'a', 0, 'b', 'c'}); got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("empty old_string rejection", func(t *testing.T) {
		result, data := run(t, "emptyold.txt", []byte("hello world\n"), map[string]any{
			"path":       "emptyold.txt",
			"old_string": "",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := result.Output, "edit: old_string is empty"; got != want {
			t.Fatalf("Output = %q, want %q", got, want)
		}
		if got, want := string(data), "hello world\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})
}
