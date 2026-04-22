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
	if got := out.String(); !strings.Contains(got, "history: conversation_messages=") || !strings.Contains(got, "no session diagnostics recorded") {
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
			output.NewStopReasonEvent(3, "max_turns", nil),
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
		"history: conversation_messages=1 diagnostics=3 context_diagnostics=2",
		"last stop: stopped after 3 turns: reached the max turn limit next: increase limits.max_turns or continue in a new prompt",
		"context fullness: budget project context used 512/256 bytes; turn 3; truncated; notes trimmed extra files",
		"recent compaction: compaction turn 3 compacted 1 turn/2 messages; retained 2 turns/4 messages; kept summary \"compacted conversation history\"; summary 128 bytes; summary truncated",
		"recent diagnostics:",
		"context: compaction turn 3 compacted 1 turn/2 messages; retained 2 turns/4 messages; kept summary \"compacted conversation history\"; summary 128 bytes; summary truncated",
		"context: budget project context used 512/256 bytes; turn 3; truncated; notes trimmed extra files",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("history output %q missing %q", got, want)
		}
	}
}

func TestSessionHistoryContextViewShowsOnlyContextDiagnostics(t *testing.T) {
	var out bytes.Buffer
	session := &Session{
		Out: output.NewStream(&out),
		Diagnostics: []output.Event{
			output.NewToolCallStartedEvent(2, "read", "call_1", map[string]any{"path": "AGENTS.md"}),
			output.NewContextCompactionEvent(3, 2, 4, 1, 2, 128, false, "compacted conversation history"),
			output.NewContextBudgetEvent("conversation", 3, 1024, 768, true, "trimmed old turns"),
		},
	}

	done, err := session.HandleLine(context.Background(), "/history context")
	if err != nil {
		t.Fatalf("history context error = %v", err)
	}
	if done {
		t.Fatal("history context returned done=true")
	}

	got := out.String()
	for _, want := range []string{
		"history: conversation_messages=0 diagnostics=3 context_diagnostics=2",
		"context fullness: budget conversation used 1024/768 bytes; turn 3; truncated; notes trimmed old turns",
		"recent compaction: compaction turn 3 compacted 1 turn/2 messages; retained 2 turns/4 messages; kept summary \"compacted conversation history\"; summary 128 bytes",
		"recent context diagnostics:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("history context output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "tool: turn=2 start tool=read") {
		t.Fatalf("history context output = %q, want tool diagnostics filtered out", got)
	}
}

func TestSessionHistoryRecentViewCapsOutput(t *testing.T) {
	var out bytes.Buffer
	session := &Session{
		Out: output.NewStream(&out),
		Diagnostics: []output.Event{
			output.NewToolCallStartedEvent(1, "read", "call_1", nil),
			output.NewToolCallFinishedEvent(1, "read", "call_1", `{"ok":true}`, nil),
			output.NewStopReasonEvent(1, "complete", nil),
			output.NewContextBudgetEvent("project_context", 1, 512, 256, true, "trimmed extra files"),
		},
	}

	done, err := session.HandleLine(context.Background(), "/history recent 2")
	if err != nil {
		t.Fatalf("history recent error = %v", err)
	}
	if done {
		t.Fatal("history recent returned done=true")
	}

	got := out.String()
	for _, want := range []string{
		"history: conversation_messages=0 diagnostics=4 context_diagnostics=1",
		"recent diagnostics: showing latest 2 of 4",
		"status: run complete after 1 turn",
		"context: budget project context used 512/256 bytes; turn 1; truncated; notes trimmed extra files",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("history recent output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "tool: turn=1 start tool=read") {
		t.Fatalf("history recent output = %q, want older diagnostics omitted", got)
	}
}

func TestSessionHistoryRejectsInvalidArguments(t *testing.T) {
	var out bytes.Buffer
	session := &Session{Out: output.NewStream(&out)}

	done, err := session.HandleLine(context.Background(), "/history recent nope")
	if err != nil {
		t.Fatalf("history invalid args returned error = %v", err)
	}
	if done {
		t.Fatal("history invalid args returned done=true")
	}

	got := out.String()
	for _, want := range []string{
		"history: recent count must be a positive integer",
		"usage: /history [summary|context|recent [count]]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("history invalid args output %q missing %q", got, want)
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

func TestSessionRunRetainsPromptInterruptForHistory(t *testing.T) {
	var out bytes.Buffer
	prompt := &fakePrompt{
		lines: []string{"/history", "/exit"},
		errs:  []error{ErrPromptInterrupted, nil},
		out:   &out,
	}
	session := &Session{
		Out:    output.NewStream(&out),
		prompt: prompt,
	}

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(session.Diagnostics) != 1 {
		t.Fatalf("Diagnostics len = %d, want 1", len(session.Diagnostics))
	}
	if got := out.String(); !strings.Contains(got, "run cancelled") || !strings.Contains(got, "last stop: run cancelled") {
		t.Fatalf("Run() output = %q, want retained cancelled stop reason", got)
	}
}

func TestSessionCancelledRunKeepsStateInspectable(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{
		runFn: func(ctx context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
			return RunResult{
				Conversation: append([]agent.Message(nil), conversation...),
				Diagnostics:  []output.Event{output.NewStopReasonEvent(1, string(agent.StopReasonCancelled), nil)},
			}, nil
		},
	}
	session := &Session{
		Runner:       runner,
		Out:          output.NewStream(&out),
		SkillNames:   []string{"codex"},
		ActiveSkills: []string{"codex"},
		Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "previous"}},
	}

	done, err := session.HandleLine(context.Background(), "keep working")
	if err != nil {
		t.Fatalf("HandleLine() error = %v", err)
	}
	if done {
		t.Fatal("HandleLine() returned done=true")
	}
	if len(session.Conversation) != 2 {
		t.Fatalf("Conversation len = %d, want 2", len(session.Conversation))
	}
	if !containsString(session.ActiveSkills, "codex") {
		t.Fatalf("ActiveSkills = %#v, want codex retained", session.ActiveSkills)
	}

	out.Reset()
	done, err = session.HandleLine(context.Background(), "/history")
	if err != nil {
		t.Fatalf("history error = %v", err)
	}
	if done {
		t.Fatal("history returned done=true")
	}
	if got := out.String(); !strings.Contains(got, "last stop: run cancelled") {
		t.Fatalf("history output = %q, want cancelled stop reason", got)
	}
}

func TestCompletionPrefixOnlyUsesFirstCommandToken(t *testing.T) {
	if got := CompletionPrefix([]rune("/he"), 3); got != "/he" {
		t.Fatalf("CompletionPrefix(/he) = %q, want /he", got)
	}
	if got := CompletionPrefix([]rune("/help extra"), 10); got != "/help" {
		t.Fatalf("CompletionPrefix(/help extra) = %q, want /help", got)
	}
	if got := CompletionPrefix([]rune("plain text"), 5); got != "" {
		t.Fatalf("CompletionPrefix(plain text) = %q, want empty", got)
	}
}

func TestPromptEventSinkRoutesEventsThroughPrompter(t *testing.T) {
	var out bytes.Buffer
	prompt := &fakePrompt{out: &out}
	sink := NewPromptEventSink(prompt)

	sink.Emit(output.NewStopReasonEvent(1, "complete", nil))

	if got := out.String(); !strings.Contains(got, "status: run complete after 1 turn") {
		t.Fatalf("prompt event sink output = %q, want routed event", got)
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
	errs  []error
	out   *bytes.Buffer
}

func (p *fakePrompt) ReadLine(ctx context.Context) (string, error) {
	_ = ctx
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return "", err
	}
	if len(p.lines) == 0 {
		return "", io.EOF
	}
	line := p.lines[0]
	p.lines = p.lines[1:]
	return line, nil
}

func (p *fakePrompt) Printf(_ output.Channel, format string, args ...any) {
	if p.out == nil {
		return
	}
	_, _ = p.out.WriteString(fmt.Sprintf(format, args...))
}

func (p *fakePrompt) Println(_ output.Channel, args ...any) {
	if p.out == nil {
		return
	}
	_, _ = p.out.WriteString(fmt.Sprintln(args...))
}
