package config

import (
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestDefaultTUIFPS pins the shipped renderer frame rate. Changing it changes
// both perceived input latency and TUI CPU cost for every user, so it should
// never move silently.
func TestDefaultTUIFPS(t *testing.T) {
	t.Parallel()
	if got := defaultConfig(nil).TUI.FPS; got != 60 {
		t.Errorf("default tui.fps = %d, want 60", got)
	}
}

// TestDefaultTUIQuotaRefreshInterval pins the shipped quota refresh interval.
func TestDefaultTUIQuotaRefreshInterval(t *testing.T) {
	t.Parallel()
	if got := defaultConfig(nil).TUI.QuotaRefreshInterval.Duration(); got != int64(5*time.Minute) {
		t.Errorf("default tui.quota_refresh_interval = %d, want 5m", got)
	}
}

func TestValidateTUIQuotaRefreshInterval(t *testing.T) {
	t.Parallel()

	const negativeProblem = "tui.quota_refresh_interval must not be negative"
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "zero accepted", value: "0s"},
		{name: "five minutes accepted", value: "5m"},
		{name: "negative rejected", value: "-1m", want: []string{negativeProblem}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var problems []string
			validateTUIConfig(&problems, TUIConfig{FPS: 60, QuotaRefreshInterval: MustDuration(tc.value)})
			if !slices.Equal(problems, tc.want) {
				t.Errorf("validateTUIConfig(quota_refresh_interval=%s) problems = %q, want %q", tc.value, problems, tc.want)
			}
		})
	}
}

func TestApplyTUIPatchQuotaRefreshInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want Duration
	}{
		{name: "absent preserves", doc: "fps: 30", want: MustDuration("5m")},
		{name: "set overrides", doc: "quota_refresh_interval: 2m", want: MustDuration("2m")},
		{name: "explicit zero overrides", doc: "quota_refresh_interval: 0s", want: MustDuration("0s")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var patch tuiPatch
			if err := yaml.Unmarshal([]byte(tc.doc), &patch); err != nil {
				t.Fatalf("yaml.Unmarshal() error = %v", err)
			}
			got := TUIConfig{FPS: 60, QuotaRefreshInterval: MustDuration("5m")}
			applyTUIPatch(&got, &patch)
			if got.QuotaRefreshInterval != tc.want {
				t.Errorf("applyTUIPatch() quota_refresh_interval = %v, want %v", got.QuotaRefreshInterval, tc.want)
			}
		})
	}
}

func TestValidateTUIConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fps         int
		wantProblem bool
	}{
		{name: "zero rejected", fps: 0, wantProblem: true},
		{name: "negative rejected", fps: -1, wantProblem: true},
		{name: "one accepted", fps: 1},
		{name: "sixty accepted", fps: 60},
		{name: "one twenty accepted", fps: 120},
		{name: "above cap rejected", fps: 121, wantProblem: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var problems []string
			validateTUIConfig(&problems, TUIConfig{FPS: tc.fps})
			if got := len(problems) > 0; got != tc.wantProblem {
				t.Errorf("validateTUIConfig(fps=%d) problems=%v, wantProblem=%v",
					tc.fps, problems, tc.wantProblem)
			}
		})
	}
}

func TestApplyTUIPatch(t *testing.T) {
	t.Parallel()

	fps := 120
	tests := []struct {
		name  string
		start TUIConfig
		patch *tuiPatch
		want  int
	}{
		{name: "nil patch preserves", start: TUIConfig{FPS: 60}, patch: nil, want: 60},
		{name: "empty patch preserves", start: TUIConfig{FPS: 60}, patch: &tuiPatch{}, want: 60},
		{name: "set overrides", start: TUIConfig{FPS: 60}, patch: &tuiPatch{FPS: &fps}, want: 120},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.start
			applyTUIPatch(&got, tc.patch)
			if got.FPS != tc.want {
				t.Errorf("applyTUIPatch() fps = %d, want %d", got.FPS, tc.want)
			}
		})
	}
}
