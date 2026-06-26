package oauth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const proactiveRefreshWindow = 5 * time.Minute

// RefreshableTokenSource implements oauth2.TokenSource with automatic refresh
// and disk persistence. It delegates refresh mechanics to the oauth2 library
// and persists tokens to disk when they change.
type RefreshableTokenSource struct {
	inner oauth2.TokenSource
	store *TokenStore
	mu    sync.Mutex
	last  *oauth2.Token
}

// NewRefreshableTokenSource creates a token source that automatically refreshes
// tokens using the oauth2 library and persists refreshed tokens to disk.
func NewRefreshableTokenSource(store *TokenStore, conf *oauth2.Config, token *oauth2.Token) *RefreshableTokenSource {
	inner := oauth2.ReuseTokenSourceWithExpiry(
		token,
		conf.TokenSource(context.Background(), token),
		proactiveRefreshWindow,
	)
	return &RefreshableTokenSource{
		inner: inner,
		store: store,
		last:  token,
	}
}

// Token returns a valid token, refreshing and persisting if necessary.
func (r *RefreshableTokenSource) Token() (*oauth2.Token, error) {
	tok, err := r.inner.Token()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil || tok.AccessToken != r.last.AccessToken {
		if err := r.store.Save(tok); err != nil {
			return nil, fmt.Errorf("save refreshed token: %w", err)
		}
		r.last = tok
	}
	return tok, nil
}
