package repl

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func TestSessionHandlesBuiltinsAndSkills(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{
		runFn: func(ctx context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
			return RunResult{
				Conversation: append([]agent.Message(nil), conversation...),
				Reply:        "done",
			}, nil
		},
	}

	session := &Session{
		Runner:     runner,
		Out:        output.NewStream(&out),
		ToolNames:  []string{"read", "bash"},
		SkillNames: []string{"codex", "review"},
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "previous"},
		},
	}

	done, err := session.HandleLine(context.Background(), "/help")
	if err != nil {
		t.Fatalf("help error = %v", err)
	}
	if done {
		t.Fatal("help returned done=true")
	}
	if got := out.String(); !strings.Contains(got, "/help") || !strings.Contains(got, "/exit") {
		t.Fatalf("help output = %q, want builtin commands", got)
	}
	out.Reset()

	done, err = session.HandleLine(context.Background(), "/tools")
	if err != nil {
		t.Fatalf("tools error = %v", err)
	}
	if done {
		t.Fatal("tools returned done=true")
	}
	if got := out.String(); !strings.Contains(got, "read") || !strings.Contains(got, "bash") {
		t.Fatalf("tools output = %q, want tool names", got)
	}
	out.Reset()

	done, err = session.HandleLine(context.Background(), "/skills")
	if err != nil {
		t.Fatalf("skills error = %v", err)
	}
	if done {
		t.Fatal("skills returned done=true")
	}
	if got := out.String(); !strings.Contains(got, "codex") || !strings.Contains(got, "review") {
		t.Fatalf("skills output = %q, want skill names", got)
	}
	out.Reset()

	done, err = session.HandleLine(context.Background(), "/codex")
	if err != nil {
		t.Fatalf("skill toggle error = %v", err)
	}
	if done {
		t.Fatal("skill toggle returned done=true")
	}
	if !containsString(session.ActiveSkills, "codex") {
		t.Fatalf("active skills = %#v, want codex enabled", session.ActiveSkills)
	}
	if got := out.String(); !strings.Contains(got, "skill enabled: codex") {
		t.Fatalf("skill enable output = %q, want enable notice", got)
	}
	out.Reset()

	done, err = session.HandleLine(context.Background(), "fix the bug")
	if err != nil {
		t.Fatalf("prompt error = %v", err)
	}
	if done {
		t.Fatal("prompt returned done=true")
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if got := out.String(); !strings.Contains(got, "done") {
		t.Fatalf("prompt output = %q, want reply", got)
	}
	if len(session.Conversation) == 0 {
		t.Fatal("conversation was not updated")
	}

	done, err = session.HandleLine(context.Background(), "/clear")
	if err != nil {
		t.Fatalf("clear error = %v", err)
	}
	if done {
		t.Fatal("clear returned done=true")
	}
	if len(session.Conversation) != 0 {
		t.Fatalf("conversation len = %d, want 0", len(session.Conversation))
	}
}

func TestSessionExitCommand(t *testing.T) {
	session := &Session{}
	done, err := session.HandleLine(context.Background(), "/exit")
	if err != nil {
		t.Fatalf("exit error = %v", err)
	}
	if !done {
		t.Fatal("exit command did not request shutdown")
	}
}

func TestCompleterSuggestsCommandsAndSkills(t *testing.T) {
	completer := Completer{
		Commands: []string{"help", "tools", "skills"},
		Skills:   []string{"codex", "review"},
	}

	got := completer.Complete("/")
	if !containsAll(got, []string{"/help", "/skills", "/review"}) {
		t.Fatalf("Complete(/) = %#v, want command and skill candidates", got)
	}
}

type fakeRunner struct {
	calls int
	runFn func(context.Context, []agent.Message, []string) (RunResult, error)
}

func (r *fakeRunner) Run(ctx context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
	r.calls++
	return r.runFn(ctx, conversation, skillNames)
}

func containsAll(values, want []string) bool {
	for _, item := range want {
		if !containsString(values, item) {
			return false
		}
	}
	return true
}
