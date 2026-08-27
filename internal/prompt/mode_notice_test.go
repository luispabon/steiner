package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestModeNotice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		mode        config.ExecutionMode
		wantPresent []string
	}{
		{
			name: "plan mode",
			mode: config.ExecutionModePlan,
			wantPresent: []string{
				"[execution mode: plan]",
				"Plan mode",
				"Project edits are restricted",
				".steiner/plans/",
				"Discuss proposals in conversation",
				"do not edit while requirements are moving",
				"write a handoff plan only when ready",
			},
		},
		{
			name: "build mode",
			mode: config.ExecutionModeBuild,
			wantPresent: []string{
				"[execution mode: build]",
				"Build mode",
				"Normal workspace editing",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notice := ModeNotice(tc.mode)
			if notice == "" {
				t.Fatalf("ModeNotice returned empty string for mode %q", tc.mode)
			}
			for _, want := range tc.wantPresent {
				if !strings.Contains(notice, want) {
					t.Fatalf("mode notice missing %q in %q", want, notice)
				}
			}
		})
	}
}

func TestModeNoticeDistinct(t *testing.T) {
	t.Parallel()

	plan := ModeNotice(config.ExecutionModePlan)
	build := ModeNotice(config.ExecutionModeBuild)

	if plan == build {
		t.Fatalf("plan and build mode notices should be distinct")
	}
	if plan == "" || build == "" {
		t.Fatalf("both notices should be non-empty")
	}
}

func TestStripModeNotice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plan mode prefix stripped",
			input: ModeNotice(config.ExecutionModePlan) + "\n\nfix the bug",
			want:  "fix the bug",
		},
		{
			name:  "build mode prefix stripped",
			input: ModeNotice(config.ExecutionModeBuild) + "\n\nfix the bug",
			want:  "fix the bug",
		},
		{
			name:  "no prefix",
			input: "fix the bug",
			want:  "fix the bug",
		},
		{
			name:  "notice-like text without prefix unchanged",
			input: "the [execution mode: build] text is mentioned in the middle",
			want:  "the [execution mode: build] text is mentioned in the middle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripModeNotice(tc.input)
			if got != tc.want {
				t.Errorf("StripModeNotice(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
