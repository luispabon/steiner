package provider

import "github.com/luispabon/steiner/internal/config"

// modelsDevProviderID returns the models.dev top-level provider key to scope
// metadata lookup for a configured provider. Provider aliases are user-chosen
// config keys, so first-party provider types map to their canonical models.dev
// key. Generic or self-hosted types have no canonical key and keep the alias,
// which still matches when users name the alias after a models.dev provider.
func modelsDevProviderID(providerType config.ProviderType, providerAlias string) string {
	switch providerType {
	case config.ProviderTypeOpenAI, config.ProviderTypeCodex:
		return "openai"
	case config.ProviderTypeAnthropic:
		return "anthropic"
	case config.ProviderTypeGemini:
		return "google"
	case config.ProviderTypeOpenRouter:
		return "openrouter"
	case config.ProviderTypeOpencodeGo:
		return "opencode-go"
	case config.ProviderTypeOpencodeZen:
		return "opencode"
	case config.ProviderTypeLMStudio:
		return "lmstudio"
	default:
		// openai_compat, ollama, and litellm front arbitrary backends.
		return providerAlias
	}
}
