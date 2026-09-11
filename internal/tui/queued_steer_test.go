package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/luispabon/steiner/internal/agent"
)

func newQueuedSteerTestModel(t *testing.T, msgs ...agent.SteerMessage) *Model {
	t.Helper()
	q := agent.NewSteerQueue()
	for _, msg := range msgs {
		q.Add(msg)
	}
	return &Model{styles: testStyles("#5599ff"), steers: q}
}

func TestQueuedSteerHeightAndRenderAgree(t *testing.T) {
	lipgloss.Writer.Profile = colorprofile.ASCII
	widths := []int{8, 12, 20, 40, 80}
	m := newQueuedSteerTestModel(t, agent.SteerMessage{Text: "hello there, this is a queued steer message"})
	for _, width := range widths {
		rendered := m.renderQueuedSteerBox(width)
		height := m.queuedSteerHeight(width)
		wantHeight := 0
		if rendered != "" {
			wantHeight = lipgloss.Height(rendered)
		}
		if height != wantHeight {
			t.Errorf("width %d: queuedSteerHeight = %d, want %d (derived from render)", width, height, wantHeight)
		}
	}
}

func TestQueuedSteerBoxEmptyWhenNoQueue(t *testing.T) {
	m := &Model{styles: testStyles("#5599ff"), steers: agent.NewSteerQueue()}
	if got := m.renderQueuedSteerBox(60); got != "" {
		t.Errorf("renderQueuedSteerBox = %q, want empty for empty queue", got)
	}
	if got := m.queuedSteerHeight(60); got != 0 {
		t.Errorf("queuedSteerHeight = %d, want 0 for empty queue", got)
	}
}

func TestQueuedSteerBoxNilQueueIsSafe(t *testing.T) {
	m := &Model{styles: testStyles("#5599ff")}
	if got := m.renderQueuedSteerBox(60); got != "" {
		t.Errorf("renderQueuedSteerBox = %q, want empty for nil queue", got)
	}
	if got := m.queuedSteerHeight(60); got != 0 {
		t.Errorf("queuedSteerHeight = %d, want 0 for nil queue", got)
	}
}

func TestQueuedSteerBoxNarrowFallback(t *testing.T) {
	m := newQueuedSteerTestModel(t, agent.SteerMessage{Text: "hello"})
	rendered := m.renderQueuedSteerBox(12)
	if !strings.Contains(rendered, "queued: hello") {
		t.Errorf("narrow render = %q, want it to contain 'queued: hello'", rendered)
	}
	if strings.Contains(rendered, "╭") {
		t.Errorf("narrow render = %q, want no box corners", rendered)
	}
	if got := m.queuedSteerHeight(12); got != 1 {
		t.Errorf("queuedSteerHeight(narrow) = %d, want 1", got)
	}
}

func TestQueuedSteerBoxTitleShowsCountWhenPlural(t *testing.T) {
	single := newQueuedSteerTestModel(t, agent.SteerMessage{Text: "one"})
	if rendered := single.renderQueuedSteerBox(60); strings.Contains(rendered, "queued (") {
		t.Errorf("single-message title = %q, want no count", rendered)
	}

	multi := newQueuedSteerTestModel(t,
		agent.SteerMessage{Text: "one"},
		agent.SteerMessage{Text: "two"},
		agent.SteerMessage{Text: "three"},
	)
	rendered := multi.renderQueuedSteerBox(60)
	if !strings.Contains(rendered, "queued (3)") {
		t.Errorf("multi-message title = %q, want it to contain 'queued (3)'", rendered)
	}
}

func TestWrapQueuedSteerLinesOverflow(t *testing.T) {
	// Three one-line messages: only the first two fit inside the 3-row
	// budget (A, blank separator, B); C gets zero rows, so the overflow row
	// names exactly the one message hidden entirely.
	msgs := []agent.SteerMessage{
		{Text: "A"}, {Text: "B"}, {Text: "C"},
	}
	lines := wrapQueuedSteerLines(msgs, 40)
	if len(lines) != 4 {
		t.Fatalf("lines = %v, want 4 rows (3 text + 1 overflow)", lines)
	}
	if strings.TrimSpace(lines[0]) != "A" || strings.TrimSpace(lines[1]) != "" || strings.TrimSpace(lines[2]) != "B" {
		t.Fatalf("lines = %v, want [A, \"\", B, ...]", lines)
	}
	if lines[3] != "+1 more" {
		t.Fatalf("overflow row = %q, want %q", lines[3], "+1 more")
	}
}

func TestWrapQueuedSteerLinesSingleMessageOverflowUsesEllipsis(t *testing.T) {
	// A single message whose own wrapped text exceeds the 3-row budget: no
	// other message is hidden, so the overflow row is "…" rather than
	// "+0 more".
	msgs := []agent.SteerMessage{
		{Text: "one\ntwo\nthree\nfour\nfive"},
	}
	lines := wrapQueuedSteerLines(msgs, 40)
	if len(lines) != 4 {
		t.Fatalf("lines = %v, want 4 rows (3 text + 1 overflow)", lines)
	}
	if lines[3] != "…" {
		t.Fatalf("overflow row = %q, want %q", lines[3], "…")
	}
}

func TestQueuedSteerPlaceholderPluralization(t *testing.T) {
	if got := queuedSteerPlaceholder(1); !strings.Contains(got, "1 message queued") {
		t.Errorf("placeholder(1) = %q, want it to contain '1 message queued'", got)
	}
	if got := queuedSteerPlaceholder(3); !strings.Contains(got, "3 messages queued") {
		t.Errorf("placeholder(3) = %q, want it to contain '3 messages queued'", got)
	}
	if got := queuedSteerPlaceholder(1); !strings.Contains(got, "ctrl+g to edit") {
		t.Errorf("placeholder(1) = %q, want it to mention ctrl+g", got)
	}
}

// TestLayoutAccountsForQueuedBoxAndTallComposer verifies that a full queued
// box together with a multi-line composer, at a small terminal height, never
// pushes the status bar off the bottom of the frame: layout must subtract
// the queued box height from every call site that positions the composer.
func TestLayoutAccountsForQueuedBoxAndTallComposer(t *testing.T) {
	q := agent.NewSteerQueue()
	q.Add(agent.SteerMessage{Text: strings.Repeat("word ", 40)})
	q.Add(agent.SteerMessage{Text: strings.Repeat("more ", 40)})
	q.Add(agent.SteerMessage{Text: strings.Repeat("even more ", 40)})

	m := newModel(Config{SteerQueue: q}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 16})
	m.input.SetValue(strings.Repeat("line\n", 20))
	m.relayoutInput()

	view := m.View().Content
	lines := strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("frame has %d lines, want exactly m.height = %d", len(lines), m.height)
	}
	if !strings.Contains(view, "queued") {
		t.Fatalf("frame does not contain the queued box:\n%s", view)
	}
}
