package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestLSPOverlay_NewClose(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	if o.IsOpen() {
		t.Fatal("expected overlay to start closed")
	}

	o = o.Open(nil, true)
	if !o.IsOpen() {
		t.Fatal("expected overlay to be open after Open()")
	}

	o = o.Close()
	if o.IsOpen() {
		t.Fatal("expected overlay to be closed after Close()")
	}
}

func TestLSPOverlay_SlashCommandOpensOverlay(t *testing.T) {
	t.Parallel()
	m := newModel(Config{LSPEnabled: true}, nil)
	m.lspServers = []LSPServerStatus{{Name: "gopls", Root: "/repo", Status: "ready"}}
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/lsp")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.lspOverlay.IsOpen() {
		t.Fatal("expected /lsp command to open the overlay")
	}

	view := m.lspOverlay.View()
	if !strings.Contains(view, "gopls") {
		t.Fatalf("expected overlay to list configured server, got: %s", view)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.lspOverlay.IsOpen() {
		t.Fatal("expected esc to close the overlay")
	}
}

func TestLSPOverlay_SlashCommandRespectsConfigEnabled(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		lspEnabled bool
		wantNotice bool
	}{
		{name: "disabled in config", lspEnabled: false, wantNotice: true},
		{name: "enabled in config", lspEnabled: true, wantNotice: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{LSPEnabled: tc.lspEnabled}, nil)
			m.lspServers = []LSPServerStatus{{Name: "gopls", Root: "/repo", Status: "ready"}}
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			m.input.SetValue("/lsp")
			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if !m.lspOverlay.IsOpen() {
				t.Fatal("expected /lsp command to open the overlay")
			}

			view := m.lspOverlay.View()
			hasNotice := strings.Contains(view, "LSP is disabled in config.")
			if hasNotice != tc.wantNotice {
				t.Fatalf("expected disabled notice present=%v, got view: %s", tc.wantNotice, view)
			}
		})
	}
}

func TestLSPOverlay_CommandRegistered(t *testing.T) {
	t.Parallel()
	sc := lookupCommand("/lsp")
	if sc == nil {
		t.Fatal("expected /lsp to be registered in slashCommands")
	}
	action := sc.Build("")
	if !action.showLSP {
		t.Fatal("expected /lsp command to build a showLSP action")
	}
}

func TestLSPOverlay_ViewEmpty(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	if view := o.View(); view != "" {
		t.Fatal("expected empty view when closed")
	}
}

func TestLSPOverlay_NoServersConfigured(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	o = o.Open(nil, true)

	view := o.View()
	if !strings.Contains(view, "no LSP servers configured") {
		t.Fatalf("expected view to say no servers configured, got: %s", view)
	}
}

func TestLSPOverlay_DisabledLeadsWithNotice(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	sessions := []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "stopped"},
	}
	o = o.Open(sessions, false)

	view := o.View()
	if !strings.Contains(view, "LSP is disabled in config") {
		t.Fatalf("expected disabled notice, got: %s", view)
	}
	if !strings.Contains(view, "gopls") {
		t.Fatalf("expected session still listed, got: %s", view)
	}
}

func TestLSPOverlay_FailedSessionShowsError(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	sessions := []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "failed", Error: "spawn failed"},
	}
	o = o.Open(sessions, true)

	view := o.View()
	if !strings.Contains(view, "gopls") {
		t.Fatalf("expected server name in view, got: %s", view)
	}
	if !strings.Contains(view, "spawn failed") {
		t.Fatalf("expected error text in view, got: %s", view)
	}
	if !strings.Contains(view, "Failed") {
		t.Fatalf("expected status label Failed in view, got: %s", view)
	}
}

func TestLSPOverlay_FailedSessionUnknownError(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	sessions := []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "failed"},
	}
	o = o.Open(sessions, true)

	view := o.View()
	if !strings.Contains(view, "unknown error") {
		t.Fatalf("expected fallback unknown error text, got: %s", view)
	}
}

func TestLSPOverlay_ReadyAndStoppedShowTimestamps(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lastUsed := time.Date(2026, 1, 2, 4, 5, 6, 0, time.UTC)
	sessions := []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "ready", StartedAt: started, LastUsed: lastUsed},
		{Name: "ts-ls", Root: "/repo2", Status: "stopped", StartedAt: started, LastUsed: lastUsed},
	}
	o = o.Open(sessions, true)

	view := o.View()
	if !strings.Contains(view, "started 2026-01-02 03:04:05") {
		t.Fatalf("expected started timestamp, got: %s", view)
	}
	if !strings.Contains(view, "last used 2026-01-02 04:05:06") {
		t.Fatalf("expected last used timestamp, got: %s", view)
	}
}

func TestLSPOverlay_MultipleSessionsSameNameNotDeduped(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	sessions := []LSPServerStatus{
		{Name: "gopls", Root: "/repo-a", Status: "ready"},
		{Name: "gopls", Root: "/repo-b", Status: "ready"},
	}
	o = o.Open(sessions, true)

	view := o.View()
	if !strings.Contains(view, "/repo-a") || !strings.Contains(view, "/repo-b") {
		t.Fatalf("expected both sessions for the same server listed by root, got: %s", view)
	}
}

func TestLSPOverlay_ScrollClampsAtBothEnds(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 10)

	var sessions []LSPServerStatus
	for i := 0; i < 30; i++ {
		sessions = append(sessions, LSPServerStatus{Name: "gopls", Root: "/repo", Status: "ready"})
	}
	o = o.Open(sessions, true)

	if o.scrollOffset != 0 {
		t.Fatalf("expected initial scroll offset of 0, got %d", o.scrollOffset)
	}

	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if o.scrollOffset != 0 {
		t.Fatalf("expected scroll offset clamped at 0 when scrolling up from top, got %d", o.scrollOffset)
	}

	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	maxOffset := o.maxScrollOffset()
	if o.scrollOffset != maxOffset {
		t.Fatalf("expected scroll offset clamped at max %d, got %d", maxOffset, o.scrollOffset)
	}

	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if o.scrollOffset != maxOffset {
		t.Fatalf("expected scroll offset to stay clamped at max %d when scrolling past end, got %d", maxOffset, o.scrollOffset)
	}

	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if o.scrollOffset != 0 {
		t.Fatalf("expected home key to reset scroll offset to 0, got %d", o.scrollOffset)
	}
}

func TestLSPOverlay_UpdateEscCloses(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o = o.Open(nil, true)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Esc")
	}
}

func TestLSPOverlay_UpdateEnterCloses(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o = o.Open(nil, true)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Enter")
	}
}

func TestLSPOverlay_UpdateWhenClosedIsNoop(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected closed overlay to stay closed")
	}
}

func TestLSPOverlay_JKKeysScroll(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 10)

	var sessions []LSPServerStatus
	for i := 0; i < 30; i++ {
		sessions = append(sessions, LSPServerStatus{Name: "gopls", Root: "/repo", Status: "stopped"})
	}
	o = o.Open(sessions, true)

	o, _ = o.Update(tea.KeyPressMsg{Text: "j"})
	if o.scrollOffset != 1 {
		t.Fatalf("expected 'j' to scroll down by 1, got offset %d", o.scrollOffset)
	}
	o, _ = o.Update(tea.KeyPressMsg{Text: "k"})
	if o.scrollOffset != 0 {
		t.Fatalf("expected 'k' to scroll up by 1, got offset %d", o.scrollOffset)
	}
}

func TestLSPStateDisplayLabel(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"declared":    "Declared",
		"starting":    "Starting",
		"ready":       "Ready",
		"failed":      "Failed",
		"stopped":     "Stopped",
		"not started": "Not started",
		"mystery":     "mystery",
	}
	for status, want := range tests {
		if got := lspStateDisplayLabel(status); got != want {
			t.Errorf("lspStateDisplayLabel(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestLSPOverlay_SessionsSortedByNameThenRoot(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	o := newLSPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	sessions := []LSPServerStatus{
		{Name: "ts-ls", Root: "/repo", Status: "ready"},
		{Name: "gopls", Root: "/b", Status: "ready"},
		{Name: "gopls", Root: "/a", Status: "ready"},
	}
	o = o.Open(sessions, true)

	if len(o.sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(o.sessions))
	}
	want := []struct{ name, root string }{
		{"gopls", "/a"},
		{"gopls", "/b"},
		{"ts-ls", "/repo"},
	}
	for i, w := range want {
		if o.sessions[i].Name != w.name || o.sessions[i].Root != w.root {
			t.Errorf("sessions[%d] = (%q, %q), want (%q, %q)", i, o.sessions[i].Name, o.sessions[i].Root, w.name, w.root)
		}
	}
}
