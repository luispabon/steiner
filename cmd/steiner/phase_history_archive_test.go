package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// TestPhaseHistoryArchiveNamesPhaseSession proves a phase archive writes to the
// history file of the phase session it is given. The run's history file is the
// one a run-ID-keyed archive would write, so it must stay absent.
func TestPhaseHistoryArchiveNamesPhaseSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	const runID = "run-1"
	const phaseSessionID = "phase-session-1"
	archive := phaseHistoryArchive(&cliRuntime{sessionStore: store}, phaseSessionID)
	if archive == nil {
		t.Fatal("phaseHistoryArchive() = nil, want archive")
	}

	source := []agent.Message{{Role: agent.MessageRoleUser, Content: "archived request"}}
	assertArchiveNoteNamesSession(t, archive, source, store, phaseSessionID)
	if err := archive.Append(context.Background(), source); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if got := readArchivedHistory(t, store, phaseSessionID); !strings.Contains(got, "archived request") {
		t.Fatalf("phase history = %q, want the appended request", got)
	}

	runPath, err := store.HistoryPath(runID)
	if err != nil {
		t.Fatalf("HistoryPath(%q) error = %v", runID, err)
	}
	if _, err := os.Stat(runPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("run history stat error = %v, want %q absent", err, runPath)
	}
}

// TestPhaseHistoryArchiveNilWithoutSessionStore keeps the archive nil when the
// runtime has no session store, instead of wrapping a nil store in a non-nil
// interface.
func TestPhaseHistoryArchiveNilWithoutSessionStore(t *testing.T) {
	if got := phaseHistoryArchive(&cliRuntime{}, "phase-session-1"); got != nil {
		t.Fatalf("phaseHistoryArchive() = %#v, want nil without a session store", got)
	}
}
