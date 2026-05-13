package config

import (
	"fmt"
	"strings"
)

func validateContextMode(path string, mode ContextMode) error {
	switch mode {
	case ContextModeNaive, ContextModeSmart:
		return nil
	default:
		if mode == "" {
			return fmt.Errorf("%s is required", path)
		}
		return fmt.Errorf("%s %q is not supported", path, mode)
	}
}

func validateCompactionStrategy(path string, strategy CompactionStrategy) error {
	switch strategy {
	case CompactionStrategyDrop, CompactionStrategySummarize, CompactionStrategyHybrid:
		return nil
	default:
		return fmt.Errorf("%s %q is not supported", path, strategy)
	}
}

func validateScratchpadMode(path string, mode ScratchpadMode) error {
	switch mode {
	case ScratchpadModeScaffoldOnly, ScratchpadModeHybrid:
		return nil
	default:
		if mode == "" {
			return fmt.Errorf("%s is required", path)
		}
		return fmt.Errorf("%s %q is not supported", path, mode)
	}
}

func validateApprovalMode(path string, mode ApprovalMode) error {
	switch mode {
	case ApprovalModeAuto, ApprovalModePrompt, ApprovalModeDeny:
		return nil
	default:
		if mode == "" {
			return fmt.Errorf("%s is required", path)
		}
		return fmt.Errorf("%s %q is not supported", path, mode)
	}
}

func validateLoggingLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace", "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("logging.level %q is not supported", level)
	}
}
