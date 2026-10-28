package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestBashArtifactTruncatedStaysOutOfTranscript(t *testing.T) {
	t.Parallel()
	b := newTestBuffer(t)
	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-1", ".steiner/artifacts/call-1.txt", "warning: bash artifact truncated: partial output saved to .steiner/artifacts/call-1.txt"))

	child := output.NewBashArtifactTruncatedEvent(3, "call-2", ".steiner/artifacts/call-2.txt", "warning: bash artifact truncated: partial output saved to .steiner/artifacts/call-2.txt")
	child.Scope = output.EventScope{AgentID: "child-1", AgentType: "code"}
	b.AppendEvent(child)

	b.AppendEvent(output.NewBashArtifactTruncatedEvent(3, "call-3", "", ""))

	if len(b.segments) != 0 {
		t.Fatalf("segments = %#v, want none: the warning belongs in the dev log only", b.segments)
	}
}
