package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

const grepCountersBinaryContent = "\x00needle\n"

// newGrepCountersTool writes files under a new workspace and returns a grep
// tool rooted there, along with the workspace path.
func newGrepCountersTool(t *testing.T, files map[string]string) (tool.ToolDef, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	return NewGrepTool(Env{WorkDir: root, PathPolicy: &policy}), root
}

func TestGrepTool_ScanCounters(t *testing.T) {
	t.Run("counts searched files and binary skips", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"match.txt": "needle\n",
			"other.txt": "nothing here\n",
			"bin.dat":   grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
		})
		if result.FilesSearched != 2 {
			t.Errorf("FilesSearched = %d, want 2", result.FilesSearched)
		}
		if result.SkippedBinary != 1 {
			t.Errorf("SkippedBinary = %d, want 1", result.SkippedBinary)
		}
		if result.SkippedUnreadable != 0 || result.SkippedDirectories != 0 {
			t.Errorf("skipped unreadable = %d, directories = %d, want 0", result.SkippedUnreadable, result.SkippedDirectories)
		}
		if !strings.Contains(result.Output, "match.txt") {
			t.Errorf("Output = %q, want the matching file", result.Output)
		}
	})

	t.Run("no candidate files reports zero searched", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"bin.dat": grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if result.FilesSearched != 0 {
			t.Errorf("FilesSearched = %d, want 0", result.FilesSearched)
		}
		if result.SkippedBinary != 1 {
			t.Errorf("SkippedBinary = %d, want 1", result.SkippedBinary)
		}
		if result.Output != "No matches found" {
			t.Errorf("Output = %q, want the no matches sentinel", result.Output)
		}
		if result.Shown != 0 || result.NextOffset != 0 {
			t.Errorf("Shown = %d, NextOffset = %d, want 0 and 0", result.Shown, result.NextOffset)
		}
	})

	t.Run("searched candidate without a match", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"plain.txt": "nothing here\n",
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "count",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedBinary != 0 {
			t.Errorf("SkippedBinary = %d, want 0", result.SkippedBinary)
		}
		if result.Output != "No matches found" {
			t.Errorf("Output = %q, want the no matches sentinel", result.Output)
		}
	})

	t.Run("binary direct root is skipped and counted", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"bin.dat": grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"path":        "bin.dat",
		})
		if result.FilesSearched != 0 {
			t.Errorf("FilesSearched = %d, want 0", result.FilesSearched)
		}
		if result.SkippedBinary != 1 {
			t.Errorf("SkippedBinary = %d, want 1", result.SkippedBinary)
		}
		if result.Output != "No matches found" {
			t.Errorf("Output = %q, want the no matches sentinel", result.Output)
		}
	})

	t.Run("nonbinary direct root is counted as searched", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"match.txt": "needle\n",
			"bin.dat":   grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"path":        "match.txt",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedBinary != 0 {
			t.Errorf("SkippedBinary = %d, want 0 for a sibling binary file outside the root", result.SkippedBinary)
		}
		if !strings.Contains(result.Output, "needle") {
			t.Errorf("Output = %q, want the matching line", result.Output)
		}
	})

	t.Run("offset past the last result keeps the whole-search counters", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"alpha.txt": "needle\n",
			"bravo.txt": "needle\n",
			"bin.dat":   grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
			"offset":      10,
		})
		if result.Shown != 0 {
			t.Errorf("Shown = %d, want 0 past the last result", result.Shown)
		}
		if result.NextOffset != 0 {
			t.Errorf("NextOffset = %d, want 0 past the last result", result.NextOffset)
		}
		if result.Output != "" {
			t.Errorf("Output = %q, want empty output past the last result", result.Output)
		}
		if result.Matches != 2 {
			t.Errorf("Matches = %d, want the 2 logical results", result.Matches)
		}
		if result.FilesSearched != 2 {
			t.Errorf("FilesSearched = %d, want 2", result.FilesSearched)
		}
		if result.SkippedBinary != 1 {
			t.Errorf("SkippedBinary = %d, want 1", result.SkippedBinary)
		}
	})

	t.Run("counters cover the whole search for a page", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"alpha.txt": "needle one\nneedle two\nneedle three\n",
			"bravo.txt": "needle four\n",
		})

		for _, mode := range []string{"content", "files_with_matches", "count"} {
			t.Run(mode, func(t *testing.T) {
				result := runGrepForTest(t, toolDef, map[string]any{
					"pattern":     "needle",
					"output_mode": mode,
					"head_limit":  1,
				})
				if result.FilesSearched != 2 {
					t.Errorf("FilesSearched = %d, want 2", result.FilesSearched)
				}
				if result.NextOffset == 0 {
					t.Error("NextOffset = 0, want a next page")
				}
				if result.SkippedBinary != 0 {
					t.Errorf("SkippedBinary = %d, want 0", result.SkippedBinary)
				}
			})
		}
	})

	t.Run("glob filtered binary file is not a skip", func(t *testing.T) {
		toolDef, _ := newGrepCountersTool(t, map[string]string{
			"match.txt": "needle\n",
			"bin.dat":   grepCountersBinaryContent,
		})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"glob":        "*.txt",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedBinary != 0 {
			t.Errorf("SkippedBinary = %d, want 0 for a filtered file", result.SkippedBinary)
		}
	})

	t.Run("excluded directory is not counted", func(t *testing.T) {
		root := t.TempDir()
		for name, content := range map[string]string{
			"match.txt":        "needle\n",
			"vendor/bin.dat":   grepCountersBinaryContent,
			"vendor/notes.txt": "needle\n",
		} {
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		policy := tool.NewPathPolicy(root, config.PathsConfig{})
		excluder := tool.NewWorkspaceExcluder(root, []string{"vendor"}, nil)
		toolDef := NewGrepTool(Env{WorkDir: root, PathPolicy: &policy, Excluder: &excluder})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedBinary != 0 {
			t.Errorf("SkippedBinary = %d, want 0 for an excluded file", result.SkippedBinary)
		}
	})

	t.Run("policy denied directory is not counted unreadable", func(t *testing.T) {
		root := t.TempDir()
		for name, content := range map[string]string{
			"visible.txt":        "needle\n",
			"secrets/secret.txt": "needle\n",
			"secrets/bin.dat":    grepCountersBinaryContent,
		} {
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		policy := tool.NewPathPolicy(root, config.PathsConfig{BlockedPaths: []string{"secrets"}})
		toolDef := NewGrepTool(Env{WorkDir: root, PathPolicy: &policy})

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedBinary != 0 || result.SkippedUnreadable != 0 || result.SkippedDirectories != 0 {
			t.Errorf("skipped binary = %d, unreadable = %d, directories = %d, want 0",
				result.SkippedBinary, result.SkippedUnreadable, result.SkippedDirectories)
		}
	})
}

func TestGrepTool_ScanCountersUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission-based traversal/read failure test is unix-oriented")
	}

	t.Run("unreadable file is counted only after filters", func(t *testing.T) {
		toolDef, root := newGrepCountersTool(t, map[string]string{
			"visible.txt": "needle\n",
			"blocked.txt": "needle\n",
		})
		blockedPath := filepath.Join(root, "blocked.txt")
		if err := os.Chmod(blockedPath, 0o000); err != nil {
			t.Fatalf("chmod blocked file: %v", err)
		}
		defer func() { _ = os.Chmod(blockedPath, 0o644) }()
		if _, err := os.ReadFile(blockedPath); err == nil {
			t.Skip("filesystem does not deny file reads after chmod 000")
		}

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedUnreadable != 1 {
			t.Errorf("SkippedUnreadable = %d, want 1", result.SkippedUnreadable)
		}

		filtered := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
			"glob":        "visible*",
		})
		if filtered.SkippedUnreadable != 0 {
			t.Errorf("SkippedUnreadable = %d, want 0 for a filtered file", filtered.SkippedUnreadable)
		}
	})

	t.Run("unreadable directory is counted", func(t *testing.T) {
		toolDef, root := newGrepCountersTool(t, map[string]string{
			"visible.txt":        "needle\n",
			"blocked/secret.txt": "needle\n",
		})
		blockedDir := filepath.Join(root, "blocked")
		if err := os.Chmod(blockedDir, 0o000); err != nil {
			t.Fatalf("chmod blocked dir: %v", err)
		}
		defer func() { _ = os.Chmod(blockedDir, 0o755) }()
		if _, err := os.ReadDir(blockedDir); err == nil {
			t.Skip("filesystem does not deny directory traversal after chmod 000")
		}

		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if result.FilesSearched != 1 {
			t.Errorf("FilesSearched = %d, want 1", result.FilesSearched)
		}
		if result.SkippedDirectories != 1 {
			t.Errorf("SkippedDirectories = %d, want 1", result.SkippedDirectories)
		}
	})
}

func TestGrepResult_ScanCounterJSON(t *testing.T) {
	t.Parallel()

	withoutSkips, err := json.Marshal(GrepResult{FilesSearched: 3, Output: "No matches found"})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(withoutSkips), `"files_searched":3`) {
		t.Errorf("JSON = %s, want an always emitted files_searched", withoutSkips)
	}
	for _, absent := range []string{"skipped_binary", "skipped_unreadable", "skipped_directories"} {
		if strings.Contains(string(withoutSkips), absent) {
			t.Errorf("JSON = %s, want %s omitted when zero", withoutSkips, absent)
		}
	}

	withSkips, err := json.Marshal(GrepResult{FilesSearched: 1, SkippedBinary: 2, SkippedUnreadable: 3, SkippedDirectories: 4})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, field := range []string{`"skipped_binary":2`, `"skipped_unreadable":3`, `"skipped_directories":4`} {
		if !strings.Contains(string(withSkips), field) {
			t.Errorf("JSON = %s, want %s", withSkips, field)
		}
	}
}
