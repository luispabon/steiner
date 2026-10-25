package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// claudeResolvePath returns the absolute form of p with symbolic links resolved. The
// part of p that does not exist yet is kept as written, after its existing ancestor is
// resolved. A dangling symbolic link is an error, because creating its target could
// write anywhere.
func claudeResolvePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("absolute path of %s: %w", p, err)
	}
	var rest []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		if _, lerr := os.Lstat(abs); lerr == nil {
			return "", fmt.Errorf("resolve %s: dangling symbolic link", p)
		}
		parent := filepath.Dir(abs)
		if !errors.Is(err, os.ErrNotExist) || parent == abs {
			return "", fmt.Errorf("resolve %s: %w", p, err)
		}
		rest = append([]string{filepath.Base(abs)}, rest...)
		abs = parent
	}
}

// claudeDirWithin reports whether dir is root or lies inside root. Both paths are
// resolved first, so relative spellings and symbolic links cannot hide an overlap.
func claudeDirWithin(dir, root string) (bool, error) {
	d, err := claudeResolvePath(dir)
	if err != nil {
		return false, err
	}
	r, err := claudeResolvePath(root)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(r, d)
	if err != nil {
		return false, fmt.Errorf("relate %s to %s: %w", d, r, err)
	}
	return filepath.IsLocal(rel), nil
}

// TestClaudeTokenCaptureGenerateDirWithin checks the generation guard on temporary
// directories. The fixture itself, nested paths (existing or not), and symbolic links
// that resolve inside it must be reported as inside. A sibling sharing a name prefix,
// the parent, and a separate directory must not. A dangling link is an error.
func TestClaudeTokenCaptureGenerateDirWithin(t *testing.T) {
	base := t.TempDir()
	fixture := filepath.Join(base, "fixture")
	if err := os.MkdirAll(filepath.Join(fixture, "captures"), 0o755); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	links := []struct{ name, target string }{
		{"link-to-fixture", fixture},
		{"link-into-fixture", filepath.Join(fixture, "captures")},
		{"dangling-link", filepath.Join(fixture, "missing")},
	}
	for _, l := range links {
		if err := os.Symlink(l.target, filepath.Join(base, l.name)); err != nil {
			t.Fatalf("symlink %s: %v", l.name, err)
		}
	}
	tests := []struct {
		name    string
		dir     string
		want    bool
		wantErr bool
	}{
		{"fixture itself", fixture, true, false},
		{"captures directory", filepath.Join(fixture, "captures"), true, false},
		{"missing directory inside", filepath.Join(fixture, "new", "deep"), true, false},
		{"spelling through a parent", filepath.Join(fixture, "captures", ".."), true, false},
		{"symlink to fixture", filepath.Join(base, "link-to-fixture"), true, false},
		{"symlink into fixture", filepath.Join(base, "link-into-fixture"), true, false},
		{"dangling symlink", filepath.Join(base, "dangling-link"), false, true},
		{"sibling sharing a name prefix", fixture + "-copy", false, false},
		{"parent of fixture", base, false, false},
		{"separate directory", filepath.Join(base, "generated"), false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := claudeDirWithin(tc.dir, fixture)
			if tc.wantErr {
				if err == nil {
					t.Error("want an error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("claudeDirWithin: %v", err)
			}
			if got != tc.want {
				t.Errorf("inside = %t, want %t", got, tc.want)
			}
		})
	}
}
