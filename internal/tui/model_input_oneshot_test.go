package tui

import (
	"context"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type oneshotPromptHistory struct {
	mu      sync.Mutex
	prompts []string
}

func (h *oneshotPromptHistory) Record(prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prompts = append(h.prompts, prompt)
	return nil
}

func (h *oneshotPromptHistory) Load() ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.prompts...), nil
}

func TestLaunchOneshotActionRecordsRelaunchableHistory(t *testing.T) {
	t.Parallel()

	history := &oneshotPromptHistory{}
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents:    output.NoopSink{},
		HistoryWriter: history,
		SessionStore:  tuiTestSessionStore{},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	styles := testStyles(theme.AccentAmber)
	m := &Model{
		controller:       sess,
		input:            newModelInput(),
		styles:           styles,
		subAgentsEnabled: true,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		oneshotRunnerFactory: func(oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
			return nil
		},
	}

	updated, _ := m.executeLaunchOneshotAction("  build the thing  ")
	if updated.(*Model).oneshotRunning != true {
		t.Fatal("oneshotRunning = false, want true after accepted launch")
	}
	if !sess.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() = false, want true")
	}

	prompts, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "/oneshot build the thing" {
		t.Fatalf("history prompts = %q, want one relaunchable command", prompts)
	}
}

func TestResumeAndRejectedOneshotDoNotRecordHistory(t *testing.T) {
	t.Parallel()

	history := &oneshotPromptHistory{}
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents:    output.NoopSink{},
		HistoryWriter: history,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	styles := testStyles(theme.AccentAmber)
	m := &Model{
		controller:       sess,
		input:            newModelInput(),
		styles:           styles,
		subAgentsEnabled: true,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
	m.executeResumeOneshotAction("run-id")
	m.executeLaunchOneshotAction("rejected without runner factory")

	prompts, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error = %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("history prompts = %q, want none", prompts)
	}
}
