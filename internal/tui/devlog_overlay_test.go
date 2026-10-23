package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func devLogTestModel(t *testing.T) *Model {
	t.Helper()
	return updateModel(t, newModel(Config{}, nil), tea.WindowSizeMsg{Width: 80, Height: 24})
}

func TestDevLogOverlaySlashCommandOpensAtTail(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	recordTestDevLog(m, 60, devLogError)

	m.input.SetValue("/errors")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.devLogOverlay.IsOpen() {
		t.Fatal("expected /errors to open the overlay")
	}

	view := stripANSI(m.devLogOverlay.View(m.devLog.entries))
	if !strings.Contains(view, "entry-59") {
		t.Errorf("overlay should open at the newest entry, got: %s", view)
	}
	if strings.Contains(view, "entry-0") {
		t.Errorf("oldest entry should be scrolled out of view, got: %s", view)
	}
}

func TestDevLogOverlayOpenClearsUnseenBadge(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	m.recordDevLog(devLogParams{Severity: devLogWarning, Message: "w"})
	m = updateModel(t, m, keybindAlt('e'))
	if !m.devLogOverlay.IsOpen() {
		t.Fatal("alt+e did not open the overlay")
	}
	if m.status.devLogUnseen != 0 {
		t.Errorf("unseen after open = %d, want 0", m.status.devLogUnseen)
	}
}

func TestDevLogOverlayCloseKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "esc", key: tea.KeyPressMsg{Code: tea.KeyEsc}},
		{name: "alt+e", key: keybindAlt('e')},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := devLogTestModel(t)
			m = updateModel(t, m, keybindAlt('e'))
			if !m.devLogOverlay.IsOpen() {
				t.Fatal("alt+e did not open the overlay")
			}
			m = updateModel(t, m, tc.key)
			if m.devLogOverlay.IsOpen() {
				t.Errorf("%s did not close the overlay", tc.name)
			}
		})
	}
}

func TestDevLogOverlayFollowsTailUntilScrolled(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	recordTestDevLog(m, 60, devLogError)
	m.openDevLogOverlay()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.devLogOverlay.follow {
		t.Fatal("up key should stop following the tail")
	}
	m.recordDevLog(devLogParams{Severity: devLogError, Message: "fresh"})
	if view := stripANSI(m.devLogOverlay.View(m.devLog.entries)); strings.Contains(view, "fresh") {
		t.Errorf("entry recorded while scrolled up should stay out of view, got: %s", view)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.devLogOverlay.follow {
		t.Fatal("end key should resume following the tail")
	}
	if view := stripANSI(m.devLogOverlay.View(m.devLog.entries)); !strings.Contains(view, "fresh") {
		t.Errorf("end key should show the newest entry, got: %s", view)
	}
}

func TestDevLogOverlayCopyReturnsCommand(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	m.recordDevLog(devLogParams{Severity: devLogError, Message: "first"})
	m.openDevLogOverlay()

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Error("y returned nil command, want clipboard copy")
	}
}

// TestDevLogCopyKeyClipboardFailure runs y through Update and the copy command.
// It is not parallel: it points os.Stdout at a read-only file so the OSC52
// fallback write fails, and clears the display variables so no real clipboard
// tool runs.
func TestDevLogCopyKeyClipboardFailure(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() {
		if err := devNull.Close(); err != nil {
			t.Errorf("close %s: %v", os.DevNull, err)
		}
	})
	stdout := os.Stdout
	os.Stdout = devNull
	t.Cleanup(func() { os.Stdout = stdout })

	m := devLogTestModel(t)
	m.openDevLogOverlay()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("y returned nil command, want clipboard copy")
	}
	msg := cmd()
	failed, ok := msg.(clipboardFailedMsg)
	if !ok {
		t.Fatalf("copy command returned %T, want clipboardFailedMsg", msg)
	}
	m, ok = next.(*Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	m = updateModel(t, m, failed)

	notices := errorSurfacingNotices(m)
	if len(notices) != 1 || notices[0].noticeSeverity != noticeError || !strings.Contains(notices[0].text, "copy to clipboard") {
		t.Fatalf("notices = %+v, want one error notice for the failed copy", notices)
	}
	if len(m.devLog.entries) != 1 {
		t.Fatalf("dev log entries = %d, want 1", len(m.devLog.entries))
	}
	if e := m.devLog.entries[0]; e.Source != "tui" || e.Severity != devLogError {
		t.Errorf("dev log entry = %+v, want tui error", e.devLogParams)
	}
}

func TestDevLogTextJoinsAllEntries(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 22, 9, 30, 0, 0, time.UTC)
	entries := []devLogEntry{
		{at: at, devLogParams: devLogParams{Severity: devLogError, Source: "provider", Message: "first"}},
		{at: at, devLogParams: devLogParams{Severity: devLogWarning, Source: "provider", CallID: "c1", Message: "second"}},
	}
	const want = "09:30:00 error provider\nfirst\n\n09:30:00 warning provider call=c1\nsecond"
	if got := devLogText(entries); got != want {
		t.Errorf("devLogText() = %q, want %q", got, want)
	}
}

func TestDevLogOverlayEmptyState(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	m.openDevLogOverlay()
	if view := stripANSI(m.devLogOverlay.View(m.devLog.entries)); !strings.Contains(view, "No internal errors recorded.") {
		t.Errorf("empty overlay missing empty-state line, got: %s", view)
	}
}

func TestDevLogMemoInvalidatesAfterPushAtCapacity(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	recordTestDevLog(m, devLogCapacity, devLogWarning)
	m.openDevLogOverlay()
	if len(m.devLog.entries) != devLogCapacity {
		t.Fatalf("ring holds %d entries, want %d", len(m.devLog.entries), devLogCapacity)
	}

	m.devLogMemoView()
	m.overlayMemos.devLog.out = memoSentinel
	if m.devLogMemoView() != memoSentinel {
		t.Fatal("unchanged ring re-rendered instead of reusing the memo")
	}

	m.recordDevLog(devLogParams{Severity: devLogError, Source: "tool", Message: "newest entry"})
	if len(m.devLog.entries) != devLogCapacity {
		t.Fatalf("ring holds %d entries after push, want %d", len(m.devLog.entries), devLogCapacity)
	}
	got := m.devLogMemoView()
	if got == memoSentinel {
		t.Fatal("stale memo served after a push at full capacity")
	}
	if want := m.devLogOverlay.View(m.devLog.entries); got != want {
		t.Error("memoised render differs from the uncached render after a push")
	}
	if !strings.Contains(stripANSI(got), "newest entry") {
		t.Error("render after a push does not show the new entry")
	}
}

func TestDevLogLineCountCache(t *testing.T) {
	t.Parallel()
	m := devLogTestModel(t)
	m.openDevLogOverlay()
	long := strings.Repeat("wrap me ", 40)
	m.recordDevLog(devLogParams{Severity: devLogError, Source: "tool", Message: long})
	check := func(step string) {
		t.Helper()
		if got, want := m.devLogLineCount(), len(m.devLogOverlay.buildLines(m.devLog.entries)); got != want {
			t.Errorf("%s: line count = %d, want %d", step, got, want)
		}
	}
	check("after push")

	m.overlayMemos.logLines.n = -1
	if m.devLogLineCount() != -1 {
		t.Error("line count recomputed for unchanged width, styles and ring")
	}

	m.recordDevLog(devLogParams{Severity: devLogWarning, Source: "tool", Message: long})
	check("after second push")

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 24})
	check("after resize")
}

func TestHelpListsAltE(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	if help := stripANSI(renderHelp(m.styles, 120)); !strings.Contains(help, "alt+e") {
		t.Errorf("help missing alt+e entry, got: %s", help)
	}
}
