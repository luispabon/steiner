package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestSubmitPromptRetainsModeNoticeAfterRunFailure(t *testing.T) {
	t.Parallel()
	cfg := guardTestConfig()
	cfg.Modes = config.ModesConfig{Default: config.ExecutionModePlan}
	s := testNewSession(t, Dependencies{Config: cfg})

	var runs [][]agent.Message
	s.SetRunner(newRunExecutorFunc(func(_ context.Context, conversation []agent.Message) (RunResult, error) {
		runs = append(runs, cloneMessages(conversation))
		if len(runs) == 1 {
			return RunResult{}, errors.New("prepare failed")
		}
		return RunResult{Conversation: conversation}, nil
	}))

	s.submitPrompt(context.Background(), "first", nil)
	notice := prompt.ModeNotice(config.ExecutionModePlan) + "\n\n"
	assertModeNoticeMessage(t, s.Conversation(), notice, "first")
	assertModeNoticeMessage(t, s.lineage.FullMessages(), notice, "first")

	s.submitPrompt(context.Background(), "second", nil)
	if len(runs) != 2 {
		t.Fatalf("runner calls = %d, want 2", len(runs))
	}
	if got := strings.Count(runs[1][0].Content, notice); got != 1 {
		t.Errorf("first user message has %d mode notices on retry, want 1", got)
	}
	if got := strings.Count(runs[1][1].Content, notice); got != 1 {
		t.Errorf("second user message has %d mode notices, want 1", got)
	}
	assertModeNoticeMessage(t, s.Conversation(), notice, "second")
}

func assertModeNoticeMessage(t *testing.T, messages []agent.Message, notice, text string) {
	t.Helper()
	if len(messages) == 0 {
		t.Fatalf("messages = %+v, want a user message", messages)
	}
	want := notice + text
	if got := messages[len(messages)-1].Content; got != want {
		t.Errorf("last message content = %q, want %q", got, want)
	}
	if got := strings.Count(messages[0].Content, notice); got != 1 {
		t.Errorf("first message has %d mode notices, want 1", got)
	}
}
