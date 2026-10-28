package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// historyHeader starts a session's history archive when the file is created.
const historyHeader = "# Conversation history\n\nArchived by compaction, oldest first.\n\n"

var errNoSessionID = errors.New("history archive: no session id")

// HistoryArchive writes compacted messages to the history file of the session
// that sessionID reports. sessionID is called on every method call, so the
// archive follows the session the caller is running.
type HistoryArchive struct {
	store     *Store
	sessionID func() string
}

var _ agent.HistoryArchive = (*HistoryArchive)(nil)

// NewHistoryArchive returns a HistoryArchive that writes to store under the
// session ID returned by sessionID.
func NewHistoryArchive(store *Store, sessionID func() string) *HistoryArchive {
	return &HistoryArchive{store: store, sessionID: sessionID}
}

// blankHistory reports whether rendered history has no text, so the archive
// neither notes nor appends it.
func blankHistory(rendered string) bool {
	return strings.TrimSpace(rendered) == ""
}

// Note returns the summary note that names the session's archive file, or ""
// when source renders nothing. It writes nothing.
func (a *HistoryArchive) Note(source []agent.Message) (string, error) {
	id := a.sessionID()
	if id == "" {
		return "", errNoSessionID
	}
	if blankHistory(renderHistory(source)) {
		return "", nil
	}
	path, err := a.store.HistoryPath(id)
	if err != nil {
		return "", fmt.Errorf("history archive: %w", err)
	}
	return agent.HistoryNoteMarker + " Conversation before this summary is archived at " + path + " (markdown, oldest first, tool results omitted). If a detail you need is missing from this summary, grep that file and read the matching lines.", nil
}

// Append renders source and appends it to the session's archive file. Source
// that renders nothing leaves the file untouched.
func (a *HistoryArchive) Append(_ context.Context, source []agent.Message) error {
	id := a.sessionID()
	if id == "" {
		return errNoSessionID
	}
	rendered := renderHistory(source)
	if blankHistory(rendered) {
		return nil
	}
	_, err := a.store.AppendHistory(id, historyHeader, rendered+"\n\n")
	return err
}
