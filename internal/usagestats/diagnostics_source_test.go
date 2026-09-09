package usagestats

import (
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestSourceDiagnosticsSource(t *testing.T) {
	tests := []struct {
		name string
		src  Source
		want diagnostics.Source
	}{
		{name: "parent", src: SourceParent, want: diagnostics.SourceParent},
		{name: "sub agent", src: SourceSubAgent, want: diagnostics.SourceSubAgent},
		{name: "advisor", src: SourceAdvisor, want: diagnostics.SourceAdvisor},
		{name: "unknown falls back to parent", src: Source(99), want: diagnostics.SourceParent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.src.DiagnosticsSource(); got != tt.want {
				t.Errorf("DiagnosticsSource() = %q, want %q", got, tt.want)
			}
		})
	}
}
