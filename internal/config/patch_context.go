package config

func applyProjectContextPatch(dst *ProjectContextConfig, patch *projectContextPatch) {
	if patch.MaxBytes != nil {
		dst.MaxBytes = *patch.MaxBytes
	} else if patch.MaxTokens != nil {
		dst.MaxBytes = *patch.MaxTokens * 4
	}
	setIfPresent(&dst.MaxTokens, patch.MaxTokens)
	if patch.ExtraFiles != nil {
		dst.ExtraFiles = append([]string(nil), (*patch.ExtraFiles)...)
	}
	if patch.IgnoreFiles != nil {
		dst.IgnoreFiles = append([]string(nil), (*patch.IgnoreFiles)...)
	}
}

func applyPathsPatch(dst *PathsConfig, patch *pathsPatch) {
	setIfPresent(&dst.ProjectRootOnly, patch.ProjectRootOnly)
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

func applyLoggingPatch(dst *LoggingConfig, patch *loggingPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.Level, patch.Level)
	setIfPresent(&dst.File, patch.File)
	setIfPresent(&dst.ThinkingChunk, patch.ThinkingChunk)
}

func applyContextManagementPatch(dst *ContextManagementConfig, patch *contextManagementPatch) {
	setIfPresent(&dst.ReadAnnotations, patch.ReadAnnotations)
}
