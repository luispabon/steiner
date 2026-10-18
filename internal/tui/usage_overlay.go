package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/quota"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	// usageRefreshTimeout caps one RefreshAll started by the overlay.
	usageRefreshTimeout = 30 * time.Second
	// usageOverlayChromeLines counts the header, dividers, footer and padding
	// rows around the scrollable body.
	usageOverlayChromeLines    = 7
	usageOverlayPreferredWidth = 100
	usageFooter                = "● active provider\nr refresh all · ↑/↓ scroll · esc close"
)

// quotaUpdateMsg reports that the quota service changed its tracked state.
type quotaUpdateMsg struct{}

// quotaRefreshDoneMsg reports that a RefreshAll started by the usage overlay
// returned.
type quotaRefreshDoneMsg struct{}

// usageViewsLoadedMsg carries views read by loadUsageViewsCmd. Update stores
// them and never reads the quota service itself.
type usageViewsLoadedMsg struct {
	views []quota.AccountView
}

// usageOverlayState is the /usage subscription usage overlay. views is the
// last loaded cache, rendered as-is; inFlight counts RefreshAll calls not yet
// returned. A nil quota leaves the overlay unavailable.
type usageOverlayState struct {
	OverlayShell
	quota           *quota.Service
	refreshInterval time.Duration
	views           []quota.AccountView
	// bodyCount is the number of body lines views render to. See withViews.
	bodyCount    int
	scrollOffset int
	inFlight     int
	spinnerFrame int
	styles       *theme.Styles
	now          func() time.Time
}

func newUsageOverlay(styles *theme.Styles, q *quota.Service, refreshInterval time.Duration) usageOverlayState {
	return usageOverlayState{
		OverlayShell:    OverlayShell{}.WithPreferredWidth(usageOverlayPreferredWidth),
		quota:           q,
		refreshInterval: refreshInterval,
		styles:          styles,
		now:             time.Now,
	}.withViews(nil)
}

// Open returns a copy of the overlay in the open state, rendering the cached
// views. It does no I/O.
func (o usageOverlayState) Open() usageOverlayState {
	o.OverlayShell = o.openShell()
	o.scrollOffset = 0
	return o
}

// Close returns a copy of the overlay in the closed state.
func (o usageOverlayState) Close() usageOverlayState {
	o.OverlayShell = o.closeShell()
	return o
}

// withViews returns a copy of the overlay caching views and bodyCount, the
// number of lines their body renders. The count comes from a render, so it
// cannot drift from the body. It stays valid while the overlay is open because
// the line count does not depend on the clock, refresh state or width: rows are
// truncated, never wrapped.
func (o usageOverlayState) withViews(views []quota.AccountView) usageOverlayState {
	o.views = views
	o.bodyCount = len(o.bodyLines())
	return o
}

func (o usageOverlayState) refreshing() bool {
	if o.inFlight > 0 {
		return true
	}
	return slices.ContainsFunc(o.views, func(v quota.AccountView) bool {
		return v.Refreshing
	})
}

// startRefresh counts one RefreshAll as in flight and returns the command that
// runs it. The command is bounded by usageRefreshTimeout and reports
// quotaRefreshDoneMsg when it returns.
func (o usageOverlayState) startRefresh() (usageOverlayState, tea.Cmd) {
	if o.quota == nil {
		return o, nil
	}
	o.inFlight++
	q := o.quota
	return o, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), usageRefreshTimeout)
		defer cancel()
		q.RefreshAll(ctx)
		return quotaRefreshDoneMsg{}
	}
}

// Update handles keys while the overlay is open: ctrl+o and esc close it, r
// refreshes all accounts, and the shared scroll keys move the body.
func (o usageOverlayState) Update(msg tea.Msg) (usageOverlayState, tea.Cmd) {
	if !o.IsOpen() {
		return o, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case isCtrl(key, 'o'):
			return o.Close(), nil
		case key.Text == "r":
			return o.startRefresh()
		}
	}
	result := o.handleScrollKey(msg, o.scrollOffset, o.visibleHeight(), o.bodyCount)
	if !result.handled {
		return o, nil
	}
	if result.close {
		return o.Close(), nil
	}
	o.scrollOffset = result.offset
	return o, nil
}

// View renders the overlay, or "" while it is closed.
func (o usageOverlayState) View() string {
	if !o.IsOpen() {
		return ""
	}
	lines := o.bodyLines()
	visible := o.visibleHeight()
	start := min(o.scrollOffset, max(len(lines)-visible, 0))
	end := min(len(lines), start+visible)
	body := lipgloss.JoinVertical(lipgloss.Left, lines[start:end]...)
	full := lipgloss.JoinVertical(lipgloss.Left,
		o.styles.CardLabel.Render("Subscription usage"),
		o.Divider(),
		body,
		o.Divider(),
		o.RenderFooter(usageFooter),
	)
	return o.RenderWithBg(o.styles.PaletteOverlay, full, theme.BgElev)
}

// bodyLines renders the body from the cached views and the overlay clock.
func (o usageOverlayState) bodyLines() []string {
	return usageBodyLines(usageFrame{
		views:        o.views,
		now:          o.now(),
		refreshing:   o.inFlight > 0,
		spinnerFrame: o.spinnerFrame,
		styles:       o.styles,
		width:        o.InnerWidth(),
	})
}

// visibleHeight returns the number of body rows that fit the terminal height.
func (o usageOverlayState) visibleHeight() int {
	return o.bodyVisibleHeight(usageOverlayChromeLines)
}

// loadUsageViewsCmd reads the quota views for activeAlias. Quota.Views reads
// the history store, so it runs only inside this command.
func loadUsageViewsCmd(q *quota.Service, activeAlias string) tea.Cmd {
	return func() tea.Msg {
		return usageViewsLoadedMsg{views: q.Views(activeAlias)}
	}
}

// openUsageOverlay opens the overlay on the cached views and starts a refresh
// and a views load. It is a no-op when no quota service is configured.
func (m *Model) openUsageOverlay() tea.Cmd {
	q := m.usageOverlay.quota
	if q == nil {
		return nil
	}
	m.usageOverlay = m.usageOverlay.Open()
	m.usageOverlay.OverlayShell = m.usageOverlay.WithDimensions(m.width, m.height)
	var refresh tea.Cmd
	m.usageOverlay, refresh = m.usageOverlay.startRefresh()
	return tea.Batch(refresh, loadUsageViewsCmd(q, m.sidebar.providerName), m.ensureTicking())
}

// usageRunFinishedCmd returns a command that refetches used accounts older
// than the refresh interval after a run finishes. When the overlay is open it
// also reloads the views. It returns nil when no service or no positive
// interval is configured.
func (m *Model) usageRunFinishedCmd() tea.Cmd {
	q, interval := m.usageOverlay.quota, m.usageOverlay.refreshInterval
	if q == nil || interval <= 0 {
		return nil
	}
	alias := m.sidebar.providerName
	reload := m.usageOverlay.IsOpen()
	return func() tea.Msg {
		q.RefreshUsed(context.Background(), interval)
		if !reload {
			return nil
		}
		return usageViewsLoadedMsg{views: q.Views(alias)}
	}
}

// waitForQuotaUpdate blocks until the quota service reports a change. Update
// re-arms it after every quotaUpdateMsg, so one waiter stays armed.
func waitForQuotaUpdate(updates <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-updates; !ok {
			return nil
		}
		return quotaUpdateMsg{}
	}
}

// handleQuotaUpdateMsg re-arms the waiter whatever the overlay state. When the
// overlay is open it also loads the views in a command.
func (m *Model) handleQuotaUpdateMsg() (tea.Model, tea.Cmd) {
	q := m.usageOverlay.quota
	if q == nil {
		return m, nil
	}
	rearm := waitForQuotaUpdate(q.Updates())
	if !m.usageOverlay.IsOpen() {
		return m, rearm
	}
	return m, tea.Batch(rearm, loadUsageViewsCmd(q, m.sidebar.providerName))
}

// handleQuotaRefreshDoneMsg settles one in-flight refresh. When the overlay is
// open it loads the views in a command.
func (m *Model) handleQuotaRefreshDoneMsg() (tea.Model, tea.Cmd) {
	if m.usageOverlay.inFlight > 0 {
		m.usageOverlay.inFlight--
	}
	if m.usageOverlay.quota == nil || !m.usageOverlay.IsOpen() {
		return m, nil
	}
	return m, loadUsageViewsCmd(m.usageOverlay.quota, m.sidebar.providerName)
}

// handleUsageViewsLoadedMsg caches the views read by a loadUsageViewsCmd.
func (m *Model) handleUsageViewsLoadedMsg(msg usageViewsLoadedMsg) (tea.Model, tea.Cmd) {
	m.usageOverlay = m.usageOverlay.withViews(msg.views)
	if m.usageOverlay.IsOpen() && m.usageOverlay.refreshing() {
		return m, m.ensureTicking()
	}
	return m, nil
}
