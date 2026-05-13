package config

import "fmt"

func validateApprovalConfig(problems *[]string, cfg ApprovalConfig) {
	if err := validateApprovalMode("approval.default", cfg.Default); err != nil {
		*problems = append(*problems, err.Error())
	}
	for name, mode := range cfg.ToolOverrides {
		if mode == nil {
			continue
		}
		if err := validateApprovalMode(fmt.Sprintf("approval.tool_overrides[%q]", name), *mode); err != nil {
			*problems = append(*problems, err.Error())
		}
	}
}
