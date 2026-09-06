package prompt

import (
	"strings"
	"testing"
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
			data: struct{ Specialists []specialistView }{Specialists: specialistViews()},
		},
		{name: templateAdvisor},
		{name: templateCoreRules},
		{name: templateWorkflow, data: struct {
			DelegatedCode     bool
			DelegatedNonCode  bool
			DelegationEnabled bool
		}{DelegatedCode: true, DelegationEnabled: true}},
		{name: templateDelegatedTask},
		{name: templateSandbox, data: struct{ Mounts []string }{Mounts: []string{"/a", "/b"}}},
		{name: templateExecutionModes},
		{name: templateCompactionSystem},
		{name: templateCompactionEmergency},
		{name: templateCompactionDefaultBody},
		{name: templateCaveHumanVoice},
		{name: templateCompactionCaveHumanBody},
		{name: templateCaveHumanCompactionEncode},
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
