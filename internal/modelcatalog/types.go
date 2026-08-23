package modelcatalog

// DiscoveredModel describes a model discovered from a provider or metadata source.
type DiscoveredModel struct {
	ProviderAlias    string   `json:"provider_alias" yaml:"provider_alias"`
	ProviderType     string   `json:"provider_type" yaml:"provider_type"`
	ID               string   `json:"id" yaml:"id"`
	DisplayName      string   `json:"display_name" yaml:"display_name"`
	Description      string   `json:"description" yaml:"description"`
	ContextLength    int      `json:"context_length" yaml:"context_length"`
	SupportedEfforts []string `json:"supported_efforts" yaml:"supported_efforts"`
	Priority         int      `json:"priority" yaml:"priority"`
}

// ModelChoice describes a model option shown to the user.
type ModelChoice struct {
	Ref              string   `json:"ref" yaml:"ref"`
	Display          string   `json:"display" yaml:"display"`
	ProviderAlias    string   `json:"provider_alias" yaml:"provider_alias"`
	ModelID          string   `json:"model_id" yaml:"model_id"`
	Aliased          bool     `json:"aliased" yaml:"aliased"`
	SwitchCount      int      `json:"switch_count" yaml:"switch_count"`
	SupportedEfforts []string `json:"supported_efforts" yaml:"supported_efforts"`
	Current          bool     `json:"current" yaml:"current"`
}
