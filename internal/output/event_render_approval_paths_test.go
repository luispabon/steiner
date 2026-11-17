package output

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderApprovalEvent_ListsOutsidePaths pins that a path approval shows the
// outside paths one per line, summarises any beyond the collected list, and
// sanitizes model-supplied path text.
func TestRenderApprovalEvent_ListsOutsidePaths(t *testing.T) {
	tests := []struct {
		name        string
		deniedPaths []string
		count       int
		wantLines   []string
		wantAbsent  []string
	}{
		{
			name:        "every collected path on its own line",
			deniedPaths: []string{"/outside/a.txt", "/outside/b.txt"},
			count:       2,
			wantLines:   []string{"\n  /outside/a.txt\n  /outside/b.txt"},
			wantAbsent:  []string{"more outside the project"},
		},
		{
			name:        "remaining paths are counted",
			deniedPaths: []string{"/outside/a.txt", "/outside/b.txt"},
			count:       5,
			wantLines:   []string{"\n  /outside/a.txt\n  /outside/b.txt\n  and 3 more outside the project"},
		},
		{
			name:        "path text stays on one sanitized line",
			deniedPaths: []string{"/outside/\x1b[31minside\nspoofed.txt"},
			count:       1,
			wantLines:   []string{"\n  /outside/inside spoofed.txt"},
			wantAbsent:  []string{"\x1b", "\n  spoofed.txt"},
		},
		{
			name:       "no paths adds no block",
			count:      0,
			wantLines:  []string{"tool=mutate"},
			wantAbsent: []string{"outside paths"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := NewApprovalRequestedEvent(ApprovalEventParams{
				Tool:            "mutate",
				Kind:            "path",
				Mode:            "paths outside the project root",
				DeniedPaths:     tt.deniedPaths,
				DeniedPathCount: tt.count,
			})
			segment := renderEvent(event)
			if len(tt.deniedPaths) > 0 && !strings.Contains(segment.Text, "outside paths:") {
				t.Fatalf("segment text = %q, want an outside paths block", segment.Text)
			}
			for _, want := range tt.wantLines {
				if !strings.Contains(segment.Text, want) {
					t.Fatalf("segment text = %q, want it to contain %q", segment.Text, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(segment.Text, absent) {
					t.Fatalf("segment text = %q, want it to omit %q", segment.Text, absent)
				}
			}
		})
	}
}

// TestPlainRendererPrintsOutsidePathsForExecPrompt pins that the non-interactive
// approval prompt shows the path list, since that line is the whole prompt.
func TestPlainRendererPrintsOutsidePathsForExecPrompt(t *testing.T) {
	var out bytes.Buffer
	renderer := NewPlainRenderer(&out)
	renderer.OnEvent(NewApprovalRequestedEvent(ApprovalEventParams{
		Tool:            "mutate",
		Kind:            "path",
		Mode:            "paths outside the project root",
		DeniedPaths:     []string{"/outside/a.txt", "/outside/b.txt"},
		DeniedPathCount: 4,
	}))

	got := out.String()
	want := "\n  /outside/a.txt\n  /outside/b.txt\n  and 2 more outside the project\n"
	if !strings.Contains(got, want) {
		t.Fatalf("prompt = %q, want it to contain %q", got, want)
	}
}
