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
