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
	rendered := m.renderQueuedSteerBox(13)
	if !strings.Contains(rendered, "queued: hello") {
		t.Errorf("narrow render = %q, want it to contain 'queued: hello'", rendered)
	}
	if strings.Contains(rendered, "╭") {
		t.Errorf("narrow render = %q, want no box corners", rendered)
	}
	if got := m.queuedSteerHeight(13); got != 1 {
		t.Errorf("queuedSteerHeight(narrow) = %d, want 1", got)
	}
}

func TestQueuedSteerBoxNarrowFallbackBoundsMultilineMessage(t *testing.T) {
	m := newQueuedSteerTestModel(t, agent.SteerMessage{Text: "line one\nline two\n" + strings.Repeat("x", 100)})
	rendered := m.renderQueuedSteerBox(12)
	if strings.Contains(rendered, "\n") {
		t.Errorf("narrow render = %q, want a single row with no embedded newlines", rendered)
	}
	if got := m.queuedSteerHeight(12); got != 1 {
		t.Errorf("queuedSteerHeight(narrow, multiline) = %d, want 1", got)
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
	// Three one-line messages wrap to 5 rows (A, blank separator, B, blank
	// separator, C); only 3 fit (A, blank, B), leaving one blank separator and
	// C hidden. The blank separator carries no content, so the overflow row
	// names only the 1 hidden text row.
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

func TestWrapQueuedSteerLinesOverflowIsInvariantUnderMerge(t *testing.T) {
	// ctrl+g take-back merges N queued messages into one via MergeSteers,
	// erasing their boundaries. The overflow row must report the same
	// hidden-row count before and after that merge, since the actual
	// rendered content is identical either way.
	msgs := []agent.SteerMessage{
		{Text: "A"}, {Text: "B"}, {Text: "C"},
	}
	before := wrapQueuedSteerLines(msgs, 40)

	merged := agent.MergeSteers(msgs)
	after := wrapQueuedSteerLines([]agent.SteerMessage{{Text: merged.Content}}, 40)

	if before[len(before)-1] != after[len(after)-1] {
		t.Fatalf("overflow row changed after merge: before %q, after %q", before[len(before)-1], after[len(after)-1])
	}
	if after[len(after)-1] != "+1 more" {
		t.Fatalf("overflow row after merge = %q, want %q", after[len(after)-1], "+1 more")
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

func newTakeBackTestModel(t *testing.T, msgs ...agent.SteerMessage) *Model {
	t.Helper()
	q := agent.NewSteerQueue()
	for _, msg := range msgs {
		q.Add(msg)
	}
	m := newModel(Config{SteerQueue: q}, nil)
	return updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
}

func TestTakeBackSingleMessageEmptyComposer(t *testing.T) {
	m := newTakeBackTestModel(t, agent.SteerMessage{Text: "hello there"})
	m = m.executeTakeBackSteersAction().(*Model)

	if got := m.input.Value(); got != "hello there" {
		t.Errorf("input.Value() = %q, want %q", got, "hello there")
	}
	if m.steers.Len() != 0 {
		t.Errorf("steers.Len() = %d, want 0", m.steers.Len())
	}
}

func TestTakeBackMultipleMessagesJoinedInOrder(t *testing.T) {
	m := newTakeBackTestModel(t,
		agent.SteerMessage{Text: "first"},
		agent.SteerMessage{Text: "second"},
		agent.SteerMessage{Text: "third"},
	)
	m = m.executeTakeBackSteersAction().(*Model)

	want := "first\n\nsecond\n\nthird"
	if got := m.input.Value(); got != want {
		t.Errorf("input.Value() = %q, want %q", got, want)
	}
}

func TestTakeBackWithDraftAppendsDraftLastAndPlacesCursorAtEnd(t *testing.T) {
	m := newTakeBackTestModel(t, agent.SteerMessage{Text: "queued one"})
	m.input.SetValue("my draft")

	m = m.executeTakeBackSteersAction().(*Model)

	want := "queued one\n\nmy draft"
	if got := m.input.Value(); got != want {
		t.Errorf("input.Value() = %q, want %q", got, want)
	}
	if m.input.Line() != 2 {
		t.Errorf("input.Line() = %d, want cursor on last line (2)", m.input.Line())
	}
}

func TestTakeBackRestoresImageMarkersInOrder(t *testing.T) {
	imgA := agent.ImageBlock{ID: "a", MediaType: "image/png", Data: "AAAA"}
	imgB := agent.ImageBlock{ID: "b", MediaType: "image/png", Data: "BBBB"}
	m := newTakeBackTestModel(t,
		agent.SteerMessage{Text: "look at [Image 1]", Images: []agent.ImageBlock{imgA}},
		agent.SteerMessage{Text: "and [Image 1]", Images: []agent.ImageBlock{imgB}},
	)

	m = m.executeTakeBackSteersAction().(*Model)

	want := "look at [Image 1]\n\nand [Image 2]"
	if got := m.input.Value(); got != want {
		t.Errorf("input.Value() = %q, want %q", got, want)
	}
	if len(m.imageMarkers) != 2 {
		t.Fatalf("len(imageMarkers) = %d, want 2", len(m.imageMarkers))
	}
	if m.imageMarkers[0].label != "[Image 1]" || m.imageMarkers[0].image != imgA {
		t.Errorf("imageMarkers[0] = %+v, want label [Image 1] and image %+v", m.imageMarkers[0], imgA)
	}
	if m.imageMarkers[1].label != "[Image 2]" || m.imageMarkers[1].image != imgB {
		t.Errorf("imageMarkers[1] = %+v, want label [Image 2] and image %+v", m.imageMarkers[1], imgB)
	}
}

func TestTakeBackClearsQueuedBoxAndLayout(t *testing.T) {
	m := newTakeBackTestModel(t, agent.SteerMessage{Text: "hello"})
	if m.queuedSteerHeight(m.width) == 0 {
		t.Fatal("expected queued box to occupy rows before take-back")
	}

	m = m.executeTakeBackSteersAction().(*Model)

	if got := m.renderQueuedSteerBox(m.width); got != "" {
		t.Errorf("renderQueuedSteerBox after take-back = %q, want empty", got)
	}
	if got := m.queuedSteerHeight(m.width); got != 0 {
		t.Errorf("queuedSteerHeight after take-back = %d, want 0", got)
	}
}

func TestTakeBackEmptyQueueIsNoOpLeavesDraft(t *testing.T) {
	m := newTakeBackTestModel(t)
	m.input.SetValue("untouched draft")

	m = m.executeTakeBackSteersAction().(*Model)

	if got := m.input.Value(); got != "untouched draft" {
		t.Errorf("input.Value() = %q, want %q (unchanged)", got, "untouched draft")
	}
}

// drainedStubQueue simulates a queue whose Drain() lost the race with a
// concurrent drain: Len() still reports a stale non-zero count, but Drain()
// returns nothing.
type drainedStubQueue struct{}

func (drainedStubQueue) Add(agent.SteerMessage)         {}
func (drainedStubQueue) Drain() []agent.SteerMessage    { return nil }
func (drainedStubQueue) Snapshot() []agent.SteerMessage { return nil }
func (drainedStubQueue) Len() int                       { return 1 }

func TestTakeBackRaceWithDrainLeavesComposerUntouched(t *testing.T) {
	m := newTakeBackTestModel(t, agent.SteerMessage{Text: "queued"})
	m.steers = drainedStubQueue{}
	m.input.SetValue("my draft")

	m = m.executeTakeBackSteersAction().(*Model)

	if got := m.input.Value(); got != "my draft" {
		t.Errorf("input.Value() = %q, want %q (unchanged)", got, "my draft")
	}
}

func TestCtrlGRoutesToTakeBackDuringActiveRun(t *testing.T) {
	m := newTakeBackTestModel(t, agent.SteerMessage{Text: "queued during run"})
	m.status.mode = "running"

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	if got := m.input.Value(); got != "queued during run" {
		t.Errorf("input.Value() = %q, want %q", got, "queued during run")
	}
	if m.steers.Len() != 0 {
		t.Errorf("steers.Len() = %d, want 0", m.steers.Len())
	}
}

// TestLayoutAccountsForQueuedBoxAndTallComposer verifies that a full queued
// box together with a multi-line composer, at a small terminal height, never
// pushes the status bar off the bottom of the frame: layout must subtract
// the queued box height from every call site that positions the composer.
func TestLayoutAccountsForQueuedBoxAndTallComposer(t *testing.T) {
	shortQueue := agent.NewSteerQueue()
	shortQueue.Add(agent.SteerMessage{Text: "A"})
	shortQueue.Add(agent.SteerMessage{Text: "B"})
	shortQueue.Add(agent.SteerMessage{Text: "C"})

	longQueue := agent.NewSteerQueue()
	longQueue.Add(agent.SteerMessage{Text: strings.Repeat("x", 1000)})

	shortModel := newModel(Config{SteerQueue: shortQueue}, nil)
	shortModel = updateModel(t, shortModel, tea.WindowSizeMsg{Width: 80, Height: 16})
	longModel := newModel(Config{SteerQueue: longQueue}, nil)
	longModel = updateModel(t, longModel, tea.WindowSizeMsg{Width: 80, Height: 16})

	shortHeight := shortModel.queuedSteerHeight(shortModel.contentWidth())
	longHeight := longModel.queuedSteerHeight(longModel.contentWidth())
	if shortHeight != longHeight {
		t.Errorf("queuedSteerHeight = %d for 3 short messages, %d for one ~1000-char message; both saturate the cap and must match", shortHeight, longHeight)
	}

	emptyModel := newModel(Config{SteerQueue: agent.NewSteerQueue()}, nil)
	emptyModel = updateModel(t, emptyModel, tea.WindowSizeMsg{Width: 80, Height: 16})

	for name, m := range map[string]*Model{"empty": emptyModel, "short": shortModel, "long": longModel} {
		m.input.SetValue(strings.Repeat("line\n", 20))
		m.relayoutInput()

		view := stripANSI(m.View().Content)
		lines := strings.Split(view, "\n")
		wantStatus := stripANSI(m.renderStatus(m.contentWidth()))
		if got := lines[len(lines)-1]; got != wantStatus {
			t.Errorf("%s queue: last frame line = %q, want status bar %q", name, got, wantStatus)
		}
	}
}
