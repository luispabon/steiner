package config

import (
	"os"
	"testing"
)

func TestDefaultConfigHumanizerModeFalse(t *testing.T) {
	cfg := defaultConfig()
	if cfg.HumanizerMode {
		t.Fatal("defaultConfig().HumanizerMode = true, want false")
	}
}

func TestLoadHumanizerModeDefaultsToFalse(t *testing.T) {
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
	if cfg.HumanizerMode {
		t.Fatal("Load().HumanizerMode = true, want false (default)")
	}
}

func TestLoadHumanizerModeEnvOverride(t *testing.T) {
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
			"STEINER_HUMANIZER_MODE": "false",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HumanizerMode {
		t.Fatal("Load() with STEINER_HUMANIZER_MODE=false = true, want false")
	}
}

func TestLoadHumanizerModeCLIOverrideFalse(t *testing.T) {
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
			HumanizerMode: &v,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HumanizerMode {
		t.Fatal("Load() with CLIOverrides{HumanizerMode: ptr(false)} = true, want false")
	}
}

func TestLoadHumanizerModeCLIOverrideNil(t *testing.T) {
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
			HumanizerMode: nil,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HumanizerMode {
		t.Fatal("Load() with CLIOverrides{HumanizerMode: nil} = true, want false (default)")
	}
}

func TestLoadHumanizerModeEnvOverrideTrue(t *testing.T) {
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
			"STEINER_HUMANIZER_MODE": "true",
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.HumanizerMode {
		t.Fatal("Load() with STEINER_HUMANIZER_MODE=true = false, want true")
	}
}

func TestLoadHumanizerModeCLIOverrideTrue(t *testing.T) {
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

	v := true
	cfg, err := Load(LoadOptions{
		CLI: CLIOverrides{
			HumanizerMode: &v,
		},
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.HumanizerMode {
		t.Fatal("Load() with CLIOverrides{HumanizerMode: ptr(true)} = false, want true")
	}
}

func TestApplyHumanizerModePatch(t *testing.T) {
	cfg := defaultConfig()
	if cfg.HumanizerMode {
		t.Fatal("defaultConfig().HumanizerMode should be false before patch")
	}

	v := true
	patch := configPatch{HumanizerMode: &v}
	applyPatch(&cfg, patch)

	if !cfg.HumanizerMode {
		t.Fatal("applyPatch() with HumanizerMode: ptr(true) = false, want true")
	}
}
