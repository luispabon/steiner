package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ExchangeOpenAIAPIKey exchanges a Codex ID token for an API-key style
// credential accepted by the OpenAI Responses API.
func ExchangeOpenAIAPIKey(ctx context.Context, tokenURL, clientID, idToken string, httpClient *http.Client) (string, error) {
	if strings.TrimSpace(tokenURL) == "" {
		return "", fmt.Errorf("token URL is required")
	}
	if strings.TrimSpace(clientID) == "" {
		return "", fmt.Errorf("client ID is required")
	}
	if strings.TrimSpace(idToken) == "" {
		return "", fmt.Errorf("id token is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:token-exchange")
	form.Set("client_id", clientID)
	form.Set("requested_token", "openai-api-key")
	form.Set("subject_token", idToken)
	form.Set("subject_token_type", "urn:ietf:params:oauth:token-type:id_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange api key: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return "", fmt.Errorf("exchange api key: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode api key exchange response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return "", fmt.Errorf("api key exchange response missing access_token")
	}
	return payload.AccessToken, nil
}
