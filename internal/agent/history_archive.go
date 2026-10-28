package agent

import (
	"context"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// HistoryNoteMarker prefixes the compaction summary line that points at the
// session's history archive.
const HistoryNoteMarker = "[steiner:history]"

// HistoryArchive appends the messages removed by compaction to a durable
// per-session archive. RunRequest.HistoryArchive carries it; nil disables
// archiving.
type HistoryArchive interface {
	// Note returns the summary note naming where source will be archived, or ""
	// when source renders nothing. It performs no writes.
	Note(source []Message) (string, error)
	// Append durably appends the rendered source to the archive.
	Append(ctx context.Context, source []Message) error
}

// StripHistoryNotes removes every history note line from summary, so the note
// appended for the current compaction is the only one the summary carries.
// The remaining text is trimmed of surrounding whitespace.
func StripHistoryNotes(summary string) string {
	var kept []string
	for _, line := range strings.Split(summary, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), HistoryNoteMarker) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// withHistoryNote strips stale history notes from summaryText and appends the
// note for source. It returns the sources to append once the outcome is
// adopted. A Note error emits a warning and leaves the summary without a note.
func withHistoryNote(req RunRequest, summaryText string, source []Message) (string, [][]Message) {
	summaryText = StripHistoryNotes(summaryText)
	note, err := req.HistoryArchive.Note(source)
	if err != nil {
		emitEvent(req.Events, historyArchiveWarning(err))
		return summaryText, nil
	}
	if note == "" {
		return summaryText, nil
	}
	return summaryText + "\n\n" + note, [][]Message{source}
}

// commitHistoryArchive appends each pending source to the archive, in order. It
// runs only where a compaction outcome is adopted. On the first Append error it
// emits a warning and stops; the adopted state is kept.
func commitHistoryArchive(ctx context.Context, req RunRequest, pending [][]Message) {
	for _, source := range pending {
		if err := req.HistoryArchive.Append(ctx, source); err != nil {
			emitEvent(req.Events, historyArchiveWarning(err))
			return
		}
	}
}

// historyArchiveWarning reports a failed archive operation as a compaction
// warning.
func historyArchiveWarning(err error) output.Event {
	return output.NewConversationWarningEvent(output.ConversationWarningKindCompact, "archive compacted history: "+err.Error())
}
