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

// codexAuthResolver caches the stored Codex token per token path for the life
// of the process, so repeated resolution neither reloads an unchanged token file
// nor refreshes it twice.
var codexAuthResolver = oauth.NewAuthResolver()

// ResolveCodexAuth loads the stored Codex token, refreshes it when it is expired
// or within the proactive refresh window, and returns its credentials. A stored
// token without expiry is used as is. The token is cached in-process and
// reloaded only when the token file changes or disappears.
//
// ctx cancels only this caller's wait for a refresh: the refresh itself runs
// detached under a fixed 30-second deadline, so one caller giving up does not
// fail the others. httpClient, when non-nil, serves a refresh this call starts;
// a call that joins a refresh already in flight uses the client of the caller
// that started it.
func ResolveCodexAuth(ctx context.Context, httpClient *http.Client) (CodexAuth, error) {
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		return CodexAuth{}, fmt.Errorf("resolve token path: %w", err)
	}
	token, err := codexAuthResolver.Resolve(ctx, oauth.NewTokenStore(path), &oauth2.Config{
		ClientID: oauth.CodexClientID,
		Endpoint: oauth2.Endpoint{TokenURL: oauth.CodexTokenURL},
	}, httpClient)
	if err != nil {
		return CodexAuth{}, fmt.Errorf("resolve codex auth: %w", err)
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
