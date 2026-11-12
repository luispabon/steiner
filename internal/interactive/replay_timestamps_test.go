package interactive

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// replayLoadedEvents replays a transcript through the full resume path,
// including the stranded-result warning, and returns every emitted event.
func replayLoadedEvents(t *testing.T, msgs []agent.Message) []output.Event {
	t.Helper()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})
	s.replayLoadedTranscript(msgs, nil)
	return events
}

func assertEventTimestamp(t *testing.T, events []output.Event, eventType string, want time.Time) {
	t.Helper()
	got := eventsOfType(events, eventType)
	if len(got) != 1 {
		t.Fatalf("%s events = %d, want 1", eventType, len(got))
	}
	if !got[0].Timestamp.Equal(want) {
		t.Fatalf("%s Timestamp = %v, want %v", eventType, got[0].Timestamp, want)
	}
}

// replayedMessageSet is one transcript whose messages carry distinct
// timestamps, so every replayed event can be traced to its source message.
type replayedMessageSet struct {
	msgs          []agent.Message
	userTime      time.Time
	assistantTime time.Time
	toolTime      time.Time
	deliveryTime  time.Time
}

func newReplayedMessageSet(t *testing.T, userTime time.Time) replayedMessageSet {
	t.Helper()
	set := replayedMessageSet{
		userTime:      userTime,
		assistantTime: userTime.Add(time.Minute),
		toolTime:      userTime.Add(2 * time.Minute),
		deliveryTime:  userTime.Add(3 * time.Minute),
	}
	delivery, ok := agent.BuildDeliveryMessage(agent.DeliveryParts{Completions: []agent.SubAgentCompletion{
		{Seq: 1, ParentCallID: "call-1", AgentID: "a", AgentType: "explore", Status: "complete", Duration: 2 * time.Second, Body: `{"output":"x"}`},
	}})
	if !ok {
		t.Fatal("BuildDeliveryMessage returned !ok")
	}
	set.msgs = []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Timestamp: set.userTime},
		{Role: agent.MessageRoleAssistant, Content: "reading", Timestamp: set.assistantTime, ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "read", Arguments: map[string]any{"file_path": "x"}},
		}},
		{Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "read", Content: `{"ok":true}`, Timestamp: set.toolTime},
		delivery,
	}
	set.msgs[len(set.msgs)-1].Timestamp = set.deliveryTime
	return set
}

func (set replayedMessageSet) assertTimestamps(t *testing.T, events []output.Event) {
	t.Helper()
	assertEventTimestamp(t, events, output.EventTypeUserInput, set.userTime)
	assertEventTimestamp(t, events, output.EventTypeAssistantMessage, set.assistantTime)
	assertEventTimestamp(t, events, output.EventTypeToolCallStarted, set.assistantTime)
	assertEventTimestamp(t, events, output.EventTypeToolCallFinished, set.toolTime)
	assertEventTimestamp(t, events, output.EventTypeSubAgentsDelivered, set.deliveryTime)
}

func TestReplayStampsEventsWithMessageTimestamps(t *testing.T) {
	t.Parallel()
	set := newReplayedMessageSet(t, time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC))
	set.assertTimestamps(t, replayEvents(t, set.msgs))
}

// TestReplayDelegationAcceptedUsesResultMessageTime covers the three replay
// paths that emit delegation accepted: an explicit admission, a ledger entry
// for an outstanding sub-agent, and a legacy result that shows a child ran.
func TestReplayDelegationAcceptedUsesResultMessageTime(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name   string
		result func(t *testing.T) agent.Message
		ledger []agent.SubAgentLedgerEntry
	}{
		{
			name:   "admission",
			result: func(_ *testing.T) agent.Message { return admissionResult("call-1", "agent-a", "batch", "group") },
		},
		{
			name:   "outstanding ledger entry",
			result: func(t *testing.T) agent.Message { return ackResult(t, "call-1", "sub_agent", "agent-a", "queued") },
			ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "call-1", AgentID: "agent-a"}},
		},
		{
			name: "legacy admitted child",
			result: func(t *testing.T) agent.Message {
				result := ackResult(t, "call-1", "sub_agent", "agent-a", "complete")
				result.DelegationAdmission = nil
				return result
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.result(t)
			result.Timestamp = historic
			msgs := []agent.Message{delegateCall("call-1", "sub_agent", "find bug"), result}
			assertEventTimestamp(t, replayEventsWithLedger(t, msgs, tt.ledger), output.EventTypeDelegationAccepted, historic)
		})
	}
}

func TestReplayKeepsZeroTimestampsForLegacyMessages(t *testing.T) {
	t.Parallel()
	set := newReplayedMessageSet(t, time.Time{})
	set.assertTimestamps(t, replayEvents(t, set.msgs))
}

func TestReplayUnansweredResultsUsesCarryingMessageTime(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name string
		time time.Time
	}{
		{name: "historic timestamp", time: historic},
		{name: "legacy zero timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msgs := []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "ok", Timestamp: tt.time},
				{Role: agent.MessageRoleUser, Content: resultEnvelope(t, "call-1", "complete", "x", 1), Timestamp: tt.time},
			}
			events := replayLoadedEvents(t, msgs)
			assertEventTimestamp(t, events, output.EventTypeSubAgentResultsUnanswered, tt.time)
		})
	}
}

// TestResumedSessionKeepsOriginalEventTimestamps pins the bug: two resumes of
// the same session must show the times the messages were first recorded, not
// the time of the latest resume.
func TestResumedSessionKeepsOriginalEventTimestamps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	set := newReplayedMessageSet(t, time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC))
	saved := session.Session{
		ID:        "resumed",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Title:     "Resumed",
		Model:     "test-model",
		Lineage:   lineageFromMessages(set.msgs),
	}
	if err := store.Save(saved); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	first, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	set.assertTimestamps(t, replayLoadedEvents(t, first.Lineage.FullMessages()))

	if err := store.Save(first); err != nil {
		t.Fatalf("re-Save failed: %v", err)
	}
	second, err := store.Load(saved.ID)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	set.assertTimestamps(t, replayLoadedEvents(t, second.Lineage.FullMessages()))
}

func TestReplaySyntheticDelegationEventsHaveNoTimestamp(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name       string
		msgs       []agent.Message
		ledger     []agent.SubAgentLedgerEntry
		zeroTypes  []string
		stampedTyp string
	}{
		{
			name: "ledger orphan lifecycle",
			msgs: []agent.Message{{
				Role:      agent.MessageRoleAssistant,
				Timestamp: historic,
				ToolCalls: []agent.ToolCall{{ID: "orphan", Name: "sub_agent", Arguments: map[string]any{"task": "lost task"}}},
			}},
			ledger:     []agent.SubAgentLedgerEntry{{ParentCallID: "orphan", AgentID: "agent-lost", AgentType: "explore", BatchID: "batch", Group: "group"}},
			zeroTypes:  []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationStarted},
			stampedTyp: output.EventTypeToolCallStarted,
		},
		{
			name: "unresolved ack has no source message",
			msgs: []agent.Message{
				delegateCall("call-1", "sub_agent", "find bug"),
				ackResult(t, "call-1", "sub_agent", "agent-a", "queued"),
			},
			zeroTypes: []string{output.EventTypeDelegationFailed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			s := testNewSession(t, Dependencies{BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) })})
			s.replaySessionMessages(tt.msgs, tt.ledger)
			for _, typ := range tt.zeroTypes {
				assertEventTimestamp(t, events, typ, time.Time{})
			}
			if tt.stampedTyp != "" {
				assertEventTimestamp(t, events, tt.stampedTyp, historic)
			}
		})
	}
}

func TestLiveUnansweredResultsCarryMessageTime(t *testing.T) {
	t.Parallel()
	historic := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	lineage := agent.ConversationLineage{}
	lineage = lineage.WithAppendedMessages([]agent.Message{
		{Role: agent.MessageRoleAssistant, Content: "ok", Timestamp: historic.Add(-time.Minute)},
		{Role: agent.MessageRoleUser, Content: resultEnvelope(t, "call-1", "complete", "x", 1), Timestamp: historic},
	})
	msgs := lineage.FullMessages()

	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})
	s.emitUnansweredResults(msgs, "boom")

	got := eventsOfType(events, output.EventTypeSubAgentResultsUnanswered)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if !got[0].Timestamp.Equal(historic) {
		t.Fatalf("event Timestamp = %v, want historic %v", got[0].Timestamp, historic)
	}
}
