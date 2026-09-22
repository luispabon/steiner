package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectTrustGate(t *testing.T) {
	tests := []struct {
		name              string
		setup             func(t *testing.T, dir string) LoadOptions
		trust             ProjectTrust
		wantErr           error
		wantErrOther      bool
		wantSandboxEnable bool
	}{
		{
			name: "no project file untrusted loads normally",
			setup: func(_ *testing.T, dir string) LoadOptions {
				return LoadOptions{WorkingDir: dir, HomeDir: dir, Env: map[string]string{}}
			},
			trust:             ProjectTrustUntrusted,
			wantSandboxEnable: true,
		},
		{
			name: "project file exists untrusted refused",
			setup: func(t *testing.T, dir string) LoadOptions {
				writeProjectConfig(t, dir, "sandbox:\n  enabled: false\n")
				return LoadOptions{WorkingDir: dir, HomeDir: dir, Env: map[string]string{}}
			},
			trust:   ProjectTrustUntrusted,
			wantErr: ErrProjectUntrusted,
		},
		{
			name: "project file exists trusted applies value",
			setup: func(t *testing.T, dir string) LoadOptions {
				writeProjectConfig(t, dir, "sandbox:\n  enabled: false\n")
				return LoadOptions{WorkingDir: dir, HomeDir: dir, Env: map[string]string{}}
			},
			trust:             ProjectTrustTrusted,
			wantSandboxEnable: false,
		},
		{
			name: "explicit config path missing untrusted returns existing error not trust error",
			setup: func(_ *testing.T, dir string) LoadOptions {
				missing := filepath.Join(dir, "missing-config.yaml")
				return LoadOptions{
					WorkingDir: dir,
					HomeDir:    dir,
					Env:        map[string]string{},
					CLI:        CLIOverrides{ConfigPath: missing},
				}
			},
			trust:        ProjectTrustUntrusted,
			wantErrOther: true,
		},
		{
			name: "global file only untrusted loads normally",
			setup: func(t *testing.T, dir string) LoadOptions {
				globalPath := filepath.Join(dir, "global-config.yaml")
				if err := os.WriteFile(globalPath, []byte("sandbox:\n  enabled: false\n"), 0o600); err != nil {
					t.Fatalf("write global config: %v", err)
				}
				return LoadOptions{
					WorkingDir:       dir,
					HomeDir:          dir,
					Env:              map[string]string{},
					GlobalConfigPath: globalPath,
				}
			},
			trust:             ProjectTrustUntrusted,
			wantSandboxEnable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := tt.setup(t, dir)
			opts.ProjectTrust = tt.trust

			cfg, err := Load(opts)

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Load() error = %v, want errors.Is %v", err, tt.wantErr)
				}
			case tt.wantErrOther:
				if err == nil {
					t.Fatalf("Load() error = nil, want a non-trust error")
				}
				if errors.Is(err, ErrProjectUntrusted) {
					t.Fatalf("Load() error = %v, did not want ErrProjectUntrusted", err)
				}
			default:
				if err != nil {
					t.Fatalf("Load() unexpected error: %v", err)
				}
				if got := cfg.Sandbox.Enabled; got != tt.wantSandboxEnable {
					t.Fatalf("Sandbox.Enabled = %v, want %v", got, tt.wantSandboxEnable)
				}
			}
		})
	}
}

func writeProjectConfig(t *testing.T, dir, content string) {
	t.Helper()
	steinerDir := filepath.Join(dir, ".steiner")
	if err := os.MkdirAll(steinerDir, 0o755); err != nil {
		t.Fatalf("mkdir .steiner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(steinerDir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
}

func TestSandboxDisabledBy(t *testing.T) {
	tests := []struct {
		name    string
		global  string
		project string
		unsafe  bool
		want    string
	}{
		{
			name:   "global disables",
			global: "sandbox:\n  enabled: false\n",
			want:   SandboxDisabledByGlobalConfig,
		},
		{
			name:    "project disables",
			project: "sandbox:\n  enabled: false\n",
			want:    SandboxDisabledByProjectConfig,
		},
		{
			name:    "project re-enables after global disable",
			global:  "sandbox:\n  enabled: false\n",
			project: "sandbox:\n  enabled: true\n",
			want:    "",
		},
		{
			name:    "cli unsafe overrides project disable",
			project: "sandbox:\n  enabled: false\n",
			unsafe:  true,
			want:    SandboxDisabledByCLIUnsafe,
		},
		{
			name: "nothing set",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			opts := LoadOptions{
				WorkingDir:   dir,
				HomeDir:      dir,
				Env:          map[string]string{},
				ProjectTrust: ProjectTrustTrusted,
			}
			if tt.global != "" {
				globalPath := filepath.Join(dir, "global-config.yaml")
				if err := os.WriteFile(globalPath, []byte(tt.global), 0o600); err != nil {
					t.Fatalf("write global config: %v", err)
				}
				opts.GlobalConfigPath = globalPath
			}
			if tt.project != "" {
				writeProjectConfig(t, dir, tt.project)
			}
			if tt.unsafe {
				opts.CLI.Unsafe = true
			}

			cfg, err := Load(opts)
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.Sandbox.DisabledBy != tt.want {
				t.Fatalf("Sandbox.DisabledBy = %q, want %q", cfg.Sandbox.DisabledBy, tt.want)
			}
		})
	}
}
