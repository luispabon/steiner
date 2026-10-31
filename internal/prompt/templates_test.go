package prompt

import (
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

// TestExecutionModesTemplateListsPlanModeWritableDirs proves the plan-mode
// section names every directory that stays writable in plan mode. The list
// comes from config.PlanModeWritableDirs so a new writable directory cannot
// drift out of the prompt.
func TestExecutionModesTemplateListsPlanModeWritableDirs(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	if err := promptTemplates.ExecuteTemplate(&b, templateExecutionModes, nil); err != nil {
		t.Fatalf("execute template %q: %v", templateExecutionModes, err)
	}

	dirs := config.PlanModeWritableDirs()
	if len(dirs) == 0 {
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

	for _, dir := range dirs {
		if want := dir + "/"; !strings.Contains(clause, want) {
			t.Errorf("plan-mode mutate denial missing writable dir %q in %q", want, clause)
		}
	}
}
