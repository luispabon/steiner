package tool

import "testing"

func TestPathExcluder_Builtins(t *testing.T) {
	ex := NewPathExcluder(nil, nil)

	tests := []struct {
		path string
		want bool
	}{
		{path: "/project/.git/config", want: true},
		{path: "/project/node_modules/foo/index.js", want: true},
		{path: "/project/.steiner/config.yaml", want: true},
		{path: "/project/.claude/config.yaml", want: true},
		{path: "/project/vendor/foo/bar.go", want: true},
		{path: "/project/.cache/huggingface", want: true},
		{path: "/project/dist/bundle.js", want: true},
		{path: "/project/build/output.o", want: true},
		{path: "/project/out/result", want: true},
		{path: "/project/target/debug/binary", want: true},
		{path: "/project/.next/static", want: true},
		{path: "/project/.nuxt/dist", want: true},
		{path: "/project/src/main.go", want: false},
		{path: "/project/README.md", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ex.ShouldExclude(tc.path)
			if got != tc.want {
				t.Fatalf("ShouldExclude(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestPathExcluder_ExactPaths(t *testing.T) {
	ex := NewPathExcluder([]string{"/project/secret", "/project/private"}, nil)

	tests := []struct {
		path string
		want bool
	}{
		{path: "/project/secret/key.txt", want: true},
		{path: "/project/secret", want: true},
		{path: "/project/private/data", want: true},
		{path: "/project/src/main.go", want: false},
		{path: "/project/secretary/notes.txt", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ex.ShouldExclude(tc.path)
			if got != tc.want {
				t.Fatalf("ShouldExclude(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestPathExcluder_GlobPatterns(t *testing.T) {
	ex := NewPathExcluder(nil, []string{"*.bak", "dump*"})

	tests := []struct {
		path string
		want bool
	}{
		{path: "/project/backups/data.bak", want: true},
		{path: "/project/error.bak", want: true},
		{path: "/project/dump/core", want: true},
		{path: "/project/deep/dump/file.txt", want: true},
		{path: "/project/src/main.go", want: false},
		{path: "/project/docs/readme.txt", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ex.ShouldExclude(tc.path)
			if got != tc.want {
				t.Fatalf("ShouldExclude(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestPathExcluder_ForceInclude(t *testing.T) {
	ex := NewPathExcluderWithIncludes(nil, nil, FilePickerAlwaysInclude)

	tests := []struct {
		path string
		want bool
	}{
		{path: "/project/.steiner/config.yaml", want: false},
		{path: "/project/.steiner/plans/overview.md", want: false},
		{path: "/project/src/.steiner/notes.md", want: false},
		{path: "/project/node_modules/foo/index.js", want: true},
		{path: "/project/.git/config", want: true},
		{path: "/project/src/main.go", want: false},
		{path: "/project/README.md", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ex.ShouldExclude(tc.path)
			if got != tc.want {
				t.Fatalf("ShouldExclude(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestPathExcluder_BuiltinsPlusUser(t *testing.T) {
	ex := NewPathExcluder([]string{"/project/secret"}, []string{"*.tmp"})

	if !ex.ShouldExclude("/project/.git/config") {
		t.Fatal("built-in .git exclusion should be active")
	}
	if !ex.ShouldExclude("/project/secret/key.txt") {
		t.Fatal("custom exclude path should be active")
	}
	if !ex.ShouldExclude("/project/cache/file.tmp") {
		t.Fatal("custom exclude pattern should be active")
	}
	if ex.ShouldExclude("/project/src/main.go") {
		t.Fatal("non-excluded path should not be excluded")
	}
}
