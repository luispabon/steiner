package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestMCPOverlay_NewClose(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
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

func TestMCPOverlay_ViewEmpty(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	if view := o.View(); view != "" {
		t.Fatal("expected empty view when closed")
	}
}

func TestMCPOverlay_NoServersConfigured(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	o = o.Open(nil, true)

	view := o.View()
	if !strings.Contains(view, "no MCP servers configured") {
		t.Fatalf("expected view to say no servers configured, got: %s", view)
	}
}

func TestMCPOverlay_DisabledLeadsWithNotice(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "srv-a", State: "disabled", Transport: "stdio"},
	}
	o = o.Open(servers, false)

	view := o.View()
	if !strings.Contains(view, "MCP is disabled in config") {
		t.Fatalf("expected disabled notice, got: %s", view)
	}
	if !strings.Contains(view, "srv-a") {
		t.Fatalf("expected disabled server still listed, got: %s", view)
	}
	if !strings.Contains(view, "Disabled") {
		t.Fatalf("expected server state to show Disabled, got: %s", view)
	}
}

func TestMCPOverlay_FailedServerShowsError(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "broken-srv", State: "failed", Transport: "stdio", Error: "connection refused"},
	}
	o = o.Open(servers, true)

	view := o.View()
	if !strings.Contains(view, "broken-srv") {
		t.Fatalf("expected server name in view, got: %s", view)
	}
	if !strings.Contains(view, "connection refused") {
		t.Fatalf("expected error text in view, got: %s", view)
	}
}

func TestMCPOverlay_ConnectedServerWithNoTools(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "empty-srv", State: "connected", Transport: "stdio"},
	}
	o = o.Open(servers, true)

	view := o.View()
	if !strings.Contains(view, "no tools advertised") {
		t.Fatalf("expected explicit no-tools note, got: %s", view)
	}
}

func TestMCPOverlay_ConnectedServerListsRegisteredTools(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "good-srv", State: "connected", Transport: "stdio", Tools: []MCPToolStatus{
			{Name: "alpha", Outcome: "registered"},
			{Name: "beta", Outcome: "registered"},
			{Name: "gamma", Outcome: "registered"},
		}},
	}
	o = o.Open(servers, true)

	view := o.View()
	for _, want := range []string{"alpha (registered)", "beta (registered)", "gamma (registered)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got: %s", want, view)
		}
	}
}

func TestMCPOverlay_MixedOutcomesShowStatusLabels(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "mixed-srv", State: "connected", Transport: "stdio", Tools: []MCPToolStatus{
			{Name: "alpha", Outcome: "registered"},
			{Name: "beta", Outcome: "filtered"},
			{Name: "gamma", Outcome: "denied"},
		}},
	}
	o = o.Open(servers, true)

	view := o.View()
	for _, want := range []string{"alpha (registered)", "beta (filtered)", "gamma (denied)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got: %s", want, view)
		}
	}
	// Filtered and denied entries are dimmed; registered stays plain.
	for _, want := range []string{s.FgMute.Render("beta (filtered)"), s.FgMute.Render("gamma (denied)")} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected dimmed render %q in view, got: %s", want, view)
		}
	}
	if strings.Contains(view, s.FgMute.Render("alpha (registered)")) {
		t.Fatalf("registered tool rendered dimmed, got: %s", view)
	}
}

func TestMCPOverlay_DenyOnlyServerShowsAllDenied(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "deny-srv", State: "connected", Transport: "stdio", Tools: []MCPToolStatus{
			{Name: "delta", Outcome: "denied"},
			{Name: "epsilon", Outcome: "denied"},
		}},
	}
	o = o.Open(servers, true)

	view := o.View()
	for _, want := range []string{"delta (denied)", "epsilon (denied)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got: %s", want, view)
		}
	}
	if strings.Contains(view, "registered") {
		t.Fatalf("deny-only server shows a registered tool, got: %s", view)
	}
}

func TestMCPOverlay_ServerNameWithUnderscoresRendersIntact(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "my_tool_srv", State: "connected", Transport: "stdio", Tools: []MCPToolStatus{{Name: "do_thing", Outcome: "registered"}}},
	}
	o = o.Open(servers, true)

	view := o.View()
	if !strings.Contains(view, "my_tool_srv") {
		t.Fatalf("expected server name with underscores to render intact, got: %s", view)
	}
}

func TestMCPOverlay_ScrollClampsAtBothEnds(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 10)

	var servers []MCPServerStatus
	for i := 0; i < 30; i++ {
		servers = append(servers, MCPServerStatus{
			Name:      fmt.Sprintf("srv-%d", i),
			State:     "connected",
			Transport: "stdio",
			Tools:     []MCPToolStatus{{Name: "tool-a", Outcome: "registered"}},
		})
	}
	o = o.Open(servers, true)

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

func TestMCPOverlay_UpdateEscCloses(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o = o.Open(nil, true)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Esc")
	}
}

func TestMCPOverlay_UpdateEnterCloses(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o = o.Open(nil, true)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.IsOpen() {
		t.Fatal("expected overlay to close on Enter")
	}
}

func TestMCPOverlay_UpdateWhenClosedIsNoop(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)

	updated, _ := o.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.IsOpen() {
		t.Fatal("expected closed overlay to stay closed")
	}
}

func TestMCPOverlay_JKKeysScroll(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 10)

	var servers []MCPServerStatus
	for i := 0; i < 30; i++ {
		servers = append(servers, MCPServerStatus{Name: fmt.Sprintf("srv-%d", i), State: "disabled", Transport: "stdio"})
	}
	o = o.Open(servers, true)

	o, _ = o.Update(tea.KeyPressMsg{Text: "j"})
	if o.scrollOffset != 1 {
		t.Fatalf("expected 'j' to scroll down by 1, got offset %d", o.scrollOffset)
	}
	o, _ = o.Update(tea.KeyPressMsg{Text: "k"})
	if o.scrollOffset != 0 {
		t.Fatalf("expected 'k' to scroll up by 1, got offset %d", o.scrollOffset)
	}
}

func TestMCPOverlay_SlashCommandOpensOverlay(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		MCPEnabled: true,
		MCPServers: []MCPServerStatus{{Name: "srv-a", State: "connected", Transport: "stdio", Tools: []MCPToolStatus{{Name: "tool-a", Outcome: "registered"}}}},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/mcp")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.mcpOverlay.IsOpen() {
		t.Fatal("expected /mcp command to open the overlay")
	}

	view := m.mcpOverlay.View()
	if !strings.Contains(view, "srv-a") {
		t.Fatalf("expected overlay to list configured server, got: %s", view)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.mcpOverlay.IsOpen() {
		t.Fatal("expected esc to close the overlay")
	}
}

func TestMCPOverlay_CommandRegistered(t *testing.T) {
	t.Parallel()
	sc := lookupCommand("/mcp")
	if sc == nil {
		t.Fatal("expected /mcp to be registered in slashCommands")
	}
	action := sc.Build("")
	if !action.showMCP {
		t.Fatal("expected /mcp command to build a showMCP action")
	}
}

func TestStateDisplayLabelLiveStates(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"connected":    "Connected",
		"failed":       "Failed",
		"disabled":     "Disabled",
		"connecting":   "Connecting",
		"reconnecting": "Reconnecting",
		"unavailable":  "Unavailable",
		"mystery":      "mystery",
	}
	for state, want := range tests {
		if got := stateDisplayLabel(state); got != want {
			t.Errorf("stateDisplayLabel(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestMCPOverlay_LiveStatesRenderWithoutFallback(t *testing.T) {
	t.Parallel()
	s := theme.BuildStyles("#ff0000")
	o := newMCPOverlay(s)
	o.OverlayShell = o.WithDimensions(80, 24)
	servers := []MCPServerStatus{
		{Name: "pending", State: "connecting", Transport: "stdio"},
		{Name: "retry", State: "reconnecting", Transport: "stdio"},
		{Name: "down", State: "unavailable", Transport: "stdio", Error: "exhausted retries"},
	}
	o = o.Open(servers, true)

	view := o.View()
	for _, want := range []string{"Connecting", "Reconnecting", "Unavailable", "exhausted retries"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected view to contain %q, got: %s", want, view)
		}
	}
}
