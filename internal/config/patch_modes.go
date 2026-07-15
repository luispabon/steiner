package config

func applyModesPatch(modes *ModesConfig, patch *modesPatch) {
	if patch.Default != nil {
		modes.Default = *patch.Default
	}
}
