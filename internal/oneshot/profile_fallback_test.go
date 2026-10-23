package oneshot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestPhaseModelFallsBackToIsolatedProfileDefault checks that a named profile
// with only default_model routes every unassigned phase to that model.
func TestPhaseModelFallsBackToIsolatedProfileDefault(t *testing.T) {
	cfg := loadIsolatedFastProfileConfig(t)

	for _, phase := range phaseOrder() {
		if got, want := phaseModelAlias(cfg, phase), "fast"; got != want {
			t.Errorf("phase %s model alias = %q, want %q", phase, got, want)
		}
	}

	snapshot := phaseModelSnapshot(cfg)
	for _, phase := range phaseOrder() {
		if got, want := snapshot.PhaseModels[phase], "fast"; got != want {
			t.Errorf("snapshot phase %s model = %q, want %q", phase, got, want)
		}
	}
}

func loadIsolatedFastProfileConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	contents := "providers:\n  local:\n    type: openai_compat\n    base_url: http://localhost:11434/v1\nmodels:\n  definitions:\n    fast:\n      provider: local\n      id: fast\n  profiles:\n    default:\n      default_model: fast\n    fast:\n      default_model: fast\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	cfg, err := config.Load(config.LoadOptions{
		ProjectConfigPath: configPath,
		WorkingDir:        dir,
		HomeDir:           filepath.Join(dir, "home"),
		Env:               map[string]string{},
		ProjectTrust:      config.ProjectTrustTrusted,
		CLI:               config.CLIOverrides{Profile: "fast"},
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}
