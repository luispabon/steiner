package session

import (
	"strconv"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// requestSummary builds stored summary content whose trailer carries request.
func requestSummary(body, request string) string {
	return body + "\n\n[steiner:latest-request bytes=" + strconv.Itoa(len(request)) + "]\n" + request
}

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
	request := agent.HistoryNoteMarker + " keep this request line verbatim"
	if parts[3] != FenceVerbatim(request) {
		t.Errorf("request part = %q, want the request byte for byte in a fence", parts[3])
	}
}

func TestSummaryPartsFencesRequestWithBackticks(t *testing.T) {
	t.Parallel()

	request := "```go\nfmt.Println(\"hi\")\n```"
	parts := summaryParts(requestSummary("summary body", request))
	if len(parts) != 4 {
		t.Fatalf("summaryParts() = %q, want divider, body, heading and request", parts)
	}
	want := "````\n" + request + "\n````"
	if parts[3] != want {
		t.Errorf("request part = %q, want %q", parts[3], want)
	}
}

func TestFenceVerbatimNeverShorterThanThree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "plain text", text: "hello", want: "```\nhello\n```"},
		{name: "single backtick", text: "a `b` c", want: "```\na `b` c\n```"},
		{name: "double backtick", text: "a ``b`` c", want: "```\na ``b`` c\n```"},
		{name: "triple backtick", text: "```\nx\n```", want: "````\n```\nx\n```\n````"},
		{name: "longest run wins", text: "x ``` y ```` z```", want: "`````\nx ``` y ```` z```\n`````"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FenceVerbatim(tt.text); got != tt.want {
				t.Errorf("FenceVerbatim(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
