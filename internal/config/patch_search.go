package config

func applySearchConfigPatch(cfg *Config, patch configPatch) {
	if patch.Search != nil {
		applySearchPatch(&cfg.Search, patch.Search)
	}
}

func applySearchPatch(dst *SearchConfig, patch *searchPatch) {
	if patch.Backend != nil {
		dst.Backend = *patch.Backend
	}
	if patch.SearxngURL != nil {
		dst.SearxngURL = *patch.SearxngURL
	}
	if patch.GoogleCx != nil {
		dst.GoogleCx = *patch.GoogleCx
	}
	if patch.GoogleAPIKey != nil {
		dst.GoogleAPIKey = *patch.GoogleAPIKey
	}
	if patch.KagiAPIKey != nil {
		dst.KagiAPIKey = *patch.KagiAPIKey
	}
	if patch.BraveAPIKey != nil {
		dst.BraveAPIKey = *patch.BraveAPIKey
	}
}
