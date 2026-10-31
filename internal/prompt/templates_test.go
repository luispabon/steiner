package prompt

import (
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestPromptTemplatesParseAndExecute proves every preamble template is
// embedded, parsed, and executable with the data shape its renderer passes —
// parsing alone would not catch a mistyped field or a missing helper.
func TestPromptTemplatesParseAndExecute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data any
	}{
		{
			name: templateDelegation,
			data: struct {
				Specialists []specialistView
				Orchestrate bool
				Async       bool
			}{Specialists: specialistViews(), Orchestrate: true, Async: true},
		},
		{name: templateAdvisor},
		{name: templateCodeChild},
		{name: templateDocumentorChild},
		{name: "child_worktree.md.tmpl"},
		{name: templateCoreRules},
		{name: templateWorkflow, data: struct {
			DelegatedCode       bool
			DelegatedDocumentor bool
			DelegatedNonCode    bool
			DelegationEnabled   bool
		}{DelegatedCode: true, DelegationEnabled: true}},
		{name: templateDelegatedTask},
		{name: templateSandbox, data: struct{ Mounts []string }{Mounts: []string{"/a", "/b"}}},
		{name: templateExecutionModes},
		{name: templateCompactionSystem},
		{name: templateCompactionEmergency},
		{name: templateCompactionDefaultBody},
		{name: templateOutputVoice},
		{name: templateLSP},
		{name: templateToolBatching},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if promptTemplates.Lookup(tc.name) == nil {
				t.Fatalf("template %q not parsed from templates/", tc.name)
			}

			var b strings.Builder
			if err := promptTemplates.ExecuteTemplate(&b, tc.name, tc.data); err != nil {
				t.Fatalf("execute template %q: %v", tc.name, err)
			}
			if strings.TrimSpace(b.String()) == "" {
				t.Fatalf("template %q rendered empty", tc.name)
			}
		})
	}
}

// TestExecutionModesTemplateListsPlanModeWritableDirs pins the plan-mode
// writable directory list in the execution modes template to
// config.PlanModeWritableDirs: the template must name exactly those
// directories, so a directory added to or removed from config fails here
// instead of leaving the prompt stale.
func TestExecutionModesTemplateListsPlanModeWritableDirs(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	if err := promptTemplates.ExecuteTemplate(&b, templateExecutionModes, nil); err != nil {
		t.Fatalf("execute template %q: %v", templateExecutionModes, err)
	}

	want := config.PlanModeWritableDirs()
	if len(want) == 0 {
		t.Fatal("config.PlanModeWritableDirs() returned no directories")
	}

	// Collapse the template's line wrapping so the denial clause is contiguous.
	rendered := strings.Join(strings.Fields(b.String()), " ")
	const clauseStart = "`mutate` is denied outside"
	i := strings.Index(rendered, clauseStart)
	if i < 0 {
		t.Fatalf("execution modes template missing %q in %q", clauseStart, rendered)
	}
	clause := rendered[i+len(clauseStart):]
	if j := strings.Index(clause, ";"); j >= 0 {
		clause = clause[:j]
	}

	// The clause lists directories as code spans, so odd split indexes are the
	// quoted paths. Each must match a configured directory, and every
	// configured directory must appear.
	parts := strings.Split(clause, "`")
	if len(parts)%2 == 0 {
		t.Fatalf("unbalanced backticks in plan-mode denial clause %q", clause)
	}
	var got []string
	for j := 1; j < len(parts); j += 2 {
		got = append(got, strings.TrimSuffix(parts[j], "/"))
	}
	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Errorf("plan-mode writable dirs in execution modes template = %#v, want %#v (clause %q)", got, want, clause)
	}
}
