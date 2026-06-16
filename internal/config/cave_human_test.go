package config

import (
	"os"
	"testing"
)

func TestDefaultConfigCaveHumanFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.CaveHuman {
		t.Fatal("defaultConfig().CaveHuman = true, want false")
	}
}

func TestLoadCaveHuman(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		cli  *bool
		want bool
	}{
		{
			name: "defaults to false",
			want: false,
		},
		{
			name: "env false",
			env: map[string]string{
				"STEINER_CAVE_HUMAN": "false",
			},
			want: false,
		},
		{
			name: "env true",
			env: map[string]string{
				"STEINER_CAVE_HUMAN": "true",
			},
			want: true,
		},
		{
			name: "cli false",
			cli:  boolPtrLocal(false),
			want: false,
		},
		{
			name: "cli nil",
			cli:  nil,
			want: false,
		},
		{
			name: "cli true",
			cli:  boolPtrLocal(true),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			t.Setenv("HOME", tempDir)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(cwd) })
			if err := os.Chdir(tempDir); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load(LoadOptions{
				Env: tt.env,
				CLI: CLIOverrides{
					CaveHuman: tt.cli,
				},
			})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.CaveHuman != tt.want {
				t.Fatalf("Load().CaveHuman = %v, want %v", cfg.CaveHuman, tt.want)
			}
		})
	}
}

func TestApplyCaveHumanPatch(t *testing.T) {
	cfg := defaultConfig()
	if cfg.CaveHuman {
		t.Fatal("defaultConfig().CaveHuman should be false before patch")
	}

	v := true
	patch := configPatch{CaveHuman: &v}
	applyPatch(&cfg, patch)

	if !cfg.CaveHuman {
		t.Fatal("applyPatch() with CaveHuman: ptr(true) = false, want true")
	}
}

func boolPtrLocal(v bool) *bool {
	return &v
}
