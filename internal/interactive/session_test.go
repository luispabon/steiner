package interactive

import (
	"context"
	"testing"
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
	deps := Dependencies{
		DisplaySink: nil,
	}
	s := NewSession(deps)
	if s == nil {
		t.Fatal("NewSession returned nil")
	}
	if s.sink != nil {
		t.Errorf("expected nil sink, got %v", s.sink)
	}
}

func TestSessionHandleNoop(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		DisplaySink: nil,
	}
	s := NewSession(deps)
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
	deps := Dependencies{
		DisplaySink: nil,
	}
	s := NewSession(deps)
	ctx := context.Background()

	if err := s.Run(ctx); err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
