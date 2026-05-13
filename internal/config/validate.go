package config

import (
	"fmt"
	"strings"
)

func validate(cfg Config) error {
	var problems []string

	appendModelProblems(&problems, "model", cfg.Model)
	validateSchedulerConfig(&problems, cfg.Scheduler)
	validateModelsConfig(&problems, cfg.Models)
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
