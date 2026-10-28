package interactive

import (
	"context"
	"errors"
	"fmt"
	"os"
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

const (
	historyReportTitle  = "Conversation history"
	noHistoryReportText = "No archived conversation history for this session yet."
)

// emitHistoryRecall reports the current session's archived conversation
// history. A non-blank archive opens in the overlay scrolled to its end. A nil
// store, a missing, or a blank archive is an inline notice, and so is a read
// error.
func (s *Session) emitHistoryRecall() {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewInlineReportEvent(historyReportTitle, noHistoryReportText))
		return
	}
	content, err := s.deps.SessionStore.ReadHistory(s.SessionID())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.events.Emit(output.NewInlineReportEvent(historyReportTitle, fmt.Sprintf("Could not read conversation history: %v", err)))
		return
	}
	if err != nil || strings.TrimSpace(content) == "" {
		s.events.Emit(output.NewInlineReportEvent(historyReportTitle, noHistoryReportText))
		return
	}
	s.events.Emit(output.NewOverlayReportEventAtEnd(historyReportTitle, content))
}

func buildConfigReport(cfg config.Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal resolved config: %w", err)
	}
	return "```yaml\n" + strings.TrimRight(string(data), "\n") + "\n```", nil
}
