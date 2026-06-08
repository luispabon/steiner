package tui

import "testing"

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{500, "500ms"},
		{999, "999ms"},
		{1000, "1.0s"},
		{1200, "1.2s"},
		{5000, "5.0s"},
	}
	for _, tc := range cases {
		got := formatDuration(tc.ms)
		if got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFormatTPS(t *testing.T) {
	cases := []struct {
		tps  float64
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{42.1, "42.1 t/s"},
		{10.0, "10.0 t/s"},
	}
	for _, tc := range cases {
		got := formatTPS(tc.tps)
		if got != tc.want {
			t.Errorf("formatTPS(%f) = %q, want %q", tc.tps, got, tc.want)
		}
	}
}

func TestPerformanceSection(t *testing.T) {
	cases := []struct {
		name           string
		perfDurationMs int64
		perfTTFTMs     int64
		perfOutputTPS  float64
	}{
		{"all zeros", 0, 0, 0.0},
		{"with duration", 1200, 0, 0.0},
		{"with ttft", 0, 340, 0.0},
		{"with tps", 0, 0, 42.1},
		{"all values", 1200, 340, 42.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sidebarState{
				perfDurationMs: tc.perfDurationMs,
				perfTTFTMs:     tc.perfTTFTMs,
				perfOutputTPS:  tc.perfOutputTPS,
			}
			got := s.performanceSection(32)
			if len(got) == 0 {
				t.Errorf("performanceSection() = empty, always want non-empty")
			}
		})
	}
}
