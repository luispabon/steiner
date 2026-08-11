package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		skill      string
		wantOutput string
		wantErr    bool
	}{
		{
			name: "single directive expands with exact splicing",
			files: map[string]string{
				"foo/SKILL.md.src":     "before\n<!-- include: partials/greeting.md -->\nafter\n",
				"partials/greeting.md": "Hello, world!\n",
			},
			skill:      "foo",
			wantOutput: "before\nHello, world!\nafter\n",
		},
		{
			name: "multiple directives in one file",
			files: map[string]string{
				"foo/SKILL.md.src": "a\n<!-- include: partials/one.md -->\nb\n<!-- include: partials/two.md -->\nc\n",
				"partials/one.md":  "ONE\n",
				"partials/two.md":  "TWO\n",
			},
			skill:      "foo",
			wantOutput: "a\nONE\nb\nTWO\nc\n",
		},
		{
			name: "no directives copied byte-identically",
			files: map[string]string{
				"foo/SKILL.md.src": "just plain text\nwith no directives\n",
			},
			skill:      "foo",
			wantOutput: "just plain text\nwith no directives\n",
		},
		{
			name: "partial without trailing newline",
			files: map[string]string{
				"foo/SKILL.md.src":     "before\n<!-- include: partials/greeting.md -->\nafter\n",
				"partials/greeting.md": "Hello, world!",
			},
			skill:      "foo",
			wantOutput: "before\nHello, world!\nafter\n",
		},
		{
			name: "unknown partial errors",
			files: map[string]string{
				"foo/SKILL.md.src": "<!-- include: partials/missing.md -->\n",
			},
			skill:   "foo",
			wantErr: true,
		},
		{
			name: "path failing the regex errors",
			files: map[string]string{
				"foo/SKILL.md.src":     "<!-- include: partials/Bad_Name.md -->\n",
				"partials/Bad_Name.md": "x\n",
			},
			skill:   "foo",
			wantErr: true,
		},
		{
			name: "path with dotdot errors",
			files: map[string]string{
				"foo/SKILL.md.src": "<!-- include: partials/../secret.md -->\n",
			},
			skill:   "foo",
			wantErr: true,
		},
		{
			name: "directive nested inside a partial errors",
			files: map[string]string{
				"foo/SKILL.md.src":  "<!-- include: partials/outer.md -->\n",
				"partials/outer.md": "<!-- include: partials/inner.md -->\n",
				"partials/inner.md": "inner\n",
			},
			skill:   "foo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range tt.files {
				writeFile(t, filepath.Join(root, path), content)
			}

			err := run(root)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("run() error = nil, want non-nil")
				}
				if _, statErr := os.Stat(filepath.Join(root, tt.skill, "SKILL.md")); statErr == nil {
					t.Fatalf("SKILL.md was written despite error")
				}
				return
			}
			if err != nil {
				t.Fatalf("run() unexpected error: %v", err)
			}

			got, err := os.ReadFile(filepath.Join(root, tt.skill, "SKILL.md"))
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			if string(got) != tt.wantOutput {
				t.Fatalf("output = %q, want %q", string(got), tt.wantOutput)
			}
		})
	}
}

func TestRunIdempotent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "foo/SKILL.md.src"), "before\n<!-- include: partials/greeting.md -->\nafter\n")
	writeFile(t, filepath.Join(root, "partials/greeting.md"), "Hello, world!\n")

	if err := run(root); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, "foo/SKILL.md"))
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}

	if err := run(root); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(root, "foo/SKILL.md"))
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}

	if string(first) != string(second) {
		t.Fatalf("run() not idempotent: first = %q, second = %q", first, second)
	}
}

func TestRunNoSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "foo/SKILL.md"), "already built\n")

	if err := run(root); err != nil {
		t.Fatalf("run() unexpected error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "foo/SKILL.md"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != "already built\n" {
		t.Fatalf("SKILL.md was modified when no .src exists: %q", got)
	}
}

func TestParseDirectiveRejectsMalformed(t *testing.T) {
	path, ok := parseDirective("<!-- include: partials/foo.md -->")
	if !ok {
		t.Fatalf("expected directive to be recognized")
	}
	if path != "partials/foo.md" {
		t.Fatalf("path = %q, want partials/foo.md", path)
	}

	if _, ok := parseDirective("plain text"); ok {
		t.Fatalf("expected plain text not to be recognized as a directive")
	}
}

func TestReadPartialRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	partialsDir := filepath.Join(root, "partials")
	if err := os.MkdirAll(partialsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := readPartial(partialsDir, "partials/../../etc/passwd.md", "src"); err == nil {
		t.Fatalf("expected error for traversal path")
	}
}
