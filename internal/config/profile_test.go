package config

import (
	"reflect"
	"strings"
	"testing"
)

func testProfileConfig() Config {
	return Config{
		Providers: map[string]ProviderConfig{"local": {}},
		Models: ModelsConfig{
			Definitions: map[string]ModelConfig{
				"base": {Provider: "local", ID: "base"},
				"fast": {Provider: "local", ID: "fast"},
			},
			Profiles: map[string]ModelProfile{
				"default": {
					DefaultModel: "base",
					Advisor:      "base",
					SubAgents:    map[string]string{"code": "base", "explore": "base"},
				},
				"fast": {
					DefaultModel: "fast",
					SubAgents:    map[string]string{"code": "fast", "explore": ""},
				},
			},
		},
	}
}

func TestResolveEffectiveAssignmentsUsesImmutableBaseline(t *testing.T) {
	cfg := testProfileConfig()
	got, err := ResolveEffectiveAssignments(&cfg, "fast")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileName != "fast" || got.DefaultModel != "fast" || got.ActiveOrchestratorModel != "fast" {
		t.Fatalf("assignments = %#v", got)
	}
	if got.SubAgents["code"] != "fast" || got.SubAgents["explore"] != "" {
		t.Fatalf("sub_agents = %#v", got.SubAgents)
	}

	got.SubAgents["code"] = "mutated"
	got, err = ResolveEffectiveAssignments(&cfg, "default")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "base" || got.SubAgents["code"] != "base" || got.SubAgents["explore"] != "base" {
		t.Fatalf("baseline leaked selection or mutation = %#v", got)
	}
}

func TestResolveProfileRejectsUnknownAndInvalidReferences(t *testing.T) {
	cfg := testProfileConfig()
	if _, err := ResolveProfile(&cfg, "missing"); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("unknown profile error = %v", err)
	}
	profile := cfg.Models.Profiles["fast"]
	profile.DefaultModel = "missing"
	cfg.Models.Profiles["fast"] = profile
	if _, err := ResolveProfile(&cfg, "fast"); err == nil || !strings.Contains(err.Error(), "default_model") {
		t.Fatalf("invalid reference error = %v", err)
	}
}

func TestResolveProfileRequiresBaseline(t *testing.T) {
	cfg := testProfileConfig()
	delete(cfg.Models.Profiles, "default")
	if _, err := ResolveProfile(&cfg, "fast"); err == nil || !strings.Contains(err.Error(), "models.profiles.default") {
		t.Fatalf("missing baseline error = %v", err)
	}
	cfg = testProfileConfig()
	profile := cfg.Models.Profiles["default"]
	profile.DefaultModel = ""
	cfg.Models.Profiles["default"] = profile
	if _, err := ResolveProfile(&cfg, "default"); err == nil || !strings.Contains(err.Error(), "default_model") {
		t.Fatalf("empty baseline error = %v", err)
	}
}

func TestResolveEffectiveAssignmentsNamedProfileOverlay(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		wantDefault   string
		wantAdvisor   string
		wantSubAgents map[string]string
	}{
		{
			name: "omitted advisor inherits baseline",
			yaml: `models:
  profiles:
    overlay:
      default_model: fast
      sub_agents:
        code: fast
        explore: ""
`,
			wantDefault:   "fast",
			wantAdvisor:   "base",
			wantSubAgents: map[string]string{"code": "fast", "explore": ""},
		},
		{
			name: "explicit empty advisor clears baseline",
			yaml: `models:
  profiles:
    overlay:
      default_model: fast
      advisor: ""
      sub_agents:
        code: fast
        explore: ""
`,
			wantDefault:   "fast",
			wantAdvisor:   "",
			wantSubAgents: map[string]string{"code": "fast", "explore": ""},
		},
		{
			name: "empty default model inherits baseline",
			yaml: `models:
  profiles:
    overlay:
      default_model: ""
      sub_agents:
        code: fast
        explore: ""
`,
			wantDefault:   "base",
			wantAdvisor:   "base",
			wantSubAgents: map[string]string{"code": "fast", "explore": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testProfileConfig()
			patch, err := parseConfigPatch(tt.yaml)
			if err != nil {
				t.Fatal(err)
			}
			applyModelsPatch(&cfg, patch.Models)

			got, err := ResolveEffectiveAssignments(&cfg, "overlay")
			if err != nil {
				t.Fatal(err)
			}
			if got.DefaultModel != tt.wantDefault || got.ActiveOrchestratorModel != tt.wantDefault {
				t.Fatalf("default model = %q, active orchestrator = %q, want %q", got.DefaultModel, got.ActiveOrchestratorModel, tt.wantDefault)
			}
			if got.Advisor != tt.wantAdvisor {
				t.Fatalf("advisor = %q, want %q", got.Advisor, tt.wantAdvisor)
			}
			if !reflect.DeepEqual(got.SubAgents, tt.wantSubAgents) {
				t.Fatalf("sub_agents = %#v, want %#v", got.SubAgents, tt.wantSubAgents)
			}

			got.SubAgents["code"] = "mutated"
			baseline, err := ResolveEffectiveAssignments(&cfg, "default")
			if err != nil {
				t.Fatal(err)
			}
			if baseline.DefaultModel != "base" || baseline.Advisor != "base" || baseline.SubAgents["code"] != "base" || baseline.SubAgents["explore"] != "base" {
				t.Fatalf("baseline changed after named selection: %#v", baseline)
			}
			other, err := ResolveEffectiveAssignments(&cfg, "fast")
			if err != nil {
				t.Fatal(err)
			}
			selectedProfile := cfg.Models.Profiles["overlay"]
			if selectedProfile.SubAgents["code"] != "fast" || selectedProfile.SubAgents["explore"] != "" {
				t.Fatalf("named profile changed after resolution: %#v", selectedProfile.SubAgents)
			}
			if other.DefaultModel != "fast" || other.Advisor != "base" || other.SubAgents["code"] != "fast" || other.SubAgents["explore"] != "" {
				t.Fatalf("prior selection leaked into another profile: %#v", other)
			}
		})
	}
}

func TestProfilePatchPresenceAndMapMerge(t *testing.T) {
	patch, err := parseConfigPatch(`models:
  profiles:
    default:
      default_model: base
      sub_agents:
        code: base
    fast:
      default_model: ""
      advisor: ""
      sub_agents:
        code: fast
        explore: ""
`)
	if err != nil {
		t.Fatal(err)
	}
	if patch.Models == nil || patch.Models.Profiles == nil {
		t.Fatal("profiles patch is nil")
	}
	fast := (*patch.Models.Profiles)["fast"]
	if fast.DefaultModel == nil || *fast.DefaultModel != "" || fast.Advisor == nil || *fast.Advisor != "" {
		t.Fatalf("scalar presence lost: %#v", fast)
	}
	if fast.SubAgents == nil || (*fast.SubAgents)["explore"] != "" {
		t.Fatalf("map presence lost: %#v", fast.SubAgents)
	}

	cfg := Config{Models: ModelsConfig{Profiles: map[string]ModelProfile{
		"default": {SubAgents: map[string]string{"code": "base", "explore": "base"}},
	}}}
	applyModelsPatch(&cfg, patch.Models)
	if got := cfg.Models.Profiles["default"].SubAgents; got["code"] != "base" {
		t.Fatalf("baseline profile changed unexpectedly: %#v", got)
	}
	if got := cfg.Models.Profiles["fast"].SubAgents; got["code"] != "fast" || got["explore"] != "" {
		t.Fatalf("named profile map = %#v", got)
	}
}

func TestResolveEffectiveAssignmentsCopiesMaps(t *testing.T) {
	cfg := testProfileConfig()
	got, err := ResolveEffectiveAssignments(&cfg, "default")
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(got.SubAgents).Pointer() == reflect.ValueOf(cfg.Models.Profiles["default"].SubAgents).Pointer() {
		t.Fatal("sub_agents map was not copied")
	}
}
