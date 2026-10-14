package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/luispabon/steiner/internal/quota"
)

const codexQuotaTestAccountKey = "codex:acct-1"

func codexQuotaTestRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/quota/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func codexQuotaTestTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}

func TestCodexQuotaAccountKey(t *testing.T) {
	if got := CodexQuotaAccountKey("acct-1"); got != codexQuotaTestAccountKey {
		t.Errorf("CodexQuotaAccountKey(\"acct-1\") = %q, want %q", got, codexQuotaTestAccountKey)
	}
}

func TestParseCodexUsageBody(t *testing.T) {
	got, err := parseCodexUsageBody(codexQuotaTestRead(t, "codex_wham_usage.json"), codexQuotaTestAccountKey)
	if err != nil {
		t.Fatalf("parseCodexUsageBody() error = %v", err)
	}
	want := quota.Snapshot{
		AccountKey:   codexQuotaTestAccountKey,
		ProviderType: "codex",
		Plan:         "pro",
		Windows: []quota.Window{
			{Label: "5h", Duration: 5 * time.Hour, UsedPercent: 42, ResetsAt: codexQuotaTestTime(1761901200)},
			{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 84, ResetsAt: codexQuotaTestTime(1764500000)},
			{Label: "codex_other 5h", Duration: 5 * time.Hour, UsedPercent: 70, ResetsAt: codexQuotaTestTime(1761910000)},
		},
		Credits: "$9.99",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCodexUsageBody() = %+v, want %+v", got, want)
	}
}

func TestParseCodexUsageBodyNullsAndCredits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want quota.Snapshot
	}{
		{
			name: "null limits and unlimited credits",
			body: `{"plan_type":"plus","rate_limit":null,"credits":{"has_credits":true,"unlimited":true,"balance":"5"},"additional_rate_limits":null}`,
			want: quota.Snapshot{AccountKey: codexQuotaTestAccountKey, ProviderType: "codex", Plan: "plus"},
		},
		{
			name: "null primary window keeps secondary",
			body: `{"plan_type":"pro","rate_limit":{"primary_window":null,"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_at":1764500000}},"credits":{"has_credits":false,"unlimited":false,"balance":"3"}}`,
			want: quota.Snapshot{
				AccountKey:   codexQuotaTestAccountKey,
				ProviderType: "codex",
				Plan:         "pro",
				Windows: []quota.Window{
					{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 10, ResetsAt: codexQuotaTestTime(1764500000)},
				},
			},
		},
		{
			name: "empty balance",
			body: `{"plan_type":"pro","credits":{"has_credits":true,"unlimited":false,"balance":""}}`,
			want: quota.Snapshot{AccountKey: codexQuotaTestAccountKey, ProviderType: "codex", Plan: "pro"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCodexUsageBody([]byte(tt.body), codexQuotaTestAccountKey)
			if err != nil {
				t.Fatalf("parseCodexUsageBody() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCodexUsageBody() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCodexUsageBodyRejectsInvalidJSON(t *testing.T) {
	if _, err := parseCodexUsageBody([]byte("{"), codexQuotaTestAccountKey); err == nil {
		t.Error("parseCodexUsageBody() error = nil for invalid JSON")
	}
}

func TestParseCodexQuotaHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		wantOK  bool
		want    quota.Snapshot
	}{
		{
			name: "main and additional families with credits",
			headers: map[string]string{
				"x-codex-primary-used-percent":         "42",
				"x-codex-primary-window-minutes":       "300",
				"x-codex-primary-reset-at":             "1761901200",
				"x-codex-secondary-used-percent":       "84",
				"x-codex-secondary-window-minutes":     "10080",
				"x-codex-secondary-reset-at":           "1764500000",
				"x-codex-credits-has-credits":          "true",
				"x-codex-credits-unlimited":            "false",
				"x-codex-credits-balance":              "9.99",
				"x-codex-other-primary-used-percent":   "70",
				"x-codex-other-primary-window-minutes": "300",
				"x-codex-other-primary-reset-at":       "1761910000",
			},
			wantOK: true,
			want: quota.Snapshot{
				AccountKey:   codexQuotaTestAccountKey,
				ProviderType: "codex",
				Source:       quota.SourcePassive,
				Windows: []quota.Window{
					{Label: "5h", Duration: 5 * time.Hour, UsedPercent: 42, ResetsAt: codexQuotaTestTime(1761901200)},
					{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 84, ResetsAt: codexQuotaTestTime(1764500000)},
					{Label: "codex_other 5h", Duration: 5 * time.Hour, UsedPercent: 70, ResetsAt: codexQuotaTestTime(1761910000)},
				},
				Credits: "$9.99",
			},
		},
		{
			name: "unparseable primary percent skips that window",
			headers: map[string]string{
				"x-codex-primary-used-percent":     "n/a",
				"x-codex-secondary-used-percent":   "12",
				"x-codex-secondary-window-minutes": "10080",
			},
			wantOK: true,
			want: quota.Snapshot{
				AccountKey:   codexQuotaTestAccountKey,
				ProviderType: "codex",
				Source:       quota.SourcePassive,
				Windows: []quota.Window{
					{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 12, ResetsAt: codexQuotaTestTime(0)},
				},
			},
		},
		{
			name:    "no rate-limit family",
			headers: map[string]string{"Content-Type": "application/json"},
			wantOK:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for name, value := range tt.headers {
				h.Set(name, value)
			}
			got, ok := parseCodexQuotaHeaders(h, codexQuotaTestAccountKey)
			if ok != tt.wantOK {
				t.Fatalf("parseCodexQuotaHeaders() ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseCodexQuotaHeaders() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCodexRateLimitsFrame(t *testing.T) {
	got, ok := parseCodexRateLimitsFrame(codexQuotaTestRead(t, "codex_ws_rate_limits.json"), codexQuotaTestAccountKey)
	if !ok {
		t.Fatal("parseCodexRateLimitsFrame() ok = false for codex.rate_limits fixture")
	}
	want := quota.Snapshot{
		AccountKey:   codexQuotaTestAccountKey,
		ProviderType: "codex",
		Plan:         "pro",
		Source:       quota.SourcePassive,
		Windows: []quota.Window{
			{Label: "5h", Duration: 5 * time.Hour, UsedPercent: 42, ResetsAt: codexQuotaTestTime(1761901200)},
			{Label: "weekly", Duration: 7 * 24 * time.Hour, UsedPercent: 84, ResetsAt: codexQuotaTestTime(1764500000)},
		},
		Credits: "$9.99",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseCodexRateLimitsFrame() = %+v, want %+v", got, want)
	}
}

func TestParseCodexRateLimitsFrameIgnoresOtherFrames(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "text delta", raw: `{"type":"response.output_text.delta","delta":"hi"}`},
		{name: "rate_limits with wrong shape", raw: `{"type":"codex.rate_limits","rate_limits":[]}`},
		{name: "not json", raw: `not json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if snap, ok := parseCodexRateLimitsFrame([]byte(tt.raw), codexQuotaTestAccountKey); ok {
				t.Errorf("parseCodexRateLimitsFrame() = %+v, true; want false", snap)
			}
		})
	}
}

// codexQuotaTestRedirect sends every request to the local httptest server, so
// the fetcher's chatgpt.com URL is exercised without network access.
type codexQuotaTestRedirect struct {
	target *url.URL
}

func (rt codexQuotaTestRedirect) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func codexQuotaTestClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	target, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return &http.Client{Transport: codexQuotaTestRedirect{target: target}}
}

func TestCodexQuotaFetcherFetch(t *testing.T) {
	body := codexQuotaTestRead(t, "codex_wham_usage.json")
	codexQuotaTestWriteToken(t, codexQuotaTestTokenPath(t), `{"access_token":"tok-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`)

	seen := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		_, _ = w.Write(body)
	}))
	defer server.Close()

	fetcher := NewCodexQuotaFetcher(codexQuotaTestClient(t, server.URL))
	snap, err := fetcher.Fetch(context.Background(), quota.FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	req := <-seen
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if req.URL.Path != "/backend-api/wham/usage" {
		t.Errorf("path = %q, want /backend-api/wham/usage", req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-1" {
		t.Errorf("Authorization = %q, want Bearer tok-1", got)
	}
	if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct-1" {
		t.Errorf("ChatGPT-Account-Id = %q, want acct-1", got)
	}
	if got := req.Header.Get("User-Agent"); got != "codex-cli" {
		t.Errorf("User-Agent = %q, want codex-cli", got)
	}

	if snap.AccountKey != codexQuotaTestAccountKey || snap.Source != quota.SourceFetch {
		t.Errorf("AccountKey, Source = %q, %q; want %q, %q", snap.AccountKey, snap.Source, codexQuotaTestAccountKey, quota.SourceFetch)
	}
	if len(snap.Windows) != 3 || snap.Credits != "$9.99" {
		t.Errorf("Windows = %d, Credits = %q; want 3 windows and $9.99", len(snap.Windows), snap.Credits)
	}
}

func TestCodexQuotaFetcherErrors(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		status   int
		wantErr  string
		wantHits int
	}{
		{
			name:     "non-2xx status",
			token:    `{"access_token":"tok-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`,
			status:   http.StatusServiceUnavailable,
			wantErr:  "wham/usage: 503",
			wantHits: 1,
		},
		{
			name:    "missing account metadata makes no request",
			token:   `{"access_token":"tok-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"}`,
			status:  http.StatusOK,
			wantErr: "codex token missing ChatGPT account metadata",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codexQuotaTestWriteToken(t, codexQuotaTestTokenPath(t), tt.token)
			hits := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits <- struct{}{}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			_, err := NewCodexQuotaFetcher(codexQuotaTestClient(t, server.URL)).Fetch(context.Background(), quota.FetchOptions{})
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Fetch() error = %v, want %q", err, tt.wantErr)
			}
			if got := len(hits); got != tt.wantHits {
				t.Errorf("requests = %d, want %d", got, tt.wantHits)
			}
		})
	}
}

func TestCodexQuotaObservesHTTPResponseHeaders(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		headers   map[string]string
		wantErr   bool
		wantCount int
	}{
		{
			name:      "successful response with account header",
			status:    http.StatusOK,
			headers:   map[string]string{"ChatGPT-Account-ID": "acct-1"},
			wantCount: 1,
		},
		{
			name:      "no account header",
			status:    http.StatusOK,
			wantCount: 0,
		},
		{
			name:      "failed response is not observed",
			status:    http.StatusInternalServerError,
			headers:   map[string]string{"ChatGPT-Account-ID": "acct-1"},
			wantErr:   true,
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Codex-Primary-Used-Percent", "42")
				w.Header().Set("X-Codex-Primary-Window-Minutes", "300")
				w.Header().Set("X-Codex-Primary-Reset-At", "1761901200")
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			var got []quota.Snapshot
			c, err := NewCodexResponses(ClientConfig{
				BaseURL:       server.URL,
				Model:         "codex-test",
				Headers:       tt.headers,
				HTTPClient:    server.Client(),
				QuotaObserver: func(snap quota.Snapshot) { got = append(got, snap) },
			})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}

			resp, err := c.executeRequest(context.Background(), ChatRequest{Model: "codex-test"}, []byte(`{}`), false)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("executeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("observed %d snapshots, want %d", len(got), tt.wantCount)
			}
			if tt.wantCount == 1 && (got[0].AccountKey != codexQuotaTestAccountKey || len(got[0].Windows) != 1 || got[0].Windows[0].UsedPercent != 42) {
				t.Errorf("observed snapshot = %+v, want account %q and one 42%% window", got[0], codexQuotaTestAccountKey)
			}
		})
	}
}

func TestCodexQuotaNilObserverIsNoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Codex-Primary-Used-Percent", "42")
	}))
	defer server.Close()

	c, err := NewCodexResponses(ClientConfig{
		BaseURL:    server.URL,
		Model:      "codex-test",
		Headers:    map[string]string{"ChatGPT-Account-ID": "acct-1"},
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}
	resp, err := c.executeRequest(context.Background(), ChatRequest{Model: "codex-test"}, []byte(`{}`), false)
	if err != nil {
		t.Fatalf("executeRequest() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestCodexQuotaWSHandshakeAndRateLimitFrames(t *testing.T) {
	frame := codexQuotaTestRead(t, "codex_ws_rate_limits.json")
	unrelated := []byte(`{"type":"response.output_text.delta","delta":"hi"}`)
	completed := []byte(`{"type":"response.completed","response":{"status":"success"}}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Codex-Primary-Used-Percent", "17")
		w.Header().Set("X-Codex-Primary-Window-Minutes", "300")
		w.Header().Set("X-Codex-Primary-Reset-At", "1761900000")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for range 2 {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			for _, payload := range [][]byte{frame, unrelated, completed} {
				if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()

	var (
		ws       *codexWSProvider
		got      []quota.Snapshot
		lockHeld bool
	)
	p, err := NewCodexResponsesWS(ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		Headers:    map[string]string{"ChatGPT-Account-ID": "acct-1"},
		HTTPClient: &http.Client{},
		QuotaObserver: func(snap quota.Snapshot) {
			// The observer runs after mu is released, so TryLock succeeds.
			if ws.mu.TryLock() {
				ws.mu.Unlock()
			} else {
				lockHeld = true
			}
			got = append(got, snap)
		},
	})
	if err != nil {
		t.Fatalf("NewCodexResponsesWS() error = %v", err)
	}
	ws = p.(*codexWSProvider)
	ws.wsURL = "ws" + strings.TrimPrefix(server.URL, "http")
	defer func() {
		ws.mu.Lock()
		ws.closeConn()
		ws.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := ChatRequest{Model: "test-model", Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}}
	for i := range 2 {
		if _, err := ws.ChatCompletion(ctx, request); err != nil {
			t.Fatalf("ChatCompletion() call %d error = %v", i+1, err)
		}
	}

	if lockHeld {
		t.Error("observer ran while the provider mutex was held")
	}
	// One handshake snapshot (17%, no plan) on the first dial, then one frame
	// snapshot per call. The unrelated delta frame is not observed.
	wantPlan := []string{"", "pro", "pro"}
	wantWindows := []int{1, 2, 2}
	wantUsed := []float64{17, 42, 42}
	if len(got) != len(wantUsed) {
		t.Fatalf("observed %d snapshots, want %d", len(got), len(wantUsed))
	}
	for i, snap := range got {
		if snap.AccountKey != codexQuotaTestAccountKey || snap.Plan != wantPlan[i] ||
			len(snap.Windows) != wantWindows[i] || snap.Windows[0].UsedPercent != wantUsed[i] {
			t.Errorf("snapshot %d = %+v, want plan %q, %d windows, first at %v%%", i, snap, wantPlan[i], wantWindows[i], wantUsed[i])
		}
	}
}
