package interactive

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/skill"
)

// fakeSkillLoader serves canned skill documents and errors by name.
type fakeSkillLoader struct {
	skills map[string]skill.Skill
	errs   map[string]error
}

func (f *fakeSkillLoader) Load(_ context.Context, name string) (skill.Skill, error) {
	if err, ok := f.errs[name]; ok {
		return skill.Skill{}, err
	}
	s, ok := f.skills[name]
	if !ok {
		return skill.Skill{}, fmt.Errorf("skill %q not found", name)
	}
	return s, nil
}

// recordingRunner records every conversation it receives and appends an
// assistant reply, mirroring the real runner's echo-and-append behaviour.
type recordingRunner struct {
	convos [][]agent.Message
}

func (r *recordingRunner) Run(_ context.Context, conversation []agent.Message, _ func() []agent.SteerMessage) (RunResult, error) {
	r.convos = append(r.convos, cloneMessages(conversation))
	result := append(cloneMessages(conversation), agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})
	return RunResult{Conversation: result}, nil
}

func (r *recordingRunner) Compact(_ context.Context, conversation []agent.Message, _ []provider.ToolSpec, _ string) ([]agent.Message, error) {
	return conversation, nil
}

func (r *recordingRunner) lastUserContent(t *testing.T) string {
	t.Helper()
	if len(r.convos) == 0 {
		t.Fatal("runner received no conversation")
	}
	conv := r.convos[len(r.convos)-1]
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Role == agent.MessageRoleUser {
			return conv[i].Content
		}
	}
	t.Fatal("no user message in last conversation")
	return ""
}

func skillTestSession(t *testing.T, runner runExecutor, loader skillLoader, names ...string) *Session {
	t.Helper()
	return testNewSession(t, Dependencies{
		Runner:      runner,
		SkillNames:  names,
		SkillLoader: loader,
		Config:      guardTestConfig(),
	})
}

func activationBlock(t *testing.T, name, body string) string {
	t.Helper()
	block, _ := prompt.RenderSkillActivation(name, body)
	return block
}

func TestSkillActivationInjectedOnFirstSubmitOnly(t *testing.T) {
	t.Parallel()
	loader := &fakeSkillLoader{skills: map[string]skill.Skill{"review": {Name: "review", Content: "REVIEW BODY"}}}
	runner := &recordingRunner{}
	s := skillTestSession(t, runner, loader, "review")

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	s.submitPrompt(context.Background(), "hello", nil)

	want := prompt.PrependSkillBlocks([]string{activationBlock(t, "review", "REVIEW BODY")}, "hello")
	if got := runner.lastUserContent(t); got != want {
		t.Fatalf("first submit content =\n%q\nwant\n%q", got, want)
	}

	s.submitPrompt(context.Background(), "again", nil)
	if got := runner.lastUserContent(t); got != "again" {
		t.Fatalf("second submit content = %q, want no re-injection", got)
	}
}

func TestSkillEnableDisableProducesNoDelta(t *testing.T) {
	t.Parallel()
	loader := &fakeSkillLoader{skills: map[string]skill.Skill{"review": {Name: "review", Content: "REVIEW BODY"}}}
	runner := &recordingRunner{}
	s := skillTestSession(t, runner, loader, "review")

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: false}); err != nil {
		t.Fatalf("disable review: %v", err)
	}

	s.submitPrompt(context.Background(), "hello", nil)
	if got := runner.lastUserContent(t); got != "hello" {
		t.Fatalf("content = %q, want no block for never-effective skill", got)
	}
}

func TestSkillSwitchDeactivatesThenActivates(t *testing.T) {
	t.Parallel()
	loader := &fakeSkillLoader{skills: map[string]skill.Skill{
		"alpha": {Name: "alpha", Content: "ALPHA BODY"},
		"beta":  {Name: "beta", Content: "BETA BODY"},
	}}
	runner := &recordingRunner{}
	s := skillTestSession(t, runner, loader, "alpha", "beta")

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "alpha", Enabled: true}); err != nil {
		t.Fatalf("enable alpha: %v", err)
	}
	s.submitPrompt(context.Background(), "one", nil)
	if got, want := runner.lastUserContent(t), prompt.PrependSkillBlocks([]string{activationBlock(t, "alpha", "ALPHA BODY")}, "one"); got != want {
		t.Fatalf("first content = %q, want %q", got, want)
	}

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "alpha", Enabled: false}); err != nil {
		t.Fatalf("disable alpha: %v", err)
	}
	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "beta", Enabled: true}); err != nil {
		t.Fatalf("enable beta: %v", err)
	}
	s.submitPrompt(context.Background(), "two", nil)

	want := prompt.PrependSkillBlocks([]string{
		prompt.RenderSkillDeactivation("alpha"),
		activationBlock(t, "beta", "BETA BODY"),
	}, "two")
	if got := runner.lastUserContent(t); got != want {
		t.Fatalf("switch content =\n%q\nwant\n%q", got, want)
	}
}

func TestLegacyRestoredSkillInjectedOnNextSubmit(t *testing.T) {
	t.Parallel()
	loader := &fakeSkillLoader{skills: map[string]skill.Skill{"review": {Name: "review", Content: "REVIEW BODY"}}}
	runner := &recordingRunner{}
	s := skillTestSession(t, runner, loader, "review")

	// A session restored with an enabled skill but no skill block in its
	// persisted conversation.
	s.SetConversation([]agent.Message{{Role: agent.MessageRoleUser, Content: "legacy prompt"}})
	s.skills.Set("review", true)

	s.submitPrompt(context.Background(), "next", nil)
	want := prompt.PrependSkillBlocks([]string{activationBlock(t, "review", "REVIEW BODY")}, "next")
	if got := runner.lastUserContent(t); got != want {
		t.Fatalf("content =\n%q\nwant\n%q", got, want)
	}
}

func TestSkillInjectionPrefixStability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "no skills", enabled: false},
		{name: "with skill", enabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			loader := &fakeSkillLoader{skills: map[string]skill.Skill{"review": {Name: "review", Content: "REVIEW BODY"}}}
			runner := &recordingRunner{}
			s := skillTestSession(t, runner, loader, "review")
			if tt.enabled {
				if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
					t.Fatalf("enable review: %v", err)
				}
			}
			s.submitPrompt(context.Background(), "first", nil)
			s.submitPrompt(context.Background(), "second", nil)

			if len(runner.convos) != 2 {
				t.Fatalf("recorded %d conversations, want 2", len(runner.convos))
			}
			first := agent.ToReplaySafeProviderMessages(runner.convos[0])
			second := agent.ToReplaySafeProviderMessages(runner.convos[1])
			if len(second) < len(first) {
				t.Fatalf("second turn has %d provider messages, want at least %d", len(second), len(first))
			}
			if !reflect.DeepEqual(second[:len(first)], first) {
				t.Fatalf("second turn prefix diverged from first turn:\nfirst  %#v\nsecond %#v", first, second[:len(first)])
			}
		})
	}
}

func TestSetSkillEnabledWarnsOnceWhenTruncated(t *testing.T) {
	t.Parallel()
	content := strings.Repeat("x", prompt.SkillContentCapBytes+5)
	loader := &fakeSkillLoader{skills: map[string]skill.Skill{"review": {Name: "review", Content: content}}}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		Runner:      &recordingRunner{},
		SkillNames:  []string{"review"},
		SkillLoader: loader,
		Config:      guardTestConfig(),
		BaseEvents:  output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	})

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: false}); err != nil {
		t.Fatalf("disable review: %v", err)
	}

	truncated := 0
	for _, event := range events {
		if event.Type != output.EventTypeSkillTruncated {
			continue
		}
		truncated++
		payload := mustEventPayload[output.SkillTruncatedEvent](t, event)
		if payload.Name != "review" || payload.SizeBytes != len(content) || payload.CapBytes != prompt.SkillContentCapBytes {
			t.Fatalf("truncated payload = %+v", payload)
		}
	}
	if truncated != 1 {
		t.Fatalf("skill_truncated events = %d, want 1", truncated)
	}
}

func TestSetSkillEnabledLoadErrorWrappedAndStaysEnabled(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	loader := &fakeSkillLoader{errs: map[string]error{"review": boom}}
	s := skillTestSession(t, &recordingRunner{}, loader, "review")

	err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true})
	if err == nil {
		t.Fatal("enable review error = nil, want failure")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped boom", err)
	}
	if got, want := err.Error(), "load skill review: boom"; got != want {
		t.Fatalf("error message = %q, want %q", got, want)
	}
	if got := s.Skills(); len(got) != 1 || got[0] != "review" {
		t.Fatalf("skills after failed enable = %v, want [review] so submit retries", got)
	}
}

func TestSkillInjectionNilLoaderIsSafe(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{}
	s := skillTestSession(t, runner, nil, "review")

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	s.submitPrompt(context.Background(), "hello", nil)
	if got := runner.lastUserContent(t); got != "hello" {
		t.Fatalf("content = %q, want no injection with nil loader", got)
	}
}
