package interactive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestWorkflowHandoffFallsBackToIsolatedProfileDefault checks that a named
// profile with only default_model hands off every destination to that model.
func TestWorkflowHandoffFallsBackToIsolatedProfileDefault(t *testing.T) {
	s := testNewSession(t, Dependencies{Config: loadIsolatedFastProfileConfig(t)})

	for _, destination := range []string{"implement", "review", "build"} {
		if got, want := s.WorkflowHandoffModelSelection(destination), (WorkflowHandoffModelSelection{
			ModelAlias:  "fast",
			SourceLabel: "profile default",
		}); got != want {
			t.Errorf("WorkflowHandoffModelSelection(%s) = %#v, want %#v", destination, got, want)
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
