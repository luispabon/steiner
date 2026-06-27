package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ErrNoToken reports that no OAuth token file exists in the token store.
var ErrNoToken = errors.New("no token stored")

const chatGPTAccountIDExtraKey = "https://api.openai.com/auth.chatgpt_account_id"

// OpenAIAPIKeyExtraKey is the oauth2.Token extra key for Codex's exchanged
// API-key style credential.
const OpenAIAPIKeyExtraKey = "openai_api_key"

// TokenStore persists OAuth2 tokens to disk.
type TokenStore struct {
	path string
	mu   sync.Mutex
}

// NewTokenStore creates a new token store at the given path.
func NewTokenStore(path string) *TokenStore {
	return &TokenStore{
		path: path,
	}
}

// DefaultTokenPath returns the default token store path.
func DefaultTokenPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("get config dir: %w", err)
	}
	return filepath.Join(configDir, "steiner", "codex_auth.json"), nil
}

// tokenJSON is the on-disk representation of a token.
type tokenJSON struct {
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	TokenType    string     `json:"token_type"`
	Expiry       *time.Time `json:"expiry"`
	IDToken      string     `json:"id_token,omitempty"`
	AccountID    string     `json:"account_id,omitempty"`
	OpenAIAPIKey string     `json:"openai_api_key,omitempty"`
}

// Save writes the token to disk with 0o600 permissions, atomically.
func (s *TokenStore) Save(token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	existing, err := s.readLocked()
	if err != nil && !errors.Is(err, ErrNoToken) {
		return fmt.Errorf("read existing token: %w", err)
	}

	tj := tokenJSON{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		IDToken:      tokenExtraString(token, "id_token"),
		AccountID:    TokenChatGPTAccountID(token),
		OpenAIAPIKey: tokenExtraString(token, OpenAIAPIKeyExtraKey),
	}
	if !token.Expiry.IsZero() {
		tj.Expiry = &token.Expiry
	}
	if tj.IDToken == "" && existing != nil {
		tj.IDToken = existing.IDToken
	}
	if tj.AccountID == "" && existing != nil {
		tj.AccountID = existing.AccountID
	}
	if tj.OpenAIAPIKey == "" && existing != nil {
		tj.OpenAIAPIKey = existing.OpenAIAPIKey
	}

	data, err := json.Marshal(tj)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	// Write to temp file, then atomically rename
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp token file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename token file: %w", err)
	}

	return nil
}

// Load reads and unmarshals the token from disk.
func (s *TokenStore) Load() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tj, err := s.readLocked()
	if err != nil {
		return nil, err
	}

	token := tokenFromJSON(tj)

	return token, nil
}

func (s *TokenStore) readLocked() (*tokenJSON, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoToken
		}
		return nil, fmt.Errorf("read token file: %w", err)
	}

	var tj tokenJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}

	return &tj, nil
}

func tokenFromJSON(tj *tokenJSON) *oauth2.Token {
	if tj == nil {
		return nil
	}

	token := &oauth2.Token{
		AccessToken:  tj.AccessToken,
		RefreshToken: tj.RefreshToken,
		TokenType:    tj.TokenType,
	}
	if tj.Expiry != nil {
		token.Expiry = *tj.Expiry
	}

	extra := map[string]any{}
	if tj.IDToken != "" {
		extra["id_token"] = tj.IDToken
	}
	if tj.AccountID != "" {
		extra[chatGPTAccountIDExtraKey] = tj.AccountID
	}
	if tj.OpenAIAPIKey != "" {
		extra[OpenAIAPIKeyExtraKey] = tj.OpenAIAPIKey
	}
	if len(extra) > 0 {
		token = token.WithExtra(extra)
	}

	return token
}

func tokenExtraString(token *oauth2.Token, key string) string {
	if token == nil {
		return ""
	}

	value := token.Extra(key)
	if value == nil {
		return ""
	}

	return fmt.Sprint(value)
}

// TokenOpenAIAPIKey returns the exchanged API-key style credential stored on token.
func TokenOpenAIAPIKey(token *oauth2.Token) string {
	return tokenExtraString(token, OpenAIAPIKeyExtraKey)
}

// TokenChatGPTAccountID returns the ChatGPT account/workspace ID stored on or
// derived from token metadata.
func TokenChatGPTAccountID(token *oauth2.Token) string {
	if accountID := tokenExtraString(token, chatGPTAccountIDExtraKey); accountID != "" {
		return accountID
	}
	return chatGPTAccountIDFromIDToken(tokenExtraString(token, "id_token"))
}

// WithOpenAIAPIKey returns token with the exchanged API-key style credential.
func WithOpenAIAPIKey(token *oauth2.Token, apiKey string) *oauth2.Token {
	if token == nil {
		return nil
	}
	extra := map[string]any{}
	if idToken := tokenExtraString(token, "id_token"); idToken != "" {
		extra["id_token"] = idToken
	}
	if accountID := tokenExtraString(token, chatGPTAccountIDExtraKey); accountID != "" {
		extra[chatGPTAccountIDExtraKey] = accountID
	}
	if apiKey != "" {
		extra[OpenAIAPIKeyExtraKey] = apiKey
	}
	return token.WithExtra(extra)
}

func chatGPTAccountIDFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if accountID, _ := claims["chatgpt_account_id"].(string); accountID != "" {
		return accountID
	}
	authClaims, _ := claims["https://api.openai.com/auth"].(map[string]any)
	accountID, _ := authClaims["chatgpt_account_id"].(string)
	return accountID
}
