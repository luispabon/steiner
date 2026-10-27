//go:build unix

package builtin

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
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
	fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
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
	fi, err := os.Stat(steinerDir)
	if err != nil {
		t.Fatal(err)
	}
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
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Errorf("outside dir modified: %v", entries)
			}
			if entries, err := os.ReadDir(filepath.Join(dir, "real")); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
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

func TestBashArtifactTightensInsecureLeaf(t *testing.T) {
	dir := t.TempDir()
	tmpDir := filepath.Join(dir, ".steiner", "tmp")
	leaf := filepath.Join(tmpDir, "bash-output")
	mustMkdir(t, leaf)
	for _, p := range []string{filepath.Join(dir, ".steiner"), tmpDir, leaf} {
		if err := os.Chmod(p, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	rel, err := saveBashArtifact(dir, []byte("x"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	wantModes := map[string]os.FileMode{tmpDir: 0o777, leaf: 0o700, filepath.Join(dir, filepath.FromSlash(rel)): 0o600}
	for p, want := range wantModes {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", p, fi.Mode().Perm(), want)
		}
	}
}

// swapLeaf replaces .steiner/tmp/bash-output with a symlink to a decoy
// directory inside workDir, simulating a race with the shell.
func swapLeaf(t *testing.T, dir string) {
	t.Helper()
	leaf := filepath.Join(dir, ".steiner", "tmp", "bash-output")
	if err := os.Rename(leaf, leaf+".orig"); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, "../../decoy", leaf)
}

func TestBashArtifactSwapDuringSaveNeverWritesSubstitute(t *testing.T) {
	for _, stage := range []string{"before-create", "before-verify"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			mustMkdir(t, filepath.Join(dir, "decoy"))
			store := &bashArtifactStore{workDir: dir, hook: func(s, _ string) {
				if s == stage {
					swapLeaf(t, dir)
				}
			}}

			rel, err := store.save([]byte("secret"))
			if err == nil {
				t.Fatalf("expected error after swap, got success %q", rel)
			}
			if entries, rerr := os.ReadDir(filepath.Join(dir, "decoy")); rerr != nil || len(entries) != 0 {
				t.Fatalf("decoy modified: %v, %v", entries, rerr)
			}
			if entries, rerr := os.ReadDir(filepath.Join(dir, ".steiner", "tmp", "bash-output.orig")); rerr != nil || len(entries) != 0 {
				t.Fatalf("discarded artifact left behind: %v, %v", entries, rerr)
			}
		})
	}
}

func TestBashArtifactParentSwapDetected(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "decoy", "bash-output"))
	store := &bashArtifactStore{workDir: dir, hook: func(s, _ string) {
		if s != "before-verify" {
			return
		}
		tmp := filepath.Join(dir, ".steiner", "tmp")
		if err := os.Rename(tmp, tmp+".orig"); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, "../decoy", tmp)
	}}
	if rel, err := store.save([]byte("x")); err == nil {
		t.Fatalf("expected error, got %q", rel)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "decoy", "bash-output")); err != nil || len(entries) != 0 {
		t.Fatalf("decoy modified: %v, %v", entries, err)
	}
}

func TestBashArtifactFileSwapDetected(t *testing.T) {
	tests := []struct {
		name string
		swap func(t *testing.T, file, decoy string)
		// wantKept is true when the substituted entry must survive untouched.
		wantKept bool
	}{
		{"symlink to decoy", func(t *testing.T, file, decoy string) {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, decoy, file)
		}, true},
		{"unlinked", func(t *testing.T, file, _ string) {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"replaced by other regular file", func(t *testing.T, file, _ string) {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("other"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			decoy := filepath.Join(dir, "decoy.txt")
			if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
				t.Fatal(err)
			}
			var swapped string
			store := &bashArtifactStore{workDir: dir, hook: func(s, name string) {
				if s == "before-verify" {
					swapped = filepath.Join(dir, ".steiner", "tmp", "bash-output", name)
					tt.swap(t, swapped, decoy)
				}
			}}
			if rel, err := store.save([]byte("secret")); err == nil {
				t.Fatalf("expected error, got success %q", rel)
			}
			if got, err := os.ReadFile(decoy); err != nil || string(got) != "decoy" {
				t.Fatalf("decoy = %q, %v", got, err)
			}
			_, err := os.Lstat(swapped)
			if tt.wantKept && err != nil {
				t.Errorf("substituted entry was deleted: %v", err)
			}
			if !tt.wantKept && !os.IsNotExist(err) {
				t.Errorf("entry should be gone: %v", err)
			}
		})
	}
}
