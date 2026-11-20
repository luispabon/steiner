package session

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestSummaryPartsShowsLatestRequestVerbatim(t *testing.T) {
	t.Parallel()

	content := "summary body\n" + agent.HistoryNoteMarker + " archived at /tmp/history.md\n\n" +
		"[steiner:latest-request bytes=49]\n" + agent.HistoryNoteMarker + " keep this request line verbatim"
	parts := summaryParts(content)
	if len(parts) != 4 {
		t.Fatalf("summaryParts() = %q, want divider, body, heading and request", parts)
	}
	if parts[1] != "summary body" {
		t.Errorf("body part = %q, want the history note stripped from the body only", parts[1])
	}
	if parts[2] != "Latest request (verbatim):" {
		t.Errorf("heading part = %q, want the verbatim request heading", parts[2])
	}
	if parts[3] != agent.HistoryNoteMarker+" keep this request line verbatim" {
		t.Errorf("request part = %q, want the request byte for byte", parts[3])
	}
}
