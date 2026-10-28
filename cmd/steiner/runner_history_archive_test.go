package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// recordingHistoryArchive is a HistoryArchive with a comparable identity, so
// request tests can check that the same archive reaches the request.
type recordingHistoryArchive struct{ id string }

func (recordingHistoryArchive) Note([]agent.Message) (string, error) { return "", nil }

func (recordingHistoryArchive) Append(context.Context, []agent.Message) error { return nil }

func TestBuildRunRequestCarriesHistoryArchive(t *testing.T) {
	archive := recordingHistoryArchive{id: "interactive"}
	runner := cliRunner{historyArchive: archive}

	request := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if request.HistoryArchive != archive {
		t.Fatalf("request.HistoryArchive = %#v, want %#v", request.HistoryArchive, archive)
	}
}

// TestBuildRunRequestLeavesHistoryArchiveNilForExec covers the exec runner,
// which never sets historyArchive, so its requests do not archive.
func TestBuildRunRequestLeavesHistoryArchiveNilForExec(t *testing.T) {
	request := buildRunRequest(cliRunner{runMode: "exec"}, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if request.HistoryArchive != nil {
		t.Fatalf("exec request.HistoryArchive = %#v, want nil", request.HistoryArchive)
	}
}

// TestWireInteractiveRunnerArchiveFollowsSessionID proves the interactive
// archive names the current session's history file, including after a fork
// changes the session ID.
func TestWireInteractiveRunnerArchiveFollowsSessionID(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "first answer"}, FinishReason: "stop"},
	}}
	rt := cliRuntime{
		cfg:          testRuntimeConfig("test-model"),
		provider:     prov,
		registry:     tool.NewRegistry(),
		workDir:      t.TempDir(),
		homeDir:      t.TempDir(),
		events:       output.NoopSink{},
		sessionStore: store,
	}
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		t.Fatalf("buildInteractiveSession() error = %v", err)
	}
	wireInteractiveRunner(rt, sess)

	archive := wiredSessionRunner(t, sess).runner.historyArchive
	if archive == nil {
		t.Fatal("interactive runner historyArchive = nil, want session archive")
	}
	source := []agent.Message{{Role: agent.MessageRoleUser, Content: "archived request"}}
	parentID := sess.SessionID()
	assertArchiveNoteNamesSession(t, archive, source, store, parentID)

	submit(t, sess, "first question")
	waitRuns(t, sess)
	if err := sess.Handle(context.Background(), interactive.ForkSession{}); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkID := sess.SessionID()
	if forkID == parentID {
		t.Fatal("fork kept the parent session ID")
	}
	assertArchiveNoteNamesSession(t, archive, source, store, forkID)
}

// assertArchiveNoteNamesSession checks that the archive's note for source names
// the history file of wantID.
func assertArchiveNoteNamesSession(t *testing.T, archive agent.HistoryArchive, source []agent.Message, store *session.Store, wantID string) {
	t.Helper()
	note, err := archive.Note(source)
	if err != nil {
		t.Fatalf("Note() error = %v", err)
	}
	wantPath, err := store.HistoryPath(wantID)
	if err != nil {
		t.Fatalf("HistoryPath(%q) error = %v", wantID, err)
	}
	if !strings.Contains(note, wantPath) {
		t.Fatalf("Note() = %q, want it to name %q", note, wantPath)
	}
}

// TestWireInteractiveRunnerWithoutSessionStoreLeavesArchiveNil keeps the
// archive nil when the runtime has no session store, instead of wrapping a nil
// store in a non-nil interface.
func TestWireInteractiveRunnerWithoutSessionStoreLeavesArchiveNil(t *testing.T) {
	rt := cliRuntime{events: output.NoopSink{}, workDir: t.TempDir(), homeDir: t.TempDir()}
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		t.Fatalf("buildInteractiveSession() error = %v", err)
	}
	wireInteractiveRunner(rt, sess)

	if got := wiredSessionRunner(t, sess).runner.historyArchive; got != nil {
		t.Fatalf("historyArchive = %#v, want nil without a session store", got)
	}
}

// TestCLIRunnerManualCompactionSummaryNotesArchive proves a manual compaction
// puts the archive note in the summary message and appends the removed turn to
// the session's history file.
func TestCLIRunnerManualCompactionSummaryNotesArchive(t *testing.T) {
	r, prov, _ := newCompactTestRunner(t)
	prov.responses = []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"},
		FinishReason: "stop",
	}}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	const sessionID = "manual-compact"
	r.historyArchive = session.NewHistoryArchive(store, func() string { return sessionID })

	compacted, _, _, err := r.CompactWithLineage(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}, agent.ConversationLineage{}, nil, nil, "")
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}

	var summary string
	for _, msg := range compacted {
		if msg.Role == agent.MessageRoleSummary {
			summary = msg.Content
		}
	}
	path, err := store.HistoryPath(sessionID)
	if err != nil {
		t.Fatalf("HistoryPath() error = %v", err)
	}
	if !strings.Contains(summary, agent.HistoryNoteMarker) || !strings.Contains(summary, path) {
		t.Fatalf("summary = %q, want a history note naming %q", summary, path)
	}
	archived, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(history) error = %v", err)
	}
	if !strings.Contains(string(archived), "first request") {
		t.Fatalf("history file = %q, want the compacted first turn", archived)
	}
}
