// Package prefs persists TUI preferences.
package prefs

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Prefs stores persisted TUI preferences.
type Prefs struct {
	Accent          string `yaml:"accent"`
	ShowThinking    bool   `yaml:"show_thinking"`
	SidebarPosition string `yaml:"sidebar_position"`
}

// DefaultPrefs returns the default TUI preferences.
func DefaultPrefs() Prefs {
	return Prefs{
		Accent:          "amber",
		ShowThinking:    true,
		SidebarPosition: "left",
	}
}

var userHomeDir = os.UserHomeDir

func configDir() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "steiner"), nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prefs.yaml"), nil
}

// Load reads prefs from ~/.config/steiner/prefs.yaml.
// Returns DefaultPrefs() if the file is absent or unreadable.
func Load() (Prefs, error) {
	p := DefaultPrefs()
	path, err := configPath()
	if err != nil {
		return p, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return DefaultPrefs(), err
	}
	return p, nil
}

// Save writes prefs to ~/.config/steiner/prefs.yaml atomically.
// Creates the config dir if absent. Uses a temp file + rename to
// prevent concurrent Load calls from reading a partial or empty file.
func Save(p Prefs) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "prefs.yaml")
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	// Write to a temp file in the same directory, then rename atomically.
	tmp, err := os.CreateTemp(dir, "prefs.yaml.*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		closeAndRemoveTempFile(tmp)
		return err
	}
	if err := tmp.Sync(); err != nil {
		closeAndRemoveTempFile(tmp)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	// Sync the directory so the rename is durable on ext4/XFS.
	dirf, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() {
		_ = dirf.Close()
	}()
	return dirf.Sync()
}

func closeAndRemoveTempFile(tmp *os.File) {
	if tmp == nil {
		return
	}
	name := tmp.Name()
	// Best-effort cleanup on an already-failing save; the caller returns the
	// original error.
	_ = tmp.Close()
	_ = os.Remove(name)
}
