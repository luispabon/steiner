package config

import (
	"os"
	"testing"
)

func TestDefaultConfigCavemanModeTrue(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.CavemanMode {
		t.Fatal("defaultConfig().CavemanMode = false, want true")
	}
}

func TestLoadCavemanModeDefaultsToTrue(t *testing.T) {
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

	cfg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.CavemanMode {
		t.Fatal("Load().CavemanMode = false, want true (default)")
	}
}

func TestLoadCavemanModeEnvOverride(t *testing.T) {
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
		Env: map[string]string{
			"STEINER_CAVEMAN_MODE": "false",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CavemanMode {
		t.Fatal("Load() with STEINER_CAVEMAN_MODE=false = true, want false")
	}
}

func TestLoadCavemanModeCLIOverrideFalse(t *testing.T) {
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

	v := false
	cfg, err := Load(LoadOptions{
		CLI: CLIOverrides{
			CavemanMode: &v,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CavemanMode {
		t.Fatal("Load() with CLIOverrides{CavemanMode: ptr(false)} = true, want false")
	}
}

func TestLoadCavemanModeCLIOverrideNil(t *testing.T) {
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
		CLI: CLIOverrides{
			CavemanMode: nil,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.CavemanMode {
		t.Fatal("Load() with CLIOverrides{CavemanMode: nil} = false, want true (default)")
	}
}
