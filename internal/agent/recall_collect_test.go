package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

// fakeRecallArchive records every Write call. failAt maps a 1-based write
// number to the error that write returns; the entry is still recorded.
type fakeRecallArchive struct {
	written []RecallEntry
	failAt  map[int]error
}

func (a *fakeRecallArchive) Write(_ context.Context, entry RecallEntry) (string, error) {
	a.written = append(a.written, entry)
	if err := a.failAt[len(a.written)]; err != nil {
		return "", err
	}
	return "/recall/" + RecallName(entry), nil
}

func (a *fakeRecallArchive) Path(name string) string {
	return "/recall/" + name
}

// recallTestCompletion returns a completion whose envelope body is distinct.
func recallTestCompletion(agentType, callID, body string) SubAgentCompletion {
	return SubAgentCompletion{
		AgentID:          agentType + "-1",
		AgentType:        agentType,
		Status:           "completed",
		ParentCallID:     callID,
		ObjectivePreview: "objective for " + callID,
		TurnCount:        2,
		TokenCount:       30,
		Duration:         2 * time.Second,
		Body:             body,
	}
}

// recallTestEnvelope renders a completion and returns the envelope with the
// inner text it parsed to.
func recallTestEnvelope(t *testing.T, c SubAgentCompletion) (raw, inner string) {
	t.Helper()
	raw = RenderSubAgentResultEnvelope(c)
	parsed, ok := ParseSubAgentResultEnvelope(raw)
	if !ok {
		t.Fatalf("ParseSubAgentResultEnvelope(%q) failed", raw)
	}
	return raw, parsed.Inner
}

// assertRecallEntries compares entries field by field.
func assertRecallEntries(t *testing.T, got, want []RecallEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestCollectRecallEntriesToolMessages proves tool messages become tool
// entries with their exact body, repeated call ids yield one entry per body,
// and non-result messages produce nothing.
func TestCollectRecallEntriesToolMessages(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: MessageRoleUser, Content: "do the thing"},
		{Role: MessageRoleAssistant, Content: "working", ToolCalls: nil},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "first body"},
		{Role: MessageRoleAssistant, Content: "more"},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "second body"},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-2", Content: "file body"},
	}
	want := []RecallEntry{
		{Kind: "tool", Tool: "bash", CallID: "call-1", Body: "first body"},
		{Kind: "tool", Tool: "bash", CallID: "call-1", Body: "second body"},
		{Kind: "tool", Tool: "read", CallID: "call-2", Body: "file body"},
	}
	assertRecallEntries(t, collectRecallEntries(msgs), want)
}

// TestCollectRecallEntriesSubAgentEnvelopes proves each envelope in a user
// message is archived with the whole parsed inner text, mixes with user text,
// and is found in messages carrying the sub-agent result source too.
func TestCollectRecallEntriesSubAgentEnvelopes(t *testing.T) {
	t.Parallel()
	codeEnvelope, codeInner := recallTestEnvelope(t, recallTestCompletion("code", "call-agent-1", `{"status":"ok"}`))
	exploreEnvelope, exploreInner := recallTestEnvelope(t, recallTestCompletion("explore", "call-agent-2", "found nothing"))
	nestedEnvelope, nestedInner := recallTestEnvelope(t, recallTestCompletion("code", "call-agent-3", "[steiner:recall] tool-call-1-abc"))
	if !strings.HasPrefix(codeInner, "This is a sub-agent result, not user input") {
		t.Fatalf("inner = %q, want the rendered preamble", codeInner)
	}

	msgs := []Message{
		{Role: MessageRoleUser, Content: codeEnvelope + "\n\n" + exploreEnvelope + "\n\nand what now?"},
		{Role: MessageRoleAssistant, Content: "reading results"},
		{Role: MessageRoleUser, Source: MessageSourceSubAgentResult, Content: nestedEnvelope},
	}
	want := []RecallEntry{
		{Kind: "agent", Tool: "code", CallID: "call-agent-1", Body: codeInner},
		{Kind: "agent", Tool: "explore", CallID: "call-agent-2", Body: exploreInner},
		{Kind: "agent", Tool: "code", CallID: "call-agent-3", Body: nestedInner},
	}
	assertRecallEntries(t, collectRecallEntries(msgs), want)
	if want[0].Body == `{"status":"ok"}` {
		t.Fatalf("body = %q, want the whole inner text including the preamble", want[0].Body)
	}
}

// TestCollectRecallEntriesIgnoresMalformedEnvelope proves a result-shaped but
// truncated envelope is not archived as an agent entry, and marker-shaped
// tool bodies are archived verbatim.
func TestCollectRecallEntriesIgnoresMalformedEnvelope(t *testing.T) {
	t.Parallel()
	marker := "[steiner:recall] agent-call-1-" + strings.Repeat("a", 64)
	truncated := `<steiner-sub-agent-result agent_id="code-1" type="code" status="completed" call_id="call-1" bytes="9999">
body`
	msgs := []Message{
		{Role: MessageRoleUser, Content: truncated},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-1", Content: marker},
	}
	want := []RecallEntry{{Kind: "tool", Tool: "read", CallID: "call-1", Body: marker}}
	assertRecallEntries(t, collectRecallEntries(msgs), want)
}

// TestArchiveRecallEntriesWritesEveryBody proves the collector writes one entry
// per tool result and envelope body, in order, without warning.
func TestArchiveRecallEntriesWritesEveryBody(t *testing.T) {
	t.Parallel()
	toolBody := "[steiner:recall] tool-call-1-abc"
	envelope, inner := recallTestEnvelope(t, recallTestCompletion("code", "call-agent-1", "agent body"))
	removed := []Message{
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: toolBody},
		{Role: MessageRoleUser, Content: envelope + "\n\nuser text"},
	}
	archive := &fakeRecallArchive{}
	var rec eventRecorder

	archiveRecallEntries(context.Background(), archive, rec.sink(), removed)

	want := []RecallEntry{
		{Kind: "tool", Tool: "bash", CallID: "call-1", Body: toolBody},
		{Kind: "agent", Tool: "code", CallID: "call-agent-1", Body: inner},
	}
	assertRecallEntries(t, archive.written, want)
	if events := rec.snapshot(); len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
}

// TestArchiveRecallEntriesWarnsAndContinuesOnWriteError proves a failed Write
// emits one compact warning and later entries are still written.
func TestArchiveRecallEntriesWarnsAndContinuesOnWriteError(t *testing.T) {
	t.Parallel()
	removed := []Message{
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "first"},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-2", Content: "second"},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-3", Content: "third"},
	}
	archive := &fakeRecallArchive{failAt: map[int]error{2: errors.New("disk full")}}
	var rec eventRecorder

	archiveRecallEntries(context.Background(), archive, rec.sink(), removed)

	if len(archive.written) != 3 {
		t.Fatalf("Write calls = %d, want 3", len(archive.written))
	}
	if got := archive.written[2].Body; got != "third" {
		t.Errorf("last written body = %q, want %q", got, "third")
	}
	assertSingleCompactWarning(t, &rec, "archive recall body: disk full")
}

// TestArchiveRecallEntriesNilArchiveDoesNothing proves a nil archive writes
// nothing and emits nothing.
func TestArchiveRecallEntriesNilArchiveDoesNothing(t *testing.T) {
	t.Parallel()
	var rec eventRecorder
	removed := []Message{{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: "body"}}

	archiveRecallEntries(context.Background(), nil, rec.sink(), removed)

	if events := rec.snapshot(); len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
	if got := collectRecallEntries(removed); len(got) != 1 {
		t.Errorf("entries = %+v, want one so nil archiving is the only reason nothing was written", got)
	}
}

// TestRecallArchiveWarningIsCompactWarning pins the warning surface the
// adoption path surfaces to the user.
func TestRecallArchiveWarningIsCompactWarning(t *testing.T) {
	t.Parallel()
	event := recallArchiveWarning(errors.New("boom"))
	payload, ok := event.Payload.(output.ConversationWarningEvent)
	if !ok {
		t.Fatalf("payload = %T, want output.ConversationWarningEvent", event.Payload)
	}
	if event.Type != output.EventTypeConversationWarning || payload.Kind != output.ConversationWarningKindCompact {
		t.Errorf("event type = %q kind = %q, want a compact conversation warning", event.Type, payload.Kind)
	}
	if payload.Message != "archive recall body: boom" {
		t.Errorf("message = %q, want %q", payload.Message, "archive recall body: boom")
	}
}
