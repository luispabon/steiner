package output

import (
	"bytes"
	"os"
	"testing"
)

func TestSupportsANSIWithNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "xterm")
	result := SupportsANSI(&bytes.Buffer{})
	if result {
		t.Error("SupportsANSI should return false with NO_COLOR set")
	}
}

func TestSupportsANSIWithDumbTerminal(t *testing.T) {
	tests := []struct {
		termValue string
		name      string
	}{
		{"dumb", "lowercase dumb"},
		{"DUMB", "uppercase DUMB"},
		{"Dumb", "mixed case Dumb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("TERM", tt.termValue)
			result := SupportsANSI(&bytes.Buffer{})
			if result {
				t.Errorf("SupportsANSI should return false with TERM=%s", tt.termValue)
			}
		})
	}
}

func TestSupportsANSIWithNonFileWriter(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	result := SupportsANSI(&bytes.Buffer{})
	if result {
		t.Error("SupportsANSI should return false for non-*os.File writers")
	}
}

func TestSupportsANSIWithCharDevice(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")

	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer func() { _ = f.Close() }()

	result := SupportsANSI(f)
	if !result {
		t.Error("SupportsANSI should return true for character device")
	}
}

func TestCountPreviewChanges(t *testing.T) {
	tests := []struct {
		name     string
		lines    []PreviewLine
		wantAdds int
		wantRms  int
	}{
		{
			name:     "no changes",
			lines:    []PreviewLine{},
			wantAdds: 0,
			wantRms:  0,
		},
		{
			name: "additions only",
			lines: []PreviewLine{
				{Kind: PreviewLineKindAdded},
				{Kind: PreviewLineKindAdded},
			},
			wantAdds: 2,
			wantRms:  0,
		},
		{
			name: "removals only",
			lines: []PreviewLine{
				{Kind: PreviewLineKindRemoved},
				{Kind: PreviewLineKindRemoved},
				{Kind: PreviewLineKindRemoved},
			},
			wantAdds: 0,
			wantRms:  3,
		},
		{
			name: "mixed changes",
			lines: []PreviewLine{
				{Kind: PreviewLineKindAdded},
				{Kind: PreviewLineKindContext},
				{Kind: PreviewLineKindRemoved},
				{Kind: PreviewLineKindAdded},
			},
			wantAdds: 2,
			wantRms:  1,
		},
		{
			name: "with headers and context",
			lines: []PreviewLine{
				{Kind: PreviewLineKindHeader},
				{Kind: PreviewLineKindContext},
				{Kind: PreviewLineKindAdded},
				{Kind: PreviewLineKindRemoved},
			},
			wantAdds: 1,
			wantRms:  1,
		},
		{
			name: "truncation lines ignored",
			lines: []PreviewLine{
				{Kind: PreviewLineKindAdded},
				{Kind: PreviewLineKindTruncated},
				{Kind: PreviewLineKindRemoved},
			},
			wantAdds: 1,
			wantRms:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adds, rms := CountPreviewChanges(PreviewDocument{Lines: tt.lines})
			if adds != tt.wantAdds || rms != tt.wantRms {
				t.Fatalf("CountPreviewChanges() = (%d, %d), want (%d, %d)", adds, rms, tt.wantAdds, tt.wantRms)
			}
		})
	}
}

func TestRenderPreviewLineText(t *testing.T) {
	tests := []struct {
		name string
		line PreviewLine
		want string
	}{
		{
			name: "prefix and text",
			line: PreviewLine{
				Prefix: "+",
				Spans: []PreviewSpan{
					{Text: "new line"},
				},
			},
			want: "+ new line",
		},
		{
			name: "prefix only",
			line: PreviewLine{
				Prefix: "@@",
				Spans:  []PreviewSpan{},
			},
			want: "@@",
		},
		{
			name: "text only",
			line: PreviewLine{
				Prefix: "",
				Spans: []PreviewSpan{
					{Text: "content"},
				},
			},
			want: "content",
		},
		{
			name: "empty line",
			line: PreviewLine{
				Prefix: "",
				Spans:  []PreviewSpan{},
			},
			want: "",
		},
		{
			name: "multiple spans",
			line: PreviewLine{
				Prefix: "-",
				Spans: []PreviewSpan{
					{Text: "old "},
					{Text: "line"},
				},
			},
			want: "- old line",
		},
		{
			name: "whitespace prefix",
			line: PreviewLine{
				Prefix: "   ",
				Spans: []PreviewSpan{
					{Text: "text"},
				},
			},
			want: "text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderPreviewLineText(tt.line)
			if result != tt.want {
				t.Fatalf("renderPreviewLineText() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestRenderPreviewChannel(t *testing.T) {
	tests := []struct {
		kind PreviewLineKind
		want Channel
	}{
		{PreviewLineKindAdded, ChannelApproval},
		{PreviewLineKindRemoved, ChannelError},
		{PreviewLineKindHeader, ChannelStatus},
		{PreviewLineKindTruncated, ChannelStatus},
		{PreviewLineKindContext, ChannelTool},
		{PreviewLineKindText, ChannelTool},
		{"unknown", ChannelStatus},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := renderPreviewChannel(PreviewLine{Kind: tt.kind})
			if result != tt.want {
				t.Fatalf("renderPreviewChannel(%q) = %q, want %q", tt.kind, result, tt.want)
			}
		})
	}
}

func TestCompactionSummaryPreviewWithTrimming(t *testing.T) {
	payload := ContextDiagnosticsEvent{
		SummaryTitle:   "  Title with spaces  ",
		SummaryPreview: "  Preview text  ",
	}
	result := compactionSummaryPreview(payload)
	if result != "Title with spaces: Preview text" {
		t.Fatalf("result = %q", result)
	}
}

func TestSummarizeInspectionBasic(t *testing.T) {
	events := []Event{
		NewAssistantMessageEvent(1, "assistant", "test"),
		NewContextCompactionEvent(1, 1, 1, 1, 1, 0, false, "summary"),
	}

	result := summarizeInspection(events, 10)
	if result.TotalDiagnostics != len(events) {
		t.Errorf("TotalDiagnostics = %d, want %d", result.TotalDiagnostics, len(events))
	}
	if result.ContextDiagnostics != 1 {
		t.Errorf("ContextDiagnostics = %d, want 1", result.ContextDiagnostics)
	}
}

func TestSummarizeInspectionWithEmptyEvents(t *testing.T) {
	result := summarizeInspection([]Event{}, 10)
	if result.TotalDiagnostics != 0 {
		t.Errorf("TotalDiagnostics = %d, want 0", result.TotalDiagnostics)
	}
	if result.ContextDiagnostics != 0 {
		t.Errorf("ContextDiagnostics = %d, want 0", result.ContextDiagnostics)
	}
	if len(result.Recent) != 0 {
		t.Errorf("Recent should be empty")
	}
}

func TestSummarizeInspectionWithNegativeLimit(t *testing.T) {
	events := []Event{
		NewAssistantMessageEvent(1, "assistant", "test"),
	}
	result := summarizeInspection(events, -1)
	if len(result.Recent) != 0 {
		t.Errorf("Recent should be empty with negative limit")
	}
}
