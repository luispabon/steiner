package provider

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
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

// claudeNoSymlinkBelow returns an error if out, or any existing path component from out
// down to dst, is a symbolic link. Components that do not exist end the check.
func claudeNoSymlinkBelow(out, dst string) error {
	rel, err := filepath.Rel(out, dst)
	if err != nil {
		return fmt.Errorf("relate %s to %s: %w", dst, out, err)
	}
	cur := out
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", cur, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link", cur)
		}
	}
	return nil
}

// claudeGenerateCheckDests returns an error unless out and every destination in files
// are free of symbolic links and lie outside fixture. It only inspects the filesystem,
// so generation calls it before creating any directory or file.
func claudeGenerateCheckDests(out, fixture string, files []claudeCaptureFile) error {
	inside, err := claudeDirWithin(out, fixture)
	if err != nil {
		return err
	}
	if inside {
		return fmt.Errorf("output directory %s is inside the fixture directory", out)
	}
	for _, f := range files {
		dst := filepath.Join(out, filepath.FromSlash(f.Path))
		if err := claudeNoSymlinkBelow(out, dst); err != nil {
			return err
		}
		inside, err := claudeDirWithin(dst, fixture)
		if err != nil {
			return err
		}
		if inside {
			return fmt.Errorf("destination %s is inside the fixture directory", dst)
		}
	}
	return nil
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

// claudeTreeSnapshot records every path under root with its kind and content. Symbolic
// links are recorded by target and not followed.
func claudeTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			snap[p] = "link " + target
		case info.IsDir():
			snap[p] = "dir"
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			snap[p] = "file " + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snap
}

// TestClaudeTokenGenerateDestinations checks the destination guard on temporary
// directories. Each refusal must fail. No case may change the output or fixture trees,
// because the guard only inspects the filesystem.
func TestClaudeTokenGenerateDestinations(t *testing.T) {
	base := t.TempDir()
	fixture := filepath.Join(base, "fixture")
	if err := os.MkdirAll(filepath.Join(fixture, "captures"), 0o755); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	for _, name := range []string{filepath.Join("captures", "attempt-01.jsonl"), claudeProvenanceName} {
		if err := os.WriteFile(filepath.Join(fixture, name), []byte("fixture\n"), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	files := []claudeCaptureFile{{Path: "captures/attempt-01.jsonl"}, {Path: claudeProvenanceName}}
	tests := []struct {
		name    string
		out     string
		links   map[string]string
		wantErr bool
	}{
		{"empty output", filepath.Join(base, "clean"), nil, false},
		{"captures symlink into fixture", filepath.Join(base, "captures-link"), map[string]string{"captures": filepath.Join(fixture, "captures")}, true},
		{"provenance symlink into fixture", filepath.Join(base, "prov-link"), map[string]string{claudeProvenanceName: filepath.Join(fixture, claudeProvenanceName)}, true},
		{"provenance symlink outside fixture", filepath.Join(base, "outside-link"), map[string]string{claudeProvenanceName: filepath.Join(base, "elsewhere.json")}, true},
		{"output is the fixture", fixture, nil, true},
		{"output inside the fixture", filepath.Join(fixture, "captures", "nested"), nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(tc.out, 0o755); err != nil {
				t.Fatalf("create output: %v", err)
			}
			for name, target := range tc.links {
				if err := os.Symlink(target, filepath.Join(tc.out, name)); err != nil {
					t.Fatalf("symlink %s: %v", name, err)
				}
			}
			before := claudeTreeSnapshot(t, base)
			err := claudeGenerateCheckDests(tc.out, fixture, files)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, want error %t", err, tc.wantErr)
			}
			if after := claudeTreeSnapshot(t, base); !maps.Equal(before, after) {
				t.Error("guard changed the output or fixture tree")
			}
		})
	}
}
