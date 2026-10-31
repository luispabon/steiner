package provider

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBuildJSONPostRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-4"}`)
	target := "http://localhost:11434/v1/chat/completions"

	req, err := buildJSONPostRequest(t.Context(), target, body, false, "test-key", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want %q", req.Method, http.MethodPost)
	}
	if req.URL.String() != target {
		t.Fatalf("URL = %q, want %q", req.URL.String(), target)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", req.Header.Get("Content-Type"), "application/json")
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer test-key")
	}
	if req.Header.Get("Accept") != "" {
		t.Fatal("Accept header should not be present for non-stream")
	}

	req, err = buildJSONPostRequest(t.Context(), target, body, true, "test-key", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", req.Header.Get("Accept"), "text/event-stream")
	}
}

func TestBuildJSONPostRequest_NoAuth(t *testing.T) {
	req, err := buildJSONPostRequest(t.Context(), "http://localhost:11434/v1/chat/completions", []byte(`{}`), false, "", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("Authorization header should not be present when apiKey is empty")
	}
}

func TestShouldDisableRemoteStorage(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "openai platform", baseURL: "https://api.openai.com/v1", want: true},
		{name: "openai subdomain", baseURL: "https://eu.api.openai.com/v1", want: true},
		{name: "chatgpt codex backend", baseURL: "https://chatgpt.com/backend-api/codex", want: true},
		{name: "local compat host", baseURL: "http://localhost:11434/v1"},
		{name: "third-party host", baseURL: "https://openrouter.ai/api/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.baseURL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if got := shouldDisableRemoteStorage(parsed); got != tt.want {
				t.Fatalf("shouldDisableRemoteStorage(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}

	if shouldDisableRemoteStorage(nil) {
		t.Fatal("shouldDisableRemoteStorage(nil) = true, want false")
	}
}

func TestReadErrorResponse(t *testing.T) {
	tests := []struct {
		name   string
		status string
		body   string
		want   string
	}{
		{
			name:   "empty body",
			status: "400 Bad Request",
			body:   "",
			want:   "chat completions request failed: 400 Bad Request",
		},
		{
			name:   "with body",
			status: "400 Bad Request",
			body:   `{"error":"bad"}`,
			want:   `chat completions request failed: 400 Bad Request: {"error":"bad"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{}
			resp := &http.Response{
				Status:     tt.status,
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte(tt.body))),
			}
			err := c.readErrorResponse(resp)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Fatalf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

// TestHTMLToErrorText pins the text extracted from an HTML error page: script,
// style and every tag are dropped, a title is kept with a marker, entities are
// decoded once, and runs of whitespace collapse.
func TestHTMLToErrorText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "script style and title",
			body: `<html><head><style>.x{color:red}</style><title data-x="1">502 Bad Gateway</title></head><body><script>alert("x")</script><h1>Upstream down</h1></body></html>`,
			want: "title: 502 Bad Gateway Upstream down",
		},
		{
			name: "uppercase and multiline blocks",
			body: "<HTML><HEAD>\n<SCRIPT>\nvar x = 1;\n</SCRIPT>\n<STYLE>\np { color: red }\n</STYLE>\n</HEAD>\n<BODY>busy</BODY></HTML>",
			want: "busy",
		},
		{
			name: "entities decode once",
			body: `<p>A&amp;B &lt;tag&gt; &quot;q&quot; &#39;s&#39;&nbsp;end &amp;lt;</p>`,
			want: `A&B <tag> "q" 's' end &lt;`,
		},
		{
			name: "plain text collapses whitespace",
			body: "  service   unavailable\n\n",
			want: "service unavailable",
		},
		{
			name: "empty body",
			body: "   ",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := htmlToErrorText(tt.body); got != tt.want {
				t.Errorf("htmlToErrorText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSummarizeErrorBodyHTML proves HTML error pages are recognised by content
// type or by shape, then converted, while other bodies pass through.
func TestSummarizeErrorBodyHTML(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{
			name:        "html content type",
			contentType: "text/html; charset=utf-8",
			body:        `<html><body><h1>502</h1></body></html>`,
			want:        "502",
		},
		{
			name: "doctype without content type",
			body: `<!DOCTYPE html><html><body><p>maintenance</p></body></html>`,
			want: "maintenance",
		},
		{
			name: "body marker without content type",
			body: `<body>busy</body>`,
			want: "busy",
		},
		{
			name:        "json passes through",
			contentType: "application/json",
			body:        `{"error":"bad"}`,
			want:        `{"error":"bad"}`,
		},
		{
			name:        "empty body",
			contentType: "text/html",
			body:        "   ",
			want:        "",
		},
		{
			name:        "long body truncates",
			contentType: "text/plain",
			body:        strings.Repeat("a", 1200),
			want:        strings.Repeat("a", 1000) + "...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeErrorBody(tt.contentType, tt.body); got != tt.want {
				t.Errorf("summarizeErrorBody() = %q, want %q", got, tt.want)
			}
		})
	}
}
