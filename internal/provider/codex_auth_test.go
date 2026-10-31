package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/oauth"
)

// codexQuotaTestTokenPath points the user config dir at a fresh temp dir and
// returns the Codex token path inside it, so tests never touch real credentials.
func codexQuotaTestTokenPath(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := oauth.DefaultTokenPath()
	if err != nil {
		t.Fatalf("DefaultTokenPath() error = %v", err)
	}
	return path
}

// codexQuotaTestWriteToken writes body as the stored Codex token at path.
func codexQuotaTestWriteToken(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create token dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
}

func TestResolveCodexAuth(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  CodexAuth
	}{
		{
			name:  "chatgpt account token",
			token: `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`,
			want:  CodexAuth{AccessToken: "access-1", AccountID: "acct-1"},
		},
		{
			name:  "exchanged api key token",
			token: `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1","openai_api_key":"sk-1"}`,
			want:  CodexAuth{AccessToken: "access-1", AccountID: "acct-1", APIKey: "sk-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codexQuotaTestWriteToken(t, codexQuotaTestTokenPath(t), tt.token)
			got, err := ResolveCodexAuth(context.Background(), nil)
			if err != nil {
				t.Fatalf("ResolveCodexAuth() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveCodexAuth() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveCodexAuthPicksUpExternalChange(t *testing.T) {
	path := codexQuotaTestTokenPath(t)
	codexQuotaTestWriteToken(t, path, `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`)
	first, err := ResolveCodexAuth(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveCodexAuth() first call error = %v", err)
	}

	// The replacement is longer, so the size changes: a same-size rewrite that
	// keeps the inode can land in the same modification-time tick and be served
	// from the cache (see oauth.AuthResolver).
	codexQuotaTestWriteToken(t, path, `{"access_token":"access-2-longer","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`)
	second, err := ResolveCodexAuth(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveCodexAuth() second call error = %v", err)
	}
	if first.AccessToken != "access-1" || second.AccessToken != "access-2-longer" {
		t.Errorf("AccessToken = %q then %q, want access-1 then access-2-longer", first.AccessToken, second.AccessToken)
	}
}

func TestResolveCodexAuthCachedTokenSkipsReload(t *testing.T) {
	path := codexQuotaTestTokenPath(t)
	codexQuotaTestWriteToken(t, path, `{"access_token":"cached-token","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`)
	first, err := ResolveCodexAuth(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveCodexAuth() first call error = %v", err)
	}

	// Rewrite the file with a same-length token and restore its modification
	// time, so the cached stamp still matches and the token must come from the
	// in-process cache.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	replaced := strings.Replace(string(original), "cached-token", "stale!-token", 1)
	if len(replaced) != len(original) {
		t.Fatalf("replacement length = %d, want %d", len(replaced), len(original))
	}
	if err := os.WriteFile(path, []byte(replaced), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	second, err := ResolveCodexAuth(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResolveCodexAuth() second call error = %v", err)
	}
	if first.AccessToken != "cached-token" || second.AccessToken != "cached-token" {
		t.Errorf("AccessToken = %q then %q, want cached-token twice", first.AccessToken, second.AccessToken)
	}
}

func TestResolveCodexAuthMissingToken(t *testing.T) {
	codexQuotaTestTokenPath(t)
	_, err := ResolveCodexAuth(context.Background(), nil)
	if !errors.Is(err, oauth.ErrNoToken) {
		t.Errorf("ResolveCodexAuth() error = %v, want an error wrapping oauth.ErrNoToken", err)
	}
}

func TestLoadCodexIdentity(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  CodexIdentity
	}{
		{
			name: "no token file",
			want: CodexIdentity{},
		},
		{
			name:  "chatgpt account",
			token: `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1"}`,
			want:  CodexIdentity{AccountID: "acct-1", SignedIn: true},
		},
		{
			name:  "api key mode",
			token: `{"access_token":"access-1","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z","account_id":"acct-1","openai_api_key":"sk-1"}`,
			want:  CodexIdentity{AccountID: "acct-1", APIMode: true, SignedIn: true},
		},
		{
			// No refresh token is stored, so a refresh attempt would fail without
			// network. LoadCodexIdentity must report the stored state instead.
			name:  "expired token is not refreshed",
			token: `{"access_token":"access-1","token_type":"Bearer","expiry":"2000-01-01T00:00:00Z","account_id":"acct-1"}`,
			want:  CodexIdentity{AccountID: "acct-1", SignedIn: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := codexQuotaTestTokenPath(t)
			if tt.token != "" {
				codexQuotaTestWriteToken(t, path, tt.token)
			}
			got, err := LoadCodexIdentity()
			if err != nil {
				t.Fatalf("LoadCodexIdentity() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadCodexIdentity() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
