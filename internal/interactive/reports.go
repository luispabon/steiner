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

// emitContextReport emits the context report for the request scope identified
// by agentID. An empty agentID reports the orchestrator's latest parent
// request; a non-empty agentID reports that sub-agent's retained request.
func (s *Session) emitContextReport(ctx context.Context, agentID string) {
	snapshot, ok := s.contextReportSnapshot(agentID)
	if !ok {
		if agentID == "" {
			s.emitContextReportEvent(agentID, "No request recorded yet in this interactive session.")
			return
		}
		s.emitContextReportEvent(agentID, fmt.Sprintf("# No retained request context\n\nNo request context is retained for `%s`. It may not have made a request yet, or the snapshot was evicted or belongs to a resumed session.", agentID))
		return
	}
	report, err := BuildContextReport(ctx, snapshot)
	if err != nil {
		s.emitContextReportEvent(agentID, "Context report unavailable.\n\n"+err.Error())
		return
	}
	s.emitContextReportEvent(agentID, report)
}

// contextReportSnapshot returns the snapshot a context report for agentID reads.
func (s *Session) contextReportSnapshot(agentID string) (RequestContextSnapshot, bool) {
	if agentID == "" {
		return s.snapshots.ParentSnapshot()
	}
	return s.snapshots.AgentSnapshot(agentID)
}

// emitContextReportEvent emits a context report event marked with its agent scope.
func (s *Session) emitContextReportEvent(agentID, content string) {
	s.events.Emit(output.WithContextReportAgent(output.NewOverlayReportEvent("Context Report", content), agentID))
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
	if errors.Is(err, os.ErrNotExist) || strings.TrimSpace(content) == "" {
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
