package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SelectionConfig holds runtime model selections that do not come from config files.
type SelectionConfig struct {
	ModelOverride string `yaml:"-"`
}

// UnmarshalYAML decodes a profile while retaining field presence for partial
// named-profile overlays.
func (p *ModelProfile) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("profile must be a mapping")
	}
	var raw struct {
		DefaultModel    *string            `yaml:"default_model"`
		Advisor         *string            `yaml:"advisor"`
		SubAgents       *map[string]string `yaml:"sub_agents"`
		OneShot         *map[string]string `yaml:"oneshot"`
		WorkflowHandoff *map[string]string `yaml:"workflow_handoff"`
	}
	allowed := map[string]bool{
		"default_model": true, "advisor": true, "sub_agents": true,
		"oneshot": true, "workflow_handoff": true,
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		if !allowed[value.Content[i].Value] {
			return fmt.Errorf("field %s not found in type config.ModelProfile", value.Content[i].Value)
		}
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	p.DefaultModel = stringValue(raw.DefaultModel)
	p.Advisor = stringValue(raw.Advisor)
	p.SubAgents = stringMapValue(raw.SubAgents)
	p.OneShot = stringMapValue(raw.OneShot)
	p.WorkflowHandoff = stringMapValue(raw.WorkflowHandoff)
	p.defaultModelSet = raw.DefaultModel != nil
	p.advisorSet = raw.Advisor != nil
	p.subAgentsSet = raw.SubAgents != nil
	p.oneShotSet = raw.OneShot != nil
	p.workflowHandoffSet = raw.WorkflowHandoff != nil
	return nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringMapValue(value *map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	return copyStringMap(*value)
}

func copyStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// ResolveProfile returns effective assignments for name. Named profiles overlay
// the immutable default profile only. An empty name selects default.
func ResolveProfile(cfg *Config, name string) (ModelProfile, error) {
	if cfg == nil {
		return ModelProfile{}, fmt.Errorf("resolve profile: config is nil")
	}
	baseline, ok := cfg.Models.Profiles["default"]
	if !ok {
		return ModelProfile{}, fmt.Errorf("resolve profile %q: models.profiles.default is required", name)
	}
	if strings.TrimSpace(baseline.DefaultModel) == "" {
		return ModelProfile{}, fmt.Errorf("resolve profile %q: models.profiles.default.default_model is required", name)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	selected, ok := cfg.Models.Profiles[name]
	if !ok {
		return ModelProfile{}, fmt.Errorf("resolve profile %q: profile is not defined", name)
	}
	if name == "default" {
		effective := cloneProfile(baseline)
		if err := validateResolvedProfile(cfg, name, effective); err != nil {
			return ModelProfile{}, err
		}
		return effective, nil
	}

	effective := cloneProfile(baseline)
	if selected.defaultModelSet || selected.DefaultModel != "" {
		if selected.DefaultModel != "" {
			effective.DefaultModel = selected.DefaultModel
		}
	}
	if selected.advisorSet || selected.Advisor != "" {
		effective.Advisor = selected.Advisor
	}
	if selected.subAgentsSet || selected.SubAgents != nil {
		mergeStringMapPatch(&effective.SubAgents, selected.SubAgents)
	}
	if selected.oneShotSet || selected.OneShot != nil {
		mergeStringMapPatch(&effective.OneShot, selected.OneShot)
	}
	if selected.workflowHandoffSet || selected.WorkflowHandoff != nil {
		mergeStringMapPatch(&effective.WorkflowHandoff, selected.WorkflowHandoff)
	}
	if err := validateResolvedProfile(cfg, name, effective); err != nil {
		return ModelProfile{}, err
	}
	return effective, nil
}

func validateResolvedProfile(cfg *Config, name string, profile ModelProfile) error {
	prefix := fmt.Sprintf("models.profiles[%q]", name)
	var problems []string
	if !IsValidModelReference(cfg, profile.DefaultModel) {
		problems = append(problems, fmt.Sprintf("%s.default_model %q is not defined in models.definitions or providers", prefix, profile.DefaultModel))
	}
	if profile.Advisor != "" && !IsValidModelReference(cfg, profile.Advisor) {
		problems = append(problems, fmt.Sprintf("%s.advisor %q is not defined in models.definitions or providers", prefix, profile.Advisor))
	}
	validateModelReferenceMap(&problems, prefix+".sub_agents", "agent type", profile.SubAgents, validAgentTypes, *cfg)
	validateModelReferenceMap(&problems, prefix+".oneshot", "phase", profile.OneShot, validOneShotPhases, *cfg)
	validateModelReferenceMap(&problems, prefix+".workflow_handoff", "destination", profile.WorkflowHandoff, validWorkflowHandoffDestinations, *cfg)
	if len(problems) > 0 {
		return fmt.Errorf("resolve profile %q: %s", name, strings.Join(problems, "; "))
	}
	return nil
}

// ResolveModelProfile is an explicit alias for callers resolving model profiles.
func ResolveModelProfile(cfg *Config, name string) (ModelProfile, error) {
	return ResolveProfile(cfg, name)
}

// ResolveEffectiveAssignments resolves all assignments for one selected profile.
func ResolveEffectiveAssignments(cfg *Config, name string) (EffectiveModelAssignments, error) {
	selected := strings.TrimSpace(name)
	if selected == "" {
		selected = "default"
	}
	profile, err := ResolveProfile(cfg, selected)
	if err != nil {
		return EffectiveModelAssignments{}, err
	}
	return EffectiveModelAssignments{
		ProfileName:             selected,
		DefaultModel:            profile.DefaultModel,
		Advisor:                 profile.Advisor,
		SubAgents:               copyStringMap(profile.SubAgents),
		OneShot:                 copyStringMap(profile.OneShot),
		WorkflowHandoff:         copyStringMap(profile.WorkflowHandoff),
		ActiveOrchestratorModel: profile.DefaultModel,
	}, nil
}

// ResolveModelAssignments is an alias for ResolveEffectiveAssignments.
func ResolveModelAssignments(cfg *Config, name string) (EffectiveModelAssignments, error) {
	return ResolveEffectiveAssignments(cfg, name)
}

func cloneProfile(src ModelProfile) ModelProfile {
	src.SubAgents = copyStringMap(src.SubAgents)
	src.OneShot = copyStringMap(src.OneShot)
	src.WorkflowHandoff = copyStringMap(src.WorkflowHandoff)
	return src
}
