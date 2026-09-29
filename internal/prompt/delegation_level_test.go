package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestDelegationInstructionsOrchestrationLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		level      config.OrchestrationLevel
		wantOrder  []string
		wantAbsent []string
	}{
		{
			name:  "standard",
			level: config.OrchestrationLevelStandard,
			wantOrder: []string{
				"## Your role",
				"## Your sub-agents",
				"## Continuing sub-agents",
				"## Delegation vs direct work",
				"## Briefing a sub-agent",
			},
		},
		{
			name:  "zero value renders standard",
			level: config.OrchestrationLevel(""),
			wantOrder: []string{
				"## Your role",
				"## Your sub-agents",
				"## Continuing sub-agents",
				"## Delegation vs direct work",
				"## Briefing a sub-agent",
			},
		},
		{
			name:  "low",
			level: config.OrchestrationLevelLow,
			wantOrder: []string{
				"## Your sub-agents",
				"## Continuing sub-agents",
				"## Briefing a sub-agent",
			},
			wantAbsent: []string{
				"## Your role",
				"You are the orchestrator",
				"## Delegation vs direct work",
				"Delegate by default",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := delegationInstructions(tc.level, true)

			last := -1
			for _, marker := range tc.wantOrder {
				idx := strings.Index(content, marker)
				if idx == -1 {
					t.Fatalf("missing marker %q in %q", marker, content)
				}
				if idx <= last {
					t.Fatalf("marker %q out of order in %q", marker, content)
				}
				last = idx
			}
			for _, marker := range tc.wantAbsent {
				if strings.Contains(content, marker) {
					t.Fatalf("unexpectedly contains %q in %q", marker, content)
				}
			}
			// The standard canon deliberately has a two-blank-line seam before
			// "## Briefing a sub-agent" (preserved byte-for-byte from before this
			// change); only the low level, which drops the section that seam
			// belonged to, must not leak a stray blank-line run.
			if tc.level == config.OrchestrationLevelLow && strings.Contains(content, "\n\n\n") {
				t.Fatalf("low-level delegation canon has a triple newline in %q", content)
			}
		})
	}
}

func TestDelegationInstructionsZeroValueMatchesStandard(t *testing.T) {
	t.Parallel()

	zero := delegationInstructions(config.OrchestrationLevel(""), true)
	standard := delegationInstructions(config.OrchestrationLevelStandard, true)
	if zero != standard {
		t.Fatalf("zero-value orchestration level output differs from standard:\nzero:\n%s\n\nstandard:\n%s", zero, standard)
	}
}

func TestDelegationInstructionsLowIncludesAllSpecialists(t *testing.T) {
	t.Parallel()

	content := delegationInstructions(config.OrchestrationLevelLow, true)
	for _, name := range SpecialistNames() {
		row := "| `" + name + "` | "
		if !strings.Contains(content, row) {
			t.Fatalf("low-level canon missing specialist roster row for %q in %q", name, content)
		}
	}
}

func TestSystemPreambleOrchestrationLevel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		level config.OrchestrationLevel
		want  bool
	}{
		{name: "standard renders role section", level: config.OrchestrationLevelStandard, want: true},
		{name: "low omits role section", level: config.OrchestrationLevelLow, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled:  true,
				OrchestrationLevel: tc.level,
				Mode:               workflowModeParent,
			}).Content

			if got := strings.Contains(content, "## Your role"); got != tc.want {
				t.Fatalf("role section present = %v, want %v in %q", got, tc.want, content)
			}
		})
	}
}

func TestSystemPreambleOrchestrationLevelWithDelegationDisabled(t *testing.T) {
	t.Parallel()

	for _, level := range []config.OrchestrationLevel{config.OrchestrationLevelStandard, config.OrchestrationLevelLow} {
		content := systemPreambleWithAdvisor(SystemPreambleParams{
			DelegationEnabled:  false,
			OrchestrationLevel: level,
			Mode:               workflowModeParent,
		}).Content
		if strings.Contains(content, "## Your sub-agents") {
			t.Fatalf("delegation section rendered with delegation disabled (level=%q) in %q", level, content)
		}
	}
}

func TestOverridePreambleOrchestrationLevelLow(t *testing.T) {
	t.Parallel()

	content := buildOverridePreamble("custom override", sectionContext{
		delegationEnabled:  true,
		orchestrationLevel: config.OrchestrationLevelLow,
		workflowMode:       workflowModeParent,
	})

	if strings.Contains(content, "## Your role") {
		t.Fatalf("override preamble unexpectedly contains role section at low level in %q", content)
	}
	if strings.Contains(content, "## Delegation vs direct work") {
		t.Fatalf("override preamble unexpectedly contains delegation-vs-direct-work section at low level in %q", content)
	}
	if !strings.Contains(content, "## Your sub-agents") {
		t.Fatalf("override preamble missing sub-agent roster at low level in %q", content)
	}
}

func TestDelegationInstructionsAsyncStructure(t *testing.T) {
	t.Parallel()

	const asyncHeading = "## Async results"
	const resultTag = "<steiner-sub-agent-result"

	for _, level := range []config.OrchestrationLevel{config.OrchestrationLevelStandard, config.OrchestrationLevelLow} {
		async := delegationInstructions(level, true)
		blocking := delegationInstructions(level, false)

		if !strings.Contains(async, asyncHeading) || !strings.Contains(async, resultTag) {
			t.Errorf("level %q async canon lacks %q section or %q tag", level, asyncHeading, resultTag)
		}
		if strings.Contains(blocking, asyncHeading) || strings.Contains(blocking, resultTag) {
			t.Errorf("level %q blocking canon contains async section or result tag", level)
		}
		if strings.Contains(blocking, "<steiner-sub-agents-pending>") {
			t.Errorf("level %q blocking canon mentions the pending line", level)
		}
		for _, heading := range []string{"## Your sub-agents", "## Continuing sub-agents", "## Briefing a sub-agent"} {
			if !strings.Contains(async, heading) || !strings.Contains(blocking, heading) {
				t.Errorf("level %q missing shared heading %q", level, heading)
			}
		}
		if async == blocking {
			t.Errorf("level %q renders identically for async and blocking", level)
		}
	}
}

func TestSystemPreambleAsyncDeterministic(t *testing.T) {
	t.Parallel()

	for _, async := range []bool{true, false} {
		params := SystemPreambleParams{DelegationEnabled: true, AsyncSubAgents: async, Mode: workflowModeParent}
		first := systemPreambleWithAdvisor(params).Content
		second := systemPreambleWithAdvisor(params).Content
		if first != second {
			t.Errorf("async=%v preamble differs between renders", async)
		}
		if got := strings.Contains(first, "## Async results"); got != async {
			t.Errorf("async=%v preamble contains async section = %v", async, got)
		}
	}
}
