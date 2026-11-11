package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// loginTestToken returns a token distinct from the refresh-due test token, so a
// test can tell a login save from a refresh save by its access token.
func loginTestToken() *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  "login-token",
		RefreshToken: "login-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
}

func TestTokenStoreSaveLockedWinsAfterRefresh(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveRefreshDueTestToken(t, store)

	saved := make(chan error, 1)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The refresh holds the token lock across the token exchange, so this
		// login save starts while the lock is taken and must wait for the save
		// that the refresh performs after the response.
		go func() {
			close(started)
			saved <- store.SaveLocked(context.Background(), loginTestToken())
		}()
		<-started
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"refreshed-token","refresh_token":"refresh_token","expires_in":3600}`)
	}))
	t.Cleanup(server.Close)

	resolver := NewAuthResolver()
	if _, err := resolver.Resolve(context.Background(), store, testTokenConf(server.URL), nil); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("SaveLocked() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SaveLocked() did not finish: the login save is deadlocked against the refresh lock")
	}

	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "login-token" {
		t.Errorf("persisted AccessToken = %q, want login-token: the login save that follows a refresh must win", stored.AccessToken)
	}
}

func TestTokenStoreSaveLockedCancelledContextDoesNotSave(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	t.Run("wait for lock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token.json")
		store := NewTokenStore(path)
		saveValidTestToken(t, store, "stored-token")

		release, err := acquireTokenLock(context.Background(), path)
		if err != nil {
			t.Fatalf("acquireTokenLock() error = %v", err)
		}
		defer release()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if err := store.SaveLocked(ctx, loginTestToken()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("SaveLocked() error = %v, want context.DeadlineExceeded", err)
		}

		stored, err := store.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if stored.AccessToken != "stored-token" {
			t.Errorf("persisted AccessToken = %q, want stored-token: a cancelled save must not write", stored.AccessToken)
		}
	})

	t.Run("already cancelled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token.json")
		store := NewTokenStore(path)
		saveValidTestToken(t, store, "stored-token")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.SaveLocked(ctx, loginTestToken()); !errors.Is(err, context.Canceled) {
			t.Fatalf("SaveLocked() error = %v, want context.Canceled", err)
		}

		stored, err := store.Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if stored.AccessToken != "stored-token" {
			t.Errorf("persisted AccessToken = %q, want stored-token: a cancelled save must not write", stored.AccessToken)
		}
	})
}

func TestTokenStoreSaveDoesNotTakeTokenLock(t *testing.T) {
	if !tokenLockSupported {
		t.Skip("cross-process token lock is unavailable on this platform")
	}

	path := filepath.Join(t.TempDir(), "token.json")
	store := NewTokenStore(path)
	saveValidTestToken(t, store, "stored-token")

	// Refresh saves while it holds the lock. Save must not take the lock itself,
	// or that save would deadlock against the lock the caller already holds.
	release, err := acquireTokenLock(context.Background(), path)
	if err != nil {
		t.Fatalf("acquireTokenLock() error = %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		done <- store.Save(&oauth2.Token{
			AccessToken:  "unlocked-token",
			RefreshToken: "unlocked-refresh",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(time.Hour),
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Save() blocked on the token lock; refresh's own save must not lock recursively")
	}

	stored, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.AccessToken != "unlocked-token" {
		t.Errorf("persisted AccessToken = %q, want unlocked-token", stored.AccessToken)
	}
}
