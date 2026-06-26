package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

var ErrNoToken = errors.New("no token stored")

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
}

// Save writes the token to disk with 0o600 permissions, atomically.
func (s *TokenStore) Save(token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	tj := tokenJSON{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
	}
	if !token.Expiry.IsZero() {
		tj.Expiry = &token.Expiry
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

	token := &oauth2.Token{
		AccessToken:  tj.AccessToken,
		RefreshToken: tj.RefreshToken,
		TokenType:    tj.TokenType,
	}
	if tj.Expiry != nil {
		token.Expiry = *tj.Expiry
	}

	return token, nil
}
