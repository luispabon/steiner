package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestResolveModelBadge(t *testing.T) {
	t.Parallel()
	aliases := map[string]string{"backend-model": "configured"}
	reasoningLabels := map[string]string{"configured": "high"}
	for _, tc := range []struct {
		name    string
		backend string
		want    string
		effort  string
	}{
		{name: "alias hit", backend: "backend-model", want: "configured", effort: "high"},
		{name: "alias miss", backend: "other-model", want: "other-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, effort := resolveModelBadge(tc.backend, aliases, reasoningLabels)
			if got != tc.want || effort != tc.effort {
				t.Fatalf("resolveModelBadge(%q) = (%q, %q), want (%q, %q)", tc.backend, got, effort, tc.want, tc.effort)
			}
		})
	}
}

func TestDelegationModelBadgeResolution(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:          make([]contentSegment, 0),
		collapseState:     make(map[int]bool),
		activeDelegations: make(map[string]delegationLocator),
		styles:            testStyles(theme.AccentAmber),
		modelBadge: func(backend string) (string, string) {
			if strings.TrimSpace(backend) == "backend-model" {
				return "configured", "high"
			}
			return strings.TrimSpace(backend), ""
		},
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "explore", "call-1", map[string]any{"task": "inspect"}))
	buffer.AppendEvent(output.NewDelegationStartedEvent("agent-1", "inspect", "call-1"))
	buffer.AppendEvent(output.WithAgentScope(output.NewModelCallStartedEvent(1, " backend-model ", 1), "agent-1"))

	loc, ok := buffer.activeDelegations["agent-1"]
	if !ok || loc.dd == nil {
		t.Fatal("delegation state not found")
	}
	if loc.dd.modelName != "configured" || loc.dd.reasoning != "high" {
		t.Fatalf("delegation model = (%q, %q), want (%q, %q)", loc.dd.modelName, loc.dd.reasoning, "configured", "high")
	}
	parts := delegationStatsParts(buffer, loc.dd)
	if len(parts) == 0 || !strings.Contains(stripANSI(parts[0]), "model configured/high") {
		t.Fatalf("delegation stats badge = %q, want configured/high", strings.Join(parts, " "))
	}
}

func TestDelegationModelBadgeFallback(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{}
	dd := &delegationDisplayState{}
	buffer.applyDelegationModelCallStarted(dd, output.NewModelCallStartedEvent(1, " backend-model ", 1))
	if dd.modelName != "backend-model" || dd.reasoning != "" {
		t.Fatalf("fallback model = (%q, %q), want (%q, %q)", dd.modelName, dd.reasoning, "backend-model", "")
	}
}
