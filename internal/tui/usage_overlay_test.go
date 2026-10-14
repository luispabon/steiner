package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/quota"
)

// usageTestFetcher counts fetches and returns a fixed snapshot for claude:work.
type usageTestFetcher struct {
	calls atomic.Int32
}

func (f *usageTestFetcher) Fetch(_ context.Context, _ quota.FetchOptions) (quota.Snapshot, error) {
	f.calls.Add(1)
	return usageTestSnapshot(), nil
}

func usageTestSnapshot() quota.Snapshot {
	return quota.Snapshot{
		AccountKey:   "claude:work",
		ProviderType: "claude_subscription",
		Plan:         "Max",
		Windows:      []quota.Window{{Label: "5h", UsedPercent: 46, ResetsAt: time.Now().Add(2 * time.Hour)}},
		FetchedAt:    time.Now(),
	}
}

func newUsageTestService(f quota.Fetcher) *quota.Service {
	svc := quota.NewService(quota.ServiceOptions{})
	svc.Register(quota.Account{
		Key:          "claude:work",
		ProviderType: "claude_subscription",
		Aliases:      []string{"work"},
		Fetcher:      f,
	})
	return svc
}

// newUsageTestModel builds a sized model whose active provider alias is work.
func newUsageTestModel(svc *quota.Service, interval time.Duration) *Model {
	m := newModel(Config{ProviderName: "work", Quota: svc, QuotaRefreshInterval: interval}, nil)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func usageCtrlO() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
}

// usageRunCmds runs cmd and returns the messages it produces, expanding a
// tea.BatchMsg. Use it only for commands that do not block, such as the
// refresh and views-load commands; never run the quota waiter this way.
func usageRunCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		if c != nil {
			out = append(out, c())
		}
	}
	return out
}

func TestUsageOverlayCtrlOTogglesAndEscCloses(t *testing.T) {
	m := newUsageTestModel(newUsageTestService(&usageTestFetcher{}), 0)
	_, _ = m.Update(usageCtrlO())
	if !m.usageOverlay.IsOpen() {
		t.Fatal("ctrl+o did not open the usage overlay")
	}
	_, _ = m.Update(usageCtrlO())
	if m.usageOverlay.IsOpen() {
		t.Error("second ctrl+o did not close the usage overlay")
	}
	_, _ = m.Update(usageCtrlO())
	_, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.usageOverlay.IsOpen() {
		t.Error("esc did not close the usage overlay")
	}
}

func TestUsageOverlayNilQuotaIsNoop(t *testing.T) {
	m := newModel(Config{}, nil)
	_, _ = m.Update(usageCtrlO())
	if m.usageOverlay.IsOpen() {
		t.Error("ctrl+o opened the overlay without a quota service")
	}
	if cmd := m.openUsageOverlay(); cmd != nil {
		t.Error("openUsageOverlay returned a command without a quota service")
	}
	if cmd := m.usageRunFinishedCmd(); cmd != nil {
		t.Error("usageRunFinishedCmd returned a command without a quota service")
	}
	if _, cmd := m.handleQuotaUpdateMsg(); cmd != nil {
		t.Error("quota update re-armed a waiter without a quota service")
	}
	if _, cmd := m.handleQuotaRefreshDoneMsg(); cmd != nil {
		t.Error("refresh done returned a command without a quota service")
	}
}

func TestUsageOverlayOpenLoadsViewsOnlyThroughCommands(t *testing.T) {
	f := &usageTestFetcher{}
	m := newUsageTestModel(newUsageTestService(f), 0)
	cmd := m.openUsageOverlay()
	if !m.usageOverlay.IsOpen() {
		t.Fatal("openUsageOverlay did not open the overlay")
	}
	if len(m.usageOverlay.views) != 0 {
		t.Fatalf("open read views synchronously: %+v", m.usageOverlay.views)
	}
	if m.usageOverlay.inFlight != 1 {
		t.Fatalf("inFlight = %d after open, want 1", m.usageOverlay.inFlight)
	}
	view := ansi.Strip(m.usageOverlay.View())
	for _, want := range []string{"Subscription usage", "● active provider", "r refresh all · ↑/↓ scroll · esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("overlay view missing %q", want)
		}
	}
	for _, msg := range usageRunCmds(cmd) {
		_, _ = m.Update(msg)
	}
	if len(m.usageOverlay.views) != 1 || m.usageOverlay.views[0].Latest == nil {
		t.Errorf("views after loaded message = %+v, want one view with a snapshot", m.usageOverlay.views)
	}
	if m.usageOverlay.inFlight != 0 {
		t.Errorf("inFlight = %d after refresh done, want 0", m.usageOverlay.inFlight)
	}
	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetch calls = %d, want 1", got)
	}
}

func TestUsageViewsLoadedMsgOnlyStores(t *testing.T) {
	m := newUsageTestModel(newUsageTestService(&usageTestFetcher{}), 0)
	_ = m.openUsageOverlay()
	views := []quota.AccountView{{DisplayAlias: "work", Active: true}}
	_, cmd := m.Update(usageViewsLoadedMsg{views: views})
	if cmd != nil {
		t.Error("views loaded message returned a command")
	}
	if len(m.usageOverlay.views) != 1 || m.usageOverlay.views[0].DisplayAlias != "work" {
		t.Errorf("views = %+v, want the message payload", m.usageOverlay.views)
	}
}

func TestUsageOverlayRKeyRepeatsRefreshAll(t *testing.T) {
	m := newUsageTestModel(newUsageTestService(&usageTestFetcher{}), 0)
	_ = m.openUsageOverlay()
	_, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.usageOverlay.inFlight != 2 {
		t.Errorf("inFlight = %d after r, want 2", m.usageOverlay.inFlight)
	}
}

func TestUsageOverlayRunFinishedRefreshIsConditional(t *testing.T) {
	tests := []struct {
		name     string
		withSvc  bool
		interval time.Duration
		open     bool
		wantCmd  bool
	}{
		{"no service", false, time.Hour, false, false},
		{"zero interval", true, 0, true, false},
		{"negative interval", true, -time.Second, false, false},
		{"configured closed", true, time.Hour, false, true},
		{"configured open", true, time.Hour, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &usageTestFetcher{}
			var svc *quota.Service
			if tt.withSvc {
				svc = newUsageTestService(f)
				svc.MarkUsed("work")
			}
			m := newUsageTestModel(svc, tt.interval)
			if tt.open && svc != nil {
				m.usageOverlay = m.usageOverlay.Open()
			}
			cmd := m.usageRunFinishedCmd()
			if (cmd != nil) != tt.wantCmd {
				t.Fatalf("command non-nil = %v, want %v", cmd != nil, tt.wantCmd)
			}
			if cmd == nil {
				return
			}
			msg := cmd()
			if got := f.calls.Load() > 0; !got {
				t.Error("run finished command did not refetch the used account")
			}
			if _, ok := msg.(usageViewsLoadedMsg); ok != tt.open {
				t.Errorf("message = %T, want views load only when open=%v", msg, tt.open)
			}
		})
	}
}

func TestUsageOverlayUpdateRearmsWaiter(t *testing.T) {
	svc := newUsageTestService(&usageTestFetcher{})
	m := newUsageTestModel(svc, 0)
	_, cmd := m.handleQuotaUpdateMsg()
	if cmd == nil {
		t.Fatal("quota update did not re-arm the waiter")
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	svc.Observe(usageTestSnapshot())
	select {
	case msg := <-got:
		if _, ok := msg.(quotaUpdateMsg); !ok {
			t.Errorf("re-armed waiter returned %T, want quotaUpdateMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("re-armed waiter did not receive the quota update")
	}
	if m.usageOverlay.views != nil {
		t.Error("closed overlay loaded views")
	}
}

func TestUsageOverlayUpdateWhenOpenOnlySchedulesLoad(t *testing.T) {
	svc := newUsageTestService(&usageTestFetcher{})
	m := newUsageTestModel(svc, 0)
	_ = m.openUsageOverlay()
	svc.Observe(usageTestSnapshot())
	_, cmd := m.handleQuotaUpdateMsg()
	if len(m.usageOverlay.views) != 0 {
		t.Fatal("quota update read views inside Update")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("open overlay update command = %T, want waiter and views load", cmd)
	}
	msg := loadUsageViewsCmd(svc, "work")()
	_, _ = m.Update(msg)
	if len(m.usageOverlay.views) != 1 || m.usageOverlay.views[0].Latest == nil {
		t.Error("views load command did not populate the overlay")
	}
}

func TestUsageOverlayRefreshDoneSchedulesLoadWhenOpen(t *testing.T) {
	svc := newUsageTestService(&usageTestFetcher{})
	m := newUsageTestModel(svc, 0)
	_ = m.openUsageOverlay()
	_, cmd := m.handleQuotaRefreshDoneMsg()
	if len(m.usageOverlay.views) != 0 {
		t.Fatal("refresh done read views inside Update")
	}
	if _, ok := cmd().(usageViewsLoadedMsg); !ok {
		t.Errorf("refresh done command = %T, want usageViewsLoadedMsg", cmd)
	}
}

func TestUsageOverlayEmptyStateAndFooter(t *testing.T) {
	m := newUsageTestModel(quota.NewService(quota.ServiceOptions{}), 0)
	_ = m.openUsageOverlay()
	view := ansi.Strip(m.usageOverlay.View())
	for _, want := range []string{"No configured providers report subscription usage.", "● active provider", "r refresh all · ↑/↓ scroll · esc close"} {
		if !strings.Contains(view, want) {
			t.Errorf("overlay view missing %q", want)
		}
	}
}

func TestUsageOverlayViewClampsScrollOffset(t *testing.T) {
	m := newUsageTestModel(newUsageTestService(&usageTestFetcher{}), 0)
	_ = m.openUsageOverlay()
	m.usageOverlay.height = 10
	m.usageOverlay.scrollOffset = 999
	if view := ansi.Strip(m.usageOverlay.View()); !strings.Contains(view, "esc close") {
		t.Error("overlay with an out-of-range scroll offset lost its footer")
	}
}

// usageTestFullView is one account with two windows, a trend row, a budget
// with a limit, a spend row and credits.
func usageTestFullView() quota.AccountView {
	limit := 10.0
	return quota.AccountView{
		ProviderType: "claude_subscription",
		DisplayAlias: "work",
		Active:       true,
		Latest: &quota.Snapshot{
			Plan:      "Max",
			FetchedAt: usageTestNow.Add(-time.Minute),
			Windows: []quota.Window{
				{Label: "5h", UsedPercent: 80, ResetsAt: usageTestNow.Add(3 * time.Hour)},
				{Label: "7d", UsedPercent: 20},
			},
			Budgets: []quota.Budget{
				{Label: "spend", Used: 4.5, Limit: &limit, Period: "monthly"},
				{Label: "extra", Used: 1},
			},
			Credits: "$3.00",
		},
		WindowProjections: []quota.Projection{{OK: true, BeforeReset: true, EmptyAt: usageTestNow.Add(time.Hour)}},
	}
}

func TestUsageBodyCountMatchesRenderedBody(t *testing.T) {
	tests := []struct {
		name  string
		views []quota.AccountView
		want  int
	}{
		{name: "empty", views: nil, want: 1},
		{name: "unavailable", views: []quota.AccountView{{DisplayAlias: "work", Unavailable: "no credentials"}}, want: 1},
		{name: "error without snapshot", views: []quota.AccountView{{DisplayAlias: "work", Err: "upstream failed"}}, want: 1},
		{name: "full account", views: []quota.AccountView{usageTestFullView()}, want: 7},
		{name: "two accounts", views: []quota.AccountView{usageTestFullView(), {DisplayAlias: "home", Unavailable: "no credentials"}}, want: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			o := m.usageOverlay
			o.now = func() time.Time { return usageTestNow }
			o = o.withViews(tt.views)
			if o.bodyCount != tt.want {
				t.Fatalf("bodyCount = %d, want %d", o.bodyCount, tt.want)
			}
			if got := len(o.bodyLines()); got != tt.want {
				t.Fatalf("rendered body lines = %d, want %d", got, tt.want)
			}
			// The count must hold when the clock, refresh state and width change.
			o.now = func() time.Time { return usageTestNow.Add(72 * time.Hour) }
			o.inFlight = 1
			o.OverlayShell = o.WithDimensions(20, 40)
			if got := len(o.bodyLines()); got != o.bodyCount {
				t.Errorf("rendered body lines = %d after clock, refresh and width change, want bodyCount %d", got, o.bodyCount)
			}
		})
	}
}

func TestUsageOverlayScrollUsesCachedBodyCount(t *testing.T) {
	m := newModel(Config{}, nil)
	o := m.usageOverlay.Open()
	o.OverlayShell = o.WithDimensions(120, 12) // 5 visible body rows
	renders := 0
	o.now = func() time.Time {
		renders++
		return usageTestNow
	}
	o = o.withViews([]quota.AccountView{usageTestFullView(), usageTestFullView()})
	visible := o.visibleHeight()

	renders = 0
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if renders != 0 {
		t.Errorf("End key rendered the body %d times, want 0", renders)
	}
	if want := o.bodyCount - visible; o.scrollOffset != want {
		t.Errorf("scrollOffset after End = %d, want %d", o.scrollOffset, want)
	}

	o = o.withViews(nil)
	o, _ = o.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if o.scrollOffset != 0 {
		t.Errorf("scrollOffset after views shrink and End = %d, want 0", o.scrollOffset)
	}
}

func TestUsageSlashCommandRouting(t *testing.T) {
	var found bool
	for _, c := range slashCommands {
		if c.ID != "/usage" {
			continue
		}
		found = true
		if action := c.Build(""); !action.showUsage {
			t.Errorf("/usage Build = %+v, want showUsage", action)
		}
	}
	if !found {
		t.Fatal("slash command registry has no /usage entry")
	}

	unconfigured := newModel(Config{}, nil)
	if _, cmd := unconfigured.executeShowUsageAction(); cmd != nil || unconfigured.usageOverlay.IsOpen() {
		t.Error("/usage without a quota service opened the overlay or returned a command")
	}

	m := newUsageTestModel(newUsageTestService(&usageTestFetcher{}), 0)
	if _, cmd := m.executeShowUsageAction(); cmd == nil || !m.usageOverlay.IsOpen() {
		t.Error("/usage with a quota service did not open the overlay with a command")
	}
}

func TestUsageHelpListsCtrlOAfterCtrlT(t *testing.T) {
	m := newModel(Config{}, nil)
	lines := strings.Split(ansi.Strip(renderHelp(m.styles, 100)), "\n")
	ctrlT, ctrlO := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "ctrl+t"):
			ctrlT = i
		case strings.Contains(l, "ctrl+o") && strings.Contains(l, "usage dashboard"):
			ctrlO = i
		}
	}
	if ctrlO < 0 {
		t.Fatal("help has no ctrl+o usage dashboard row")
	}
	if ctrlO != ctrlT+1 {
		t.Errorf("ctrl+o row at %d, want directly after ctrl+t at %d", ctrlO, ctrlT)
	}
}
