package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchInputAcceptsPatchAndDryRun(t *testing.T) {
	in, err := decodeInput[ApplyPatchInput](map[string]any{
		"patch":   "*** Begin Patch\n*** End Patch",
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("decodeInput(apply_patch) error = %v", err)
	}
	if got, want := in.Patch, "*** Begin Patch\n*** End Patch"; got != want {
		t.Fatalf("Patch = %q, want %q", got, want)
	}
	if !in.DryRun {
		t.Fatal("DryRun = false, want true")
	}
}

func TestApplyPatchInputRejectsLegacyFields(t *testing.T) {
	_, err := decodeInput[ApplyPatchInput](map[string]any{
		"path": "note.txt",
		"hunks": []any{
			map[string]any{"old": "hello", "new": "world"},
		},
	})
	if err == nil {
		t.Fatal("decodeInput(apply_patch legacy input) = nil error, want unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decodeInput(apply_patch legacy input) error = %v, want unknown field error", err)
	}
}

func TestApplyPatchToolEmptyPatchReturnsGoError(t *testing.T) {
	t.Parallel()

	_, err := NewApplyPatchTool(Env{}).Handler(context.Background(), map[string]any{
		"patch": "   \n\t",
	})
	if err == nil {
		t.Fatal("Handler() error = nil, want patch required error")
	}
	if !strings.Contains(err.Error(), "patch is required") {
		t.Fatalf("Handler() error = %v, want patch required error", err)
	}
}

func TestApplyPatchToolMalformedPatchReturnsFailureResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolDef := NewApplyPatchTool(Env{WorkDir: root})

	result, err := toolDef.Handler(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"bogus",
			"*** End Patch",
		}, "\n"),
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("Handler() error = %v, want failure result", err)
	}

	got, ok := result.(*ApplyPatchResult)
	if !ok {
		t.Fatalf("Handler() result type = %T, want *ApplyPatchResult", result)
	}
	if !got.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if got.HunksFailed != 1 {
		t.Fatalf("HunksFailed = %d, want 1", got.HunksFailed)
	}
	if got.Output == "" {
		t.Fatal("Output = empty, want parse error text")
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("file mutation = %v, want no file written", err)
	}
}

func TestApplyPatchToolDryRunAddDoesNotWriteFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolDef := NewApplyPatchTool(Env{WorkDir: root})

	result, err := toolDef.Handler(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Add File: note.txt",
			"+hello",
			"*** End Patch",
		}, "\n"),
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}

	got, ok := result.(*ApplyPatchResult)
	if !ok {
		t.Fatalf("Handler() result type = %T, want *ApplyPatchResult", result)
	}
	if !got.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if got.HunksFailed != 0 {
		t.Fatalf("HunksFailed = %d, want 0", got.HunksFailed)
	}
	if len(got.Added) != 1 || got.Added[0] != "note.txt" {
		t.Fatalf("Added = %#v, want [note.txt]", got.Added)
	}
	if got.Output != "Dry run succeeded.\nUpdated the following files:\nA note.txt" {
		t.Fatalf("Output = %q, want dry-run summary", got.Output)
	}
	if _, err := os.Stat(filepath.Join(root, "note.txt")); !os.IsNotExist(err) {
		t.Fatalf("file mutation = %v, want no file written", err)
	}
}

func TestApplyPatchToolAddWritesNestedFileAndReturnsSummary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	toolDef := NewApplyPatchTool(Env{WorkDir: root})

	result, err := toolDef.Handler(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Add File: nested/note.txt",
			"+hello",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}

	got, ok := result.(*ApplyPatchResult)
	if !ok {
		t.Fatalf("Handler() result type = %T, want *ApplyPatchResult", result)
	}
	if got.DryRun {
		t.Fatal("DryRun = true, want false")
	}
	if got.HunksFailed != 0 {
		t.Fatalf("HunksFailed = %d, want 0", got.HunksFailed)
	}
	if len(got.Added) != 1 || got.Added[0] != "nested/note.txt" {
		t.Fatalf("Added = %#v, want [nested/note.txt]", got.Added)
	}
	if len(got.Paths) != 1 || got.Paths[0] != "nested/note.txt" {
		t.Fatalf("Paths = %#v, want [nested/note.txt]", got.Paths)
	}
	if got.Output != "Success.\nUpdated the following files:\nA nested/note.txt" {
		t.Fatalf("Output = %q, want add summary", got.Output)
	}

	data, err := os.ReadFile(filepath.Join(root, "nested", "note.txt"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("file content = %q, want %q", string(data), "hello\n")
	}
}

func TestApplyPatchToolUpdateModifiesExistingFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	result, err := NewApplyPatchTool(Env{WorkDir: root}).Handler(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: note.txt",
			"@@ hello",
			"-world",
			"+planet",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}

	got, ok := result.(*ApplyPatchResult)
	if !ok {
		t.Fatalf("Handler() result type = %T, want *ApplyPatchResult", result)
	}
	if got.HunksFailed != 0 {
		t.Fatalf("HunksFailed = %d, want 0; output=%q", got.HunksFailed, got.Output)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "note.txt" {
		t.Fatalf("Modified = %#v, want [note.txt]", got.Modified)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "hello\nplanet\n" {
		t.Fatalf("file content = %q, want %q", string(data), "hello\nplanet\n")
	}
}

func TestApplyPatchToolInvalidApplyReturnsFailureResult(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	result, err := NewApplyPatchTool(Env{WorkDir: root}).Handler(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: note.txt",
			"@@",
			"-missing",
			"+present",
			"*** End Patch",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Handler() error = %v, want failure result", err)
	}

	got, ok := result.(*ApplyPatchResult)
	if !ok {
		t.Fatalf("Handler() result type = %T, want *ApplyPatchResult", result)
	}
	if got.HunksFailed != 1 {
		t.Fatalf("HunksFailed = %d, want 1", got.HunksFailed)
	}
	if got.Output == "" {
		t.Fatal("Output = empty, want apply error text")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("file content = %q, want unchanged", string(data))
	}
}
