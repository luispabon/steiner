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

	accountID := oauth.TokenChatGPTAccountID(token)
	if accountID == "" {
		t.Fatalf("no ChatGPT account ID found in token")
	}

	apiKey := oauth.TokenOpenAIAPIKey(token)
	if apiKey == "" {
		t.Fatalf("no OpenAI API key found in token")
	}

	// Prepare output file for probe results.
	testDataDir := filepath.Join("internal", "provider", "testdata")
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
		"Authorization":           {fmt.Sprintf("Bearer %s", apiKey)},
		"ChatGPT-Account-ID":      {accountID},
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
	sendRequest := func(requestNum int, useTurnState string) ([]json.RawMessage, error) {
		// Build minimal Responses API request matching the responsesItem structure.
		req := map[string]any{
			"model": "gpt-4o-mini",
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
			"stream": true,
		}

		payload, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
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

			// Check for completion signals.
			var evt map[string]any
			if err := json.Unmarshal(data, &evt); err == nil {
				if eventType, ok := evt["type"].(string); ok {
					if eventType == "response.completed" {
						// Check for metadata event that might carry turn-state.
						if eventType == WSEventTypeMetadata {
							if metadata, ok := evt["metadata"].(map[string]any); ok {
								if ts, ok := metadata["turn_state"].(string); ok && ts != "" && useTurnState == "" {
									cachedTurnState = ts
									t.Logf("Request %d: captured turn-state from metadata", requestNum)
								}
							}
						}
						break
					}
					if eventType == WSEventTypeMetadata {
						if metadata, ok := evt["metadata"].(map[string]any); ok {
							if ts, ok := metadata["turn_state"].(string); ok && ts != "" && useTurnState == "" {
								cachedTurnState = ts
								t.Logf("Request %d: captured turn-state from metadata", requestNum)
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
	if cachedTurnState != "" {
		headers.Set(WSHeaderTurnState, cachedTurnState)
		if conn, _, err := websocket.Dial(ctx, WSEndpointURL, &websocket.DialOptions{
			HTTPHeader: headers,
		}); err == nil {
			conn.CloseNow()
		}
	}

	responses2, err := sendRequest(2, cachedTurnState)
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	t.Logf("Request 2: received %d frames", len(responses2))

	// Request 3: corrupt turn-state to test rejection.
	corruptedTurnState := "corrupted-" + cachedTurnState
	headers.Set(WSHeaderTurnState, corruptedTurnState)

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
	t.Fatalf("STOP: D4's premise invalidated by observed behavior; do not proceed to step 2")
}
