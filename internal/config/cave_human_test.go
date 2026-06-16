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
	t.Run("defaults to false", func(t *testing.T) {
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
		if cfg.CaveHuman {
			t.Fatalf("Load().CaveHuman = true, want false")
		}
	})

	t.Run("from config file", func(t *testing.T) {
		tempDir := t.TempDir()
		configDir := tempDir + "/.config/steiner"
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configDir+"/config.yaml", []byte("cave_human: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
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
		if !cfg.CaveHuman {
			t.Fatalf("Load().CaveHuman = false, want true")
		}
	})
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
