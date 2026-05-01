package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"gopkg.in/yaml.v3"
)

func (s *Session) emitContextReport(ctx context.Context) {
	if snapshot, ok := s.snapshots.Snapshot(); ok {
		report, err := output.BuildContextReport(ctx, snapshot)
		if err != nil {
			s.events.Emit(output.NewContextReportEvent("Context report unavailable.\n\n" + err.Error()))
			return
		}
		s.events.Emit(output.NewContextReportEvent(report))
		return
	}
	s.events.Emit(output.NewContextReportEvent("No request recorded yet in this interactive session."))
}

func (s *Session) emitConfigReport() {
	report, err := buildConfigReport(s.deps.Config)
	if err != nil {
		s.events.Emit(output.NewConfigReportEvent("Resolved config unavailable.\n\n" + err.Error()))
		return
	}
	s.events.Emit(output.NewConfigReportEvent(report))
}

func buildConfigReport(cfg config.Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal resolved config: %w", err)
	}
	return "```yaml\n" + strings.TrimRight(string(data), "\n") + "\n```", nil
}
