package provider

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestCodexConnectionHandshakeCost measures what a persistent connection
// actually saves: the TCP + TLS handshake to the Codex host.
//
// This is the ceiling on the WebSocket transport's latency advantage. The
// production HTTP client pools connections with a 90s idle timeout
// (cmd/steiner/runtime_build.go), so for turns less than 90s apart HTTP pays no
// handshake either and the WebSocket saves nothing. Beyond 90s the pooled
// connection is dropped and HTTP pays this cost again, while a live WebSocket
// would not.
//
// Measuring it directly with httptrace avoids the generation-time noise that
// swamps end-to-end round-trip comparisons, and costs no tokens: the request is
// unauthenticated and its response status is irrelevant — only the connection
// timings matter.
//
// Gated: STEINER_CODEX_HANDSHAKE=<samples>, e.g. 10.
func TestCodexConnectionHandshakeCost(t *testing.T) {
	raw := os.Getenv("STEINER_CODEX_HANDSHAKE")
	if raw == "" {
		t.Skip("set STEINER_CODEX_HANDSHAKE=<samples> (e.g. 10) to measure connection handshake cost")
	}
	samples, err := strconv.Atoi(raw)
	if err != nil || samples < 1 {
		t.Fatalf("STEINER_CODEX_HANDSHAKE must be a positive integer, got %q", raw)
	}

	const target = "https://chatgpt.com/backend-api/codex/responses"

	var fresh, reused []int64

	for i := 0; i < samples; i++ {
		// A dedicated client per sample guarantees a cold pool, so every
		// measurement here is a genuine handshake.
		client := &http.Client{Transport: &http.Transport{ForceAttemptHTTP2: true}}

		for _, phase := range []string{"fresh", "reused"} {
			var connectStart, gotConn time.Time
			var wasReused bool

			trace := &httptrace.ClientTrace{
				GetConn: func(string) { connectStart = time.Now() },
				GotConn: func(info httptrace.GotConnInfo) {
					gotConn = time.Now()
					wasReused = info.Reused
				},
			}

			ctx := httptrace.WithClientTrace(context.Background(), trace)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Logf("sample %d (%s): request error (timings unusable): %v", i, phase, err)
				continue
			}
			_ = resp.Body.Close()

			if connectStart.IsZero() || gotConn.IsZero() {
				t.Logf("sample %d (%s): incomplete trace, skipping", i, phase)
				continue
			}
			elapsed := gotConn.Sub(connectStart).Milliseconds()
			if wasReused {
				reused = append(reused, elapsed)
			} else {
				fresh = append(fresh, elapsed)
			}
		}
	}

	if len(fresh) == 0 {
		t.Fatal("no fresh-connection samples captured; cannot report handshake cost")
	}

	t.Log("=== connection acquisition cost ===")
	t.Logf("fresh (TCP+TLS handshake): n=%d median=%dms mean=%dms min=%dms max=%dms",
		len(fresh), medianOf(fresh), meanInt(fresh), minOf(fresh), maxOf(fresh))
	if len(reused) > 0 {
		t.Logf("reused (pooled connection): n=%d median=%dms mean=%dms",
			len(reused), medianOf(reused), meanInt(reused))
		t.Logf("=> maximum possible saving from holding a connection open: ~%dms per request",
			medianOf(fresh)-medianOf(reused))
	}
}

func medianOf(values []int64) int64 {
	sorted := append([]int64(nil), values...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return median(sorted)
}

func minOf(values []int64) int64 {
	result := values[0]
	for _, v := range values {
		if v < result {
			result = v
		}
	}
	return result
}

func maxOf(values []int64) int64 {
	result := values[0]
	for _, v := range values {
		if v > result {
			result = v
		}
	}
	return result
}
