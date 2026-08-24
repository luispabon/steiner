package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/luispabon/steiner/internal/oauth"
	"golang.org/x/oauth2"
)

// TestCodexWSProbe dials the real Codex WebSocket endpoint with the user's
// OAuth session and observes actual traffic to resolve protocol questions
// that research.md could not answer from desk research alone.
//
// This test is gated and always skipped unless STEINER_CODEX_WS_PROBE=1 is set.
// When run manually with the environment variable set, it:
//
//  1. Loads the real OAuth token and extracts auth headers.
//  2. Dials the WS endpoint using coder/websocket.
//  3. Sends 3 sequential minimal requests on the same connection, caching
//     and reusing turn-state from the first response.
//  4. On the 3rd request, deliberately corrupts the turn-state to observe
//     rejection behavior.
//  5. Dumps every inbound frame verbatim (one JSON line per frame) to
//     testdata/codex_ws_probe_output.jsonl.
//  6. Applies the decision table from the plan to the 3rd-request outcome
//     and logs which branch was observed.
func TestCodexWSProbe(t *testing.T) {
	if os.Getenv("STEINER_CODEX_WS_PROBE") == "" {
		t.Skip("set STEINER_CODEX_WS_PROBE=1 to run the live Codex WS probe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Load OAuth token and extract credentials.
	tokenPath, err := oauth.DefaultTokenPath()
	if err != nil {
		t.Fatalf("get default token path: %v", err)
	}

	store := oauth.NewTokenStore(tokenPath)
	token, err := store.Load()
	if err != nil {
		t.Fatalf("load OAuth token: %v", err)
	}

	// Refresh the token like the real code (newCodexProvider in cmd/steiner/runtime_build.go).
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		t.Fatalf("refresh codex token: %v", err)
	}

	// Resolve auth headers: prefer TokenOpenAIAPIKey if present, else fall back to
	// AccessToken with ChatGPT-Account-ID (for ChatGPT-plan accounts without an
	// exchanged API key), mirroring the real code's fallback in newCodexProvider.
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

	// Prepare output file for probe results.
	// Note: go test runs with the package directory as CWD, so this is testdata/
	// relative to internal/provider, not internal/provider/testdata/.
	testDataDir := "testdata"
	if err := os.MkdirAll(testDataDir, 0o755); err != nil {
		t.Fatalf("create testdata directory: %v", err)
	}

	outputFile := filepath.Join(testDataDir, "codex_ws_probe_output.jsonl")
	f, err := os.Create(outputFile)
	if err != nil {
		t.Fatalf("create probe output file: %v", err)
	}
	defer f.Close()

	// Build WebSocket headers mirroring wire_responses.go's HTTPRequest.
	headers := http.Header{
		"OpenAI-Beta":             {WSBetaHeaderValue},
		"x-codex-installation-id": {"default"},
		"x-client-request-id":     {fmt.Sprintf("probe-request-%d", time.Now().Unix())},
		"Authorization":           {fmt.Sprintf("Bearer %s", bearerToken)},
	}
	if accountID != "" {
		headers.Set("ChatGPT-Account-ID", accountID)
	}

	// Dial the WebSocket endpoint.
	conn, _, err := websocket.Dial(ctx, WSEndpointURL, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	defer conn.CloseNow()

	// Track turn-state across requests.
	var cachedTurnState string

	// Helper to send a request and read responses.
	// Per research.md's documented ResponsesWsRequest::ResponseCreate variant,
	// the request shape is flat with type, model, input, and optionally client_metadata.
	// Model is included here at the top level (sibling of type) based on live probe
	// iteration: nested-under-response (round 2) and omitted-entirely (round 4) both
	// failed with identical "'None' model" errors, suggesting model belongs as a
	// top-level sibling. This model ID (gpt-5.6-luna) is from the real account's
	// config.yaml, not guessed — it's the Codex model for this ChatGPT account.
	sendRequest := func(requestNum int, useTurnState string) ([]json.RawMessage, error) {
		// Build the ResponsesWsRequest::ResponseCreate frame per research.md's shape,
		// with model as a top-level sibling of type and input.
		req := map[string]any{
			"type":  "response.create",
			"model": "gpt-5.6-luna",
			"input": []map[string]any{
				{
					"type": "message",
					"role": "user",
					"content": []map[string]string{
						{
							"type": "input_text",
							"text": fmt.Sprintf("Test message %d", requestNum),
						},
					},
				},
			},
		}

		// Include turn-state in client_metadata per research.md's documented
		// ResponseCreate variant. Turn-state travels in client_metadata, never at
		// the top level or in instructions/input per D7.
		if useTurnState != "" {
			req["client_metadata"] = map[string]any{
				"turn_state": useTurnState,
			}
		}

		// previous_response_id and generate are both documented as optional in
		// research.md's ResponseCreate variant — omit both for this initial probe.

		payload, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}

		// Log outbound payload for self-documenting probe output.
		t.Logf("Request %d outbound: %s", requestNum, string(payload))
		if _, err := fmt.Fprintf(f, "OUTBOUND: %s\n", string(payload)); err != nil {
			return nil, fmt.Errorf("write outbound to probe output: %w", err)
		}

		// Send request frame.
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}

		// Read all response frames for this request.
		var responses []json.RawMessage
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}

			if typ != websocket.MessageText {
				continue
			}

			// Write frame verbatim to output file.
			if _, err := fmt.Fprintf(f, "%s\n", string(data)); err != nil {
				return nil, fmt.Errorf("write probe output: %w", err)
			}

			responses = append(responses, data)

			// Check for completion signals and capture turn-state from metadata event.
			var evt map[string]any
			if err := json.Unmarshal(data, &evt); err == nil {
				if eventType, ok := evt["type"].(string); ok {
					if eventType == "response.completed" {
						break
					}
					// Turn-state is carried in the headers field of the metadata event,
					// at evt["headers"]["x-codex-turn-state"]. First-value-wins: capture
					// only on the first request when cachedTurnState is still empty.
					if eventType == WSEventTypeMetadata {
						if headers, ok := evt["headers"].(map[string]any); ok {
							if ts, ok := headers["x-codex-turn-state"].(string); ok && ts != "" && useTurnState == "" {
								cachedTurnState = ts
								t.Logf("Request %d: captured turn-state from metadata event headers: %s", requestNum, ts)
							}
						}
					}
				}
			}
		}

		return responses, nil
	}

	// Request 1: baseline, capture turn-state.
	responses1, err := sendRequest(1, "")
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	t.Logf("Request 1: received %d frames", len(responses1))

	// Request 2: reuse captured turn-state.
	responses2, err := sendRequest(2, cachedTurnState)
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	t.Logf("Request 2: received %d frames", len(responses2))

	// Request 3: corrupt turn-state to test rejection.
	corruptedTurnState := "corrupted-" + cachedTurnState

	responses3, err := sendRequest(3, corruptedTurnState)
	if err != nil {
		// Close code 1008 or explicit error event indicates policy rejection.
		if strings.Contains(err.Error(), "1008") {
			t.Logf("DECISION TABLE: Backend rejected corrupted turn-state with close code 1008")
			t.Logf("BRANCH OBSERVED: D4 default (session-wide hold, reacquire-on-rejection) holds as designed")
			return
		}

		// Check if error contains policy/rejection language.
		if strings.Contains(strings.ToLower(err.Error()), "policy") ||
			strings.Contains(strings.ToLower(err.Error()), "reject") ||
			strings.Contains(strings.ToLower(strings.ToLower(err.Error())), "forbidden") {
			t.Logf("DECISION TABLE: Backend rejected corrupted turn-state with error: %v", err)
			t.Logf("BRANCH OBSERVED: D4 default (session-wide hold, reacquire-on-rejection) holds as designed")
			return
		}

		t.Fatalf("request 3 failed unexpectedly: %v", err)
	}

	// If we got here, server silently accepted the corrupted token.
	t.Logf("Request 3: received %d frames (server accepted corrupted turn-state)", len(responses3))

	// Check if any of the response events indicate a rejection.
	for _, data := range responses3 {
		var evt map[string]any
		if err := json.Unmarshal(data, &evt); err != nil {
			continue
		}

		if eventType, ok := evt["type"].(string); ok {
			if strings.Contains(eventType, "error") {
				t.Logf("DECISION TABLE: Response contained error event: %s", eventType)
				t.Logf("BRANCH OBSERVED: D4 default (session-wide hold, reacquire-on-rejection) may hold")
				return
			}
		}

		if errField, ok := evt["error"].(map[string]any); ok {
			if msg, ok := errField["message"].(string); ok && msg != "" {
				t.Logf("DECISION TABLE: Response contained error message: %s", msg)
				t.Logf("BRANCH OBSERVED: D4 default (session-wide hold, reacquire-on-rejection) may hold")
				return
			}
		}
	}

	// Server silently accepted corrupted token: D4's premise does not hold.
	t.Logf("DECISION TABLE BRANCH: does not hold - server silently accepted corrupted turn-state")
	t.Logf("This indicates D4's default (session-wide hold, reacquire-on-rejection) does NOT hold as designed")
	t.Logf("Raw response frames logged to %s for further inspection", outputFile)
	t.Logf("D4 resolved to no-cross-call-caching by probe; see probe_findings.md — nothing further to assert here")
	t.Skip("D4 resolved to no-cross-call-caching by probe; see probe_findings.md — nothing further to assert here")
}
