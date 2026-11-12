package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// The TUI test binary renders under an ASCII colour profile (see TestMain), so
// these tests pin the phases, values and visibility of the prompt cache
// indicator, not its colours.
func TestPromptCacheStateAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		coldAt      time.Time
		wantMinutes int
		wantPhase   promptCachePhase
	}{
		{name: "untracked", wantPhase: promptCacheOff},
		{name: "well over a minute", coldAt: now.Add(10*time.Minute + 30*time.Second), wantMinutes: 11, wantPhase: promptCacheWarm},
		{name: "just over a minute", coldAt: now.Add(time.Minute + time.Second), wantMinutes: 2, wantPhase: promptCacheWarm},
		{name: "exactly a minute", coldAt: now.Add(time.Minute), wantMinutes: 1, wantPhase: promptCacheNear},
		{name: "just under a minute", coldAt: now.Add(59 * time.Second), wantMinutes: 1, wantPhase: promptCacheNear},
		{name: "exactly the deadline", coldAt: now, wantMinutes: 1, wantPhase: promptCacheNear},
		{name: "just past the deadline", coldAt: now.Add(-time.Second), wantPhase: promptCacheExpired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			minutes, phase := promptCacheStateAt(now, tt.coldAt)
			if minutes != tt.wantMinutes {
				t.Errorf("minutes = %d, want %d", minutes, tt.wantMinutes)
			}
			if phase != tt.wantPhase {
				t.Errorf("phase = %d, want %d", phase, tt.wantPhase)
			}
		})
	}
}

func TestSidebarCacheRow(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	tests := []struct {
		name  string
		state sidebarState
		want  string
	}{
		{name: "warm", state: sidebarState{styles: styles, cachePhase: promptCacheWarm, cacheMinutes: 12}, want: "~12m left"},
		{name: "near", state: sidebarState{styles: styles, cachePhase: promptCacheNear, cacheMinutes: 1}, want: "~1m left"},
		{name: "expired", state: sidebarState{styles: styles, cachePhase: promptCacheExpired}, want: "expired"},
		{name: "off", state: sidebarState{styles: styles}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line := tt.state.cacheValueLine(32)
			if tt.want == "" {
				if line != "" {
					t.Errorf("cacheValueLine() = %q, want empty when untracked", line)
				}
				return
			}
			if got := stripANSI(line); !strings.Contains(got, tt.want) {
				t.Errorf("cacheValueLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPerformanceSectionIncludesCacheValue(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	tracked := sidebarState{styles: styles, cachePhase: promptCacheNear, cacheMinutes: 1}
	lines := tracked.performanceSection(32)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "~1m left") {
		t.Fatalf("performanceSection() = %q, want the cache value", joined)
	}
	for _, line := range lines {
		if w := lipgloss.Width(stripANSI(line)); w > 32 {
			t.Errorf("performanceSection() line %q width = %d, want <= 32", stripANSI(line), w)
		}
	}

	untracked := sidebarState{styles: styles}
	if got := stripANSI(strings.Join(untracked.performanceSection(32), "\n")); strings.Contains(got, "left") {
		t.Errorf("performanceSection() = %q, must omit the cache value when untracked", got)
	}
}

func TestStatusBarShowsCacheExpired(t *testing.T) {
	t.Parallel()
	styles := testStyles(theme.AccentAmber)

	expired := stripANSI(statusState{styles: styles, promptCacheExpired: true}.view(120))
	if !strings.Contains(expired, "cache expired") {
		t.Fatalf("status bar = %q, want %q", expired, "cache expired")
	}
	warm := stripANSI(statusState{styles: styles}.view(120))
	if strings.Contains(warm, "cache expired") {
		t.Fatalf("status bar = %q, must not show the expired marker", warm)
	}
}

func TestStatusBarCacheExpiredFollowsContext(t *testing.T) {
	t.Parallel()
	plain := stripANSI(statusState{styles: testStyles(theme.AccentAmber), promptCacheExpired: true}.view(120))
	ctxAt := strings.Index(plain, "ctx ")
	cacheAt := strings.Index(plain, "cache expired")
	if ctxAt < 0 || cacheAt < 0 {
		t.Fatalf("status bar = %q, want both the ctx segment and the expired marker", plain)
	}
	if cacheAt < ctxAt {
		t.Errorf("status bar = %q, want cache expired after the ctx segment", plain)
	}
}

func TestApplyModelCallFinishedEventIgnoredDuringOneshot(t *testing.T) {
	t.Parallel()
	m := &Model{oneshotRunning: true}

	m.applyModelCallFinishedEvent(output.ModelCallFinishedEvent{Success: true, CacheColdAt: time.Now().Add(5 * time.Minute)})

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero: a oneshot phase has its own prompt cache", m.promptCacheColdAt)
	}
}

func TestOneshotFinishedEventClearsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := &Model{promptCacheColdAt: time.Now().Add(5 * time.Minute)}
	m.status.promptCacheExpired = true
	m.sidebar.cachePhase = promptCacheExpired

	m.applyPayloadEvent(output.NewOneshotFinishedEvent("run-1", nil))

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after the oneshot finished", m.promptCacheColdAt)
	}
	if m.sidebar.cachePhase != promptCacheOff {
		t.Errorf("sidebar.cachePhase = %d, want off", m.sidebar.cachePhase)
	}
	if m.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true, want false after the oneshot finished")
	}
}

func TestResetConversationUIClearsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.promptCacheColdAt = time.Now().Add(5 * time.Minute)
	m.status.promptCacheExpired = true
	m.sidebar.cachePhase = promptCacheExpired

	m.resetConversationUI()

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after /clear", m.promptCacheColdAt)
	}
	if m.sidebar.cachePhase != promptCacheOff {
		t.Errorf("sidebar.cachePhase = %d, want off after /clear", m.sidebar.cachePhase)
	}
	if m.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true, want false after /clear")
	}
}

func TestApplyModelCallFinishedEventSetsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := &Model{}
	coldAt := time.Now().Add(10 * time.Minute)

	m.applyModelCallFinishedEvent(output.ModelCallFinishedEvent{Success: true, CacheColdAt: coldAt})

	if !m.promptCacheColdAt.Equal(coldAt) {
		t.Errorf("promptCacheColdAt = %v, want %v", m.promptCacheColdAt, coldAt)
	}
	if m.sidebar.cachePhase != promptCacheWarm {
		t.Errorf("sidebar.cachePhase = %d, want warm", m.sidebar.cachePhase)
	}
	if m.sidebar.cacheMinutes != 10 {
		t.Errorf("sidebar.cacheMinutes = %d, want 10", m.sidebar.cacheMinutes)
	}
	if m.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true for a future deadline, want false")
	}
}

func TestApplyModelCallFinishedEventZeroDeadlineClears(t *testing.T) {
	t.Parallel()
	m := &Model{promptCacheColdAt: time.Now().Add(time.Minute)}
	m.status.promptCacheExpired = true
	m.sidebar.cachePhase = promptCacheExpired

	m.applyModelCallFinishedEvent(output.ModelCallFinishedEvent{Success: true})

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after a call without a deadline", m.promptCacheColdAt)
	}
	if m.sidebar.cachePhase != promptCacheOff {
		t.Errorf("sidebar.cachePhase = %d, want off", m.sidebar.cachePhase)
	}
	if m.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true, want false after the deadline was cleared")
	}
}

func TestApplyModelCallFinishedEventFailureIsIgnored(t *testing.T) {
	t.Parallel()
	deadline := time.Now().Add(5 * time.Minute)
	m := &Model{promptCacheColdAt: deadline}

	m.applyModelCallFinishedEvent(output.ModelCallFinishedEvent{Error: "boom", CacheColdAt: time.Now().Add(time.Hour)})

	if !m.promptCacheColdAt.Equal(deadline) {
		t.Errorf("promptCacheColdAt = %v, want the unchanged deadline %v", m.promptCacheColdAt, deadline)
	}
}

func TestApplyModelCallFinishedEventReplayIsIgnored(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.devLog.replaying = true

	m.applyModelCallFinishedEvent(output.ModelCallFinishedEvent{Success: true, CacheColdAt: time.Now().Add(5 * time.Minute)})

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero: a replayed call must not set the estimate", m.promptCacheColdAt)
	}
}

func TestConversationResetClearsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := &Model{promptCacheColdAt: time.Now().Add(5 * time.Minute)}
	m.status.promptCacheExpired = true
	m.sidebar.cachePhase = promptCacheExpired

	m.applyConversationReset()

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after a conversation reset", m.promptCacheColdAt)
	}
	if m.sidebar.cachePhase != promptCacheOff {
		t.Errorf("sidebar.cachePhase = %d, want off", m.sidebar.cachePhase)
	}
	if m.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true, want false after a conversation reset")
	}
}

func TestCommitModelSwitchClearsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{Controller: &testController{}})
	if m.switchAppliesAfterTurn() {
		t.Fatal("switchAppliesAfterTurn() = true on an idle session, want false")
	}
	m.promptCacheColdAt = time.Now().Add(5 * time.Minute)

	m.commitModelSwitch("large", nil)

	if !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after a successful model switch", m.promptCacheColdAt)
	}
}

// TestDeferredModelSwitchPreservesPromptCacheDeadline proves a switch that only
// applies to the next run keeps the in-flight run's estimate, so the running
// model's success event cannot race the clear.
func TestDeferredModelSwitchPreservesPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{Controller: &testController{}})
	m.oneshotRunning = true // forces the deferred switch path
	if !m.switchAppliesAfterTurn() {
		t.Fatal("switchAppliesAfterTurn() = false with a oneshot in flight, want true")
	}
	deadline := time.Now().Add(5 * time.Minute)
	m.promptCacheColdAt = deadline

	m.commitModelSwitch("large", nil)

	if !m.promptCacheColdAt.Equal(deadline) {
		t.Errorf("promptCacheColdAt = %v, want the unchanged deadline %v after a deferred switch", m.promptCacheColdAt, deadline)
	}
}

func TestCommitModelSwitchFailurePreservesPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{Controller: &testController{switchModelErr: errors.New("switch rejected")}})
	deadline := time.Now().Add(5 * time.Minute)
	m.promptCacheColdAt = deadline

	m.commitModelSwitch("large", nil)

	if !m.promptCacheColdAt.Equal(deadline) {
		t.Errorf("promptCacheColdAt = %v, want the unchanged deadline %v after a failed switch", m.promptCacheColdAt, deadline)
	}
}

func TestProfileSwitchClearsPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{Controller: &testController{}})
	m.promptCacheColdAt = time.Now().Add(5 * time.Minute)

	if _, _ = m.executeSwitchProfileAction("fast"); !m.promptCacheColdAt.IsZero() {
		t.Errorf("promptCacheColdAt = %v, want zero after a successful profile switch", m.promptCacheColdAt)
	}
}

func TestProfileSwitchFailurePreservesPromptCacheDeadline(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{Controller: &testController{err: errors.New("switch rejected")}})
	deadline := time.Now().Add(5 * time.Minute)
	m.promptCacheColdAt = deadline

	if _, _ = m.executeSwitchProfileAction("fast"); !m.promptCacheColdAt.Equal(deadline) {
		t.Errorf("promptCacheColdAt = %v, want the unchanged deadline %v after a failed profile switch", m.promptCacheColdAt, deadline)
	}
}

func TestSessionTickRefreshesPromptCachePhase(t *testing.T) {
	t.Parallel()

	expiring := &Model{promptCacheColdAt: time.Now().Add(-time.Minute)}
	expiring.sidebar.cachePhase = promptCacheWarm
	expiring.sidebar.cacheMinutes = 1
	if _, _ = expiring.handleSessionTickMsg(sessionTickMsg{gen: expiring.sessionTickGen}); !expiring.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = false after a tick past the deadline, want true")
	}
	if expiring.sidebar.cachePhase != promptCacheExpired {
		t.Errorf("sidebar.cachePhase = %d, want expired", expiring.sidebar.cachePhase)
	}

	warming := &Model{promptCacheColdAt: time.Now().Add(5 * time.Minute)}
	warming.status.promptCacheExpired = true
	warming.sidebar.cachePhase = promptCacheExpired
	if _, _ = warming.handleSessionTickMsg(sessionTickMsg{gen: warming.sessionTickGen}); warming.status.promptCacheExpired {
		t.Error("status.promptCacheExpired = true after a tick before the deadline, want false")
	}
	if warming.sidebar.cachePhase != promptCacheWarm {
		t.Errorf("sidebar.cachePhase = %d, want warm", warming.sidebar.cachePhase)
	}
}
