package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestCompactionResultMarkdownShowsLatestRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		summaryText string
		want        []string
		absent      []string
	}{
		{
			name:        "body and request",
			summaryText: "summary body\n\n[steiner:latest-request bytes=19]\nkeep the exact text",
			want:        []string{"summary body", "Latest request (verbatim):", "keep the exact text"},
			absent:      []string{"[steiner:latest-request"},
		},
		{
			name:        "body only leaves no heading",
			summaryText: "summary body",
			want:        []string{"summary body"},
			absent:      []string{"Latest request (verbatim):"},
		},
		{
			name:        "history notes stay in the body",
			summaryText: "summary body\n[steiner:history] archived at /tmp/history.md\n\n[steiner:latest-request bytes=49]\n[steiner:history] keep this request line verbatim",
			want:        []string{"archived at /tmp/history.md", "Latest request (verbatim):", "[steiner:history] keep this request line"},
			absent:      []string{"[steiner:latest-request"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compactionResultMarkdown(tt.summaryText)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("rendered summary = %q, want %q", got, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("rendered summary = %q, want no %q", got, absent)
				}
			}
		})
	}
}

func TestCompactionResultMarkdownFencesRequestWithBackticks(t *testing.T) {
	t.Parallel()

	request := "```go\nfmt.Println(\"hi\")\n```"
	summaryText := "summary body\n\n[steiner:latest-request bytes=" + strconv.Itoa(len(request)) + "]\n" + request
	got := compactionResultMarkdown(summaryText)
	want := "summary body\n\nLatest request (verbatim):\n\n````\n" + request + "\n````"
	if got != want {
		t.Errorf("rendered summary = %q, want %q", got, want)
	}
}

func TestCompactionSummaryDetailReportsRequestBlock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   string
	}{
		{status: output.RequestBlockNew, want: "latest request kept verbatim (new)"},
		{status: output.RequestBlockCarried, want: "latest request kept verbatim (carried)"},
		{status: output.RequestBlockDropped, want: "latest request dropped to fit"},
		{status: output.RequestBlockNone, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := compactionSummaryDetail(&compactionBannerData{requestBlock: tt.status})
			if tt.want == "" {
				if strings.Contains(got, "latest request") {
					t.Fatalf("summary detail = %q, want no request block note", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("summary detail = %q, want %q", got, tt.want)
			}
			if tt.status == output.RequestBlockDropped && strings.Contains(got, "kept verbatim") {
				t.Fatalf("summary detail = %q, want no kept-verbatim note for a dropped block", got)
			}
		})
	}
}
