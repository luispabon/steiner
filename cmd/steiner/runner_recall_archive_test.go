package main

import (
	"context"
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// recordingRecallArchive is a RecallArchive with a comparable identity, so
// request tests can check that the same archive reaches the request.
type recordingRecallArchive struct{ id string }

func (recordingRecallArchive) Write(context.Context, agent.RecallEntry) (string, error) {
	return "", nil
}

func (recordingRecallArchive) Path(string) string { return "" }

func TestBuildRunRequestCarriesRecallArchive(t *testing.T) {
	archive := recordingRecallArchive{id: "interactive"}
	runner := cliRunner{recallArchive: archive}

	request := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if request.RecallArchive != archive {
		t.Fatalf("request.RecallArchive = %#v, want %#v", request.RecallArchive, archive)
	}
}

// TestCompactionRunRequestCarriesRecallArchive covers the manual compaction
// path, which builds its request through compactionRunRequest.
func TestCompactionRunRequestCarriesRecallArchive(t *testing.T) {
	archive := recordingRecallArchive{id: "manual"}
	runner := cliRunner{recallArchive: archive}

	request := runner.compactionRunRequest(runnerSetup{}, nil, agent.LastMainRequest{})
	if request.RecallArchive != archive {
		t.Fatalf("request.RecallArchive = %#v, want %#v", request.RecallArchive, archive)
	}
}

// TestExecRunnerLeavesRecallArchiveNil covers the exec runner, which never sets
// recallArchive, so neither a run nor a manual compaction request archives.
func TestExecRunnerLeavesRecallArchiveNil(t *testing.T) {
	runner := cliRunner{runMode: "exec"}

	request := buildRunRequest(runner, runnerSetup{}, tool.NewRegistry(), nil, runHooks{})
	if request.RecallArchive != nil {
		t.Fatalf("exec request.RecallArchive = %#v, want nil", request.RecallArchive)
	}
	compaction := runner.compactionRunRequest(runnerSetup{}, nil, agent.LastMainRequest{})
	if compaction.RecallArchive != nil {
		t.Fatalf("exec compaction request.RecallArchive = %#v, want nil", compaction.RecallArchive)
	}
}

// TestWireInteractiveRunnerRecallArchiveFollowsSessionID proves the interactive
// archive names the current session's recall directory, including after a fork
// changes the session ID.
func TestWireInteractiveRunnerRecallArchiveFollowsSessionID(t *testing.T) {
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

	archive := wiredSessionRunner(t, sess).runner.recallArchive
	if archive == nil {
		t.Fatal("interactive runner recallArchive = nil, want session archive")
	}
	entry := agent.RecallEntry{Kind: "tool", Tool: "read", CallID: "call-1", Body: "recalled body"}
	parentID := sess.SessionID()
	assertRecallArchiveNamesSession(t, archive, entry, store, parentID)

	submit(t, sess, "first question")
	waitRuns(t, sess)
	if err := sess.Handle(context.Background(), interactive.ForkSession{}); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkID := sess.SessionID()
	if forkID == parentID {
		t.Fatal("fork kept the parent session ID")
	}
	assertRecallArchiveNamesSession(t, archive, entry, store, forkID)
}

// assertRecallArchiveNamesSession checks that the archive writes name under
// wantID's recall directory and that Path() names the same file.
func assertRecallArchiveNamesSession(t *testing.T, archive agent.RecallArchive, entry agent.RecallEntry, store *session.Store, wantID string) {
	t.Helper()
	name := agent.RecallName(entry)
	wantPath, err := store.RecallPath(wantID, name)
	if err != nil {
		t.Fatalf("RecallPath(%q) error = %v", wantID, err)
	}
	if got := archive.Path(name); got != wantPath {
		t.Fatalf("Path() = %q, want %q", got, wantPath)
	}
	got, err := archive.Write(context.Background(), entry)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got != wantPath {
		t.Fatalf("Write() = %q, want %q", got, wantPath)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("recall file %q missing: %v", got, err)
	}
}

// TestWireInteractiveRunnerWithoutSessionStoreLeavesRecallArchiveNil keeps the
// recall archive nil when the runtime has no session store, instead of wrapping
// a nil store in a non-nil interface.
func TestWireInteractiveRunnerWithoutSessionStoreLeavesRecallArchiveNil(t *testing.T) {
	rt := cliRuntime{events: output.NoopSink{}, workDir: t.TempDir(), homeDir: t.TempDir()}
	sess, err := buildInteractiveSession(rt)
	if err != nil {
		t.Fatalf("buildInteractiveSession() error = %v", err)
	}
	wireInteractiveRunner(rt, sess)

	if got := wiredSessionRunner(t, sess).runner.recallArchive; got != nil {
		t.Fatalf("recallArchive = %#v, want nil without a session store", got)
	}
}

// TestPhaseRecallArchiveNamesPhaseSession proves a phase recall archive writes
// to the recall directory of the phase session it is given, not the run ID's.
func TestPhaseRecallArchiveNamesPhaseSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	const runID = "run-1"
	const phaseSessionID = "phase-session-1"
	archive := phaseRecallArchive(&cliRuntime{sessionStore: store}, phaseSessionID)
	if archive == nil {
		t.Fatal("phaseRecallArchive() = nil, want archive")
	}

	entry := agent.RecallEntry{Kind: "tool", Tool: "read", CallID: "call-1", Body: "recalled body"}
	assertRecallArchiveNamesSession(t, archive, entry, store, phaseSessionID)

	runPath, err := store.RecallPath(runID, agent.RecallName(entry))
	if err != nil {
		t.Fatalf("RecallPath(%q) error = %v", runID, err)
	}
	if _, err := os.Stat(runPath); err == nil {
		t.Fatalf("run recall file %q exists, want the archive keyed on the phase session", runPath)
	}
}

// TestPhaseRecallArchiveNilWithoutSessionStore keeps the recall archive nil
// when the runtime has no session store.
func TestPhaseRecallArchiveNilWithoutSessionStore(t *testing.T) {
	if got := phaseRecallArchive(&cliRuntime{}, "phase-session-1"); got != nil {
		t.Fatalf("phaseRecallArchive() = %#v, want nil without a session store", got)
	}
}
