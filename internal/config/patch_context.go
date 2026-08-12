package config

func applyProjectContextPatch(dst *ProjectContextConfig, patch *projectContextPatch) {
	if patch.MaxBytes != nil {
		dst.MaxBytes = *patch.MaxBytes
	} else if patch.MaxTokens != nil {
		dst.MaxBytes = *patch.MaxTokens * 4
	}
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

func applyContextManagementPatch(dst *ContextManagementConfig, patch *contextManagementPatch) {
	if patch.ReadAnnotations != nil {
		dst.ReadAnnotations = *patch.ReadAnnotations
	}
}
