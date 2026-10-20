package fsatomic

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
