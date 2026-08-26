package tui

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestNeedsTickingReturnsTrueWhenMCPConnecting(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		sidebar: sidebarState{
			mcpConnecting: true,
			styles:        styles,
		},
		content: contentBuffer{styles: styles},
	}
	if !m.needsTicking() {
		t.Errorf("needsTicking() = false, want true when mcpConnecting is true")
	}
}

func TestNeedsTickingReturnsFalseWhenMCPSettled(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		sidebar: sidebarState{
			mcpConnecting: false,
			styles:        styles,
		},
		content: contentBuffer{styles: styles},
	}
	if m.needsTicking() {
		t.Errorf("needsTicking() = true, want false when mcpConnecting is false and no other tick conditions")
	}
}

func TestHandleTickMsgAdvancesSidebarTickCountWhenMCPConnecting(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := Model{
		styles: styles,
		sidebar: sidebarState{
			mcpConnecting: true,
			tickCount:     0,
			styles:        styles,
		},
		content: contentBuffer{styles: styles, tickCount: 0},
	}

	msg := tickMsg{}
	updated, _ := m.Update(msg)
	m2 := updated.(*Model)

	// After tick, sidebar tickCount should be advanced
	if m2.sidebar.tickCount != 1 {
		t.Errorf("sidebar.tickCount = %d, want 1 after tick", m2.sidebar.tickCount)
	}

	// needsTicking should still be true while mcpConnecting
	if !m2.needsTicking() {
		t.Errorf("needsTicking() = false, want true when mcpConnecting is still true")
	}
}

func TestSessionTickIntervalIsOneSecond(t *testing.T) {
	t.Parallel()
	if sessionTickInterval != time.Second {
		t.Errorf("sessionTickInterval = %v, want 1s cadence", sessionTickInterval)
	}
}

func TestSyncSidebarComputesSessionElapsedSeconds(t *testing.T) {
	t.Parallel()
	started := time.Now().Add(-(67*time.Minute + 30*time.Second))
	m := &Model{sessionStartedAt: &started, sidebar: sidebarState{styles: testStyles(theme.AccentAmber)}}

	m.syncSidebar()

	if !m.sidebar.sessionActive {
		t.Fatal("sidebar.sessionActive = false, want true")
	}
	if m.sidebar.sessionElapsedSec < 4050 {
		t.Errorf("sidebar.sessionElapsedSec = %d, want at least 4050", m.sidebar.sessionElapsedSec)
	}
	if m.sidebar.sessionElapsedSec > 4080 {
		t.Errorf("sidebar.sessionElapsedSec = %d, want at most 4080", m.sidebar.sessionElapsedSec)
	}
}

func TestSessionTickArmsAndStopsBySessionStart(t *testing.T) {
	t.Parallel()

	// With a session start, a sessionTickMsg re-arms the session timer.
	started := time.Now()
	active := &Model{sessionStartedAt: &started}
	updated, cmd := active.Update(sessionTickMsg{})
	if cmd == nil {
		t.Fatal("Update(sessionTickMsg{}) with sessionStartedAt set returned nil cmd, want an arming sessionTickCmd")
	}
	if got := updated.(*Model).sidebar.sessionActive; !got {
		t.Errorf("sidebar.sessionActive = false after tick, want true")
	}

	// After a reset (sessionStartedAt nil), the tick stops chaining.
	idle := &Model{}
	_, cmd = idle.Update(sessionTickMsg{})
	if cmd != nil {
		t.Errorf("Update(sessionTickMsg{}) with sessionStartedAt nil returned non-nil cmd, want nil to stop the timer")
	}
	if got := idle.sidebar.sessionActive; got {
		t.Errorf("sidebar.sessionActive = true after reset, want false")
	}
}
func TestSyncSidebarResetsAndDetectsMCPConnecting(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		mcpServers: []MCPServerStatus{
			{Name: "server1", State: "connecting"},
			{Name: "server2", State: "connected"},
		},
		sidebar: sidebarState{styles: styles},
	}
	m.syncSidebar()

	if !m.sidebar.mcpConnecting {
		t.Errorf("sidebar.mcpConnecting = false, want true when a server is connecting")
	}
	if m.sidebar.mcpTotal != 2 {
		t.Errorf("sidebar.mcpTotal = %d, want 2", m.sidebar.mcpTotal)
	}
	if m.sidebar.mcpConnected != 1 {
		t.Errorf("sidebar.mcpConnected = %d, want 1", m.sidebar.mcpConnected)
	}
}

func TestSyncSidebarDetectsReconnecting(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		mcpServers: []MCPServerStatus{
			{Name: "server1", State: "reconnecting"},
		},
		sidebar: sidebarState{styles: styles},
	}
	m.syncSidebar()

	if !m.sidebar.mcpConnecting {
		t.Errorf("sidebar.mcpConnecting = false, want true when a server is reconnecting")
	}
}

func TestSyncSidebarClearsMCPConnectingWhenSettled(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		mcpServers: []MCPServerStatus{
			{Name: "server1", State: "connected"},
		},
		sidebar: sidebarState{mcpConnecting: true, styles: styles},
	}
	m.syncSidebar()

	if m.sidebar.mcpConnecting {
		t.Errorf("sidebar.mcpConnecting = true, want false when all servers are settled")
	}
}

func TestSyncSidebarExcludesDisabledServers(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles: styles,
		mcpServers: []MCPServerStatus{
			{Name: "server1", State: "disabled"},
			{Name: "server2", State: "connecting"},
		},
		sidebar: sidebarState{styles: styles},
	}
	m.syncSidebar()

	if m.sidebar.mcpTotal != 1 {
		t.Errorf("sidebar.mcpTotal = %d, want 1 (disabled excluded)", m.sidebar.mcpTotal)
	}
	if !m.sidebar.mcpConnecting {
		t.Errorf("sidebar.mcpConnecting = false, want true (connecting server included)")
	}
}

func TestNewModelHasTickingTrue(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "test"}, nil)
	if !m.ticking {
		t.Errorf("newModel() ticking = false, want true")
	}
}

func TestEnsureTickingReturnsNilWhenTickingAlreadyTrue(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		styles:  styles,
		ticking: true,
		sidebar: sidebarState{styles: styles},
		content: contentBuffer{styles: styles},
	}
	cmd := m.ensureTicking()
	if cmd != nil {
		t.Errorf("ensureTicking() returned %v, want nil when ticking already true", cmd)
	}
}
