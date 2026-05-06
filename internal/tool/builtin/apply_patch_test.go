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

func TestApplyPatchTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewApplyPatchTool(env)
	ctx := context.Background()

	t.Run("single_hunk_exact", func(t *testing.T) {
		content := "hello world\nfoo bar\nbaz qux\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "single.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "single.txt",
			"hunks": []any{
				map[string]any{"old": "foo bar", "new": "replaced"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 1 {
			t.Errorf("HunksApplied = %d, want 1", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "single.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != "hello world\nreplaced\nbaz qux\n" {
			t.Errorf("file content = %q, want %q", string(data), "hello world\nreplaced\nbaz qux\n")
		}
	})

	t.Run("multiple_hunks_atomic", func(t *testing.T) {
		content := "line1\nline2\nline3\nline4\nline5\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "multi.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "multi.txt",
			"hunks": []any{
				map[string]any{"old": "line1", "new": "changed1"},
				map[string]any{"old": "line3", "new": "changed3"},
				map[string]any{"old": "line5", "new": "changed5"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 3 {
			t.Errorf("HunksApplied = %d, want 3", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "multi.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := "changed1\nline2\nchanged3\nline4\nchanged5\n"
		if string(data) != want {
			t.Errorf("file content = %q, want %q", string(data), want)
		}
	})

	t.Run("hunk_no_match", func(t *testing.T) {
		content := "hello world\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "nomatch.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "nomatch.txt",
			"hunks": []any{
				map[string]any{"old": "nonexistent", "new": "replaced"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v (should return result, not Go error)", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksFailed != 1 {
			t.Errorf("HunksFailed = %d, want 1", res.HunksFailed)
		}
		if !strings.Contains(res.Output, "no match") {
			t.Errorf("Output does not contain 'no match': %q", res.Output)
		}
	})

	t.Run("hunk_ambiguous", func(t *testing.T) {
		content := "hello\nhello\nworld\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "ambig.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "ambig.txt",
			"hunks": []any{
				map[string]any{"old": "hello", "new": "hi"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v (should return result, not Go error)", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksFailed != 1 {
			t.Errorf("HunksFailed = %d, want 1", res.HunksFailed)
		}
		if !strings.Contains(res.Output, "ambiguous") {
			t.Errorf("Output does not contain 'ambiguous': %q", res.Output)
		}
	})

	t.Run("dry_run_no_write", func(t *testing.T) {
		content := "original content\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "dry.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":    "dry.txt",
			"dry_run": true,
			"hunks": []any{
				map[string]any{"old": "original content", "new": "modified content"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if !res.DryRun {
			t.Error("DryRun should be true")
		}
		if res.HunksApplied != 1 {
			t.Errorf("HunksApplied = %d, want 1", res.HunksApplied)
		}
		// Verify file was NOT modified
		data, err := os.ReadFile(filepath.Join(tmpDir, "dry.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		if string(data) != "original content\n" {
			t.Errorf("file content = %q, want original unchanged %q", string(data), "original content\n")
		}
	})

	t.Run("dry_run_shows_diff", func(t *testing.T) {
		content := "hello world\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "drydiff.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":    "drydiff.txt",
			"dry_run": true,
			"hunks": []any{
				map[string]any{"old": "hello world", "new": "changed world"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if !strings.Contains(res.Output, "Preview:") {
			t.Errorf("Output should contain 'Preview:', got %q", res.Output)
		}
		if !strings.Contains(res.Output, "@@") {
			t.Errorf("Output should contain diff-style '@@', got %q", res.Output)
		}
	})

	t.Run("empty_hunks", func(t *testing.T) {
		content := "some content\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "emptyhunks.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":  "emptyhunks.txt",
			"hunks": []any{},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 0 {
			t.Errorf("HunksApplied = %d, want 0", res.HunksApplied)
		}
	})

	t.Run("empty_file", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tmpDir, "empty.txt"), []byte(""), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		// Applying to an empty file - the old text won't match, so we get an error
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "empty.txt",
			"hunks": []any{
				map[string]any{"old": "content", "new": "new"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksFailed != 1 {
			t.Errorf("HunksFailed = %d, want 1", res.HunksFailed)
		}
	})

	t.Run("hunk_at_start", func(t *testing.T) {
		content := "first line\nsecond line\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "start.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "start.txt",
			"hunks": []any{
				map[string]any{"old": "first line", "new": "replaced first"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 1 {
			t.Errorf("HunksApplied = %d, want 1", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "start.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := "replaced first\nsecond line\n"
		if string(data) != want {
			t.Errorf("file content = %q, want %q", string(data), want)
		}
	})

	t.Run("hunk_at_end", func(t *testing.T) {
		content := "first line\nlast line"
		if err := os.WriteFile(filepath.Join(tmpDir, "end.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "end.txt",
			"hunks": []any{
				map[string]any{"old": "last line", "new": "replaced last"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 1 {
			t.Errorf("HunksApplied = %d, want 1", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "end.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := "first line\nreplaced last"
		if string(data) != want {
			t.Errorf("file content = %q, want %q", string(data), want)
		}
	})

	t.Run("hunk_with_special_chars", func(t *testing.T) {
		content := "price is $10.00 (regex chars: [foo].*bar?)\nnext line\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "special.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "special.txt",
			"hunks": []any{
				map[string]any{"old": "price is $10.00 (regex chars: [foo].*bar?)", "new": "replaced"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 1 {
			t.Errorf("HunksApplied = %d, want 1", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "special.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := "replaced\nnext line\n"
		if string(data) != want {
			t.Errorf("file content = %q, want %q", string(data), want)
		}
	})

	t.Run("handler_invalid_input", func(t *testing.T) {
		_, err := toolDef.Handler(ctx, map[string]any{
			"nonexistent": "value",
		})
		if err == nil {
			t.Error("expected error for unknown field")
		}
	})

	t.Run("handler_outside_workspace", func(t *testing.T) {
		_, err := toolDef.Handler(ctx, map[string]any{
			"path": "../outside.txt",
			"hunks": []any{
				map[string]any{"old": "foo", "new": "bar"},
			},
		})
		if err == nil {
			t.Error("expected error for path outside workspace")
		}
	})

	t.Run("hunks_applied_out_of_order", func(t *testing.T) {
		// Hunks provided in reverse order should still work because sorting
		// by position happens before application.
		content := "AAA\nBBB\nCCC\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "outoforder.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "outoforder.txt",
			"hunks": []any{
				map[string]any{"old": "CCC", "new": "ccc"},
				map[string]any{"old": "AAA", "new": "aaa"},
				map[string]any{"old": "BBB", "new": "bbb"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksApplied != 3 {
			t.Errorf("HunksApplied = %d, want 3", res.HunksApplied)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "outoforder.txt"))
		if err != nil {
			t.Fatalf("read file: %v", err)
		}
		want := "aaa\nbbb\nccc\n"
		if string(data) != want {
			t.Errorf("file content = %q, want %q", string(data), want)
		}
	})

	t.Run("overlapping_hunks_rejected", func(t *testing.T) {
		content := "the quick brown fox jumps over the lazy dog\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "overlap.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": "overlap.txt",
			"hunks": []any{
				map[string]any{"old": "quick brown", "new": "fast brown"},
				map[string]any{"old": "brown fox jumps", "new": "red fox leaps"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v (should return result, not Go error)", err)
		}
		res, ok := resultI.(*ApplyPatchResult)
		if !ok {
			t.Fatalf("result type = %T, want *ApplyPatchResult", resultI)
		}
		if res.HunksFailed != 2 {
			t.Errorf("HunksFailed = %d, want 2", res.HunksFailed)
		}
		if !strings.Contains(res.Output, "overlap") {
			t.Errorf("Output should contain 'overlap', got %q", res.Output)
		}
	})
}

func TestApplyPatchResultWasMutated(t *testing.T) {
	tests := []struct {
		name   string
		result *ApplyPatchResult
		want   bool
	}{
		{
			name: "successful patch",
			result: &ApplyPatchResult{
				Path:         "note.txt",
				HunksApplied: 1,
			},
			want: true,
		},
		{
			name: "dry run",
			result: &ApplyPatchResult{
				Path:         "note.txt",
				HunksApplied: 1,
				DryRun:       true,
			},
			want: false,
		},
		{
			name: "failed patch",
			result: &ApplyPatchResult{
				Path:         "note.txt",
				HunksApplied: 1,
				HunksFailed:  1,
			},
			want: false,
		},
		{
			name: "no hunks applied",
			result: &ApplyPatchResult{
				Path: "note.txt",
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.WasMutated(); got != tc.want {
				t.Fatalf("WasMutated() = %v, want %v", got, tc.want)
			}
		})
	}
}
