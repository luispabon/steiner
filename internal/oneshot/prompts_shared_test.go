package oneshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The oneshot phase prompts and the interactive skills of the same name are
// separate documents that deliberately diverge: oneshot runs unattended
// against a plan manifest, the skills run interactively. They do share some
// blocks verbatim, and those are the missed-migration risk — a rule edited in
// one document and forgotten in the other. Every span below was measured to be
// byte-identical in both files; the test fails if any copy drifts.
//
// This is the whole of the measured overlap, not a sample. If a block here
// should legitimately differ between the two documents, delete it from this
// table and record the divergence in docs/canon-drift-checks.md.

const researchTriggersBlock = "- external APIs, SDKs, providers, model behavior, or product behavior\n" +
	"- third-party dependencies, framework behavior, or CLI/tool behavior\n" +
	"- security-sensitive behavior, compliance-sensitive behavior, or published best practices\n" +
	"- unfamiliar, domain-specific, risky, or low-confidence areas\n" +
	"- uncertainty that could materially change scope, risks, acceptance criteria, or implementation steps\n" +
	"\n" +
	"Research may be skipped when the task is repo-local, stable, and sufficiently understood from nearby code and repository instructions."

const planStepSchemaBlock = "```yaml\n" +
	"steps:\n" +
	"  - id: step-1\n" +
	"    title: ...\n" +
	"    scope: ...\n" +
	"    decisions: []\n" +
	"    approach: ...\n" +
	"    files: []\n" +
	"    constraints: []\n" +
	"    acceptance: []\n" +
	"    verification: []\n" +
	"```"

const reviewInputsBlock = "## Review Standard\n" +
	"\n" +
	"Compare these inputs:\n" +
	"\n" +
	"- `overview.md` for original intent, boundaries, and verification strategy\n" +
	"- `plan.yaml` for the approved implementation contract\n" +
	"- `execution.md` for completed steps, deviations, and verification\n" +
	"- repository state for what actually landed"

const reviewStatusMappingBlock = "Map final status as:\n" +
	"\n" +
	"- `fail` if blocking findings remain\n" +
	"- `pass_with_notes` if no blocking findings remain but non-blocking findings remain\n" +
	"- `pass` if only informational findings or no findings remain\n" +
	"\n" +
	"Only fixable blocking findings enter the fix plan by default. Include non-blocking fixes only when directly adjacent and negligible in scope."

func TestSharedBlocksMatchSkillCounterparts(t *testing.T) {
	blocks := []struct {
		name  string
		phase string
		text  string
	}{
		{"research triggers", "plan", researchTriggersBlock},
		{"plan step schema", "plan", planStepSchemaBlock},
		{"review inputs", "review", reviewInputsBlock},
		{"review status mapping", "review", reviewStatusMappingBlock},
	}

	for _, b := range blocks {
		t.Run(b.name, func(t *testing.T) {
			prompt, err := promptFiles.ReadFile("prompts/" + b.phase + ".md")
			if err != nil {
				t.Fatalf("read oneshot prompt %q: %v", b.phase, err)
			}

			skillPath := filepath.Join("..", "..", "skills", b.phase, "SKILL.md")
			skill, err := os.ReadFile(skillPath)
			if err != nil {
				t.Fatalf("read %s: %v", skillPath, err)
			}

			for _, doc := range []struct {
				path    string
				content string
			}{
				{"internal/oneshot/prompts/" + b.phase + ".md", string(prompt)},
				{"skills/" + b.phase + "/SKILL.md", string(skill)},
			} {
				if count := strings.Count(doc.content, b.text); count != 1 {
					t.Errorf("%s contains the %q block %d times, want exactly 1", doc.path, b.name, count)
				}
			}
		})
	}
}
