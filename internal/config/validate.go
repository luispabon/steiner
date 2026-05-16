package config

import (
	"fmt"
	"strings"
)

func validate(cfg Config) error {
	var problems []string

	validateDefaultModel(&problems, cfg)
	validateProvidersConfig(&problems, cfg.Providers)
	validateSchedulerConfig(&problems, cfg.Scheduler)
	validateModelsConfig(&problems, cfg.Models, cfg.Providers)
	validateLimitsConfig(&problems, cfg.Limits)
	validateApprovalConfig(&problems, cfg.Approval)
	validateSubAgentConfig(&problems, cfg.SubAgent)
	validateProjectContextConfig(&problems, cfg.ProjectContext)
	validateLoggingConfig(&problems, cfg.Logging)
	validateToolsConfig(&problems, cfg.Tools)
	validateContextManagementConfig(&problems, cfg.ContextManagement)

	if len(problems) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateDefaultModel(problems *[]string, cfg Config) {
	if cfg.DefaultModel == "" {
		*problems = append(*problems, "default_model is required")
		return
	}
	if _, ok := cfg.Models[cfg.DefaultModel]; !ok {
		*problems = append(*problems, fmt.Sprintf("default_model %q is not defined in models", cfg.DefaultModel))
	}
}
