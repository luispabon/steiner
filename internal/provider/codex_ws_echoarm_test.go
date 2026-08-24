package provider

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
)

// TestCodexWSEchoArmREPL provides an interactive measurement harness for the
// Codex WebSocket echo arm (turn-state echoing for cache-hit-rate A/B/C comparison).
//
// This test is gated and always skipped unless STEINER_CODEX_WS_ECHO_ARM=1 is set.
// When run manually with the environment variable set, it:
//
//  1. Loads the real OAuth token and extracts auth headers (mirroring cmd/steiner).
//  2. Constructs a Codex WebSocket provider with echo=true, fallback=false.
//  3. Enters an interactive stdin/stdout loop: read a line, send as a
//     ChatRequest via ChatCompletion (non-streaming), print the assistant's
//     reply, and repeat until EOF or user types /quit.
//  4. Banner explains this is for measurement; hit rate should be monitored
//     via steiner's own /cache-stats in a concurrent session using the same
//     account, or via the Codex/OpenAI dashboard.
//
// This harness does NOT participate in steiner's normal usage-stats recording,
// so hit-rate metrics will not appear in a solo run of this test. Instead,
// watch Codex/OpenAI dashboard or run concurrent steiner sessions to observe
// the echo-arm traffic's cache-hit impact via steiner's /cache-stats tool.
func TestCodexWSEchoArmREPL(t *testing.T) {
	if os.Getenv("STEINER_CODEX_WS_ECHO_ARM") == "" {
		t.Skip("set STEINER_CODEX_WS_ECHO_ARM=1 to run the interactive echo-arm measurement harness")
	}

	ctx := context.Background()

	// Load OAuth token and extract credentials (mirror cmd/steiner/runtime_build.go::newCodexProvider).
	tokenPath, err := oauth.DefaultTokenPath()
	if err != nil {
		t.Fatalf("resolve token path: %v", err)
	}

	store := oauth.NewTokenStore(tokenPath)
	token, err := store.Load()
	if err != nil {
		t.Fatalf("load OAuth token: %v", err)
	}

	// Refresh token like the real code.
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		t.Fatalf("refresh codex token: %v", err)
	}

	// Resolve auth headers: prefer TokenOpenAIAPIKey, else fallback to
	// AccessToken with ChatGPT-Account-ID (for ChatGPT-plan accounts).
	// Mirror cmd/steiner/runtime_build.go::newCodexProvider exactly.
	var bearerToken, baseURL string
	var accountID string
	if apiKey := oauth.TokenOpenAIAPIKey(token); apiKey != "" {
		bearerToken = apiKey
		baseURL = "https://api.openai.com/v1"
	} else {
		accountID = oauth.TokenChatGPTAccountID(token)
		if accountID == "" {
			t.Fatalf("codex token missing ChatGPT account metadata — run 'steiner login codex' again")
		}
		bearerToken = token.AccessToken
		baseURL = "https://chatgpt.com/backend-api/codex"
	}

	// Build ClientConfig for the WebSocket provider.
	model := os.Getenv("STEINER_CODEX_WS_ECHO_ARM_MODEL")
	if model == "" {
		model = "gpt-5.6-luna"
	}

	cfg := ClientConfig{
		BaseURL: baseURL,
		APIKey:  bearerToken,
		Headers: make(map[string]string),
		Model:   model,
		Timeout: 30 * time.Second,
	}

	if accountID != "" {
		cfg.Headers["ChatGPT-Account-ID"] = accountID
	}

	// Construct the WebSocket provider with echo=true, fallback=false.
	// fallback=false ensures hard failures if WS breaks (not silent HTTP masking).
	wsProvider, err := newCodexResponsesWSWithEcho(cfg, false, true)
	if err != nil {
		t.Fatalf("construct echo-arm provider: %v", err)
	}

	// Print banner.
	fmt.Fprintf(os.Stderr, `Codex WebSocket Echo-Arm Measurement Harness
This is a real interactive session against the Codex WebSocket endpoint
with echo=true (turn-state echoing) enabled for cache-hit-rate measurement.

Type messages on stdin; each line is sent as a request and the assistant's
reply is printed to stdout.

To monitor cache hit rate:
  - Run a concurrent 'steiner' session against the same Codex account.
  - Use steiner's /cache-stats command to observe hit-rate metrics.
  - Or monitor via the Codex/OpenAI dashboard directly.

IMPORTANT: This test harness itself does NOT record usage stats; metrics come
from concurrent steiner sessions or the Codex dashboard.

Type /quit or EOF to exit.
`)

	// Read lines from stdin and send as chat requests.
	scanner := bufio.NewScanner(os.Stdin)
	turnNumber := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/quit" {
			fmt.Println("Exiting.")
			break
		}

		turnNumber++
		fmt.Fprintf(os.Stderr, "\n[Turn %d] Sending request...\n", turnNumber)

		// Construct a simple ChatRequest with this line as a user message.
		request := ChatRequest{
			Model: cfg.Model,
			Messages: []Message{
				{
					Role:    MessageRoleUser,
					Content: line,
				},
			},
			Stream: false,
		}

		// Per-request timeout: 120s to avoid hanging on a single request,
		// but allow long-running sessions (no session-wide timeout).
		reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		response, err := wsProvider.ChatCompletion(reqCtx, request)
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
			continue
		}

		// Print the assistant's reply to stdout.
		if response.Message.Content != "" {
			fmt.Println(response.Message.Content)
		}

		// Print token usage if available.
		if response.Usage != nil {
			fmt.Fprintf(os.Stderr,
				"[Turn %d] Usage: %d prompt tokens, %d completion tokens (cache read: %d, cache creation: %d)\n",
				turnNumber,
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.CacheReadInputTokens,
				response.Usage.CacheCreationInputTokens,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		t.Logf("stdin read error: %v", err)
	}

	t.Log("Echo-arm measurement session complete.")
}
