package licensescan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestScanRejectsModuleWithoutLicense(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# no license here\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := scanModule("example.com/nolicense", "v1.0.0", dir, nil)
	if err == nil {
		t.Fatal("scanModule without license files returned no error")
	}
	if !strings.Contains(err.Error(), "example.com/nolicense") {
		t.Errorf("error %q does not name the module", err)
	}
}

func TestScanCurrentRepo(t *testing.T) {
	platforms := []Platform{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}
	report, err := Scan(context.Background(), "../..", platforms)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(report.Modules) <= 20 {
		t.Errorf("found %d modules, want more than 20", len(report.Modules))
	}
	if len(report.GoFiles) == 0 {
		t.Error("report has no Go toolchain license files")
	}
	for _, module := range report.Modules {
		if len(module.Files) == 0 {
			t.Errorf("module %s has no license files", module.Path)
		}
		if len(module.Licenses) == 0 {
			t.Errorf("module %s has no detected license", module.Path)
		}
	}
}

func TestAddDeps(t *testing.T) {
	const mainPath = "example.com/main"
	module := func(path, version, dir string) *listedModule {
		return &listedModule{Path: path, Version: version, Dir: dir}
	}

	tests := []struct {
		name    string
		deps    []listedPackage
		want    map[string][]string
		wantErr string
	}{
		{
			name: "skips standard library and main module packages",
			deps: []listedPackage{
				{Dir: "/goroot/src/fmt", Standard: true},
				{Dir: "/repo/internal/agent", Module: module(mainPath, "", "/repo")},
			},
			want: map[string][]string{},
		},
		{
			name: "unions package directories across platforms",
			deps: []listedPackage{
				{Dir: "/cache/m/p1", Module: module("example.com/m", "v1.0.0", "/cache/m")},
				{Dir: "/cache/m/p2", Module: module("example.com/m", "v1.0.0", "/cache/m")},
				{Dir: "/cache/n/p1", Module: module("example.com/n", "v2.0.0", "/cache/n")},
			},
			want: map[string][]string{
				"example.com/m": {"/cache/m/p1", "/cache/m/p2"},
				"example.com/n": {"/cache/n/p1"},
			},
		},
		{
			name: "version mismatch",
			deps: []listedPackage{
				{Dir: "/cache/m/p1", Module: module("example.com/m", "v1.0.0", "/cache/m")},
				{Dir: "/cache/m/p2", Module: module("example.com/m", "v1.1.0", "/cache/m")},
			},
			wantErr: "across platforms",
		},
		{
			name: "directory mismatch",
			deps: []listedPackage{
				{Dir: "/cache/m/p1", Module: module("example.com/m", "v1.0.0", "/cache/m")},
				{Dir: "/cache2/m/p1", Module: module("example.com/m", "v1.0.0", "/cache2/m")},
			},
			wantErr: "across platforms",
		},
		{
			name: "missing source directory",
			deps: []listedPackage{
				{Dir: "/repo/x", Module: module("example.com/m", "v1.0.0", "")},
			},
			wantErr: "no source directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accums := map[string]*moduleAccum{}
			err := addDeps(accums, tt.deps, mainPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("addDeps returned no error, want one containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("addDeps: %v", err)
			}
			got := map[string][]string{}
			for path, acc := range accums {
				got[path] = sortedKeys(acc.pkgDirs)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("accumulated modules = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeDeps(t *testing.T) {
	out := []byte(`{"Dir":"/goroot/src/fmt","Standard":true}
{"Dir":"/cache/m/p1","Module":{"Path":"example.com/m","Version":"v1.0.0","Dir":"/cache/m"}}
`)
	deps, err := decodeDeps(out)
	if err != nil {
		t.Fatalf("decodeDeps: %v", err)
	}
	want := []listedPackage{
		{Dir: "/goroot/src/fmt", Standard: true},
		{Dir: "/cache/m/p1", Module: &listedModule{Path: "example.com/m", Version: "v1.0.0", Dir: "/cache/m"}},
	}
	if !reflect.DeepEqual(deps, want) {
		t.Errorf("decodeDeps = %+v, want %+v", deps, want)
	}

	tests := []struct {
		name    string
		out     []byte
		wantErr string
	}{
		{name: "empty input", out: nil, wantErr: "no packages listed"},
		{name: "whitespace only", out: []byte("\n"), wantErr: "no packages listed"},
		{name: "truncated JSON", out: []byte(`{"Dir":`), wantErr: "decode package"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeDeps(tt.out); err == nil {
				t.Fatalf("decodeDeps(%q) returned no error, want one containing %q", tt.out, tt.wantErr)
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseGoEnv(t *testing.T) {
	tests := []struct {
		name        string
		out         string
		wantGOROOT  string
		wantVersion string
		wantErr     bool
	}{
		{name: "two lines", out: "/usr/local/go\ngo1.26.11\n", wantGOROOT: "/usr/local/go", wantVersion: "go1.26.11"},
		{name: "no trailing newline", out: "/usr/local/go\ngo1.26.11", wantGOROOT: "/usr/local/go", wantVersion: "go1.26.11"},
		{name: "one line", out: "/usr/local/go\n", wantErr: true},
		{name: "empty version", out: "/usr/local/go\n\n", wantErr: true},
		{name: "empty output", out: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goroot, version, err := parseGoEnv([]byte(tt.out))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseGoEnv(%q) returned no error", tt.out)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGoEnv(%q): %v", tt.out, err)
			}
			if goroot != tt.wantGOROOT || version != tt.wantVersion {
				t.Errorf("parseGoEnv(%q) = %q, %q, want %q, %q", tt.out, goroot, version, tt.wantGOROOT, tt.wantVersion)
			}
		})
	}
}

func TestGoLicenseFiles(t *testing.T) {
	t.Run("license only", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("line one\r\nline two\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		files, err := goLicenseFiles(dir)
		if err != nil {
			t.Fatalf("goLicenseFiles: %v", err)
		}
		if len(files) != 1 || files[0].Path != "LICENSE" {
			t.Fatalf("files = %+v, want only LICENSE", files)
		}
		if files[0].Text != "line one\nline two\n" {
			t.Errorf("LICENSE text = %q, want CRLF normalised to LF", files[0].Text)
		}
	})

	t.Run("license and patents", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"LICENSE", "PATENTS"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" text\n"), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
		}
		files, err := goLicenseFiles(dir)
		if err != nil {
			t.Fatalf("goLicenseFiles: %v", err)
		}
		if len(files) != 2 || files[0].Path != "LICENSE" || files[1].Path != "PATENTS" {
			t.Fatalf("files = %+v, want LICENSE then PATENTS", files)
		}
	})

	t.Run("missing license", func(t *testing.T) {
		if _, err := goLicenseFiles(t.TempDir()); err == nil {
			t.Error("goLicenseFiles without LICENSE returned no error")
		}
	})
}
