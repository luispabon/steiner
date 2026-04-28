package prefs

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Prefs struct {
	Accent       string `yaml:"accent"`
	ShowThinking bool   `yaml:"show_thinking"`
}

// DefaultPrefs returns the default TUI preferences.
func DefaultPrefs() Prefs {
	return Prefs{
		Accent:       "amber",
		ShowThinking: true,
	}
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
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

// Save writes prefs to ~/.config/steiner/prefs.yaml.
// Creates the config dir if absent.
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
	return os.WriteFile(path, data, 0o600)
}
