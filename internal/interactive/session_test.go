package interactive

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// compile-time interface checks.
var (
	_ Action = SubmitPrompt{}
	_ Action = RequestContextReport{}
	_ Action = RequestConfigReport{}
	_ Action = SubmitApproval{}
	_ Action = InterruptActiveRun{}
	_ Action = RequestExit{}
	_ Action = SetSkillEnabled{}
	_ Action = SwitchModel{}
	_ Action = ClearConversation{}
	_ Action = TriggerManualCompaction{}
)

func TestNewSession(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{})
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.events == nil {
		t.Error("expected non-nil event sink")
	}
	if s.displaySink == nil {
		t.Error("expected non-nil display sink")
	}
	if s.runController == nil {
		t.Error("expected non-nil run controller")
	}
	if s.skills == nil {
		t.Error("expected non-nil skills")
	}
	if s.snapshots == nil {
		t.Error("expected non-nil snapshot store")
	}
	if s.approvalCoordinator == nil {
		t.Error("expected non-nil approval coordinator")
	}
}

func TestNewSessionWithSkillNames(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{
		SkillNames: []string{"go-code-audit", "slop-detector"},
	})
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	names := s.Skills().Snapshot()
	if got, want := len(names), 2; got != want {
		t.Fatalf("skill count = %d, want %d", got, want)
	}
}

func TestActiveRunControllerInterrupt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := &ActiveRunController{}
	ctrl.Set(cancel)

	ctrl.Interrupt()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected interrupt to cancel the context")
	}
}

func TestActiveRunControllerClear(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := &ActiveRunController{}
	ctrl.Set(cancel)
	ctrl.Clear()

	if ctrl.HasCancel() {
		t.Fatal("expected HasCancel to be false after Clear")
	}

	ctrl.Interrupt()
	select {
	case <-ctx.Done():
		t.Fatal("expected Interrupt to be a no-op after Clear")
	default:
	}
}

func TestSkillsSnapshot(t *testing.T) {
	t.Parallel()
	skills := NewSkills([]string{"a", "b", "c"})
	if got, want := len(skills.Snapshot()), 3; got != want {
		t.Fatalf("initial snapshot length = %d, want %d", got, want)
	}
	skills.Set("b", false)
	skills.Set("d", true)
	got := skills.Snapshot()
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot = %v, want %v", got, want)
		}
	}
}

func TestSkillsSetNilSafe(t *testing.T) {
	t.Parallel()
	var s *Skills
	s.Set("anything", true)
}

func TestSnapshotStoreStoreAndSnapshot(t *testing.T) {
	t.Parallel()
	store := &SnapshotStore{}
	_, ok := store.Snapshot()
	if ok {
		t.Fatal("expected ok=false for empty store")
	}

	store.Store(output.RequestContextSnapshot{
		Model: "test-model",
	})
	snapshot, ok := store.Snapshot()
	if !ok {
		t.Fatal("expected ok=true after Store")
	}
	if snapshot.Model != "test-model" {
		t.Fatalf("model = %q, want %q", snapshot.Model, "test-model")
	}
}

func TestApprovalCoordinatorLifecycle(t *testing.T) {
	t.Parallel()
	coord := &ApprovalCoordinator{}
	if coord.HasPending() {
		t.Fatal("expected no pending initially")
	}

	ch := coord.Begin("write", "auto")
	if !coord.HasPending() {
		t.Fatal("expected pending after Begin")
	}

	coord.Submit(SubmitApproval{
		Tool:     "write",
		Mode:     "auto",
		Decision: "allow_once",
	})

	select {
	case sub := <-ch:
		if sub.Decision != "allow_once" {
			t.Fatalf("decision = %q, want %q", sub.Decision, "allow_once")
		}
	default:
		t.Fatal("expected submission on channel")
	}

	coord.Finish(ch)
	if coord.HasPending() {
		t.Fatal("expected no pending after Finish")
	}
}

func TestApprovalCoordinatorMismatch(t *testing.T) {
	t.Parallel()
	coord := &ApprovalCoordinator{}
	ch := coord.Begin("write", "auto")

	coord.Submit(SubmitApproval{
		Tool: "not-write",
		Mode: "auto",
	})
	select {
	case <-ch:
		t.Fatal("expected mismatch to block submission")
	default:
	}

	coord.Submit(SubmitApproval{
		Tool:     "write",
		Mode:     "auto",
		Decision: "deny",
	})
	select {
	case sub := <-ch:
		if sub.Decision != "deny" {
			t.Fatalf("decision = %q, want %q", sub.Decision, "deny")
		}
	default:
		t.Fatal("expected submission on channel")
	}
}

func TestSessionHandleNoop(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{})
	ctx := context.Background()

	tests := []struct {
		name   string
		action Action
	}{
		{"SubmitPrompt", SubmitPrompt{Text: "hello"}},
		{"RequestContextReport", RequestContextReport{}},
		{"RequestConfigReport", RequestConfigReport{}},
		{"SubmitApproval", SubmitApproval{Tool: "write", Mode: "auto", Decision: "allow"}},
		{"InterruptActiveRun", InterruptActiveRun{}},
		{"RequestExit", RequestExit{}},
		{"SetSkillEnabled", SetSkillEnabled{Name: "go-code-audit", Enabled: true}},
		{"SwitchModel", SwitchModel{Name: "gpt-4"}},
		{"ClearConversation", ClearConversation{}},
		{"TriggerManualCompaction", TriggerManualCompaction{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Handle(ctx, tt.action); err != nil {
				t.Errorf("Handle(%T) = %v, want nil", tt.action, err)
			}
		})
	}
}

func TestSessionRunClose(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{})
	ctx := context.Background()

	if err := s.Run(ctx); err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestSessionConversationAccessors(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{})
	if got := s.Conversation(); got != nil {
		t.Fatalf("initial conversation = %v, want nil", got)
	}

	msgs := []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}
	s.SetConversation(msgs)
	if got := len(s.Conversation()); got != 1 {
		t.Fatalf("conversation length = %d, want 1", got)
	}
}

func TestSessionAccessorsNonNil(t *testing.T) {
	t.Parallel()
	s := NewSession(Dependencies{})
	if s.EventSink() == nil {
		t.Error("EventSink() returned nil")
	}
	if s.DisplaySink() == nil {
		t.Error("DisplaySink() returned nil")
	}
	if s.ActiveRunController() == nil {
		t.Error("ActiveRunController() returned nil")
	}
	if s.Skills() == nil {
		t.Error("Skills() returned nil")
	}
	if s.SnapshotStore() == nil {
		t.Error("SnapshotStore() returned nil")
	}
	if s.ApprovalCoordinator() == nil {
		t.Error("ApprovalCoordinator() returned nil")
	}
}
