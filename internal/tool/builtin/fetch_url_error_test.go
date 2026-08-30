package builtin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestFetchURLHTMLErrorIncludesRichDetail(t *testing.T) {
	errorBody := strings.Repeat("Cloudflare blocked this request. ", 30)

	var getCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&getCount, 1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("<html><body>" + errorBody + "</body></html>"))
	}))
	defer server.Close()

	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := Env{
		WorkDir:    workDir,
		PathPolicy: &policy,
		httpClient: func() *http.Client { return server.Client() },
	}

	result, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := result.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want *FetchURLError", result)
	}
	if fetchErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", fetchErr.StatusCode, http.StatusTooManyRequests)
	}
	if fetchErr.RetryAfter != "30" {
		t.Errorf("RetryAfter = %q, want %q", fetchErr.RetryAfter, "30")
	}
	if fetchErr.Body == "" {
		t.Error("Body is empty, want a bounded body snippet")
	}
	if got := len([]rune(fetchErr.Body)); got > errorBodySnippetRunes {
		t.Errorf("Body rune count = %d, want <= %d", got, errorBodySnippetRunes)
	}
	if got := atomic.LoadInt32(&getCount); got != 1 {
		t.Errorf("GET requests = %d, want 1 (no retry)", got)
	}
}

func TestFetchURLRawTextErrorIncludesRichDetail(t *testing.T) {
	errorBody := strings.Repeat("rate limited, please slow down. ", 30)

	var getCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&getCount, 1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"` + errorBody + `"}`))
	}))
	defer server.Close()

	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := Env{
		WorkDir:    workDir,
		PathPolicy: &policy,
		httpClient: func() *http.Client { return server.Client() },
	}

	result, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := result.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want *FetchURLError", result)
	}
	if fetchErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", fetchErr.StatusCode, http.StatusTooManyRequests)
	}
	if fetchErr.RetryAfter != "60" {
		t.Errorf("RetryAfter = %q, want %q", fetchErr.RetryAfter, "60")
	}
	if !strings.Contains(fetchErr.Body, "rate limited") {
		t.Errorf("Body = %q, want it to contain the response body text", fetchErr.Body)
	}
	// The raw path reads bytes untransformed, so the snippet length is
	// deterministic: this pins the cap exactly, unlike the HTML path where
	// wonton's markdown conversion makes an exact length brittle.
	if got := len([]rune(fetchErr.Body)); got != errorBodySnippetRunes {
		t.Errorf("Body rune count = %d, want exactly %d (truncation should have fired)", got, errorBodySnippetRunes)
	}
	if got := atomic.LoadInt32(&getCount); got != 1 {
		t.Errorf("GET requests = %d, want 1 (no retry)", got)
	}
}

func TestFetchURLUnexpectedContentTypeFallsBackToRawText(t *testing.T) {
	// The HEAD preflight 405s, so contentType stays empty and the request
	// falls through to wonton/fetch. wonton then rejects the non-HTML GET
	// response before ever reading its status code, headers, or body.
	// Pins the strings.HasPrefix match in unexpectedContentType against
	// wonton's message; if wonton reword it, this test fails instead of
	// silently losing StatusCode/RetryAfter/Body.
	errorBody := `{"error":"rate limited"}`

	var getCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		atomic.AddInt32(&getCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(errorBody))
	}))
	defer server.Close()

	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := Env{
		WorkDir:    workDir,
		PathPolicy: &policy,
		httpClient: func() *http.Client { return server.Client() },
	}

	result, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := result.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want *FetchURLError", result)
	}
	if fetchErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", fetchErr.StatusCode, http.StatusTooManyRequests)
	}
	if fetchErr.RetryAfter != "30" {
		t.Errorf("RetryAfter = %q, want %q", fetchErr.RetryAfter, "30")
	}
	if !strings.Contains(fetchErr.Body, "rate limited") {
		t.Errorf("Body = %q, want it to contain the response body text", fetchErr.Body)
	}
	// Two GETs: wonton's own GET, whose body it discards on the
	// unexpected-content-type error, and fetchRawText's GET that recovers
	// the rich error detail. Not a retry of the same request path.
	if got := atomic.LoadInt32(&getCount); got != 2 {
		t.Errorf("GET requests = %d, want 2 (wonton's discarded GET, then fetchRawText's)", got)
	}
}

func TestBoundedRuneSnippet(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     int
	}{
		{name: "under limit", input: strings.Repeat("a", 499), maxRunes: 500, want: 499},
		{name: "at limit", input: strings.Repeat("a", 500), maxRunes: 500, want: 500},
		{name: "over limit", input: strings.Repeat("a", 501), maxRunes: 500, want: 500},
		{
			// é is 2 bytes in UTF-8; bounding by bytes instead of runes
			// would cut this to 500 bytes = 250 runes.
			name:     "multi-byte runes bounded by rune count not byte count",
			input:    strings.Repeat("é", 600),
			maxRunes: 500,
			want:     500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boundedRuneSnippet(tt.input, tt.maxRunes)
			if n := len([]rune(got)); n != tt.want {
				t.Errorf("boundedRuneSnippet rune count = %d, want %d", n, tt.want)
			}
		})
	}
}

func TestFetchURLRawTextErrorSurvivesBodyReadFailure(t *testing.T) {
	// Content-Length promises more bytes than are ever sent, and the
	// connection is hijacked and closed mid-body, so the client's body read
	// fails. StatusCode and RetryAfter must still make it onto the error
	// even though the body snippet is lost.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Retry-After", "45")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("short"))

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	workDir := t.TempDir()
	policy := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := Env{
		WorkDir:    workDir,
		PathPolicy: &policy,
		httpClient: func() *http.Client { return server.Client() },
	}

	result, err := NewFetchURLTool(env).Handler(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fetchErr, ok := result.(*FetchURLError)
	if !ok {
		t.Fatalf("result type = %T, want *FetchURLError", result)
	}
	if fetchErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d (must survive body-read failure)", fetchErr.StatusCode, http.StatusTooManyRequests)
	}
	if fetchErr.RetryAfter != "45" {
		t.Errorf("RetryAfter = %q, want %q (must survive body-read failure)", fetchErr.RetryAfter, "45")
	}
	if !strings.Contains(fetchErr.Error, "429") {
		t.Errorf("Error = %q, want it to still name the status code", fetchErr.Error)
	}
}
