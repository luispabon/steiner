package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestAppendEventDelegationStarted(t *testing.T) {
	event := output.NewDelegationStartedEvent("child-1", "fix the bug in module X")

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	// Verify no task content leakage
	if strings.Contains(seg.text, "module X") {
		t.Errorf("segment contains task content: %q", seg.text)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-1") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}
}

func TestAppendEventDelegationComplete(t *testing.T) {
	event := output.NewDelegationCompleteEvent("child-2", "complete", 5, 2000)

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "complete") {
		t.Errorf("segment missing 'complete': %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-2") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}

	if !strings.Contains(seg.text, "5 turns") {
		t.Errorf("segment missing turn count: %q", seg.text)
	}
}

func TestAppendEventDelegationFailed(t *testing.T) {
	event := output.NewDelegationFailedEvent("child-3", "build package", "compilation error")

	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(event)

	if len(buffer.segments) != 1 {
		t.Errorf("segments count = %d, want 1", len(buffer.segments))
		return
	}

	seg := buffer.segments[0]
	if seg.kind != segmentPlain {
		t.Errorf("segment kind = %v, want segmentPlain", seg.kind)
	}

	if !strings.Contains(seg.text, "delegate:") {
		t.Errorf("segment missing 'delegate:' prefix: %q", seg.text)
	}

	if !strings.Contains(seg.text, "failed") {
		t.Errorf("segment missing 'failed': %q", seg.text)
	}

	if !strings.Contains(seg.text, "child-3") {
		t.Errorf("segment missing agent ID: %q", seg.text)
	}

	// Verify no task content or error details leak
	if strings.Contains(seg.text, "build package") {
		t.Errorf("segment contains task preview: %q", seg.text)
	}

	if strings.Contains(seg.text, "compilation error") {
		t.Errorf("segment contains error message: %q", seg.text)
	}
}

func TestAppendEventDelegationNoContentLeakage(t *testing.T) {
	tests := []struct {
		name  string
		event output.Event
	}{
		{
			name:  "delegation_started",
			event: output.NewDelegationStartedEvent("agent-1", "secret task content here"),
		},
		{
			name:  "delegation_complete",
			event: output.NewDelegationCompleteEvent("agent-2", "complete", 1, 100),
		},
		{
			name:  "delegation_failed",
			event: output.NewDelegationFailedEvent("agent-3", "secret task", "error details"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{
				segments: make([]contentSegment, 0),
			}

			buffer.AppendEvent(tt.event)

			if len(buffer.segments) != 1 {
				t.Errorf("segments count = %d, want 1", len(buffer.segments))
				return
			}

			seg := buffer.segments[0]

			// These secrets should never appear in rendered output
			secrets := []string{
				"secret task content here",
				"error details",
				"secret task",
			}

			for _, secret := range secrets {
				if strings.Contains(seg.text, secret) {
					t.Errorf("segment contains sensitive content: %q found in %q", secret, seg.text)
				}
			}
		})
	}
}

func TestFormatDelegationEvent(t *testing.T) {
	tests := []struct {
		name      string
		event     output.Event
		wantMatch string
	}{
		{
			name:      "started",
			event:     output.NewDelegationStartedEvent("test-agent", "do work"),
			wantMatch: "delegate: starting test-agent",
		},
		{
			name:      "complete",
			event:     output.NewDelegationCompleteEvent("test-agent", "complete", 2, 500),
			wantMatch: "delegate: complete test-agent (2 turns)",
		},
		{
			name:      "failed",
			event:     output.NewDelegationFailedEvent("test-agent", "task", "err"),
			wantMatch: "delegate: failed test-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDelegationEvent(tt.event)
			if result != tt.wantMatch {
				t.Errorf("formatDelegationEvent = %q, want %q", result, tt.wantMatch)
			}
		})
	}
}

func TestAppendEventContextDiagnosticsAreVisible(t *testing.T) {
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "warning",
		SessionState:    "fragile",
		CompactionCount: 2,
		RestartGuidance: "restart soon in a fresh session; repeated compaction is making retention fragile",
	}))
	buffer.AppendEvent(output.NewContextSessionHealthEvent("conversation", 2, 2, "warning", "fragile", "restart soon in a fresh session; repeated compaction is making retention fragile"))

	if len(buffer.segments) != 2 {
		t.Fatalf("segments count = %d, want 2", len(buffer.segments))
	}
	// First segment should be compaction banner
	if buffer.segments[0].kind != segmentCompactionBanner {
		t.Fatalf("segment[0] kind = %v, want segmentCompactionBanner", buffer.segments[0].kind)
	}
	if buffer.segments[0].compactionData == nil {
		t.Fatalf("segment[0] compactionData is nil")
	}
	if !strings.Contains(buffer.segments[0].compactionData.summary, "compacted") {
		t.Fatalf("compaction summary = %q, want visible compaction data", buffer.segments[0].compactionData.summary)
	}
	// Second segment should be session health (still thinking)
	if buffer.segments[1].kind != segmentThinking {
		t.Fatalf("segment[1] kind = %v, want segmentThinking", buffer.segments[1].kind)
	}
	if got := buffer.segments[1].text; !strings.Contains(got, "session health") {
		t.Fatalf("session health text = %q, want visible health state", got)
	}
}

func TestAppendEventContextReportRendersMarkdownBlock(t *testing.T) {
	buffer := &contentBuffer{
		segments: make([]contentSegment, 0),
	}

	buffer.AppendEvent(output.NewContextReportEvent("# Last Request Context\n\nPrompt tokens: `42`"))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}
	if got := buffer.segments[0].text; !strings.Contains(got, "Last Request Context") || !strings.Contains(got, "Prompt tokens") {
		t.Fatalf("segment text = %q, want context report block", got)
	}
}

func TestPluralTurns(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{0, "0 turns"},
		{1, "1 turn"},
		{2, "2 turns"},
		{10, "10 turns"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := pluralTurns(tt.count)
			if result != tt.want {
				t.Errorf("pluralTurns(%d) = %q, want %q", tt.count, result, tt.want)
			}
		})
	}
}

func TestAppendEventToolPreviewUsesStructuredData(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	before := false
	buffer.AppendEvent(output.NewToolCallStartedEventWithPreviewState(1, "write", "call_1", map[string]any{
		"path":    "notes.md",
		"content": "hello\nworld\n",
	}, &before))
	buffer.AppendEvent(output.NewToolCallFinishedEventWithPreview(1, "write", "call_1", `{"path":"notes.md","bytes_written":12}`, nil, output.ToolPreview{
		Kind:     output.ToolPreviewKindFileWrite,
		Path:     "notes.md",
		Language: "markdown",
		Contents: "hello\nworld\n",
		Created:  true,
	}))

	if len(buffer.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(buffer.segments))
	}

	seg := buffer.segments[0].toolData
	if seg == nil {
		t.Fatalf("tool segment is nil")
	}
	if got, want := seg.bodyKind, "file"; got != want {
		t.Fatalf("bodyKind = %q, want %q", got, want)
	}
	if got, want := seg.preview.Kind, output.ToolPreviewKindFileWrite; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if seg.writeTargetExistedBefore == nil || *seg.writeTargetExistedBefore {
		t.Fatalf("writeTargetExistedBefore = %v, want false", seg.writeTargetExistedBefore)
	}
	if got, want := seg.preview.Path, "notes.md"; got != want {
		t.Fatalf("preview path = %q, want %q", got, want)
	}
	if got, want := seg.preview.Contents, "hello\nworld\n"; got != want {
		t.Fatalf("preview contents = %q, want %q", got, want)
	}
	if !seg.preview.Created {
		t.Fatalf("preview created = false, want true")
	}
}

func TestAppendEventBuildsFallbackPreviewFromRetainedArgs(t *testing.T) {
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewToolCallStartedEvent(1, "edit", "call_1", map[string]any{
		"path":       "main.go",
		"old_string": "oldLine()",
		"new_string": "newLine()",
	}))
	buffer.AppendEvent(output.NewToolCallFinishedEvent(1, "edit", "call_1", `{"path":"main.go","replacements":1}`, nil))

	if len(buffer.segments) != 1 || buffer.segments[0].toolData == nil {
		t.Fatalf("tool segments = %#v, want one populated tool segment", buffer.segments)
	}
	seg := buffer.segments[0].toolData
	if got, want := seg.preview.Kind, output.ToolPreviewKindEditDiff; got != want {
		t.Fatalf("preview kind = %q, want %q", got, want)
	}
	if got, want := seg.bodyKind, "diff"; got != want {
		t.Fatalf("bodyKind = %q, want %q", got, want)
	}
	if got, want := seg.preview.Before, "oldLine()"; got != want {
		t.Fatalf("preview before = %q, want %q", got, want)
	}
	if got, want := seg.preview.After, "newLine()"; got != want {
		t.Fatalf("preview after = %q, want %q", got, want)
	}
}

func TestRenderToolPreviewUsesStructuredFilePreview(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "write",
				args:      "notes.md",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindFileWrite,
					Path:     "notes.md",
					Contents: "hello\nworld\n",
					Created:  true,
				},
			},
		},
	}

	got := buffer.String(80)
	for _, want := range []string{"new file preview", "notes.md", "hello", "world"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered preview %q missing %q", got, want)
		}
	}
}

func TestRenderToolPreviewUsesStructuredDiffPreview(t *testing.T) {
	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "edit",
				args:      "internal/tui/content.go",
				bodyKind:  "diff",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:   output.ToolPreviewKindEditDiff,
					Path:   "internal/tui/content.go",
					Before: "fmt.Println(\"old\")\n",
					After:  "fmt.Println(\"new\")\n",
				},
			},
		},
	}

	got := buffer.String(100)
	for _, want := range []string{"edit", "internal/tui/content.go", "+1", "-1", "old", "new"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered diff %q missing %q", got, want)
		}
	}
}

func TestRenderToolPreviewTruncatesLongFileBodies(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 30; i++ {
		body.WriteString("line\n")
	}

	buffer := &contentBuffer{
		styles:        theme.BuildStyles(theme.AccentAmber),
		collapseState: make(map[int]bool),
	}
	buffer.segments = []contentSegment{
		{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:      "read",
				args:      "notes.txt",
				bodyKind:  "file",
				collapsed: false,
				preview: output.ToolPreview{
					Kind:     output.ToolPreviewKindReadFile,
					Path:     "notes.txt",
					Contents: body.String(),
				},
			},
		},
	}

	got := buffer.String(80)
	if !strings.Contains(got, "… output truncated") && !strings.Contains(got, "↓ more") {
		t.Fatalf("rendered preview %q missing truncation marker", got)
	}
}
