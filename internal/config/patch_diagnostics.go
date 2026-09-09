package config

func applyDiagnosticsPatch(dst *DiagnosticsConfig, patch *diagnosticsPatch) {
	setIfPresent(&dst.Enabled, patch.Enabled)
	setIfPresent(&dst.Dir, patch.Dir)
	setIfPresent(&dst.RetentionDays, patch.RetentionDays)
	setIfPresent(&dst.CaptureBodies, patch.CaptureBodies)
	if patch.Streams != nil {
		applyDiagnosticsStreamsPatch(&dst.Streams, patch.Streams)
	}
}

func applyDiagnosticsStreamsPatch(dst *DiagnosticsStreamsConfig, patch *diagnosticsStreamsPatch) {
	setIfPresent(&dst.Cache, patch.Cache)
	setIfPresent(&dst.Provider, patch.Provider)
	setIfPresent(&dst.Tool, patch.Tool)
}
