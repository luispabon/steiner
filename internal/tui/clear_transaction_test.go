package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/usagestats"
)

type clearTransactionController struct {
	err    error
	calls  int
	onCall func()
}

func (c *clearTransactionController) Handle(_ context.Context, action interactive.Action) error {
	if _, ok := action.(interactive.ClearConversation); ok {
		c.calls++
		if c.onCall != nil {
			c.onCall()
		}
	}
	return c.err
}

func TestClearConversationRefusalPreservesUIAndRecorder(t *testing.T) {
	clearErr := errors.New("clear refused")
	ctrl := &clearTransactionController{err: clearErr}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.sessionResetCleanup = func() { t.Error("cleanup called on refused clear") }
	m.content.AppendLine("old transcript")
	m.input.SetValue("draft")
	m.imageMarkers = []imageMarker{{label: "[img-1]", image: agent.ImageBlock{ID: "img-1"}}}
	m.recorder = usagestats.New(nil)
	m.recorder.Record(usagestats.Observation{Source: usagestats.SourceParent, PromptTokens: 10})

	_, cleared, err := m.clearConversationStateWithError()

	if cleared || !errors.Is(err, clearErr) {
		t.Fatalf("clear result = (%v, %v), want (false, %v)", cleared, err, clearErr)
	}
	if got := strings.Join(m.viewport.Lines(), "\n"); !strings.Contains(got, "old transcript") || !strings.Contains(got, "clear refused") {
		t.Fatalf("viewport lines = %q, want transcript and visible error", got)
	}
	if got := m.input.Value(); got != "draft" {
		t.Fatalf("input = %q, want draft preserved", got)
	}
	if len(m.imageMarkers) != 1 {
		t.Fatalf("image markers = %v, want pending image preserved", m.imageMarkers)
	}
	if got := m.recorder.SessionReport().Requests; got != 1 {
		t.Fatalf("recorder requests = %d, want 1", got)
	}
	if ctrl.calls != 1 {
		t.Fatalf("controller clear calls = %d, want 1", ctrl.calls)
	}
}

func TestClearConversationSuccessCallsControllerBeforeUICleanup(t *testing.T) {
	m := newModel(Config{}, nil)
	ctrl := &clearTransactionController{}
	ctrl.onCall = func() {
		if got := m.content.String(m.viewport.Width()); !strings.Contains(got, "old transcript") {
			t.Errorf("transcript cleared before controller accepted clear: %q", got)
		}
	}
	m.controller = ctrl
	cleanupCalls := 0
	m.sessionResetCleanup = func() { cleanupCalls++ }
	m.content.AppendLine("old transcript")
	m.input.SetValue("draft")
	m.imageMarkers = []imageMarker{{label: "[img-1]", image: agent.ImageBlock{ID: "img-1"}}}
	m.recorder = usagestats.New(nil)
	m.recorder.Record(usagestats.Observation{Source: usagestats.SourceParent, PromptTokens: 10})

	_, cleared, err := m.clearConversationStateWithError()

	if err != nil || !cleared {
		t.Fatalf("clear result = (%v, %v), want (true, nil)", cleared, err)
	}
	if ctrl.calls != 1 {
		t.Fatalf("controller clear calls = %d, want 1", ctrl.calls)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", cleanupCalls)
	}
	if got := m.content.String(m.viewport.Width()); strings.Contains(got, "old transcript") {
		t.Fatalf("content after cleanup = %q, want cleared", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input = %q, want empty", got)
	}
	if len(m.imageMarkers) != 0 {
		t.Fatalf("image markers = %v, want none", m.imageMarkers)
	}
	if got := m.recorder.SessionReport().Requests; got != 0 {
		t.Fatalf("recorder requests = %d, want 0", got)
	}
}

func TestResetConversationUIDoesNotCallController(t *testing.T) {
	m := newModel(Config{}, nil)
	ctrl := &clearTransactionController{}
	m.controller = ctrl
	m.content.AppendLine("old transcript")
	m.resetConversationUI()
	if ctrl.calls != 0 {
		t.Fatalf("controller clear calls = %d, want 0", ctrl.calls)
	}
}
