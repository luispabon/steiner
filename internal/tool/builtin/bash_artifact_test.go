package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var bashArtifactNameRE = regexp.MustCompile(`^\.steiner/tmp/bash-output/[0-9a-f]{32}\.log$`)

func TestBashArtifactSavesPrivateFile(t *testing.T) {
	dir := t.TempDir()
	body := []byte("hello\noutput")
	rel, err := saveBashArtifact(dir, body)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !bashArtifactNameRE.MatchString(rel) {
		t.Fatalf("unexpected path %q", rel)
	}
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("read back = %q, %v", got, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, _ := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o, want 600", fi.Mode().Perm())
	}
	for _, d := range []string{".steiner", ".steiner/tmp", ".steiner/tmp/bash-output"} {
		di, err := os.Stat(filepath.Join(dir, filepath.FromSlash(d)))
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, di.Mode().Perm())
		}
	}
}

func TestBashArtifactKeepsExistingDirPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	steinerDir := filepath.Join(dir, ".steiner")
	if err := os.Mkdir(steinerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(steinerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := saveBashArtifact(dir, []byte("x")); err != nil {
		t.Fatalf("save: %v", err)
	}
	fi, _ := os.Stat(steinerDir)
	if fi.Mode().Perm() != 0o755 {
		t.Errorf(".steiner mode = %o, want unchanged 755", fi.Mode().Perm())
	}
}

func TestBashArtifactSizeLimit(t *testing.T) {
	dir := t.TempDir()
	if _, err := saveBashArtifact(dir, make([]byte, bashArtifactMaxBytes)); err != nil {
		t.Fatalf("body at limit rejected: %v", err)
	}
	_, err := saveBashArtifact(dir, make([]byte, bashArtifactMaxBytes+1))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large", err)
	}
}

func TestBashArtifactRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, dir, outside string)
	}{
		{"steiner dir outside", func(t *testing.T, dir, outside string) {
			mustSymlink(t, outside, filepath.Join(dir, ".steiner"))
		}},
		{"steiner dir inside", func(t *testing.T, dir, _ string) {
			mustMkdir(t, filepath.Join(dir, "real"))
			mustSymlink(t, "real", filepath.Join(dir, ".steiner"))
		}},
		{"tmp dir inside", func(t *testing.T, dir, _ string) {
			mustMkdir(t, filepath.Join(dir, ".steiner"))
			mustMkdir(t, filepath.Join(dir, "real"))
			mustSymlink(t, "../real", filepath.Join(dir, ".steiner", "tmp"))
		}},
		{"bash-output outside", func(t *testing.T, dir, outside string) {
			mustMkdir(t, filepath.Join(dir, ".steiner", "tmp"))
			mustSymlink(t, outside, filepath.Join(dir, ".steiner", "tmp", "bash-output"))
		}},
		{"not a directory", func(t *testing.T, dir, _ string) {
			if err := os.WriteFile(filepath.Join(dir, ".steiner"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			tt.setup(t, dir, outside)
			if rel, err := saveBashArtifact(dir, []byte("x")); err == nil {
				t.Fatalf("expected error, got %q", rel)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("outside dir modified: %v", entries)
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "real")); len(entries) != 0 {
				t.Errorf("redirect target modified: %v", entries)
			}
		})
	}
}

func TestBashArtifactConcurrentUnique(t *testing.T) {
	dir := t.TempDir()
	const n = 32
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = saveBashArtifact(dir, []byte{byte(i)})
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("save %d: %v", i, errs[i])
		}
		if seen[paths[i]] {
			t.Fatalf("duplicate path %q", paths[i])
		}
		seen[paths[i]] = true
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(paths[i])))
		if err != nil || len(got) != 1 || got[0] != byte(i) {
			t.Fatalf("path %q content = %v, %v", paths[i], got, err)
		}
	}
}

func TestBashArtifactMissingWorkDir(t *testing.T) {
	if _, err := saveBashArtifact(filepath.Join(t.TempDir(), "missing"), []byte("x")); err == nil {
		t.Fatal("expected error for missing workDir")
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
}
