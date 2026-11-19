package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

const (
	testIdentityMarker     = "You are steiner, a lean coding agent."
	testDelegationMarker   = "## Your role"
	testSandboxMarker      = "## Sandbox\n\nThe sandbox is enabled. The filesystem is read-only except the current\nworkdir."
	testToolBatchingMarker = "## Tool batching"
	testWorkflowMarker     = "## Work methodology"
	testOutputVoiceMarker  = "## Output voice"
	parentApprovalLine     = "- Ask the user for confirmation before editing."
	childApprovalLine      = "Do not ask the user for approval, confirmation, or feedback."
)

func TestSystemPreambleHasNoToolGuidance(t *testing.T) {
	t.Parallel()

	content := SystemPreambleWithAdvisor(SystemPreambleParams{Mode: workflowModeParent}).Content
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
		suffix          string
		wantPresent     []string
		wantAbsent      []string
		wantOrder       []string
		wantSuffixLast  bool
		wantIdentityCnt int
	}{
		{
			name:            "delegation enabled",
			delegation:      true,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker},
			wantIdentityCnt: 1,
		},
		{
			name:            "sandbox enabled between workflow and execution modes",
			delegation:      true,
			sandbox:         true,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, testSandboxMarker},
			wantIdentityCnt: 1,
		},
		{
			name:            "sandbox disabled",
			delegation:      true,
			sandbox:         false,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker},
			wantAbsent:      []string{testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker},
			wantIdentityCnt: 1,
		},
		{
			name:            "delegation disabled",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testToolBatchingMarker, testWorkflowMarker},
			wantAbsent:      []string{testDelegationMarker},
			wantOrder:       []string{testIdentityMarker, testToolBatchingMarker, testWorkflowMarker},
			wantIdentityCnt: 1,
		},
		{
			name:            "output voice append after base sections",
			delegation:      true,
			suffix:          "system suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, testOutputVoiceMarker, "system suffix"},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, testOutputVoiceMarker, "system suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "override preserves identity and delegation",
			override:        "Custom override content",
			delegation:      true,
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, "Custom override content", "suffix"},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testWorkflowMarker, "Custom override content", testToolBatchingMarker, "suffix"},
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
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, "Custom override content", "suffix"},
			wantAbsent:      []string{testSandboxMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testWorkflowMarker, "Custom override content", testToolBatchingMarker, "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "override without delegation",
			override:        "Custom override content",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testToolBatchingMarker, testWorkflowMarker, "Custom override content"},
			wantAbsent:      []string{testDelegationMarker},
			wantOrder:       []string{testIdentityMarker, testWorkflowMarker, "Custom override content", testToolBatchingMarker},
			wantIdentityCnt: 1,
		},
		{
			name:            "override keeps output voice before the suffix",
			override:        "Custom override content",
			delegation:      true,
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testToolBatchingMarker, testWorkflowMarker, "Custom override content", testOutputVoiceMarker, "suffix"},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testWorkflowMarker, "Custom override content", testToolBatchingMarker, testOutputVoiceMarker, "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "advisor enabled",
			advisor:         true,
			wantPresent:     []string{testIdentityMarker, "## Advisor", testToolBatchingMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, "## Advisor", testToolBatchingMarker, testWorkflowMarker},
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

			if strings.Contains(content, "## Core rules:") {
				t.Fatalf("preamble renders a separate core rules section in %q", content)
			}
		})
	}
}

func TestOverridePreamblePlacesToolBatchingAfterOverride(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{
		Override:          "specialist override",
		DelegationEnabled: true,
		Mode:              workflowModeParent,
		SystemSuffix:      "caller suffix",
	}).Content

	override := strings.Index(content, "specialist override")
	batching := strings.Index(content, testToolBatchingMarker)
	outputVoice := strings.Index(content, testOutputVoiceMarker)
	suffix := strings.Index(content, "caller suffix")
	if override == -1 || batching == -1 || outputVoice == -1 || suffix == -1 {
		t.Fatalf("missing ordering marker in %q", content)
	}
	if override >= batching {
		t.Fatalf("override marker at index %d should precede tool batching at index %d in %q", override, batching, content)
	}
	if batching >= outputVoice || outputVoice >= suffix {
		t.Fatalf("expected tool batching, output voice, and suffix order in %q", content)
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

	content := SystemPreambleWithAdvisor(SystemPreambleParams{DelegationEnabled: true, Mode: workflowModeParent}).Content

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
		{"|\n\nEach ", "blank line between the roster table and the worktree paragraph"},
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

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

var delegationSectionHeaders = []string{
	"## Your role",
	"## Your sub-agents",
	"## Continuing sub-agents",
	"## Delegation vs direct work",
	"## Briefing a sub-agent",
}

func TestSystemPreambleWorkflowMethodologyMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		delegation      bool
		mode            workflowMode
		wantParallel    bool
		wantWorking     bool
		wantWorkflow    bool
		wantParentRules bool
		wantChildRules  bool
		wantDelegation  bool
	}{
		{name: "parent delegation enabled", delegation: true, mode: workflowModeParent, wantParallel: true, wantWorking: true, wantWorkflow: true, wantParentRules: true, wantDelegation: true},
		{name: "parent delegation disabled", delegation: false, mode: workflowModeParent, wantWorking: true, wantWorkflow: true, wantParentRules: true},
		{name: "delegated code child", delegation: false, mode: workflowModeDelegatedChild, wantWorking: true, wantChildRules: true},
		{name: "delegated documentor child", delegation: false, mode: workflowModeDelegatedDocumentor, wantWorking: true, wantChildRules: true},
		{name: "delegated non-code child", delegation: false, mode: workflowModeDelegatedNonCodeChild, wantChildRules: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled: tc.delegation,
				Mode:              tc.mode,
			}).Content

			if got := strings.Count(content, "## Work methodology"); got != boolCount(tc.wantWorkflow) {
				t.Fatalf("workflow heading count = %d, want %d in %q", got, boolCount(tc.wantWorkflow), content)
			}
			for _, marker := range []string{"### While editing", "### Verification", "## Final response"} {
				wantCount := boolCount(tc.wantWorking)
				if got := strings.Count(content, marker); got != wantCount {
					t.Fatalf("methodology marker %q count = %d, want %d in %q", marker, got, wantCount, content)
				}
			}
			if got := strings.Contains(content, "Complete independent deliverables in parallel."); got != tc.wantParallel {
				t.Fatalf("parallel-deliverable rule present = %v, want %v", got, tc.wantParallel)
			}
			if got := strings.Contains(content, "### Splitting tasks into deliverables"); got != tc.wantParentRules {
				t.Fatalf("parent methodology present = %v, want %v", got, tc.wantParentRules)
			}
			if got := strings.Contains(content, "## Delegated task"); got != tc.wantChildRules {
				t.Fatalf("child authorization present = %v, want %v", got, tc.wantChildRules)
			}
			if got := strings.Contains(content, "## Your sub-agents"); got != tc.wantDelegation {
				t.Fatalf("delegation canon present = %v, want %v", got, tc.wantDelegation)
			}
			if tc.mode == workflowModeParent && !tc.delegation {
				workflowStart := strings.Index(content, testWorkflowMarker)
				workflowEnd := strings.Index(content, "## Execution modes")
				if workflowStart == -1 || workflowEnd == -1 || workflowEnd <= workflowStart {
					t.Fatalf("cannot isolate workflow methodology in %q", content)
				}
				workflow := content[workflowStart:workflowEnd]
				for _, unavailable := range []string{"`sub_agent`", "`follow_up`", "`code`", "`explore`", "`evaluate`", "`review`", "`sanity_check`", "`advisor`"} {
					if strings.Contains(workflow, unavailable) {
						t.Fatalf("delegation-disabled parent unexpectedly contains %q in %q", unavailable, workflow)
					}
				}
			}
			if tc.mode != workflowModeParent {
				if got := strings.Count(content, "## Delegated task"); got != 1 {
					t.Fatalf("delegated-task authorization count = %d, want 1 in %q", got, content)
				}
				for _, forbidden := range []string{"## Your role", "## Your sub-agents", "## Continuing sub-agents", "## Delegation vs direct work", "## Briefing a sub-agent", "## Advisor", "### Planning", "### Splitting tasks into deliverables", "### Implementing", "Ask the user for confirmation before editing."} {
					if strings.Contains(content, forbidden) {
						t.Fatalf("child preamble unexpectedly contains orchestrator marker %q in %q", forbidden, content)
					}
				}
			}
		})
	}
}

// TestDelegationCanonIndependentOfAdvisor verifies that delegation canon is
// unchanged when separately gated advisor guidance is enabled or disabled.
func TestDelegationCanonIndependentOfAdvisor(t *testing.T) {
	t.Parallel()

	canon := strings.TrimSpace(delegationInstructions(config.OrchestrationLevelStandard, true))
	for _, advisor := range []bool{false, true} {
		content := systemPreambleWithAdvisor(SystemPreambleParams{DelegationEnabled: true, AsyncSubAgents: true, AdvisorEnabled: advisor, Mode: workflowModeParent}).Content
		if !strings.Contains(content, canon) {
			t.Fatalf("delegation canon missing with advisor enabled=%v in %q", advisor, content)
		}
	}
}

func TestSystemPreambleOutputVoiceMode(t *testing.T) {
	t.Parallel()

	content := SystemPreambleWithAdvisor(SystemPreambleParams{Mode: workflowModeParent}).Content
	if !strings.Contains(content, testOutputVoiceMarker) {
		t.Fatalf("output voice preamble missing output voice block in %q", content)
	}
}

func TestSystemPreambleAdvisorGuidance(t *testing.T) {
	t.Parallel()

	const waitMarker = "wait for their results first"
	cases := []struct {
		name       string
		override   string
		delegation bool
		async      bool
		advisor    bool
		wantWait   bool
	}{
		{name: "advisor only", advisor: true},
		{name: "advisor with blocking delegation", advisor: true, delegation: true},
		{name: "advisor with async delegation", advisor: true, delegation: true, async: true, wantWait: true},
		{name: "advisor with async delegation and override", override: "Custom override content", advisor: true, delegation: true, async: true, wantWait: true},
		{name: "advisor disabled with async delegation", delegation: true, async: true},
		{name: "async flag without delegation", advisor: true, async: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				Override:          tc.override,
				DelegationEnabled: tc.delegation,
				AsyncSubAgents:    tc.async,
				AdvisorEnabled:    tc.advisor,
				Mode:              workflowModeParent,
			}).Content

			if got := strings.Contains(content, "## Advisor"); got != tc.advisor {
				t.Fatalf("advisor section present = %v, want %v in %q", got, tc.advisor, content)
			}
			if got := strings.Contains(content, waitMarker); got != tc.wantWait {
				t.Fatalf("pending-investigation guidance present = %v, want %v in %q", got, tc.wantWait, content)
			}
		})
	}
}

func TestSystemPreambleAdvisorWaitGuidanceByteStable(t *testing.T) {
	t.Parallel()

	params := SystemPreambleParams{DelegationEnabled: true, AsyncSubAgents: true, AdvisorEnabled: true, Mode: workflowModeParent}
	first := systemPreambleWithAdvisor(params).Content
	second := systemPreambleWithAdvisor(params).Content
	if first != second {
		t.Fatalf("preamble not byte-identical across builds:\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}
}

func TestSystemPreambleLSPGuidance(t *testing.T) {
	t.Parallel()

	const marker = "## Code intelligence (LSP)"
	for _, tc := range []struct {
		name     string
		override string
		lsp      bool
		want     bool
	}{
		{name: "enabled", lsp: true, want: true},
		{name: "disabled"},
		{name: "enabled with override", override: "custom override", lsp: true, want: true},
		{name: "disabled with override", override: "custom override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, LSPEnabled: tc.lsp, Mode: workflowModeParent}).Content
			if strings.Contains(content, marker) != tc.want {
				t.Fatalf("LSP guidance present = %t, want %t in %q", strings.Contains(content, marker), tc.want, content)
			}
		})
	}
}

func TestSystemPreambleDoesNotNameLSPWhenDisabled(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		override string
	}{
		{name: "normal preamble"},
		{name: "override preamble", override: "Custom override content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, DelegationEnabled: true, LSPEnabled: false, Mode: workflowModeParent}).Content
			if strings.Contains(content, "lsp_") {
				t.Errorf("preamble names an LSP tool with LSP disabled:\n%s", content)
			}
		})
	}
}

func TestSystemPreambleLSPGuidanceFollowsAdvisor(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{AdvisorEnabled: true, LSPEnabled: true, Mode: workflowModeParent}).Content
	advisor := strings.Index(content, "## Advisor")
	lsp := strings.Index(content, "## Code intelligence (LSP)")
	if advisor == -1 || lsp == -1 || advisor >= lsp {
		t.Fatalf("advisor and LSP sections out of order in %q", content)
	}
}

// TestDelegationCanonDoesNotNameAdvisorWhenDisabled verifies that delegation
// canon does not reference the separately gated `advisor` tool when advisor
// guidance is disabled. It covers both normal and override preamble paths.
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
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, SystemSuffix: ""}).Content

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
			name:       "suffix appended after output voice mode",
			suffix:     "Extended thinking enabled",
			wantIn:     "Extended thinking enabled",
			wantInLast: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := SystemPreambleWithAdvisor(SystemPreambleParams{Mode: workflowModeParent, SystemSuffix: tc.suffix}).Content
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
	content := SystemPreambleWithAdvisor(SystemPreambleParams{Override: override, Mode: workflowModeParent, SystemSuffix: suffix}).Content

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

	parent := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, SystemSuffix: ""}).Content
	if !strings.Contains(parent, testWorkflowMarker) {
		t.Fatalf("parent preamble missing %q in %q", testWorkflowMarker, parent)
	}
	if !strings.Contains(parent, parentApprovalLine) {
		t.Fatalf("parent preamble missing approval line %q in %q", parentApprovalLine, parent)
	}
	for _, want := range []string{"### While editing", "### Verification", "## Final response"} {
		if !strings.Contains(parent, want) {
			t.Fatalf("parent preamble missing %q in %q", want, parent)
		}
	}

	child := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, SystemSuffix: ""}).Content
	if strings.Contains(child, testWorkflowMarker) {
		t.Fatalf("code child unexpectedly contains parent methodology")
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

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeParent, SystemSuffix: ""}).Content
	if !strings.Contains(content, "## Execution modes") {
		t.Fatalf("parent preamble missing %q in %q", "## Execution modes", content)
	}
}

func TestSystemPreambleExecutionModesAbsentInDelegatedChild(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, SystemSuffix: ""}).Content
	if strings.Contains(content, "## Execution modes") {
		t.Fatalf("delegated child preamble should not contain execution modes section in %q", content)
	}
	for _, mode := range []workflowMode{workflowModeDelegatedDocumentor, workflowModeDelegatedNonCodeChild} {
		content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: mode}).Content
		if strings.Contains(content, "## Execution modes") {
			t.Fatalf("non-code delegated child preamble should not contain execution modes section in %q", content)
		}
	}
}

func TestSystemPreambleByteStable(t *testing.T) {
	t.Parallel()

	// Call the preamble builder twice with identical inputs and verify byte-identity.
	// This proves the preamble has no mode variance (since ExecutionMode is not a parameter)
	// and no per-turn randomness.
	first := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, SystemSuffix: ""}).Content
	second := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, SystemSuffix: ""}).Content

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

func TestDelegatedWorkflowProfilesExcludeOrchestratorContent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		mode     workflowMode
		override string
	}{
		{name: "code", mode: workflowModeDelegatedCodeSubAgent},
		{name: "code override", mode: workflowModeDelegatedCodeSubAgent, override: "custom override"},
		{name: "documentor", mode: workflowModeDelegatedDocumentor},
		{name: "documentor override", mode: workflowModeDelegatedDocumentor, override: "custom override"},
		{name: "non-code", mode: workflowModeDelegatedNonCodeChild},
		{name: "non-code override", mode: workflowModeDelegatedNonCodeChild, override: "custom override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, Mode: tc.mode}).Content
			for _, marker := range []string{"## Work methodology", "### Splitting tasks into deliverables", "### Planning", "### Implementing"} {
				if strings.Contains(content, marker) {
					t.Fatalf("child contains orchestrator-only marker %q", marker)
				}
			}
			if got := strings.Count(content, "## Delegated task"); got != 1 {
				t.Fatalf("delegated-task authorization count = %d, want 1 in %q", got, content)
			}
			batching := strings.Index(content, testToolBatchingMarker)
			outputVoice := strings.Index(content, testOutputVoiceMarker)
			delegatedTask := strings.Index(content, "## Delegated task")
			if batching < 0 || outputVoice < 0 || delegatedTask < 0 {
				t.Fatalf("missing ordering marker in %q", content)
			}
			if batching >= outputVoice || outputVoice >= delegatedTask {
				t.Fatalf("child preamble must order tool batching, output voice, then the delegated-task authorization in %q", content)
			}
			switch tc.mode {
			case workflowModeDelegatedCodeSubAgent:
				if strings.Count(content, "## Code agent duties") != 1 {
					t.Fatalf("code child duties count = %d, want 1", strings.Count(content, "## Code agent duties"))
				}
				for _, marker := range []string{"### While editing", "### Verification", "## Final response"} {
					if !strings.Contains(content, marker) {
						t.Fatalf("code child missing %q", marker)
					}
				}
			case workflowModeDelegatedDocumentor:
				if strings.Count(content, "## Documentor duties") != 1 {
					t.Fatalf("documentor child duties count = %d, want 1", strings.Count(content, "## Documentor duties"))
				}
				for _, marker := range []string{"## Delegated code agent", "## Code agent duties"} {
					if strings.Contains(content, marker) {
						t.Fatalf("documentor child contains code-child marker %q", marker)
					}
				}
				for _, marker := range []string{"### While editing", "### Verification", "## Final response"} {
					if !strings.Contains(content, marker) {
						t.Fatalf("documentor child missing %q", marker)
					}
				}
			default:
				for _, marker := range []string{"## Delegated code agent", "## Code agent duties"} {
					if strings.Contains(content, marker) {
						t.Fatalf("non-code child contains code-child marker %q", marker)
					}
				}
				for _, marker := range []string{"### While editing", "### Verification", "## Final response"} {
					if strings.Contains(content, marker) {
						t.Fatalf("non-code child contains %q", marker)
					}
				}
			}
		})
	}
}

func TestCodeChildApprovedOpeningOrder(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: workflowModeDelegatedCodeSubAgent}).Content
	const roleSentence = "You are steiner's code sub-agent. Complete the task in your brief within its stated scope."
	markers := []string{testIdentityMarker, roleSentence, "## Code agent duties", testToolBatchingMarker, "## Working rules", testOutputVoiceMarker, "## Delegated task"}
	last := -1
	for _, marker := range markers {
		idx := strings.Index(content, marker)
		if idx == -1 {
			t.Fatalf("code child missing %q in %q", marker, content)
		}
		if idx <= last {
			t.Fatalf("code child marker %q is out of order in %q", marker, content)
		}
		last = idx
	}
	if got := strings.Count(content, roleSentence); got != 1 {
		t.Fatalf("code-child role sentence count = %d, want 1", got)
	}
}

func TestCodeChildMarkersAbsentFromParentAndNonCode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode workflowMode
	}{
		{name: "parent", mode: workflowModeParent},
		{name: "delegated non-code", mode: workflowModeDelegatedNonCodeChild},
		{name: "delegated documentor", mode: workflowModeDelegatedDocumentor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: tc.mode}).Content
			for _, marker := range []string{"## Delegated code agent", "## Code agent duties"} {
				if strings.Contains(content, marker) {
					t.Fatalf("%s contains code-child marker %q", tc.name, marker)
				}
			}
		})
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
	workflowIdx := strings.Index(content, "## Work methodology")

	if roleIdx < 0 || advisorIdx < 0 || workflowIdx < 0 {
		t.Fatalf("missing marker: roleIdx=%d advisorIdx=%d workflowIdx=%d in %q", roleIdx, advisorIdx, workflowIdx, content)
	}
	if roleIdx >= advisorIdx {
		t.Fatalf("role prose (index %d) should appear before advisor section (index %d) in %q", roleIdx, advisorIdx, content)
	}
	if roleIdx >= workflowIdx {
		t.Fatalf("role prose (index %d) should appear before the workflow section (index %d) in %q", roleIdx, workflowIdx, content)
	}
}

// TestCoreRulesNotRenderedSeparately pins the removal of the standalone core
// rules section: the shared workflow template owns those rules for every mode,
// with and without a system override.
func TestCoreRulesNotRenderedSeparately(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		mode     workflowMode
		override string
	}{
		{name: "parent", mode: workflowModeParent},
		{name: "parent override", mode: workflowModeParent, override: "custom override"},
		{name: "legacy delegated child", mode: workflowModeDelegatedChild},
		{name: "legacy delegated child override", mode: workflowModeDelegatedChild, override: "custom override"},
		{name: "code sub-agent", mode: workflowModeDelegatedCodeSubAgent},
		{name: "documentor", mode: workflowModeDelegatedDocumentor},
		{name: "non-code child", mode: workflowModeDelegatedNonCodeChild},
		{name: "non-code override", mode: workflowModeDelegatedNonCodeChild, override: "custom override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, Mode: tc.mode}).Content
			if strings.Contains(content, "## Core rules:") {
				t.Fatalf("%s preamble renders a separate core rules section in %q", tc.name, content)
			}
		})
	}
}

// TestDelegatedTaskAuthorizationOrderedBeforeSuffix pins the delegated-task
// authorization as the last built-in preamble section for every delegated
// child mode, with and without an override, so it lands after the output voice
// and before any caller suffix.
func TestDelegatedTaskAuthorizationOrderedBeforeSuffix(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name string
		mode workflowMode
	}{
		{name: "legacy delegated child", mode: workflowModeDelegatedChild},
		{name: "code sub-agent", mode: workflowModeDelegatedCodeSubAgent},
		{name: "documentor", mode: workflowModeDelegatedDocumentor},
		{name: "non-code child", mode: workflowModeDelegatedNonCodeChild},
	}

	for _, mode := range modes {
		for _, tc := range []struct {
			name     string
			override string
		}{
			{name: "default"},
			{name: "override", override: "custom override"},
		} {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				const suffix = "caller suffix"
				content := systemPreambleWithAdvisor(SystemPreambleParams{
					Override:     tc.override,
					Mode:         mode.mode,
					SystemSuffix: suffix,
				}).Content

				if got := strings.Count(content, "## Delegated task"); got != 1 {
					t.Fatalf("delegated-task authorization count = %d, want 1 in %q", got, content)
				}
				batching := strings.Index(content, testToolBatchingMarker)
				outputVoice := strings.Index(content, testOutputVoiceMarker)
				delegatedTask := strings.Index(content, "## Delegated task")
				if batching < 0 || outputVoice < 0 {
					t.Fatalf("missing ordering marker in %q", content)
				}
				if batching >= outputVoice || outputVoice >= delegatedTask {
					t.Fatalf("want tool batching, output voice, then delegated task order in %q", content)
				}
				if !strings.HasSuffix(strings.TrimSpace(content), suffix) {
					t.Fatalf("suffix %q not at end of preamble in %q", suffix, content)
				}
			})
		}
	}
}

// TestParentOverrideRetainsWorkflowWorkingRules pins the override behaviour for
// orchestrators: the shared workflow working rules still render around override
// text, and no delegated-task authorization is added for the parent.
func TestParentOverrideRetainsWorkflowWorkingRules(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{
		Override:          "custom override",
		DelegationEnabled: true,
		Mode:              workflowModeParent,
	}).Content

	for _, want := range []string{testWorkflowMarker, "## Working rules", "### While editing", "### Verification", "## Final response"} {
		if !strings.Contains(content, want) {
			t.Fatalf("parent override preamble missing %q in %q", want, content)
		}
	}
	if strings.Contains(content, "## Delegated task") {
		t.Fatalf("parent override preamble contains delegated-task authorization in %q", content)
	}
	workflow := strings.Index(content, testWorkflowMarker)
	override := strings.Index(content, "custom override")
	if workflow < 0 || override < 0 || workflow >= override {
		t.Fatalf("workflow methodology should precede the override text in %q", content)
	}
}

// TestWorkflowGeneralRulesAndAmbiguityByMode pins the consolidated working
// rules that replaced the standalone core rules section. The general rules
// render for every writer role, survive a system override, and each role gets
// the ambiguity instruction the shared workflow template selects.
func TestWorkflowGeneralRulesAndAmbiguityByMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		mode          workflowMode
		override      string
		wantRules     bool
		wantAmbiguity string
	}{
		{name: "parent", mode: workflowModeParent, wantRules: true, wantAmbiguity: "ask the user"},
		{name: "parent override", mode: workflowModeParent, override: "custom override", wantRules: true, wantAmbiguity: "ask the user"},
		{name: "legacy delegated child", mode: workflowModeDelegatedChild, wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "legacy delegated child override", mode: workflowModeDelegatedChild, override: "custom override", wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "code sub-agent", mode: workflowModeDelegatedCodeSubAgent, wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "code sub-agent override", mode: workflowModeDelegatedCodeSubAgent, override: "custom override", wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "documentor", mode: workflowModeDelegatedDocumentor, wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "documentor override", mode: workflowModeDelegatedDocumentor, override: "custom override", wantRules: true, wantAmbiguity: "stop and report the blocker"},
		{name: "non-code child", mode: workflowModeDelegatedNonCodeChild, wantRules: false},
		{name: "non-code override", mode: workflowModeDelegatedNonCodeChild, override: "custom override", wantRules: false},
	}

	const ambiguity = "If ambiguity materially changes implementation, "

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, Mode: tc.mode}).Content

			for _, rule := range []string{
				"### General",
				"Do only the assigned task.",
				"### Choosing the change",
				"Prefer the smallest correct change.",
				"  1. Skip what the task does not need",
				"Match existing project style and structure:",
				"The codebase root is the current folder.",
				"Use at most one `bash` call per turn",
			} {
				if got := strings.Contains(content, rule); got != tc.wantRules {
					t.Fatalf("consolidated rule %q present = %v, want %v in %q", rule, got, tc.wantRules, content)
				}
			}
			if !tc.wantRules {
				if strings.Contains(content, ambiguity) {
					t.Fatalf("non-writer preamble contains the ambiguity instruction in %q", content)
				}
				return
			}

			// Code, documentor, and the legacy delegated child are writers.
			writer := tc.mode != workflowModeParent
			roleRules := []string{"Push back on overcomplicated, risky, or unnecessary requests and surface important tradeoffs briefly."}
			if writer {
				roleRules = []string{
					"Report overcomplicated, risky, or unnecessary requests and surface important tradeoffs briefly.",
					"The codebase root is the current folder. Work in the assigned worktree.",
				}
			}
			for _, rule := range roleRules {
				if !strings.Contains(content, rule) {
					t.Fatalf("preamble missing role rule %q in %q", rule, content)
				}
			}
			if !writer && strings.Contains(content, "Work in the assigned worktree.") {
				t.Fatalf("parent preamble contains the writer worktree rule in %q", content)
			}

			wantLine := ambiguity + tc.wantAmbiguity + "."
			if got := strings.Count(content, wantLine); got != 1 {
				t.Fatalf("ambiguity instruction %q count = %d, want 1 in %q", wantLine, got, content)
			}
			if got := strings.Count(content, wantLine+" Otherwise state the assumption and continue."); got != 1 {
				t.Fatalf("ambiguity fallback count = %d, want 1 in %q", got, content)
			}
			other := "stop and report the blocker"
			if tc.wantAmbiguity == other {
				other = "ask the user"
			}
			if strings.Contains(content, ambiguity+other+".") {
				t.Fatalf("preamble contains the other role's ambiguity branch %q in %q", ambiguity+other+".", content)
			}
		})
	}
}

// TestWorkflowRulesAllowBoundedMoveAndCopy pins the bounded move and copy
// escape hatch in the shared editing rules for the roles that may write with
// bash.
func TestWorkflowRulesAllowBoundedMoveAndCopy(t *testing.T) {
	t.Parallel()

	want := "- Use the `mutate` tool for content edits; its `move` op handles single-file moves. Where a copy or move cannot be expressed by `mutate` (for example a directory tree), bash `cp` or `mv` may be used only where your role, mode, and sandbox allow bash writes, both paths are inside your assigned write scope, and the destination does not exist. Do not use bash, sed, cat, or shell redirection for content edits."
	for _, tc := range []struct {
		name string
		mode workflowMode
	}{
		{name: "parent", mode: workflowModeParent},
		{name: "code child", mode: workflowModeDelegatedCodeSubAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: tc.mode}).Content
			if !strings.Contains(content, want) {
				t.Fatalf("workflow editing rules missing %q in %q", want, content)
			}

			if old := "do not use bash, sed, cat, or shell redirection for edits."; strings.Contains(content, old) {
				t.Fatalf("workflow editing rules still contain %q in %q", old, content)
			}
		})
	}
}

// TestWorkflowRulesForbidDocumentorBashWrites pins the documentor editing rule:
// bash writes are forbidden, so a copy or move that `mutate` cannot express is
// left undone and reported instead.
func TestWorkflowRulesForbidDocumentorBashWrites(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: DelegatedDocumentorWorkflowMode()}).Content

	want := "- Use the `mutate` tool for content edits; its `move` op handles single-file moves. Your role forbids bash writes: never use bash, including `cp` or `mv`, to create or change files. Leave a copy or move that `mutate` cannot express (for example a directory tree) undone and report it."
	if !strings.Contains(content, want) {
		t.Fatalf("documentor editing rules missing %q in %q", want, content)
	}

	for _, forbidden := range []string{
		"bash `cp` or `mv` may be used",
		"Do not use bash, sed, cat, or shell redirection for content edits.",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("documentor editing rules contain %q in %q", forbidden, content)
		}
	}
}
