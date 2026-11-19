package licensescan

import (
	"context"
	"os"
	"path/filepath"
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
