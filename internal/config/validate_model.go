package config

import (
	"fmt"
	"strings"
)

func appendModelProblems(problems *[]string, prefix string, model ModelConfig) {
	if model.Type == "" {
		*problems = append(*problems, fmt.Sprintf("%s.type is required", prefix))
	} else if model.Type != "openai_compat" {
		*problems = append(*problems, fmt.Sprintf("%s.type %q is not supported", prefix, model.Type))
	}
	if strings.TrimSpace(model.BaseURL) == "" {
		*problems = append(*problems, fmt.Sprintf("%s.base_url is required", prefix))
	}
	if strings.TrimSpace(model.Model) == "" {
		*problems = append(*problems, fmt.Sprintf("%s.model is required", prefix))
	}
	if model.MaxCompletionTokens < 1 {
		*problems = append(*problems, fmt.Sprintf("%s.max_completion_tokens must be at least 1", prefix))
	}
	if model.ContextSize < 1 {
		*problems = append(*problems, fmt.Sprintf("%s.context_size must be at least 1", prefix))
	}

	appendRetryProblems(problems, prefix+".retry", model.Retry)
	appendCompactionProblems(problems, prefix, model)
}

func appendRetryProblems(problems *[]string, path string, retry RetryConfig) {
	if retry.MaxAttempts < 1 {
		*problems = append(*problems, fmt.Sprintf("%s.max_attempts must be at least 1", path))
	}
	if retry.InitialBackoff.IsZero() {
		*problems = append(*problems, fmt.Sprintf("%s.initial_backoff must be greater than zero", path))
	}
	if retry.MaxBackoff.IsZero() {
		*problems = append(*problems, fmt.Sprintf("%s.max_backoff must be greater than zero", path))
	}
	if retry.RetryAfterMax.IsZero() {
		*problems = append(*problems, fmt.Sprintf("%s.retry_after_max must be greater than zero", path))
	}
	if !retry.InitialBackoff.IsZero() && !retry.MaxBackoff.IsZero() && retry.MaxBackoff.Duration() < retry.InitialBackoff.Duration() {
		*problems = append(*problems, fmt.Sprintf("%s.max_backoff must be greater than or equal to %s.initial_backoff", path, path))
	}
	if !retry.InitialBackoff.IsZero() && !retry.RetryAfterMax.IsZero() && retry.RetryAfterMax.Duration() < retry.InitialBackoff.Duration() {
		*problems = append(*problems, fmt.Sprintf("%s.retry_after_max must be greater than or equal to %s.initial_backoff", path, path))
	}
}

func appendCompactionProblems(problems *[]string, prefix string, model ModelConfig) {
	if model.Compaction.SafetyMarginTokens < 0 {
		*problems = append(*problems, fmt.Sprintf("%s.compaction.safety_margin_tokens must be at least 0", prefix))
	}
	if model.Compaction.SummaryMaxTokens < 1 {
		*problems = append(*problems, fmt.Sprintf("%s.compaction.summary_max_tokens must be at least 1", prefix))
	}
	if model.Compaction.SummaryMaxTokens > model.MaxCompletionTokens {
		*problems = append(*problems, fmt.Sprintf("%s.compaction.summary_max_tokens must be less than or equal to %s.max_completion_tokens", prefix, prefix))
	}
}

func validateSchedulerConfig(problems *[]string, cfg SchedulerConfig) {
	if cfg.Parallelism < 1 {
		*problems = append(*problems, "scheduler.parallelism must be at least 1")
	}
}

func validateModelsConfig(problems *[]string, models map[string]ModelConfig) {
	if len(models) == 0 {
		*problems = append(*problems, "models is required")
	}
	for name, model := range models {
		if strings.TrimSpace(name) == "" {
			*problems = append(*problems, "models contains an empty alias")
		}
		appendModelProblems(problems, fmt.Sprintf("models[%q]", name), model)
	}
}
