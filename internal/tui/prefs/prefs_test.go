package prefs

import (
	"errors"
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
	if p.SidebarPosition != "left" {
		t.Errorf("DefaultPrefs().SidebarPosition = %q, want %q", p.SidebarPosition, "left")
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
	p := Prefs{Accent: "mint", ShowThinking: false, SidebarPosition: "right"}
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

	cfgDir := filepath.Join(dir, ".config", "steiner")
	dirInfo, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("config dir perms: got %o, want 0o700", mode)
	}

	filePath := filepath.Join(cfgDir, "prefs.yaml")
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat prefs.yaml: %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("prefs.yaml perms: got %o, want 0o600", mode)
	}

	entries, err := os.ReadDir(cfgDir)
	if err != nil {
		t.Fatalf("ReadDir config dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "prefs.yaml" {
		t.Errorf("config dir contents: got %v, want [prefs.yaml]", entries)
	}
}

func TestSaveCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	p := Prefs{Accent: "cyan", ShowThinking: true}
	if err := Save(p); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	cfgDir := filepath.Join(dir, ".config", "steiner")
	dirInfo, err := os.Stat(cfgDir)
	if err != nil {
		t.Fatalf("config dir was not created at %s: %v", cfgDir, err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("config dir perms: got %o, want 0o700", mode)
	}

	cfgPath := filepath.Join(cfgDir, "prefs.yaml")
	fileInfo, err := os.Stat(cfgPath)
	if os.IsNotExist(err) {
		t.Fatalf("prefs.yaml was not created at %s", cfgPath)
	}
	if err != nil {
		t.Fatalf("stat prefs.yaml: %v", err)
	}
	if mode := fileInfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("prefs.yaml perms: got %o, want 0o600", mode)
	}
}

func TestSaveErrorPaths(t *testing.T) {
	tests := []struct {
		name           string
		setup          func(homeDir string) error
		wantSaveError  bool
		wantLoadError  bool
		checkTempFiles func(t *testing.T, cfgDir string)
	}{
		{
			name: "userHomeDir_returns_error",
			setup: func(_ string) error {
				return nil
			},
			wantSaveError:  true,
			wantLoadError:  true,
			checkTempFiles: nil,
		},
		{
			name: "config_dir_path_is_regular_file",
			setup: func(homeDir string) error {
				cfgDir := filepath.Join(homeDir, ".config", "steiner")
				if err := os.MkdirAll(filepath.Dir(cfgDir), 0o700); err != nil {
					return err
				}
				return os.WriteFile(cfgDir, []byte{}, 0o600)
			},
			wantSaveError:  true,
			wantLoadError:  true,
			checkTempFiles: nil,
		},
		{
			name: "prefs_yaml_path_is_nonempty_directory",
			setup: func(homeDir string) error {
				cfgDir := filepath.Join(homeDir, ".config", "steiner")
				if err := os.MkdirAll(cfgDir, 0o700); err != nil {
					return err
				}
				prefsPath := filepath.Join(cfgDir, "prefs.yaml")
				if err := os.Mkdir(prefsPath, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(prefsPath, "dummy"), []byte("data"), 0o600)
			},
			wantSaveError: true,
			wantLoadError: true,
			checkTempFiles: func(t *testing.T, cfgDir string) {
				entries, err := os.ReadDir(cfgDir)
				if err != nil {
					t.Fatalf("ReadDir config dir: %v", err)
				}
				for _, e := range entries {
					if matched, _ := filepath.Match("prefs.yaml.*", e.Name()); matched {
						t.Errorf("found stray temp file: %s", e.Name())
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			homeDir := t.TempDir()
			t.Setenv("HOME", homeDir)

			if tt.name == "userHomeDir_returns_error" {
				oldUserHomeDir := userHomeDir
				t.Cleanup(func() {
					userHomeDir = oldUserHomeDir
				})
				userHomeDir = func() (string, error) {
					return "", errors.New("mock home dir error")
				}
			}

			if tt.setup != nil {
				if err := tt.setup(homeDir); err != nil {
					t.Fatalf("setup failed: %v", err)
				}
			}

			p := Prefs{Accent: "purple", ShowThinking: true, SidebarPosition: "left"}

			saveErr := Save(p)
			if (saveErr == nil) == tt.wantSaveError {
				if tt.wantSaveError {
					t.Errorf("Save() expected error, got nil")
				} else {
					t.Errorf("Save() unexpected error: %v", saveErr)
				}
			}

			loadVal, loadErr := Load()
			if (loadErr == nil) == tt.wantLoadError {
				if tt.wantLoadError {
					t.Errorf("Load() expected error, got nil")
				} else {
					t.Errorf("Load() unexpected error: %v", loadErr)
				}
			}
			if tt.wantLoadError && loadVal != DefaultPrefs() {
				t.Errorf("Load() = %#v, want %#v on error", loadVal, DefaultPrefs())
			}

			if tt.checkTempFiles != nil && tt.setup != nil {
				cfgDir := filepath.Join(homeDir, ".config", "steiner")
				tt.checkTempFiles(t, cfgDir)
			}
		})
	}
}

func TestCloseAndRemoveTempFile(t *testing.T) {
	t.Run("nil_file_does_not_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("closeAndRemoveTempFile(nil) panicked: %v", r)
			}
		}()
		closeAndRemoveTempFile(nil)
	})

	t.Run("removes_temp_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmp, err := os.CreateTemp(tmpDir, "prefs-test-*")
		if err != nil {
			t.Fatalf("CreateTemp failed: %v", err)
		}
		tmpName := tmp.Name()

		_, err = tmp.Write([]byte("test"))
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		if _, err := os.Stat(tmpName); os.IsNotExist(err) {
			t.Fatalf("temp file not created: %v", err)
		}

		closeAndRemoveTempFile(tmp)

		if _, err := os.Stat(tmpName); !os.IsNotExist(err) {
			t.Errorf("temp file not removed after closeAndRemoveTempFile, stat err: %v", err)
		}
	})

	t.Run("idempotent_on_already_closed", func(t *testing.T) {
		tmpDir := t.TempDir()
		tmp, err := os.CreateTemp(tmpDir, "prefs-test-*")
		if err != nil {
			t.Fatalf("CreateTemp failed: %v", err)
		}
		tmpName := tmp.Name()

		closeAndRemoveTempFile(tmp)
		if _, err := os.Stat(tmpName); !os.IsNotExist(err) {
			t.Errorf("temp file not removed: %v", err)
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("second closeAndRemoveTempFile call panicked: %v", r)
			}
		}()
		closeAndRemoveTempFile(tmp)
	})
}
