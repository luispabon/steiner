package provider

import "github.com/luispabon/steiner/internal/config"

// transportChoice is a placeholder for a provider's fixed transport outcome.
// TODO(stage B): will be replaced/extended when the fact-based resolver lands.
type transportChoice struct {
	Reason string
}

// providerProfile describes provider-type-specific metadata resolution behaviour.
type providerProfile struct {
	ModelsDevID    string // "" = generic: no canonical models.dev key
	DefaultBaseURL string
	Generic        bool             // fronts arbitrary backends (openai_compat, ollama, litellm)
	FixedTransport *transportChoice // non-nil only for codex; nil otherwise
	LiveProbe      bool             // true only for ollama
}

var providerProfiles = map[config.ProviderType]providerProfile{
	config.ProviderTypeOpenAI: {
		ModelsDevID:    "openai",
		DefaultBaseURL: "https://api.openai.com/v1",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeCodex: {
		ModelsDevID:    "openai",
		DefaultBaseURL: "https://api.openai.com/v1",
		Generic:        false,
		FixedTransport: &transportChoice{Reason: "codex provider uses OAuth Responses transport"},
		LiveProbe:      false,
	},
	config.ProviderTypeAnthropic: {
		ModelsDevID:    "anthropic",
		DefaultBaseURL: "",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeGemini: {
		ModelsDevID:    "google",
		DefaultBaseURL: "",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeOpenRouter: {
		ModelsDevID:    "openrouter",
		DefaultBaseURL: "https://openrouter.ai/api/v1",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeOpencodeGo: {
		ModelsDevID:    "opencode-go",
		DefaultBaseURL: "https://opencode.ai/zen/go/v1",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeOpencodeZen: {
		ModelsDevID:    "opencode",
		DefaultBaseURL: "https://opencode.ai/zen/v1",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeLMStudio: {
		ModelsDevID:    "lmstudio",
		DefaultBaseURL: "",
		Generic:        false,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeOpenAICompat: {
		ModelsDevID:    "",
		DefaultBaseURL: "",
		Generic:        true,
		FixedTransport: nil,
		LiveProbe:      false,
	},
	config.ProviderTypeOllama: {
		ModelsDevID:    "",
		DefaultBaseURL: "",
		Generic:        true,
		FixedTransport: nil,
		LiveProbe:      true,
	},
	config.ProviderTypeLiteLLM: {
		ModelsDevID:    "",
		DefaultBaseURL: "",
		Generic:        true,
		FixedTransport: nil,
		LiveProbe:      false,
	},
}

// profileFor returns the provider profile for a given provider type.
// For unknown or empty types, returns a generic profile.
func profileFor(t config.ProviderType) providerProfile {
	if p, ok := providerProfiles[t]; ok {
		return p
	}
	return providerProfile{Generic: true}
}
