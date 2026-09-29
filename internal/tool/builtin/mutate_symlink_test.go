package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func assertLink(t *testing.T, link, wantDest string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %q: %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%q is no longer a symlink (mode %v)", link, fi.Mode())
	}
	dest, err := os.Readlink(link)
	if err != nil || dest != wantDest {
		t.Fatalf("Readlink(%q) = %q, %v; want %q", link, dest, err, wantDest)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%q content = %q, want %q", path, got, want)
	}
}

// readOnlyDir creates a read-only directory under root so that a later write
// into it fails at commit time, exercising rollback.
func readOnlyDir(t *testing.T, root, name string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("test requires non-root")
	}
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Errorf("cleanup chmod: %v", err)
		}
	})
}

func TestMutateThroughSymlink(t *testing.T) {
	tests := []struct {
		name string
		op   map[string]any
	}{
		{"write", map[string]any{"type": "write", "path": "link.txt", "content": "new"}},
		{"replace", map[string]any{"type": "replace", "path": "link.txt", "old_string": "old", "new_string": "new"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "real.txt"), "old")
			if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
				t.Fatal(err)
			}
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": []any{tc.op}})
			if got.OperationsFailed != 0 {
				t.Fatalf("OperationsFailed = %d: %+v", got.OperationsFailed, got)
			}
			assertLink(t, filepath.Join(root, "link.txt"), "real.txt")
			assertContent(t, filepath.Join(root, "real.txt"), "new")
			if fi, err := os.Stat(filepath.Join(root, "real.txt")); err != nil || fi.Mode().Perm() != 0o640 {
				t.Errorf("target mode = %v, %v; want 0640", fi, err)
			}
		})
	}
}

func TestMutateSymlinkRollbackOnLaterFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.txt"), "old")
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	readOnlyDir(t, root, "zdir")

	got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": []any{
		map[string]any{"type": "write", "path": "link.txt", "content": "new"},
		map[string]any{"type": "write", "path": "zdir/b.txt", "content": "new b"},
	}})
	if got.OperationsFailed == 0 {
		t.Fatalf("expected commit failure, got %+v", got)
	}
	assertLink(t, filepath.Join(root, "link.txt"), "real.txt")
	assertContent(t, filepath.Join(root, "real.txt"), "old")
}

func TestMutateSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.txt"), "secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.txt", filepath.Join(root, "dangling.txt")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
	}{
		{"outside root", "escape.txt"},
		{"dangling", "dangling.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": []any{
				map[string]any{"type": "write", "path": tc.path, "content": "pwned"},
			}})
			if got.OperationsFailed == 0 {
				t.Fatalf("expected failure, got %+v", got)
			}
			assertContent(t, filepath.Join(outside, "secret.txt"), "secret")
			if _, err := os.Stat(filepath.Join(root, "missing.txt")); !os.IsNotExist(err) {
				t.Errorf("missing.txt was created: %v", err)
			}
			if fi, err := os.Lstat(filepath.Join(root, tc.path)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Errorf("%s should remain a symlink: %v, %v", tc.path, fi, err)
			}
		})
	}
}

func TestMutateDeleteSymlinkRemovesOnlyLink(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.txt"), "keep")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": []any{
		map[string]any{"type": "delete_file", "path": "link.txt"},
	}})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d: %+v", got.OperationsFailed, got)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("link still present: %v", err)
	}
	assertContent(t, filepath.Join(root, "real.txt"), "keep")
}

func TestMutateDeleteSymlinkRollbackRestoresLink(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "real.txt"), "keep")
	if err := os.Symlink("real.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	readOnlyDir(t, root, "zdir")
	got := runMutate(t, newMutateTestTool(t, root), map[string]any{"operations": []any{
		map[string]any{"type": "delete_file", "path": "link.txt"},
		map[string]any{"type": "write", "path": "zdir/new.txt", "content": "x"},
	}})
	if got.OperationsFailed == 0 {
		t.Fatalf("expected commit failure, got %+v", got)
	}
	assertLink(t, filepath.Join(root, "link.txt"), "real.txt")
	assertContent(t, filepath.Join(root, "real.txt"), "keep")
}
