package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
)

// TestCodexWSBurstLoad1008Watch fires a rapid burst of sequential ChatCompletion
// requests over a single WebSocket connection without artificial pacing,
// monitoring for close code 1008 (Policy) which would indicate the backend is
// rejecting the request rate despite the promise of deterministic cache-shard
// stickiness over a pinned connection.
//
// This test is gated and always skipped unless STEINER_CODEX_WS_BURST=1 is set.
// When run manually, it:
//
//  1. Loads real OAuth credentials and builds an authenticated WS provider.
//  2. Fires 18 sequential minimal ChatCompletion requests back-to-back (no delay).
//  3. Tracks which requests succeeded, failed, or hit close code 1008.
//  4. Logs a summary with request counts and any 1008 observation.
//  5. Does NOT fatalf on a 1008 close (that is real, useful backend data, not a
//     harness failure); only fails if the harness itself breaks (e.g., auth fails
//     before the burst starts).
//
// N=18 is chosen to match the "roughly 15 requests/minute" overflow threshold
// mentioned in docs/cache-stats.md#request-pacing for HTTP affinity headers,
// since D3's hypothesis is that WS transport avoids that same overflow by having
// deterministic per-connection shard stickiness.
func TestCodexWSBurstLoad1008Watch(t *testing.T) {
	if os.Getenv("STEINER_CODEX_WS_BURST") == "" {
		t.Skip("set STEINER_CODEX_WS_BURST=1 to run the live Codex WS burst load test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Load OAuth token and extract credentials, mirroring TestCodexWSProbe.
	tokenPath, err := oauth.DefaultTokenPath()
	if err != nil {
		t.Fatalf("get default token path: %v", err)
	}

	store := oauth.NewTokenStore(tokenPath)
	token, err := store.Load()
	if err != nil {
		t.Fatalf("load OAuth token: %v", err)
	}

	// Refresh token like the real code (newCodexProvider in cmd/steiner/runtime_build.go).
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		t.Fatalf("refresh codex token: %v", err)
	}

	// Resolve auth headers: prefer TokenOpenAIAPIKey if present, else fall back to
	// AccessToken with ChatGPT-Account-ID, mirroring the real code's fallback.
	var bearerToken string
	var accountID string
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		bearerToken = apiKey
	} else {
		accountID = oauth.TokenChatGPTAccountID(token)
		if accountID == "" {
			t.Fatalf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		bearerToken = token.AccessToken
	}
	if accountID == "" {
		accountID = oauth.TokenChatGPTAccountID(token)
	}

	// The model and base URL are required; we use placeholder values since
	// the WS provider uses WSEndpointURL and the model from the request.
	cfg := ClientConfig{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  bearerToken,
		Headers: map[string]string{},
		Model:   "gpt-5.6-luna",
	}
	if accountID != "" {
		cfg.Headers["ChatGPT-Account-ID"] = accountID
	}

	// Build the WS provider. It has no HTTP fallback, which would otherwise
	// mask the dial failures this test is looking for.
	provider, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create WS provider: %v", err)
	}

	// Burst parameters.
	const burstSize = 18
	var successCount, failureCount, close1008Count int
	var firstClose1008RequestNum int

	t.Logf("Starting burst of %d requests with no artificial pacing...", burstSize)

	// Fire requests back-to-back.
	for requestNum := 1; requestNum <= burstSize; requestNum++ {
		// Build a minimal request: "ping N" to keep token cost low.
		request := ChatRequest{
			Model: cfg.Model,
			Messages: []Message{
				{
					Role:    MessageRoleUser,
					Content: fmt.Sprintf("ping %d", requestNum),
				},
			},
		}

		// Send request without cancellation; let it complete or fail naturally.
		response, err := provider.ChatCompletion(ctx, request)
		if err != nil {
			failureCount++

			// Check if this is a close-code 1008 error.
			if strings.Contains(err.Error(), "1008") ||
				strings.Contains(err.Error(), "StatusPolicyViolation") ||
				strings.Contains(err.Error(), "policy") {
				close1008Count++
				if firstClose1008RequestNum == 0 {
					firstClose1008RequestNum = requestNum
				}
				t.Logf("Request %d: CLOSE CODE 1008 OBSERVED: %v", requestNum, err)
			} else {
				t.Logf("Request %d: error (non-1008): %v", requestNum, err)
			}
			continue
		}

		successCount++
		t.Logf("Request %d: success (received %d tokens)", requestNum, len(response.Message.Content))
	}

	// Log final summary.
	t.Logf("=== BURST LOAD TEST SUMMARY ===")
	t.Logf("Total requests attempted: %d", burstSize)
	t.Logf("Succeeded end-to-end: %d", successCount)
	t.Logf("Failed: %d", failureCount)
	if close1008Count > 0 {
		t.Logf("Close code 1008 (Policy) observed: YES, on request %d (count: %d)", firstClose1008RequestNum, close1008Count)
		t.Logf("OBSERVATION: Backend rejected request rate with close code 1008 despite WS deterministic shard stickiness.")
		t.Logf("This indicates the overflow threshold still applies over WS and is not avoided by per-connection pinning.")
	} else {
		t.Logf("Close code 1008 (Policy) observed: NO")
		if successCount == burstSize {
			t.Logf("OBSERVATION: All %d requests succeeded without rate-limit rejection.", burstSize)
			t.Logf("This supports D3's hypothesis: WS deterministic shard stickiness avoids the HTTP overflow threshold.")
		} else {
			t.Logf("OBSERVATION: Some requests failed, but no 1008 close code detected.")
			t.Logf("Check error details above for the nature of failures.")
		}
	}
}
