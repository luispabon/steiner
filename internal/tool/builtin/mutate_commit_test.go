package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestCommitAtomicWrite_FailureDoesNotCorruptFirstFile(t *testing.T) {
	root := t.TempDir()

	// Create two files; we'll inject failure on the second (alphabetically later).
	aPath := filepath.Join(root, "a.txt")
	bPath := filepath.Join(root, "b.txt")
	if err := os.WriteFile(aPath, []byte("original a"), 0o644); err != nil {
		t.Fatalf("setup WriteFile %q: %v", aPath, err)
	}
	if err := os.WriteFile(bPath, []byte("original b"), 0o644); err != nil {
		t.Fatalf("setup WriteFile %q: %v", bPath, err)
	}

	// To inject failure on b.txt, make its parent directory read-only.
	// This causes CreateTemp to fail (can't create in read-only dir).
	bDir := filepath.Dir(bPath)
	if err := os.Chmod(bDir, 0o555); err != nil {
		t.Fatalf("chmod dir to read-only: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(bDir, 0o755); err != nil {
			t.Errorf("cleanup chmod %q: %v", bDir, err)
		}
	})

	// Skip test if running as root (root ignores directory permissions).
	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}

	toolDef := newMutateTestTool(t, root)
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type":    "write",
				"path":    "a.txt",
				"content": "modified a",
			},
			map[string]any{
				"type":    "write",
				"path":    "b.txt",
				"content": "modified b",
			},
		},
	})

	// Commit should have failed.
	if got.OperationsFailed != 1 {
		t.Errorf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}

	// First file (a.txt) should be rolled back to original.
	aContent, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatalf("read rolled-back a.txt: %v", err)
	}
	if string(aContent) != "original a" {
		t.Errorf("a.txt content = %q, want %q (rolled back)", string(aContent), "original a")
	}

	// Second file (b.txt) should never have been touched.
	bContent, err := os.ReadFile(bPath)
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(bContent) != "original b" {
		t.Errorf("b.txt content = %q, want %q (never touched)", string(bContent), "original b")
	}
}

func TestCommitNoLeftoverTempFiles(t *testing.T) {
	root := t.TempDir()

	// Test 1: Successful commit
	toolDef := newMutateTestTool(t, root)
	_ = runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type":    "create",
				"path":    "file1.txt",
				"content": "content1",
			},
			map[string]any{
				"type":    "create",
				"path":    "file2.txt",
				"content": "content2",
			},
		},
	})

	tempFiles := globTempFiles(t, root)
	if len(tempFiles) > 0 {
		t.Errorf("after successful commit, found temp files: %v", tempFiles)
	}

	// Test 2: Failed commit with read-only dir
	bDir := filepath.Join(root, "readonly_dir")
	if err := os.Mkdir(bDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(bDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(bDir, 0o755); err != nil {
			t.Errorf("cleanup chmod %q: %v", bDir, err)
		}
	})

	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}

	toolDef = newMutateTestTool(t, root)
	_ = runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type":    "create",
				"path":    "readonly_dir/file.txt",
				"content": "content",
			},
		},
	})

	tempFiles = globTempFiles(t, bDir)
	if len(tempFiles) > 0 {
		t.Errorf("after failed commit, found temp files in dir: %v", tempFiles)
	}
}

func TestCommitRollbackFailureReportsAffectedPaths(t *testing.T) {
	root := t.TempDir()

	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}

	// Create file a.txt with mode 0o444 (read-only). Commit will succeed (temp+rename bypasses
	// the read-only file). But rollback will fail when trying to restore it with os.WriteFile.
	aPath := filepath.Join(root, "a.txt")
	if err := os.WriteFile(aPath, []byte("original a"), 0o444); err != nil {
		t.Fatalf("setup aPath: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(aPath, 0o755); err != nil {
			t.Errorf("cleanup chmod %q: %v", aPath, err)
		}
	})

	// Create b.txt in a subdirectory that will become read-only.
	bDir := filepath.Join(root, "bdir")
	if err := os.Mkdir(bDir, 0o755); err != nil {
		t.Fatalf("mkdir bDir: %v", err)
	}
	bPath := filepath.Join(bDir, "b.txt")
	if err := os.WriteFile(bPath, []byte("original b"), 0o644); err != nil {
		t.Fatalf("setup bPath: %v", err)
	}

	// Make bDir read-only to fail the write of b.txt (CreateTemp will fail).
	if err := os.Chmod(bDir, 0o555); err != nil {
		t.Fatalf("chmod bDir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(bDir, 0o755); err != nil {
			t.Errorf("cleanup chmod %q: %v", bDir, err)
		}
	})

	toolDef := newMutateTestTool(t, root)
	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type":    "write",
				"path":    "a.txt",
				"content": "modified a",
			},
			map[string]any{
				"type":    "write",
				"path":    "bdir/b.txt",
				"content": "modified b",
			},
		},
	})

	// Commit should have failed.
	if got.OperationsFailed != 1 {
		t.Errorf("OperationsFailed = %d, want 1", got.OperationsFailed)
	}

	// On rollback failure, result should report affected paths.
	if len(got.Paths) == 0 {
		t.Errorf("Paths = %v, want non-empty list of affected paths", got.Paths)
	}

	// Output should mention rollback failure or inconsistent state.
	if !strings.Contains(got.Output, "rollback failure") && !strings.Contains(got.Output, "inconsistent") {
		t.Errorf("Output = %q, want mention of rollback failure or inconsistent state", got.Output)
	}
}

func TestCommitCreatesParentDirInSandbox(t *testing.T) {
	root := t.TempDir()
	sandboxTmpDir := filepath.Join(root, "sandbox_tmp")
	if err := os.Mkdir(sandboxTmpDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox: %v", err)
	}

	// Create a policy with sandbox tmp dir.
	policy := tool.NewPathPolicyWithSandbox(root, config.PathsConfig{}, sandboxTmpDir)
	toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy})

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{
				"type":    "create",
				"path":    "/tmp/nested/deep/file.txt",
				"content": "test content",
			},
		},
	})

	// Commit should succeed.
	if got.OperationsFailed != 0 {
		t.Errorf("OperationsFailed = %d, want 0; output: %s", got.OperationsFailed, got.Output)
	}

	// The parent directory should have been created in sandbox.
	nestedPath := filepath.Join(sandboxTmpDir, "nested", "deep")
	info, err := os.Stat(nestedPath)
	if err != nil {
		t.Errorf("stat nested path: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("nested path is not a directory")
	}

	// The file should exist.
	filePath := filepath.Join(nestedPath, "file.txt")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "test content" {
		t.Errorf("file content = %q, want %q", string(content), "test content")
	}
}

// TestWriteFileAtomicCleansUpOnRenameFailure exercises the failure path *after*
// CreateTemp has already succeeded (write, chmod, sync, and the explicit close
// all complete normally; only the final Rename fails because the destination
// is an existing directory). This guards against writeFileAtomic's error
// variable being shadowed by inner `:=` declarations, which would leave the
// deferred cleanup unable to observe the failure and orphan the temp file.
func TestWriteFileAtomicCleansUpOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "conflict")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	err := writeFileAtomic(target, []byte("content"), 0o644)
	if err == nil {
		t.Fatal("writeFileAtomic err = nil, want rename failure (target is a directory)")
	}

	leftover := globTempFiles(t, dir)
	if len(leftover) > 0 {
		t.Errorf("temp files left behind after rename failure: %v", leftover)
	}
}

func globTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".mutate-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	return matches
}
