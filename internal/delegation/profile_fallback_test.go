package delegation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// TestProfileFallbacksResolveToIsolatedDefaultModel checks that the advisor and
// sub-agents without explicit assignments resolve to a named profile's
// default_model.
func TestProfileFallbacksResolveToIsolatedDefaultModel(t *testing.T) {
	cfg := loadIsolatedFastProfileConfig(t)

	t.Run("advisor", func(t *testing.T) {
		var resolved string
		_, err := newAdvisorRuntime(DelegateDeps{
			Config: cfg,
			ResolveModel: func(alias string) (provider.ResolvedModel, error) {
				resolved = alias
				return provider.ResolvedModel{}, errors.New("stop after alias")
			},
		})
		if err == nil {
			t.Fatal("newAdvisorRuntime() error = nil, want resolver error")
		}
		if resolved != "fast" {
			t.Fatalf("advisor resolved alias = %q, want %q", resolved, "fast")
		}
	})

	t.Run("sub-agent", func(t *testing.T) {
		var resolved string
		deps := minimalDeps(nil)
		deps.CurrentEffectiveAssignments = func() config.EffectiveModelAssignments { return cfg.Models.Effective }
		deps.ModelResolver = func(alias string) (provider.Provider, provider.ResolvedModel, error) {
			resolved = alias
			return nil, provider.ResolvedModel{}, errors.New("stop after alias")
		}
		if _, _, err := resolveModel(AgentTypeExplore, deps); err == nil {
			t.Fatal("resolveModel() error = nil, want resolver error")
		}
		if resolved != "fast" {
			t.Fatalf("sub-agent resolved alias = %q, want %q", resolved, "fast")
		}
	})
}

func loadIsolatedFastProfileConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	contents := "models:\n  definitions:\n    fast:\n      provider: local\n      id: fast\n  profiles:\n    default:\n      default_model: fast\n    fast:\n      default_model: fast\n"
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
