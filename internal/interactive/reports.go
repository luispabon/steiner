package interactive

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

func (s *Session) emitContextReport(ctx context.Context) {
	if snapshot, ok := s.snapshots.Snapshot(); ok {
		report, err := BuildContextReport(ctx, snapshot)
		if err != nil {
			s.events.Emit(output.NewOverlayReportEvent("Context Report", "Context report unavailable.\n\n"+err.Error()))
			return
		}
		s.events.Emit(output.NewOverlayReportEvent("Context Report", report))
		return
	}
	s.events.Emit(output.NewOverlayReportEvent("Context Report", "No request recorded yet in this interactive session."))
}

func (s *Session) emitConfigReport() {
	report, err := buildConfigReport(s.deps.Config)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Config", "Resolved config unavailable.\n\n"+err.Error()))
		return
	}
	s.events.Emit(output.NewOverlayReportEvent("Config", report))
}

func buildConfigReport(cfg config.Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal resolved config: %w", err)
	}
	return "```yaml\n" + strings.TrimRight(string(data), "\n") + "\n```", nil
}
