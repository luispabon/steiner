package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// TestInteractiveForkNextRequestCarriesForkIDAndInheritedState proves that after
// an interactive fork, the next model request names the fork's session ID as its
// transport session and carries the backend state of the forked active
// generation, which the parent's last turn produced.
func TestInteractiveForkNextRequestCarriesForkIDAndInheritedState(t *testing.T) {
	const parentState = `{"session_id":"parent-cli"}`
	prov := &fakeProvider{
		responses: []provider.ChatResponse{
			{Message: provider.Message{Content: "first answer"}, FinishReason: "stop", BackendState: []byte(parentState)},
			{Message: provider.Message{Content: "second answer"}, FinishReason: "stop"},
		},
	}
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
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
	parentID := sess.SessionID()

	submit(t, sess, "first question")
	waitRuns(t, sess)
	if err := sess.Handle(context.Background(), interactive.ForkSession{}); err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	forkID := sess.SessionID()
	if forkID == parentID {
		t.Fatal("fork kept the parent session ID")
	}

	submit(t, sess, "second question")
	waitRuns(t, sess)
	if len(prov.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(prov.requests))
	}
	if got := prov.requests[0].TransportSession; got != parentID {
		t.Errorf("parent request TransportSession = %q, want %q", got, parentID)
	}
	next := prov.requests[1]
	if next.TransportSession != forkID {
		t.Errorf("fork request TransportSession = %q, want fork ID %q", next.TransportSession, forkID)
	}
	if got := string(next.BackendState); got != parentState {
		t.Errorf("fork request BackendState = %s, want the inherited %s", got, parentState)
	}
}
