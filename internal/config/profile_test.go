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

func TestResolveProfileNamedOnlyProfiles(t *testing.T) {
	cfg := testProfileConfig()
	delete(cfg.Models.Profiles, "default")
	if _, err := ResolveProfile(&cfg, "fast"); err != nil {
		t.Fatalf("ResolveProfile(fast) without a default profile error = %v, want nil", err)
	}
	if _, err := ResolveProfile(&cfg, "default"); err == nil || !strings.Contains(err.Error(), "profile is not defined") {
		t.Fatalf("explicit missing default profile error = %v, want profile is not defined", err)
	}
	cfg = testProfileConfig()
	profile := cfg.Models.Profiles["default"]
	profile.DefaultModel = ""
	cfg.Models.Profiles["default"] = profile
	if _, err := ResolveProfile(&cfg, "default"); err == nil || !strings.Contains(err.Error(), "models.profiles.default.default_model") {
		t.Fatalf("empty baseline error = %v", err)
	}
}

func TestResolveProfileRequiresDefaultModelOnNamedProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile ModelProfile
	}{
		{name: "omitted", profile: ModelProfile{Advisor: "base", SubAgents: map[string]string{"code": "base"}}},
		{name: "empty", profile: ModelProfile{DefaultModel: ""}},
		{name: "whitespace", profile: ModelProfile{DefaultModel: " \t "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testProfileConfig()
			cfg.Models.Profiles["solo"] = tt.profile
			if _, err := ResolveProfile(&cfg, "solo"); err == nil || !strings.Contains(err.Error(), "models.profiles.solo.default_model is required") {
				t.Fatalf("ResolveProfile() error = %v, want default_model required", err)
			}
			if _, err := ResolveEffectiveAssignments(&cfg, "solo"); err == nil || !strings.Contains(err.Error(), "models.profiles.solo.default_model is required") {
				t.Fatalf("ResolveEffectiveAssignments() error = %v, want default_model required", err)
			}
		})
	}
}

func TestValidateProfilesRequireDefaultModelOnEveryProfile(t *testing.T) {
	cfg := testProfileConfig()
	cfg.Models.Profiles["zeta"] = ModelProfile{SubAgents: map[string]string{"code": "base"}}
	cfg.Models.Profiles["alpha"] = ModelProfile{DefaultModel: "  "}

	var problems []string
	validateProfilesConfig(&problems, cfg)
	var got []string
	for _, problem := range problems {
		if strings.Contains(problem, "default_model is required") {
			got = append(got, problem)
		}
	}
	want := []string{
		"models.profiles.alpha.default_model is required",
		"models.profiles.zeta.default_model is required",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default_model problems = %q, want %q", got, want)
	}
}

func TestResolveEffectiveAssignmentsNamedProfileIsIsolated(t *testing.T) {
	cfg := testProfileConfig()
	base := cfg.Models.Profiles["default"]
	base.OneShot = map[string]string{"plan": "base"}
	base.WorkflowHandoff = map[string]string{"implement": "base"}
	cfg.Models.Profiles["default"] = base
	cfg.Models.Profiles["solo"] = ModelProfile{DefaultModel: "fast"}

	got, err := ResolveEffectiveAssignments(&cfg, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "fast" || got.ActiveOrchestratorModel != "fast" {
		t.Fatalf("default model = %q, active orchestrator = %q, want fast", got.DefaultModel, got.ActiveOrchestratorModel)
	}
	if got.Advisor != "" || len(got.SubAgents) != 0 || len(got.OneShot) != 0 || len(got.WorkflowHandoff) != 0 {
		t.Fatalf("named profile inherited roles from default: %#v", got)
	}
}

func TestResolveProfileReturnsIndependentMaps(t *testing.T) {
	cfg := testProfileConfig()
	got, err := ResolveProfile(&cfg, "fast")
	if err != nil {
		t.Fatal(err)
	}
	got.SubAgents["code"] = "mutated"
	if cfg.Models.Profiles["fast"].SubAgents["code"] != "fast" {
		t.Fatalf("resolved sub_agents alias config: %#v", cfg.Models.Profiles["fast"].SubAgents)
	}

	cfg.Models.Profiles["fast"].SubAgents["explore"] = "changed"
	if got.SubAgents["explore"] != "" {
		t.Fatalf("config mutation leaked into resolved sub_agents: %#v", got.SubAgents)
	}
}

func TestResolveProfileSwitchingDoesNotLeakRoles(t *testing.T) {
	cfg := testProfileConfig()
	steps := []struct {
		name        string
		wantProfile string
		wantModel   string
		wantCode    string
	}{
		{name: "fast", wantProfile: "fast", wantModel: "fast", wantCode: "fast"},
		{name: "default", wantProfile: "default", wantModel: "base", wantCode: "base"},
		{name: "fast", wantProfile: "fast", wantModel: "fast", wantCode: "fast"},
		{name: "", wantProfile: "default", wantModel: "base", wantCode: "base"},
	}
	for _, step := range steps {
		got, err := ResolveEffectiveAssignments(&cfg, step.name)
		if err != nil {
			t.Fatalf("ResolveEffectiveAssignments(%q) error = %v", step.name, err)
		}
		if got.ProfileName != step.wantProfile || got.DefaultModel != step.wantModel || got.SubAgents["code"] != step.wantCode {
			t.Fatalf("ResolveEffectiveAssignments(%q) = %#v, want profile %q model %q code %q", step.name, got, step.wantProfile, step.wantModel, step.wantCode)
		}
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
