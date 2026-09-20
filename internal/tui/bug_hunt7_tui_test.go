package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestOverlayShellInnerWidthNeverNegative(t *testing.T) {
	t.Parallel()
	for _, w := range []int{0, 1, 4, 7, 8, 40} {
		o := OverlayShell{width: w}
		if got := o.InnerWidth(); got < 0 {
			t.Errorf("width %d: InnerWidth = %d, want >= 0", w, got)
		}
		_ = o.Divider() // must not panic
	}
}

func TestSessionPickerViewAtWidthOneDoesNotPanic(t *testing.T) {
	t.Parallel()
	s := newSessionPickerOverlay(testStyles(theme.AccentAmber)).withDimensions(1, 20).Open(nil)
	_ = s.View()
}

func TestToolCallHeaderRespectsCellWidth(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	tests := []struct {
		name string
		args string
	}{
		{"ascii", strings.Repeat("abcdef", 30)},
		{"cjk", strings.Repeat("文件路径", 30)},
		{"emoji", strings.Repeat("🚀ok", 30)},
	}
	const width = 40
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			seg := &toolCallSegment{tool: "bash", args: tt.args, meta: "✓", collapsed: true}
			header := strings.TrimSuffix(b.renderToolCall(seg, width), "\n")
			if w := lipgloss.Width(header); w > width {
				t.Fatalf("header width = %d, want <= %d: %q", w, width, stripANSI(header))
			}
		})
	}
}

func TestClipboardFailureMessageShowsNotice(t *testing.T) {
	m := newModel(Config{Controller: &testController{}}, nil)
	before := m.content.String(80)
	_, _ = m.Update(clipboardFailedMsg{err: context.DeadlineExceeded})
	if after := m.content.String(80); after == before || !strings.Contains(stripANSI(after), "deadline") {
		t.Fatalf("content after clipboard failure = %q, want status notice", stripANSI(after))
	}
}

func TestSecondCtrlCDuringWorktreeCountQuits(t *testing.T) {
	plan := NewWorktreeCleanupPlan(func(context.Context) (int, error) { return 1, nil }, nil)
	m := newModel(Config{WorktreeCleanup: plan}, nil)
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	_, cmd := m.handleNavigationKeyMsgResult(ctrlC)
	if cmd == nil || m.exitFlowPhase != exitFlowPhaseCounting || m.exitCountCancel == nil {
		t.Fatalf("first ctrl-c: phase=%d cancel=%v cmd=%v, want counting", m.exitFlowPhase, m.exitCountCancel != nil, cmd != nil)
	}
	_, cmd = m.handleNavigationKeyMsgResult(ctrlC)
	if cmd == nil {
		t.Fatal("second ctrl-c returned nil cmd, want quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("second ctrl-c cmd = %T, want tea.QuitMsg", cmd())
	}
	if m.exitCountCancel != nil {
		t.Fatal("exitCountCancel not cleared")
	}
}

func (m *Model) handleNavigationKeyMsgResult(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	_, mm, cmd := m.handleNavigationKeyMsg(k)
	return mm, cmd
}
