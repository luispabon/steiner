package repl

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

	done, err = session.HandleLine(context.Background(), "/history")
	if err != nil {
		t.Fatalf("history error = %v", err)
	}
	if done {
		t.Fatal("history returned done=true")
	}
	if got := out.String(); !strings.Contains(got, "history: conversation_messages=") || !strings.Contains(got, "no context diagnostics recorded") {
		t.Fatalf("history output = %q, want empty diagnostics notice", got)
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

func TestSessionHistoryCommandShowsDiagnostics(t *testing.T) {
	var out bytes.Buffer
	session := &Session{
		Out: output.NewStream(&out),
		Conversation: []agent.Message{
			{Role: agent.MessageRoleUser, Content: "previous"},
		},
		Diagnostics: []output.Event{
			output.NewContextCompactionEvent(3, 2, 4, 1, 2, 128, true, "compacted conversation history"),
			output.NewContextBudgetEvent("project_context", 3, 512, 256, true, "trimmed extra files"),
		},
	}

	done, err := session.HandleLine(context.Background(), "/history")
	if err != nil {
		t.Fatalf("history error = %v", err)
	}
	if done {
		t.Fatal("history returned done=true")
	}
	got := out.String()
	for _, want := range []string{
		"history: conversation_messages=1 diagnostics=2",
		"context diagnostics:",
		"kind=compaction",
		"retained_turns=2",
		"retained_messages=4",
		"compacted_turns=1",
		"compacted_messages=2",
		"summary=compacted conversation history",
		"summary_bytes=128",
		"kind=budget",
		"scope=project_context",
		"budget_bytes=256",
		"truncated=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("history output %q missing %q", got, want)
		}
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

func TestSessionEmitsUserInputEvent(t *testing.T) {
	var events []output.Event
	session := &Session{
		Runner: &fakeRunner{
			runFn: func(ctx context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
				return RunResult{Conversation: append([]agent.Message(nil), conversation...)}, nil
			},
		},
		Events: output.SinkFunc(func(event output.Event) { events = append(events, event) }),
	}

	done, err := session.HandleLine(context.Background(), "inspect bug")
	if err != nil {
		t.Fatalf("HandleLine() error = %v", err)
	}
	if done {
		t.Fatal("HandleLine() returned done=true")
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if got, want := events[0].Type, output.EventTypeUserInput; got != want {
		t.Fatalf("event type = %q, want %q", got, want)
	}
}

func TestCompleterSuggestsCommandsAndSkills(t *testing.T) {
	completer := Completer{
		Commands: []string{"help", "history", "tools", "skills"},
		Skills:   []string{"codex", "review"},
	}

	got := completer.Complete("/")
	if !containsAll(got, []string{"/help", "/history", "/skills", "/review"}) {
		t.Fatalf("Complete(/) = %#v, want command and skill candidates", got)
	}
}

func TestSessionRunUsesPromptAbstraction(t *testing.T) {
	var out bytes.Buffer
	prompt := &fakePrompt{
		lines: []string{"/help", "/exit"},
		out:   &out,
	}
	session := &Session{
		Out:        output.NewStream(&out),
		prompt:     prompt,
		ToolNames:  []string{"read"},
		SkillNames: []string{"codex"},
		Completer:  Completer{Commands: BuiltinCommands(), Skills: []string{"codex"}},
	}

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "/help") || !strings.Contains(got, "/exit") {
		t.Fatalf("Run() output = %q, want help text", got)
	}
}

func TestCompletionPrefixOnlyUsesFirstCommandToken(t *testing.T) {
	if got := completionPrefix([]rune("/he"), 3); got != "/he" {
		t.Fatalf("completionPrefix(/he) = %q, want /he", got)
	}
	if got := completionPrefix([]rune("/help extra"), 10); got != "/help" {
		t.Fatalf("completionPrefix(/help extra) = %q, want /help", got)
	}
	if got := completionPrefix([]rune("plain text"), 5); got != "" {
		t.Fatalf("completionPrefix(plain text) = %q, want empty", got)
	}
}

func TestPromptStreamRoutesWritesThroughPrompter(t *testing.T) {
	var out bytes.Buffer
	prompt := &fakePrompt{out: &out}
	stream := NewPromptStream(prompt)

	stream.Println("status line")

	if got := out.String(); !strings.Contains(got, "status line") {
		t.Fatalf("prompt stream output = %q, want routed text", got)
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

type fakePrompt struct {
	lines []string
	out   *bytes.Buffer
}

func (p *fakePrompt) ReadLine(ctx context.Context) (string, error) {
	_ = ctx
	if len(p.lines) == 0 {
		return "", io.EOF
	}
	line := p.lines[0]
	p.lines = p.lines[1:]
	return line, nil
}

func (p *fakePrompt) Printf(format string, args ...any) {
	if p.out == nil {
		return
	}
	_, _ = p.out.WriteString(fmt.Sprintf(format, args...))
}

func (p *fakePrompt) Println(args ...any) {
	if p.out == nil {
		return
	}
	_, _ = p.out.WriteString(fmt.Sprintln(args...))
}
