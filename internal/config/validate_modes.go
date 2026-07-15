package config

import (
	"fmt"
)

func validateModesConfig(problems *[]string, cfg ModesConfig) {
	switch cfg.Default {
	case ExecutionModePlan, ExecutionModeBuild:
		return
	default:
		*problems = append(*problems, fmt.Sprintf("modes.default %q is not supported (valid: %q, %q)", cfg.Default, ExecutionModePlan, ExecutionModeBuild))
	}
}
