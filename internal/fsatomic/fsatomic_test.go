package fsatomic

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWriteFileAppliesMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "owner only", mode: 0o600},
		{name: "owner and group read", mode: 0o640},
		{name: "world readable", mode: 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.json")
			if err := WriteFile(path, []byte("payload"), tt.mode); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != tt.mode {
				t.Errorf("mode = %o, want %o", info.Mode().Perm(), tt.mode)
			}
		})
	}
}

func TestWriteFileReplacesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	if err := os.WriteFile(path, []byte("old content that is longer"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", info.Mode().Perm())
	}
}

func TestWriteFileLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	for i := range 3 {
		if err := WriteFile(path, []byte{byte('a' + i)}, 0o600); err != nil {
			t.Fatalf("WriteFile #%d: %v", i, err)
		}
	}
	if got := dirEntryNames(t, dir); !slices.Equal(got, []string{"out.json"}) {
		t.Errorf("dir entries = %v, want [out.json]", got)
	}
}

func TestWriteFileFailureRemovesTempFile(t *testing.T) {
	tests := []struct {
		name        string
		target      func(t *testing.T, dir string) string
		wantErr     string
		wantEntries []string
	}{
		{
			name: "rename onto directory",
			target: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "target")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantErr:     "rename temp file",
			wantEntries: []string{"target"},
		},
		{
			name: "missing parent directory",
			target: func(_ *testing.T, dir string) string {
				return filepath.Join(dir, "missing", "out.json")
			},
			wantErr:     "create temp file",
			wantEntries: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.target(t, dir)

			err := WriteFile(path, []byte("payload"), 0o600)
			if err == nil {
				t.Fatal("WriteFile: err = nil, want failure")
			}
			if !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("err = %q, want prefix %q", err, tt.wantErr)
			}
			if got := dirEntryNames(t, dir); !slices.Equal(got, tt.wantEntries) {
				t.Errorf("dir entries = %v, want %v", got, tt.wantEntries)
			}
		})
	}
}

func TestSweepStaleTemps(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-time.Minute)
	tests := []struct {
		name  string
		files map[string]time.Time
		dirs  map[string]time.Time
		want  []string
	}{
		{
			name:  "old orphan is removed",
			files: map[string]time.Time{".tmp-out.json-123456789": old},
			want:  []string{},
		},
		{
			name:  "fresh orphan is kept",
			files: map[string]time.Time{".tmp-out.json-123456789": fresh},
			want:  []string{".tmp-out.json-123456789"},
		},
		{
			name:  "orphan for other base name is kept",
			files: map[string]time.Time{".tmp-other.json-123456789": old},
			want:  []string{".tmp-other.json-123456789"},
		},
		{
			name:  "orphan for longer base name sharing the prefix is kept",
			files: map[string]time.Time{".tmp-out.json-x-123456789": old},
			want:  []string{".tmp-out.json-x-123456789"},
		},
		{
			name:  "target file itself is kept",
			files: map[string]time.Time{"out.json": old},
			want:  []string{"out.json"},
		},
		{
			name: "old orphan directory is kept",
			dirs: map[string]time.Time{".tmp-out.json-123456789": old},
			want: []string{".tmp-out.json-123456789"},
		},
		{
			name: "only stale regular orphans are removed",
			files: map[string]time.Time{
				".tmp-out.json-111111111": old,
				".tmp-out.json-222222222": fresh,
				"out.json":                old,
			},
			want: []string{".tmp-out.json-222222222", "out.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, mtime := range tt.files {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				setAge(t, path, mtime)
			}
			for name, mtime := range tt.dirs {
				path := filepath.Join(dir, name)
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				setAge(t, path, mtime)
			}

			sweepStaleTemps(filepath.Join(dir, "out.json"), now)
			if got := dirEntryNames(t, dir); !slices.Equal(got, tt.want) {
				t.Errorf("dir entries = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSweepStaleTempsKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".tmp-out.json-123456789")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	// Chtimes follows links, so the link's own mtime cannot be aged. Running the
	// sweep two hours ahead makes the link older than the cutoff, which leaves
	// the regular-file check as the only thing that can keep it.
	sweepStaleTemps(filepath.Join(dir, "out.json"), time.Now().Add(2*time.Hour))
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("symlink removed: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("symlink target removed: %v", err)
	}
}

func TestWriteFileSweepsOncePerPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	first := filepath.Join(dir, ".tmp-out.json-111111111")
	if err := os.WriteFile(first, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAge(t, first, time.Now().Add(-2*time.Hour))
	if err := WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatalf("WriteFile #1: %v", err)
	}
	if _, err := os.Lstat(first); !os.IsNotExist(err) {
		t.Errorf("stale orphan survived the first write to path: err = %v", err)
	}

	second := filepath.Join(dir, ".tmp-out.json-222222222")
	if err := os.WriteFile(second, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setAge(t, second, time.Now().Add(-2*time.Hour))
	if err := WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatalf("WriteFile #2: %v", err)
	}
	if _, err := os.Lstat(second); err != nil {
		t.Errorf("stale orphan removed by a later write to the same path: %v", err)
	}
}

// setAge sets the access and modification times of path to mtime.
func setAge(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
