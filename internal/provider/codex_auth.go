package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"

	"github.com/luispabon/steiner/internal/oauth"
)

// CodexChatGPTBackendURL is the model-serving host used by ChatGPT-subscription
// OAuth sessions (no distinct OpenAI API key). It differs from the OpenAI
// Platform API host used when the Codex token carries its own API key.
const CodexChatGPTBackendURL = "https://chatgpt.com/backend-api/codex"

// CodexAuth holds the credentials resolved from the stored Codex token. APIKey
// is set when the token carries an exchanged OpenAI API key.
type CodexAuth struct {
	AccessToken string
	AccountID   string
	APIKey      string
}

// CodexIdentity describes the stored Codex sign-in as read from disk.
type CodexIdentity struct {
	AccountID string
	APIMode   bool
	SignedIn  bool
}

// ResolveCodexAuth loads the stored Codex token, refreshes it when needed, and
// returns its credentials. Every call reads the token store again; nothing is
// cached.
//
// ctx and httpClient are accepted but not used yet: the refresh goes through
// oauth.NewRefreshableTokenSource, which takes neither. Follow-up: thread both
// into internal/oauth so the refresh honours them.
func ResolveCodexAuth(_ context.Context, _ *http.Client) (CodexAuth, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return CodexAuth{}, fmt.Errorf("resolve token path: %w", err)
	}
	store := oauth.NewTokenStore(path)
	token, err := store.Load()
	if err != nil {
		return CodexAuth{}, fmt.Errorf("load codex token: %w", err)
	}
	token, err = oauth.NewRefreshableTokenSource(store, &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, token).Token()
	if err != nil {
		return CodexAuth{}, fmt.Errorf("refresh codex token: %w", err)
	}
	return CodexAuth{
		AccessToken: token.AccessToken,
		AccountID:   oauth.TokenChatGPTAccountID(token),
		APIKey:      oauth.TokenOpenAIAPIKey(token),
	}, nil
}

// LoadCodexIdentity reads the stored Codex token without refreshing it and
// without network calls. A missing token file reports SignedIn false with a nil
// error.
func LoadCodexIdentity() (CodexIdentity, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return CodexIdentity{}, fmt.Errorf("resolve token path: %w", err)
	}
	token, err := oauth.NewTokenStore(path).Load()
	if errors.Is(err, oauth.ErrNoToken) {
		return CodexIdentity{}, nil
	}
	if err != nil {
		return CodexIdentity{}, fmt.Errorf("load codex token: %w", err)
	}
	return CodexIdentity{
		AccountID: oauth.TokenChatGPTAccountID(token),
		APIMode:   oauth.TokenOpenAIAPIKey(token) != "",
		SignedIn:  true,
	}, nil
}
