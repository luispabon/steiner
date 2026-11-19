package licensescan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCollectLicenseFiles(t *testing.T) {
	root := t.TempDir()
	contents := map[string]string{
		"LICENSE":               "root license\n",
		"NOTICE":                "line one\r\nline two\r\n",
		"UNICODE-LICENSE":       "unicode data license\n",
		"README.md":             "not a license\n",
		"main.go":               "package example\n",
		"internal/norm/LICENSE": "nested license\n",
		"internal/norm/PATENTS": "nested patents\n",
		"internal/norm/norm.go": "package norm\n",
		"testdata/LICENSE":      "test fixture, never scanned\n",
	}
	for name, content := range contents {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	files, err := collectLicenseFiles(root, []string{filepath.Join(root, "internal", "norm")})
	if err != nil {
		t.Fatalf("collectLicenseFiles: %v", err)
	}

	var got []string
	texts := map[string]string{}
	for _, file := range files {
		got = append(got, file.Path)
		texts[file.Path] = file.Text
	}
	want := []string{
		"LICENSE",
		"NOTICE",
		"UNICODE-LICENSE",
		"internal/norm/LICENSE",
		"internal/norm/PATENTS",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collected paths = %v, want %v", got, want)
	}
	if texts["NOTICE"] != "line one\nline two\n" {
		t.Errorf("NOTICE text = %q, want CRLF normalised to LF", texts["NOTICE"])
	}
}

func TestIsLicenseName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "LICENSE", want: true},
		{name: "LICENSE.txt", want: true},
		{name: "LICENSE.md", want: true},
		{name: "LICENCE.markdown", want: true},
		{name: "COPYING.rst", want: true},
		{name: "LICENSE-MIT", want: true},
		{name: "UNICODE-LICENSE", want: true},
		{name: "NOTICE", want: true},
		{name: "PATENTS", want: true},
		{name: "UNLICENSE", want: true},
		{name: "COPYING", want: true},
		{name: "README.md", want: false},
		{name: "license.go", want: false},
		{name: "LICENSE.json", want: false},
		{name: "licenses", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLicenseName(tt.name); got != tt.want {
				t.Errorf("isLicenseName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
