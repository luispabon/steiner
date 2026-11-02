package config

func applySearchPatch(dst *SearchConfig, patch *searchPatch) {
	if patch.Backend != nil {
		dst.Backend = *patch.Backend
	}
	if patch.SearxngURL != nil {
		dst.SearxngURL = *patch.SearxngURL
	}
	if patch.KagiAPIKey != nil {
		dst.KagiAPIKey = *patch.KagiAPIKey
	}
	if patch.BraveAPIKey != nil {
		dst.BraveAPIKey = *patch.BraveAPIKey
	}
}
