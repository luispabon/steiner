package config

import (
	"fmt"
	"strings"
)

func providerNeedsBaseURL(providerType ProviderType) bool {
	switch providerType {
	case ProviderTypeOpenAICompat, ProviderTypeOllama, ProviderTypeLMStudio, ProviderTypeLiteLLM:
		return true
	default:
		return false
	}
}

func providerNeedsCredential(providerType ProviderType) bool {
	switch providerType {
	case ProviderTypeOpenRouter, ProviderTypeOpenAI, ProviderTypeAnthropic, ProviderTypeGemini:
		return true
	default:
		return false
	}
}

func appendModelProblems(problems *[]string, prefix string, model ModelConfig, providers map[string]ProviderConfig) {
	if model.Provider == "" {
		*problems = append(*problems, fmt.Sprintf("%s.provider is required", prefix))
	} else if _, ok := providers[model.Provider]; !ok {
		*problems = append(*problems, fmt.Sprintf("%s.provider %q is not defined in providers", prefix, model.Provider))
	}
	if strings.TrimSpace(model.ID) == "" {
		*problems = append(*problems, fmt.Sprintf("%s.id is required", prefix))
	}
	switch model.Advanced.Transport {
	case "", ModelTransportAuto, ModelTransportOpenAICompat, ModelTransportAnthropic:
		// valid
	default:
		*problems = append(*problems, fmt.Sprintf("%s.advanced.transport %q is not supported", prefix, model.Advanced.Transport))
	}
	appendReasoningProblems(problems, prefix+".advanced.reasoning", model.Advanced.Reasoning)
	appendRetryProblems(problems, prefix+".retry", model.Retry)
}

func appendReasoningProblems(problems *[]string, path string, reasoning ReasoningConfig) {
	effort := strings.TrimSpace(reasoning.Effort)
	if reasoning.Effort != "" && effort == "" {
		*problems = append(*problems, fmt.Sprintf("%s.effort must not be blank", path))
	}

	seen := make(map[string]bool, len(reasoning.SupportedEfforts))
	for _, raw := range reasoning.SupportedEfforts {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			*problems = append(*problems, fmt.Sprintf("%s.supported_efforts must not contain blank values", path))
			continue
		}
		if seen[trimmed] {
			*problems = append(*problems, fmt.Sprintf("%s.supported_efforts contains duplicate value %q", path, trimmed))
			continue
		}
		seen[trimmed] = true
	}

	if effort != "" && len(reasoning.SupportedEfforts) > 0 && !seen[effort] {
		*problems = append(*problems, fmt.Sprintf("%s.effort %q is not present in %s.supported_efforts", path, reasoning.Effort, path))
	}
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

func validateTUIConfig(problems *[]string, cfg TUIConfig) {
	if cfg.FPS < 1 || cfg.FPS > 120 {
		*problems = append(*problems, "tui.fps must be between 1 and 120")
	}
}

func validateSchedulerConfig(problems *[]string, cfg SchedulerConfig) {
	if cfg.Parallelism < 1 {
		*problems = append(*problems, "scheduler.parallelism must be at least 1")
	}
}

func validateProvidersConfig(problems *[]string, providers map[string]ProviderConfig) {
	if len(providers) == 0 {
		*problems = append(*problems, "providers is required")
	}
	for name, p := range providers {
		if strings.TrimSpace(name) == "" {
			*problems = append(*problems, "providers contains an empty alias")
		}
		if p.Type == "" {
			*problems = append(*problems, fmt.Sprintf("providers[%q].type is required", name))
		} else {
			switch p.Type {
			case ProviderTypeOpenAICompat, ProviderTypeOllama, ProviderTypeLMStudio,
				ProviderTypeOpenRouter, ProviderTypeOpenAI, ProviderTypeAnthropic,
				ProviderTypeGemini, ProviderTypeLiteLLM, ProviderTypeCodex:
				// valid
			default:
				*problems = append(*problems, fmt.Sprintf("providers[%q].type %q is not supported", name, p.Type))
			}
		}
		if providerNeedsBaseURL(p.Type) && strings.TrimSpace(p.BaseURL) == "" {
			*problems = append(*problems, fmt.Sprintf("providers[%q].base_url is required", name))
		}
		if providerNeedsCredential(p.Type) && strings.TrimSpace(p.APIKey) == "" && strings.TrimSpace(p.APIKeyEnv) == "" {
			*problems = append(*problems, fmt.Sprintf("providers[%q] must set api_key or api_key_env", name))
		}
	}
}

func validateModelsConfig(problems *[]string, models map[string]ModelConfig, providers map[string]ProviderConfig) {
	if len(models) == 0 {
		*problems = append(*problems, "models is required")
	}
	for name, model := range models {
		if strings.TrimSpace(name) == "" {
			*problems = append(*problems, "models contains an empty alias")
		}
		appendModelProblems(problems, fmt.Sprintf("models[%q]", name), model, providers)
	}
}
