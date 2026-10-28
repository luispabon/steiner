package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// TestRunPhaseCompactionArchivesUnderPhaseSession drives a real phase run whose
// prompt exceeds the context budget, so the first turn compacts. The removed
// messages must land in the phase session's history file, and the summary note
// must name that file. The run ID's history file must not exist.
func TestRunPhaseCompactionArchivesUnderPhaseSession(t *testing.T) {
	const runID = "run-1"
	const phaseSessionID = "phase-session-1"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	cfg := testRuntimeConfig("test-model")
	model := cfg.Models.Definitions["test-model"]
	model.Advanced.Limits.ContextWindow = 8192
	cfg.Models.Definitions["test-model"] = model

	// The first response answers the compaction summary request; the second
	// answers the phase turn that follows it.
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop"},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}, FinishReason: "stop"},
	}}
	workDir := t.TempDir()
	orig := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = orig })
	buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
		return cliRuntime{
			cfg:          cfg,
			provider:     prov,
			registry:     tool.NewRegistry(),
			workDir:      workDir,
			homeDir:      t.TempDir(),
			events:       output.NoopSink{},
			sessionStore: store,
		}, nil
	}
	runner, err := newPhaseRunner(context.Background(), &cobra.Command{}, &cliFlags{}, phaseRunnerParams{
		ProjectRoot: workDir,
		WorkDir:     workDir,
		ModelAlias:  "test-model",
		RunMode:     "oneshot",
		SessionID:   runID,
	})
	if err != nil {
		t.Fatalf("newPhaseRunner() error = %v", err)
	}

	long := strings.Repeat("archived turn text ", 80)
	conversation := make([]agent.Message, 0, 17)
	for i := 1; i <= 16; i++ {
		conversation = append(conversation,
			agent.Message{Role: agent.MessageRoleUser, Content: fmt.Sprintf("request %d", i)},
			agent.Message{Role: agent.MessageRoleAssistant, Content: fmt.Sprintf("answer %d %s", i, long)},
		)
	}
	conversation = append(conversation, agent.Message{Role: agent.MessageRoleUser, Content: "latest request"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := runner.RunPhase(ctx, oneshot.PhaseRunInput{
		Conversation: conversation,
		Session: oneshot.PhaseSession{
			ID:   phaseSessionID,
			Save: func(context.Context, agent.DriverSnapshot) error { return nil },
		},
	}); err != nil {
		t.Fatalf("RunPhase() error = %v", err)
	}

	if got := readArchivedHistory(t, store, phaseSessionID); !strings.Contains(got, "answer 1 ") {
		t.Fatalf("phase history = %q, want the compacted first turn", got)
	}
	phasePath, err := store.HistoryPath(phaseSessionID)
	if err != nil {
		t.Fatalf("HistoryPath(%q) error = %v", phaseSessionID, err)
	}
	noted := false
	for _, req := range prov.requests {
		for _, msg := range req.Messages {
			if strings.Contains(msg.Content, agent.HistoryNoteMarker) && strings.Contains(msg.Content, phasePath) {
				noted = true
			}
		}
	}
	if !noted {
		t.Fatalf("no request carries a history note naming %q", phasePath)
	}

	runPath, err := store.HistoryPath(runID)
	if err != nil {
		t.Fatalf("HistoryPath(%q) error = %v", runID, err)
	}
	if _, err := os.Stat(runPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("run history stat error = %v, want %q absent", err, runPath)
	}
}

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
