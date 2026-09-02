package prompt

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	testIdentityMarker     = "You are steiner, a lean coding agent."
	testDelegationMarker   = "## Your role"
	testSandboxMarker      = "## Sandbox\n\nThe sandbox is enabled. The filesystem is read-only except the current\nworkdir."
	testCoreRulesMarker    = "Core rules:"
	testToolBatchingMarker = "## Tool batching"
	testWorkflowMarker     = "Before editing:"
	testCaveHumanMarker    = "## Output voice"
	parentApprovalLine     = "Ask for user's permission before editing."
	childApprovalLine      = "Do not ask for permission to proceed or for confirmation before editing."
)

func TestSystemPreambleHasNoToolGuidance(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, false, "").Content
	// Tool guidance and patch format moved to tool descriptions — must not appear in system prompt.
	// Note: delegation guidance (## Your role block) is workflow strategy, not tool mechanics — it is intentionally absent from this test's assertions.
	for _, forbidden := range []string{
		"Tool guidance:",
		"Patch format:",
		"*** Begin Patch",
		"*** End Patch",
		"Use apply_patch for all file mutations.",
		"Use edit for targeted modifications.",
		"Use write only for new files or intentional full rewrites.",
		"old_string",
		"new_string",
		"path+hunks",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("system preamble still contains %q in %q", forbidden, content)
		}
	}
}

func TestSystemPreambleSectionsAndOrdering(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		override        string
		delegation      bool
		sandbox         bool
		mounts          []string
		advisor         bool
		caveHuman       bool
		suffix          string
		wantPresent     []string
		wantAbsent      []string
		wantOrder       []string
		wantSuffixLast  bool
		wantCoreAbsent  []string
		wantIdentityCnt int
	}{
		{
			name:            "delegation enabled",
			delegation:      true,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "A cold initial sub-agent brief is six-part and MUST use every section of this template:", "## Your workflow"},
			wantIdentityCnt: 1,
		},
		{
			name:            "sandbox enabled between workflow and execution modes",
			delegation:      true,
			sandbox:         true,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker, testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker, testSandboxMarker},
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "A cold initial sub-agent brief is six-part and MUST use every section of this template:", "## Your workflow"},
			wantIdentityCnt: 1,
		},
		{
			name:            "sandbox disabled",
			delegation:      true,
			sandbox:         false,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantAbsent:      []string{testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "A cold initial sub-agent brief is six-part and MUST use every section of this template:", "## Your workflow"},
			wantIdentityCnt: 1,
		},
		{
			name:            "delegation disabled",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantAbsent:      []string{testDelegationMarker},
			wantOrder:       []string{testIdentityMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide."},
			wantIdentityCnt: 1,
		},
		{
			name:            "cave-human append after base sections",
			delegation:      true,
			caveHuman:       true,
			suffix:          "system suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker, testCaveHumanMarker, "system suffix"},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker, testCaveHumanMarker, "system suffix"},
			wantSuffixLast:  true,
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide."},
			wantIdentityCnt: 1,
		},
		{
			name:            "override preserves identity and delegation",
			override:        "Custom override content",
			delegation:      true,
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", "suffix"},
			wantAbsent:      []string{testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "override drops sandbox section even when sandbox enabled",
			override:        "Custom override content",
			delegation:      true,
			sandbox:         true,
			mounts:          []string{"/host/rw"},
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", "suffix"},
			wantAbsent:      []string{testCoreRulesMarker, testWorkflowMarker, testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "override without delegation",
			override:        "Custom override content",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testToolBatchingMarker, "Custom override content"},
			wantAbsent:      []string{testDelegationMarker, testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testToolBatchingMarker, "Custom override content"},
			wantIdentityCnt: 1,
		},
		{
			name:            "override keeps cave-human before the suffix",
			override:        "Custom override content",
			delegation:      true,
			caveHuman:       true,
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", testCaveHumanMarker, "suffix"},
			wantAbsent:      []string{testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, "Custom override content", testCaveHumanMarker, "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "advisor enabled",
			advisor:         true,
			wantPresent:     []string{testIdentityMarker, "## Advisor", testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, "## Advisor", testCoreRulesMarker, testToolBatchingMarker, testWorkflowMarker},
			wantIdentityCnt: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				Override:              tc.override,
				DelegationEnabled:     tc.delegation,
				AdvisorEnabled:        tc.advisor,
				SandboxEnabled:        tc.sandbox,
				SandboxWritableMounts: tc.mounts,
				Mode:                  workflowModeParent,
				CaveHuman:             tc.caveHuman,
				SystemSuffix:          tc.suffix,
			}).Content

			for _, want := range tc.wantPresent {
				if !strings.Contains(content, want) {
					t.Fatalf("system preamble missing %q in %q", want, content)
				}
			}
			for _, forbidden := range tc.wantAbsent {
				if strings.Contains(content, forbidden) {
					t.Fatalf("system preamble unexpectedly contains %q in %q", forbidden, content)
				}
			}
			if got := strings.Count(content, testIdentityMarker); got != tc.wantIdentityCnt {
				t.Fatalf("identity count = %d, want %d in %q", got, tc.wantIdentityCnt, content)
			}

			for i := 1; i < len(tc.wantOrder); i++ {
				prev := strings.Index(content, tc.wantOrder[i-1])
				next := strings.Index(content, tc.wantOrder[i])
				if prev == -1 || next == -1 {
					t.Fatalf("missing ordering marker in %q", content)
				}
				if prev >= next {
					t.Fatalf("marker %q (index %d) should appear before %q (index %d) in %q", tc.wantOrder[i-1], prev, tc.wantOrder[i], next, content)
				}
			}

			if tc.wantSuffixLast && tc.suffix != "" && !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
				t.Fatalf("suffix %q not at end of preamble in %q", tc.suffix, content)
			}

			coreStart := strings.Index(content, testCoreRulesMarker)
			workflowStart := strings.Index(content, testWorkflowMarker)
			if coreStart != -1 && workflowStart != -1 && coreStart < workflowStart {
				coreSection := content[coreStart:workflowStart]
				for _, forbidden := range tc.wantCoreAbsent {
					if strings.Contains(coreSection, forbidden) {
						t.Fatalf("core rules section unexpectedly contains %q in %q", forbidden, coreSection)
					}
				}
			}
		})
	}
}

func TestSandboxInstructionRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enabled bool
		mounts  []string
		want    string
	}{
		{
			name:    "enabled with no mounts",
			enabled: true,
			want:    "## Sandbox\n\nThe sandbox is enabled. The filesystem is read-only except the current\nworkdir.",
		},
		{
			name:    "enabled with writable mounts",
			enabled: true,
			mounts:  []string{"/var/log", "/home/u/go"},
			want:    "## Sandbox\n\nThe sandbox is enabled. The filesystem is read-only except the current\nworkdir. Additional writable paths: /var/log, /home/u/go",
		},
		{
			name:    "disabled with mounts",
			enabled: false,
			mounts:  []string{"/var/log"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				SandboxEnabled:        tc.enabled,
				SandboxWritableMounts: tc.mounts,
				Mode:                  workflowModeParent,
			}).Content

			if !tc.enabled {
				if strings.Contains(content, "## Sandbox") {
					t.Fatalf("sandbox section present when disabled in %q", content)
				}
				return
			}
			if !strings.Contains(content, tc.want) {
				t.Fatalf("sandbox section missing %q in %q", tc.want, content)
			}
		})
	}
}

func TestSystemPreambleDelegationInstructions(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, "").Content

	// Section headers, in canon order. Prose inside each section lives in
	// templates/delegation.md.tmpl and is deliberately not pinned here.
	last := -1
	for _, header := range delegationSectionHeaders {
		idx := strings.Index(content, header)
		if idx == -1 {
			t.Fatalf("delegation preamble missing section %q in %q", header, content)
		}
		if idx <= last {
			t.Fatalf("delegation section %q is out of canon order in %q", header, content)
		}
		last = idx
	}

	// The roster table is rendered from the specialists slice, so assert
	// against the live roster rather than a hardcoded table dump.
	if !strings.Contains(content, "| Type | Lane | Do not use for |") {
		t.Fatalf("delegation preamble missing roster table header in %q", content)
	}
	for _, name := range SpecialistNames() {
		row := "| `" + name + "` | "
		if !strings.Contains(content, row) {
			t.Fatalf("delegation preamble missing roster row for %q in %q", name, content)
		}
	}

	// Whitespace seams that markdown rendering depends on and that template
	// trim markers can silently break.
	for _, seam := range []struct {
		want   string
		detail string
	}{
		{" |\n\nEvery ", "blank line between the roster table and the worktree paragraph"},
		{"follow this workflow:\n\n1. ", "blank line between the workflow lead-in and the numbered list"},
		{"\n\n## Delegation vs direct work", "blank line between the numbered list and the following header"},
	} {
		if !strings.Contains(content, seam.want) {
			t.Fatalf("delegation preamble missing %s (%q) in %q", seam.detail, seam.want, content)
		}
	}
	// The seam above matches a longer run of newlines too, so pin the exact
	// separation: one blank line, not two.
	if strings.Contains(content, "\n\n\n## Delegation vs direct work") {
		t.Fatalf("delegation preamble has more than one blank line before the delegation-vs-direct-work header in %q", content)
	}
}

var delegationSectionHeaders = []string{
	"## Your role",
	"## Your sub-agents",
	"## Continuing sub-agents",
	"## Your workflow",
	"## Delegation vs direct work",
	"## Briefing a sub-agent",
}

var workflowStepPattern = regexp.MustCompile(`(?m)^(\d+)\. `)

// workflowSection returns the rendered `## Your workflow` section, bounded by
// the next canon header.
func workflowSection(t *testing.T, content string) string {
	t.Helper()

	start := strings.Index(content, "## Your workflow")
	end := strings.Index(content, "## Delegation vs direct work")
	if start == -1 || end == -1 || end <= start {
		t.Fatalf("cannot locate workflow section (start=%d end=%d) in %q", start, end, content)
	}
	return content[start:end]
}

// TestSystemPreambleDelegationWorkflow pins the numbered `## Your workflow`
// list: numbering stays contiguous from 1, the optional advisor step renders
// only when advisorEnabled, and the steps after it renumber accordingly. The
// disabled form must not name the `advisor` tool at all — it must be absent,
// not reworded.
func TestSystemPreambleDelegationWorkflow(t *testing.T) {
	t.Parallel()

	const baseSteps = 6

	cases := []struct {
		name      string
		advisor   bool
		wantSteps int
	}{
		{name: "advisor disabled", advisor: false, wantSteps: baseSteps},
		{name: "advisor enabled", advisor: true, wantSteps: baseSteps + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled: true,
				AdvisorEnabled:    tc.advisor,
				Mode:              workflowModeParent,
			}).Content
			section := workflowSection(t, content)

			matches := workflowStepPattern.FindAllStringSubmatch(section, -1)
			if len(matches) != tc.wantSteps {
				t.Fatalf("workflow step count = %d, want %d in %q", len(matches), tc.wantSteps, section)
			}
			for i, m := range matches {
				got, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("workflow step number %q not an integer: %v", m[1], err)
				}
				if got != i+1 {
					t.Fatalf("workflow numbering is not contiguous: step %d is numbered %d in %q", i+1, got, section)
				}
			}

			advisorLines := 0
			for _, line := range strings.Split(section, "\n") {
				if strings.Contains(line, "`advisor`") {
					advisorLines++
				}
			}
			wantAdvisorLines := 0
			if tc.advisor {
				wantAdvisorLines = 1
			}
			if advisorLines != wantAdvisorLines {
				t.Fatalf("workflow lines naming `advisor` = %d, want %d in %q", advisorLines, wantAdvisorLines, section)
			}
		})
	}
}

// TestDelegationWorkflowConsistentAcrossOverridePath pins that the normal and
// override preamble paths both embed the same flag-aware delegation canon, so
// the advisor step gating and renumbering behave identically in both.
func TestDelegationWorkflowConsistentAcrossOverridePath(t *testing.T) {
	t.Parallel()

	for _, advisor := range []bool{false, true} {
		name := "advisor disabled"
		if advisor {
			name = "advisor enabled"
		}
		t.Run(name, func(t *testing.T) {
			canon := strings.TrimSpace(delegationInstructions(advisor))
			normal := systemPreambleWithAdvisor(SystemPreambleParams{DelegationEnabled: true, AdvisorEnabled: advisor, Mode: workflowModeParent}).Content
			override := systemPreambleWithAdvisor(SystemPreambleParams{Override: "Custom override content", DelegationEnabled: true, AdvisorEnabled: advisor, Mode: workflowModeParent}).Content

			if !strings.Contains(normal, canon) {
				t.Fatalf("normal preamble does not embed the rendered delegation canon in %q", normal)
			}
			if !strings.Contains(override, canon) {
				t.Fatalf("override preamble does not embed the rendered delegation canon in %q", override)
			}
		})
	}
}

func TestSystemPreambleCaveHumanMode(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "").Content
	if !strings.Contains(content, testCaveHumanMarker) {
		t.Fatalf("cave-human preamble missing output voice block in %q", content)
	}
}

func TestSystemPreambleAdvisorGuidance(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: true, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(content, "## Advisor") {
		t.Fatalf("advisor preamble missing %q in %q", "## Advisor", content)
	}
}

// TestDelegationCanonDoesNotNameAdvisorWhenDisabled pins the fact that the
// delegation and advisor sections are gated independently: canon must not
// point the orchestrator at `advisor` in a session where the advisor tool is
// not registered and the advisor section is not rendered. It covers both the
// normal and override preamble paths, which share the same flag-aware canon
// renderer.
func TestDelegationCanonDoesNotNameAdvisorWhenDisabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		override string
	}{
		{name: "normal preamble"},
		{name: "override preamble", override: "Custom override content"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content

			if !strings.Contains(content, "## Your sub-agents") {
				t.Fatalf("delegation canon not rendered in %q", content)
			}
			if strings.Contains(content, "`advisor`") {
				t.Errorf("preamble names the `advisor` tool with advisor disabled; canon must not reference a tool that is not registered:\n%s", content)
			}
		})
	}
}

func TestSystemPreambleSystemSuffix(t *testing.T) {
	cases := []struct {
		name       string
		suffix     string
		wantIn     string
		wantInLast bool
	}{
		{
			name:       "empty suffix produces unchanged output",
			suffix:     "",
			wantIn:     "You are steiner",
			wantInLast: false,
		},
		{
			name:       "suffix appended after default preamble",
			suffix:     "Custom instruction here.",
			wantIn:     "Custom instruction here.",
			wantInLast: true,
		},
		{
			name:       "suffix appended after cave-human mode",
			suffix:     "Extended thinking enabled",
			wantIn:     "Extended thinking enabled",
			wantInLast: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := SystemPreamble("", false, false, tc.suffix).Content
			if !strings.Contains(content, tc.wantIn) {
				t.Fatalf("preamble missing %q", tc.wantIn)
			}
			if tc.wantInLast && tc.suffix != "" {
				if !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
					t.Fatalf("suffix %q not at end of preamble", tc.suffix)
				}
			}
		})
	}
}

func TestSystemPreambleSuffixAfterOverride(t *testing.T) {
	t.Parallel()

	override := "Custom system prompt"
	suffix := "Additional instruction"
	content := SystemPreamble(override, false, false, suffix).Content

	if !strings.Contains(content, override) {
		t.Fatalf("override not found in content")
	}
	if !strings.Contains(content, suffix) {
		t.Fatalf("suffix not found in content")
	}
	if !strings.HasSuffix(strings.TrimSpace(content), suffix) {
		t.Fatalf("suffix should appear after override")
	}
}

func TestSystemPreambleWorkflowApprovalByMode(t *testing.T) {
	t.Parallel()

	parent := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(parent, testWorkflowMarker) {
		t.Fatalf("parent preamble missing %q in %q", testWorkflowMarker, parent)
	}
	if !strings.Contains(parent, parentApprovalLine) {
		t.Fatalf("parent preamble missing approval line %q in %q", parentApprovalLine, parent)
	}
	// The before-editing and verification bullets of the shared workflow block
	// are pinned here so a regression in renderWorkflowInstructions is caught.
	for _, want := range []string{
		"- Ensure relevant files and nearby tests are inspected before making changes.",
		"- Ensure the narrowest relevant checks run first.",
	} {
		if !strings.Contains(parent, want) {
			t.Fatalf("parent preamble missing %q in %q", want, parent)
		}
	}

	child := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(child, testWorkflowMarker) {
		t.Fatalf("child preamble missing %q in %q", testWorkflowMarker, child)
	}
	if !strings.Contains(child, childApprovalLine) {
		t.Fatalf("child preamble missing delegated approval line %q in %q", childApprovalLine, child)
	}
	if strings.Contains(child, parentApprovalLine) {
		t.Fatalf("child preamble unexpectedly contains parent approval line %q in %q", parentApprovalLine, child)
	}
}

func TestSystemPreambleExecutionModesInParent(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(content, "## Execution modes") {
		t.Fatalf("parent preamble missing %q in %q", "## Execution modes", content)
	}
}

func TestSystemPreambleExecutionModesAbsentInDelegatedChild(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, CaveHuman: false, SystemSuffix: ""}).Content
	if strings.Contains(content, "## Execution modes") {
		t.Fatalf("delegated child preamble should not contain execution modes section in %q", content)
	}
}

func TestSystemPreambleByteStable(t *testing.T) {
	t.Parallel()

	// Call the preamble builder twice with identical inputs and verify byte-identity.
	// This proves the preamble has no mode variance (since ExecutionMode is not a parameter)
	// and no per-turn randomness.
	first := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	second := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content

	if first != second {
		t.Fatalf("preamble not byte-identical across builds:\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}
}

const testRoleProseMarker = "not the default implementation worker"

func TestSystemPreambleRoleProseGating(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		delegationEnabled bool
		mode              workflowMode
		wantRole          bool
	}{
		{
			name:              "delegation enabled, parent mode: role prose present",
			delegationEnabled: true,
			mode:              workflowModeParent,
			wantRole:          true,
		},
		{
			name:              "delegation disabled, parent mode: role prose absent",
			delegationEnabled: false,
			mode:              workflowModeParent,
			wantRole:          false,
		},
		{
			name:              "oneshot phase (delegated child mode, delegation enabled): role prose present",
			delegationEnabled: true,
			mode:              DelegatedChildWorkflowMode(),
			wantRole:          true,
		},
		{
			name:              "delegated child (delegation disabled): role prose absent",
			delegationEnabled: false,
			mode:              DelegatedChildWorkflowMode(),
			wantRole:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled: tc.delegationEnabled,
				Mode:              tc.mode,
			}).Content

			if !strings.Contains(content, testIdentityMarker) {
				t.Fatalf("preamble missing identity %q in %q", testIdentityMarker, content)
			}

			gotRole := strings.Contains(content, testRoleProseMarker)
			if gotRole != tc.wantRole {
				t.Fatalf("role prose present = %v, want %v in %q", gotRole, tc.wantRole, content)
			}
		})
	}
}

func TestSystemPreambleRoleProseViaOverride(t *testing.T) {
	t.Parallel()

	content := SystemPreambleWithAdvisor(SystemPreambleParams{
		Override:          "Custom override content",
		DelegationEnabled: true,
		Mode:              workflowModeParent,
	}).Content

	if !strings.Contains(content, testIdentityMarker) {
		t.Fatalf("override preamble missing identity %q in %q", testIdentityMarker, content)
	}
	if !strings.Contains(content, testRoleProseMarker) {
		t.Fatalf("override preamble missing %q in %q", testRoleProseMarker, content)
	}
	if !strings.Contains(content, "Custom override content") {
		t.Fatalf("override preamble missing override content in %q", content)
	}
}

func TestSystemPreambleRoleProsePosition(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{
		DelegationEnabled: true,
		AdvisorEnabled:    true,
		Mode:              workflowModeParent,
	}).Content

	roleIdx := strings.Index(content, testRoleProseMarker)
	advisorIdx := strings.Index(content, "## Advisor")
	coreIdx := strings.Index(content, testCoreRulesMarker)

	if roleIdx < 0 || advisorIdx < 0 || coreIdx < 0 {
		t.Fatalf("missing marker: roleIdx=%d advisorIdx=%d coreIdx=%d in %q", roleIdx, advisorIdx, coreIdx, content)
	}
	if roleIdx >= advisorIdx {
		t.Fatalf("role prose (index %d) should appear before advisor section (index %d) in %q", roleIdx, advisorIdx, content)
	}
	if roleIdx >= coreIdx {
		t.Fatalf("role prose (index %d) should appear before core rules (index %d) in %q", roleIdx, coreIdx, content)
	}
}
