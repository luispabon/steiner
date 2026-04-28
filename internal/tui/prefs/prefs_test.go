package prefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPrefs(t *testing.T) {
	p := DefaultPrefs()
	if p.Accent != "amber" {
		t.Errorf("DefaultPrefs().Accent = %q, want %q", p.Accent, "amber")
	}
	if !p.ShowThinking {
		t.Errorf("DefaultPrefs().ShowThinking = %v, want %v", p.ShowThinking, true)
	}
}

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p, err := Load()
	if err != nil {
		t.Fatalf("Load() = _, %v; want no error for missing file", err)
	}
	if p != DefaultPrefs() {
		t.Fatalf("Load() = %#v, want %#v", p, DefaultPrefs())
	}
}

func TestLoadReturnsDefaultsOnBadYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	cfgDir := filepath.Join(dir, ".config", "steiner")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "prefs.yaml"), []byte("invalid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for malformed YAML")
	}
	if p != DefaultPrefs() {
		t.Fatalf("Load() = %#v, want %#v", p, DefaultPrefs())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	p := Prefs{Accent: "mint", ShowThinking: false}
	if err := Save(p); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() = _, %v", err)
	}
	if got != p {
		t.Fatalf("round-trip = %#v, want %#v", got, p)
	}
}

func TestSaveCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	p := Prefs{Accent: "cyan", ShowThinking: true}
	if err := Save(p); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	cfgPath := filepath.Join(dir, ".config", "steiner", "prefs.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatalf("prefs.yaml was not created at %s", cfgPath)
	}
}
