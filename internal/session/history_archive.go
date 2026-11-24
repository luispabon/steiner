package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// recallPresence returns the renderer's file check for session id: a recall
// file counts only when Lstat finds a regular file that is not a symlink.
func (a *HistoryArchive) recallPresence(id string) recallPresence {
	return func(name string) bool {
		path, err := a.store.RecallPath(id, name)
		if err != nil {
			return false
		}
		info, err := os.Lstat(path)
		if err != nil {
			return false
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return false
		}
		return info.Mode().IsRegular()
	}
}

// Note returns the summary note that names the session's archive file, or ""
// when source renders nothing. A source that holds result bodies also gets the
// recall directory: it may hold the bodies those lines point at, though the note
// does not promise every body is there. Note writes nothing.
func (a *HistoryArchive) Note(source []agent.Message) (string, error) {
	id := a.sessionID()
	if id == "" {
		return "", errNoSessionID
	}
	// Note runs before the compaction writes this source's recall files, so
	// render with every recall file present. Otherwise a source of only result
	// bodies would look blank and lose its note.
	if blankHistory(renderHistory(source, recallAllPresent)) {
		return "", nil
	}
	path, err := a.store.HistoryPath(id)
	if err != nil {
		return "", fmt.Errorf("history archive: %w", err)
	}
	note := agent.HistoryNoteMarker + " Conversation before this summary is archived at " + path + " (markdown, oldest first, tool results omitted). If a detail you need is missing from this summary, grep that file and read the matching lines."
	if !historyHasResultBodies(source) {
		return note, nil
	}
	recallDir, err := a.store.RecallDir(id)
	if err != nil {
		return "", fmt.Errorf("history archive: %w", err)
	}
	return note + " Result bodies may be stored under " + recallDir + "; an archived line that names a recall file points at the file that holds that body.", nil
}

// Append renders source and appends it to the session's archive file. Source
// that renders nothing leaves the file untouched.
func (a *HistoryArchive) Append(_ context.Context, source []agent.Message) error {
	id := a.sessionID()
	if id == "" {
		return errNoSessionID
	}
	rendered := renderHistory(source, a.recallPresence(id))
	if blankHistory(rendered) {
		return nil
	}
	_, err := a.store.AppendHistory(id, historyHeader, rendered+"\n\n")
	return err
}
