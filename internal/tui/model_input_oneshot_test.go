package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
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

func TestLaunchOneshotHistoryCanRecallAndResume(t *testing.T) {
	t.Parallel()
	projectRoot := setupOneshotProject(t)
	history := &oneshotPromptHistory{}
	var eventsMu sync.Mutex
	var events []output.Event
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
		}),
		HistoryWriter: history,
		SessionStore:  tuiTestSessionStore{},
		WorkDir:       projectRoot,
		Config:        oneshotTestConfig(),
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	styles := testStyles(theme.AccentAmber)
	m := &Model{
		controller:       sess,
		sessionStore:     tuiTestSessionStore{},
		input:            newModelInput(),
		styles:           styles,
		subAgentsEnabled: true,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
	var phaseCalls int
	m.oneshotRunnerFactory = func(oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
		return oneshot.PhaseRunnerFactoryFunc(func(context.Context, oneshot.Phase, string, tool.ApprovalResponder, config.AdvisorConfig) (oneshot.PhaseRunner, error) {
			phaseCalls++
			return nil, errors.New("stop after admission")
		})
	}

	updated, _ := m.executeLaunchOneshotAction("  build the thing  ")
	m = updated.(*Model)
	if !sess.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() = false, want true")
	}
	if phaseCalls != 1 {
		t.Fatalf("phase factory calls after launch = %d, want 1", phaseCalls)
	}

	prompts, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "/oneshot build the thing" {
		t.Fatalf("history prompts = %q, want one relaunchable command", prompts)
	}
	eventsMu.Lock()
	for _, event := range events {
		if event.Type == output.EventTypeHistoryLoaded {
			m.applyEvent(event)
		}
	}
	eventsMu.Unlock()
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	recalled := parseInput(m.input.Value())
	if recalled.launchOneshotTask != "build the thing" {
		t.Fatalf("Up recalled %q, parsed task = %q, want relaunchable task", m.input.Value(), recalled.launchOneshotTask)
	}

	runs, err := oneshot.ListRuns(projectRoot)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs after accepted launch = %d, want 1", len(runs))
	}
	m.executeResumeOneshotAction(runs[0].RunID)
	if !sess.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() = false after resume, want true")
	}
	if phaseCalls != 2 {
		t.Fatalf("phase factory calls after resume = %d, want 2", phaseCalls)
	}
	prompts, err = history.Load()
	if err != nil {
		t.Fatalf("history.Load() after resume error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != "/oneshot build the thing" {
		t.Fatalf("history prompts after resume = %q, want only the launch command", prompts)
	}
}

func TestRejectedOneshotLaunchDoesNotRecordHistory(t *testing.T) {
	t.Parallel()
	history := &oneshotPromptHistory{}
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents:    output.NoopSink{},
		HistoryWriter: history,
		Config:        oneshotTestConfig(),
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
		sessionStore:     listOnlySessionStore{},
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
	m.oneshotRunnerFactory = func(oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
		return oneshot.PhaseRunnerFactoryFunc(func(context.Context, oneshot.Phase, string, tool.ApprovalResponder, config.AdvisorConfig) (oneshot.PhaseRunner, error) {
			return nil, errors.New("unexpected phase")
		})
	}
	m.executeLaunchOneshotAction("rejected task")
	if !sess.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() = false, want true")
	}
	prompts, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error = %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("history prompts = %q, want none", prompts)
	}
}

func TestOneshotModelPreflightFailureDoesNotRecordHistory(t *testing.T) {
	t.Parallel()
	history := &oneshotPromptHistory{}
	projectRoot := t.TempDir()
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents:    output.NoopSink{},
		HistoryWriter: history,
		SessionStore:  tuiTestSessionStore{},
		WorkDir:       projectRoot,
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		controller:       sess,
		sessionStore:     tuiTestSessionStore{},
		input:            newModelInput(),
		styles:           styles,
		subAgentsEnabled: true,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
	m.oneshotRunnerFactory = func(oneshot.RunIdentity) oneshot.PhaseRunnerFactory {
		return oneshot.PhaseRunnerFactoryFunc(func(context.Context, oneshot.Phase, string, tool.ApprovalResponder, config.AdvisorConfig) (oneshot.PhaseRunner, error) {
			t.Fatal("phase runner factory called before model config validation")
			return nil, nil
		})
	}
	updated, _ := m.executeLaunchOneshotAction("missing model config")
	if updated.(*Model).oneshotRunning {
		t.Fatal("oneshotRunning = true, want false after model preflight rejection")
	}
	prompts, err := history.Load()
	if err != nil {
		t.Fatalf("history.Load() error = %v", err)
	}
	if len(prompts) != 0 {
		t.Fatalf("history prompts = %q, want none", prompts)
	}
	if !sess.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() = false, want true")
	}
}

func oneshotTestConfig() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"local-model": {Provider: "local", ID: "local-model"},
			},
			Effective: config.EffectiveModelAssignments{
				DefaultModel: "local-model",
				Advisor:      "local-model",
			},
		},
	}
}

func setupOneshotProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return root
}
