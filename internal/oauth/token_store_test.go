package oauth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenStoreSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	tests := []struct {
		name  string
		token *oauth2.Token
	}{
		{
			name: "token with expiry",
			token: &oauth2.Token{
				AccessToken:  "access123",
				RefreshToken: "refresh456",
				TokenType:    "Bearer",
				Expiry:       time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "token without expiry",
			token: &oauth2.Token{
				AccessToken:  "access789",
				RefreshToken: "refresh000",
				TokenType:    "Bearer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore(tokenPath)

			if err := store.Save(tt.token); err != nil {
				t.Fatalf("Save() error = %v", err)
			}

			// Check file permissions
			fi, err := os.Stat(tokenPath)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if fi.Mode()&0o077 != 0 {
				t.Errorf("file permissions = %o, want 0o600", fi.Mode().Perm())
			}

			loaded, err := store.Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if loaded.AccessToken != tt.token.AccessToken {
				t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, tt.token.AccessToken)
			}
			if loaded.RefreshToken != tt.token.RefreshToken {
				t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, tt.token.RefreshToken)
			}
			if loaded.TokenType != tt.token.TokenType {
				t.Errorf("TokenType = %q, want %q", loaded.TokenType, tt.token.TokenType)
			}

			// Check expiry (handles zero time specially)
			if tt.token.Expiry.IsZero() && loaded.Expiry.IsZero() {
				// Both zero, OK
			} else if !tt.token.Expiry.Equal(loaded.Expiry) {
				t.Errorf("Expiry = %v, want %v", loaded.Expiry, tt.token.Expiry)
			}

			// Clean up for next iteration
			os.Remove(tokenPath)
		})
	}
}

func TestTokenStoreLoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "nonexistent.json")

	store := NewTokenStore(tokenPath)
	_, err := store.Load()

	if !errors.Is(err, ErrNoToken) {
		t.Errorf("Load() error = %v, want ErrNoToken", err)
	}
}

func TestTokenStoreAtomicOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	tokenPath := filepath.Join(tmpDir, "token.json")

	store := NewTokenStore(tokenPath)

	token1 := &oauth2.Token{
		AccessToken:  "first",
		RefreshToken: "refresh1",
		TokenType:    "Bearer",
	}
	if err := store.Save(token1); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}

	token2 := &oauth2.Token{
		AccessToken:  "second",
		RefreshToken: "refresh2",
		TokenType:    "Bearer",
	}
	if err := store.Save(token2); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.AccessToken != "second" {
		t.Errorf("AccessToken = %q, want 'second'", loaded.AccessToken)
	}
}

func TestDefaultTokenPath(t *testing.T) {
	path, err := DefaultTokenPath()
	if err != nil {
		t.Fatalf("DefaultTokenPath() error = %v", err)
	}

	if path == "" {
		t.Errorf("DefaultTokenPath() returned empty string")
	}

	if !filepath.IsAbs(path) {
		t.Errorf("DefaultTokenPath() = %q, not absolute", path)
	}

	if filepath.Base(path) != "codex_auth.json" {
		t.Errorf("DefaultTokenPath() base = %q, want codex_auth.json", filepath.Base(path))
	}
}
