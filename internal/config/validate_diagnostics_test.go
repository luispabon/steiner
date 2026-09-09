package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInsideProjectRootResolvesDiagnosticsSymlink(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "state")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatalf("mkdir inside: %v", err)
	}
	link := filepath.Join(t.TempDir(), "diagnostics")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !insideProjectRoot(link, root) {
		t.Fatalf("insideProjectRoot(%q, %q) = false, want true for symlink target inside root", link, root)
	}
	if insideProjectRoot(filepath.Join(t.TempDir(), "external"), root) {
		t.Fatal("insideProjectRoot() = true for external path")
	}
}

func TestValidateDiagnosticsConfig(t *testing.T) {
	root := "/home/dev/project"
	tests := []struct {
		name    string
		cfg     DiagnosticsConfig
		root    string
		wantErr string
	}{
		{
			name: "disabled skips every check",
			cfg:  DiagnosticsConfig{Dir: filepath.Join(root, "diag")},
			root: root,
		},
		{
			name: "valid enabled config",
			cfg:  DiagnosticsConfig{Enabled: true, Dir: "/home/dev/.local/state/steiner/diagnostics", RetentionDays: 30},
			root: root,
		},
		{
			name:    "retention must be positive",
			cfg:     DiagnosticsConfig{Enabled: true, Dir: "/home/dev/state", RetentionDays: 0},
			root:    root,
			wantErr: "diagnostics.retention_days must be greater than zero",
		},
		{
			name:    "dir is required",
			cfg:     DiagnosticsConfig{Enabled: true, RetentionDays: 30},
			root:    root,
			wantErr: "diagnostics.dir is required",
		},
		{
			name:    "dir inside the project root is rejected",
			cfg:     DiagnosticsConfig{Enabled: true, Dir: filepath.Join(root, ".steiner", "diagnostics"), RetentionDays: 30},
			root:    root,
			wantErr: "must not resolve inside the project root",
		},
		{
			name:    "dir equal to the project root is rejected",
			cfg:     DiagnosticsConfig{Enabled: true, Dir: root, RetentionDays: 30},
			root:    root,
			wantErr: "must not resolve inside the project root",
		},
		{
			name: "sibling of the project root is allowed",
			cfg:  DiagnosticsConfig{Enabled: true, Dir: "/home/dev/project-diagnostics", RetentionDays: 30},
			root: root,
		},
		{
			name: "empty root skips the containment check",
			cfg:  DiagnosticsConfig{Enabled: true, Dir: filepath.Join(root, "diag"), RetentionDays: 30},
			root: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var problems []string
			validateDiagnosticsConfig(&problems, tt.cfg, tt.root)
			joined := strings.Join(problems, "; ")
			if tt.wantErr == "" {
				if len(problems) != 0 {
					t.Fatalf("validateDiagnosticsConfig() problems = %q, want none", joined)
				}
				return
			}
			if !strings.Contains(joined, tt.wantErr) {
				t.Fatalf("validateDiagnosticsConfig() problems = %q, want substring %q", joined, tt.wantErr)
			}
		})
	}
}
