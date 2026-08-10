package delegation

import (
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

func TestPreambleSpecialistRosterMatchesAgentTypes(t *testing.T) {
	content := prompt.SystemPreamble("", true, false, "").Content

	// (1) Parse the table
	parsed := parseSpecialistRoster(t, content)

	// (2) Compare against explicit literal expectation
	want := []string{"explore", "research", "code", "evaluate", "sanity_check", "review"}
	if !slices.Equal(parsed, want) {
		t.Errorf("parsed roster = %v, want %v", parsed, want)
	}

	// (3) Assert exclusions explicitly
	excluded := map[string]string{
		string(AgentTypeVision): "internal-only: dispatched for image analysis, never routed by the orchestrator",
		FollowUpToolName:        "not an AgentType: a continuation of an existing sub-agent, not a specialist to route to",
	}

	// Verify no overlap between parsed and excluded
	for name := range excluded {
		if slices.Contains(parsed, name) {
			t.Errorf("parsed roster should not contain excluded %q", name)
		}
	}

	// Verify every name in AllAgentTypes() is either in parsed or excluded
	allAgentTypes := AllAgentTypes()
	parsedSet := make(map[string]bool)
	for _, name := range parsed {
		parsedSet[name] = true
	}
	excludedSet := make(map[string]bool)
	for name := range excluded {
		excludedSet[name] = true
	}

	for _, agentType := range allAgentTypes {
		agentTypeStr := string(agentType)
		if !parsedSet[agentTypeStr] && !excludedSet[agentTypeStr] {
			t.Errorf("AgentType %q is in AllAgentTypes() but not in parsed roster and not in excluded map", agentTypeStr)
		}
		if parsedSet[agentTypeStr] && excludedSet[agentTypeStr] {
			t.Errorf("AgentType %q is in both parsed roster and excluded map", agentTypeStr)
		}
	}

	// Count identity: len(parsed) + len(excludedAgentTypes) == len(AllAgentTypes())
	excludedAgentTypeCount := 0
	for name := range excluded {
		if findAgentTypeByString(name, allAgentTypes) {
			excludedAgentTypeCount++
		}
	}
	if len(parsed)+excludedAgentTypeCount != len(allAgentTypes) {
		t.Errorf("count mismatch: len(parsed)=%d + len(excludedAgentTypes)=%d != len(AllAgentTypes())=%d", len(parsed), excludedAgentTypeCount, len(allAgentTypes))
	}

	// Verify excluded AgentTypes are absent from parsed
	for agentType := range excluded {
		if findAgentTypeByString(agentType, allAgentTypes) {
			if slices.Contains(parsed, agentType) {
				t.Errorf("excluded AgentType %q should not be in parsed roster", agentType)
			}
		}
	}

	// Separately assert FollowUpToolName is absent from parsed
	if slices.Contains(parsed, FollowUpToolName) {
		t.Errorf("FollowUpToolName %q should not be in parsed roster (not an AgentType)", FollowUpToolName)
	}

	// Use reason strings to ensure they are not dead weight
	for name, reason := range excluded {
		if reason == "" {
			t.Errorf("excluded %q has empty reason string", name)
		}
	}
}

func parseSpecialistRoster(t *testing.T, content string) []string {
	t.Helper()

	lines := strings.Split(content, "\n")

	var headingIndex int
	var foundHeading bool

	// Find the heading
	for i, line := range lines {
		if strings.Contains(line, "## Your specialists") {
			foundHeading = true
			headingIndex = i
			break
		}
	}

	if !foundHeading {
		t.Fatalf("## Your specialists heading not found in preamble")
	}

	var parsed []string

	// Start from the line after the heading
	for i := headingIndex + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip blank lines (but continue looking for table rows)
		if trimmed == "" {
			continue
		}

		// Stop at next heading (##)
		if strings.HasPrefix(trimmed, "##") {
			break
		}

		// Table rows start with "| "
		if !strings.HasPrefix(line, "| ") {
			// If we've found rows and hit a non-table line, we're done
			if len(parsed) > 0 {
				break
			}
			continue
		}

		// Skip header row (contains "Agent", "Lane", "Do not use for")
		if strings.Contains(line, "Agent") && strings.Contains(line, "Lane") && strings.Contains(line, "Do not use for") {
			continue
		}

		// Skip separator row (cells contain only dashes)
		if isSeparatorRow(line) {
			continue
		}

		// Extract first cell (the agent name)
		cells := strings.Split(line, "|")
		if len(cells) < 2 {
			continue
		}

		firstCell := strings.TrimSpace(cells[1])
		// Remove backticks
		firstCell = strings.Trim(firstCell, "`")
		if firstCell != "" {
			parsed = append(parsed, firstCell)
		}
	}

	if len(parsed) == 0 {
		t.Fatalf("parsed specialist roster is empty (heading found but no data rows)")
	}

	return parsed
}

func isSeparatorRow(line string) bool {
	cells := strings.Split(line, "|")
	// A separator row has cells that are only dashes and whitespace
	for _, cell := range cells[1 : len(cells)-1] {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		// Check if cell is only dashes
		allDashes := true
		for _, ch := range trimmed {
			if ch != '-' {
				allDashes = false
				break
			}
		}
		if !allDashes {
			return false
		}
	}
	return true
}

func findAgentTypeByString(s string, agentTypes []AgentType) bool {
	for _, at := range agentTypes {
		if string(at) == s {
			return true
		}
	}
	return false
}
