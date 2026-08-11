package delegation

import (
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

// TestPreambleSpecialistRosterMatchesAgentTypes pins the canon roster in
// internal/prompt to the AgentType constants registered here. internal/prompt
// cannot import this package to derive the roster (bootstrap.go imports
// internal/prompt, so the reverse is an import cycle), so a Go rename or an
// added/removed AgentType is caught here rather than being impossible.
func TestPreambleSpecialistRosterMatchesAgentTypes(t *testing.T) {
	roster := prompt.SpecialistNames()

	want := []string{"explore", "research", "code", "evaluate", "sanity_check", "review"}
	if !slices.Equal(roster, want) {
		t.Errorf("prompt.SpecialistNames() = %v, want %v", roster, want)
	}

	// Names that exist here but are deliberately absent from the roster,
	// with the reason each is excluded.
	excluded := map[string]string{
		string(AgentTypeVision): "internal-only: dispatched for image analysis, never routed by the orchestrator",
		FollowUpToolName:        "not an AgentType: a continuation of an existing sub-agent, not a specialist to route to",
	}
	for name, reason := range excluded {
		if reason == "" {
			t.Errorf("excluded %q has empty reason string", name)
		}
		if slices.Contains(roster, name) {
			t.Errorf("roster should not contain excluded %q (%s)", name, reason)
		}
	}

	// Every registered AgentType must be either rostered or explicitly excluded.
	for _, agentType := range AllAgentTypes() {
		name := string(agentType)
		if _, isExcluded := excluded[name]; isExcluded {
			continue
		}
		if !slices.Contains(roster, name) {
			t.Errorf("AgentType %q is registered but neither in the canon roster nor explicitly excluded", name)
		}
	}
}
