package config

func applyProjectContextConfigPatch(cfg *Config, patch configPatch) {
	if patch.ProjectContext != nil {
		applyProjectContextPatch(&cfg.ProjectContext, patch.ProjectContext)
	}
}

func applyProjectContextPatch(dst *ProjectContextConfig, patch *projectContextPatch) {
	if patch.MaxTokens != nil {
		dst.MaxTokens = *patch.MaxTokens
	}
	if patch.ExtraFiles != nil {
		dst.ExtraFiles = append([]string(nil), (*patch.ExtraFiles)...)
	}
	if patch.IgnoreFiles != nil {
		dst.IgnoreFiles = append([]string(nil), (*patch.IgnoreFiles)...)
	}
}

func applyPathsConfigPatch(cfg *Config, patch configPatch) {
	if patch.Paths != nil {
		applyPathsPatch(&cfg.Paths, patch.Paths)
	}
}

func applyPathsPatch(dst *PathsConfig, patch *pathsPatch) {
	if patch.ProjectRootOnly != nil {
		dst.ProjectRootOnly = *patch.ProjectRootOnly
	}
	if patch.WritablePaths != nil {
		dst.WritablePaths = append([]string(nil), (*patch.WritablePaths)...)
	}
	if patch.BlockedPaths != nil {
		dst.BlockedPaths = append([]string(nil), (*patch.BlockedPaths)...)
	}
	if patch.ExcludePaths != nil {
		dst.ExcludePaths = append([]string(nil), (*patch.ExcludePaths)...)
	}
	if patch.ExcludePatterns != nil {
		dst.ExcludePatterns = append([]string(nil), (*patch.ExcludePatterns)...)
	}
}

func applyLoggingConfigPatch(cfg *Config, patch configPatch) {
	if patch.Logging != nil {
		applyLoggingPatch(&cfg.Logging, patch.Logging)
	}
}

func applyLoggingPatch(dst *LoggingConfig, patch *loggingPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Level != nil {
		dst.Level = *patch.Level
	}
	if patch.File != nil {
		dst.File = *patch.File
	}
	if patch.ThinkingChunk != nil {
		dst.ThinkingChunk = *patch.ThinkingChunk
	}
}

func applyDebugConfigPatch(cfg *Config, patch configPatch) {
	if patch.Debug != nil {
		applyDebugPatch(&cfg.Debug, patch.Debug)
	}
}

func applyDebugPatch(dst *DebugConfig, patch *debugPatch) {
	if patch.ShowInternalScaffoldInference != nil {
		dst.ShowInternalScaffoldInference = *patch.ShowInternalScaffoldInference
	}
}

func applyContextManagementConfigPatch(cfg *Config, patch configPatch) {
	if patch.ContextManagement != nil {
		applyContextManagementPatch(&cfg.ContextManagement, patch.ContextManagement)
	}
}

func applyContextManagementPatch(dst *ContextManagementConfig, patch *contextManagementPatch) {
	if patch.Mode != nil {
		dst.Mode = *patch.Mode
	}
	if patch.CompactionStrategy != nil {
		dst.CompactionStrategy = *patch.CompactionStrategy
	}
	if patch.MaskingWindowTurns != nil {
		dst.MaskingWindowTurns = *patch.MaskingWindowTurns
	}
	if patch.ReadAnnotations != nil {
		dst.ReadAnnotations = *patch.ReadAnnotations
	}
	if patch.ScratchpadMode != nil {
		dst.ScratchpadMode = *patch.ScratchpadMode
	}
}
