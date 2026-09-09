package usagestats

import "github.com/luispabon/steiner/internal/diagnostics"

// DiagnosticsSource converts s to the diagnostics package's own Source. The
// conversion lives here because internal/diagnostics imports stdlib only.
func (s Source) DiagnosticsSource() diagnostics.Source {
	switch s {
	case SourceSubAgent:
		return diagnostics.SourceSubAgent
	case SourceAdvisor:
		return diagnostics.SourceAdvisor
	default:
		return diagnostics.SourceParent
	}
}
