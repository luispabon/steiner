package config

import "testing"

// TestDefaultTUIFPS pins the shipped renderer frame rate. Changing it changes
// both perceived input latency and TUI CPU cost for every user, so it should
// never move silently.
func TestDefaultTUIFPS(t *testing.T) {
	t.Parallel()
	if got := defaultConfig().TUI.FPS; got != 60 {
		t.Errorf("default tui.fps = %d, want 60", got)
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
