package interactive

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
)

func (s *Session) manualCompaction(ctx context.Context) {
	conversation := s.Conversation()
	if len(conversation) == 0 {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "No conversation to compact."))
		return
	}
	if !manualCompactionHasSource(conversation) {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "Nothing to compact yet; need at least two conversation turns."))
		return
	}

	newConv, err := s.runManualCompaction(ctx, s.CurrentModelAlias(), s.compactRunner(conversation))
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.emitCompactError(err)
		}
		return
	}

	s.setCompactedConversation(newConv)
	if err := s.saveSession(); err != nil {
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind: "session_health", Severity: "warning", Notes: []string{fmt.Sprintf("save session: %v", err)},
		}))
	}
}
