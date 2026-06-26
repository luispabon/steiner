package oauth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// RefreshableTokenSource implements oauth2.TokenSource with automatic refresh.
type RefreshableTokenSource struct {
	store *TokenStore
	conf  *oauth2.Config
	mu    sync.Mutex
}

// NewRefreshableTokenSource creates a token source that automatically refreshes tokens.
func NewRefreshableTokenSource(store *TokenStore, conf *oauth2.Config) *RefreshableTokenSource {
	return &RefreshableTokenSource{
		store: store,
		conf:  conf,
	}
}

// Token returns a valid token, refreshing if necessary.
// It implements oauth2.TokenSource.
func (r *RefreshableTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	token, err := r.store.Load()
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}

	// Check if token is still valid and not within 5 minutes of expiry
	if token.Valid() && !r.isNearExpiry(token) {
		return token, nil
	}

	// Refresh the token
	refreshedToken, err := r.refresh(token)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}

	// Persist the refreshed token
	if err := r.store.Save(refreshedToken); err != nil {
		return nil, fmt.Errorf("save refreshed token: %w", err)
	}

	return refreshedToken, nil
}

// refresh manually refreshes the token by calling the token endpoint.
func (r *RefreshableTokenSource) refresh(token *oauth2.Token) (*oauth2.Token, error) {
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
	}
	if r.conf.ClientID != "" {
		values.Set("client_id", r.conf.ClientID)
	}
	if r.conf.ClientSecret != "" {
		values.Set("client_secret", r.conf.ClientSecret)
	}

	resp, err := http.PostForm(r.conf.Endpoint.TokenURL, values)
	if err != nil {
		return nil, fmt.Errorf("post token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	accessToken, ok := result["access_token"].(string)
	if !ok {
		return nil, fmt.Errorf("no access_token in response")
	}

	newToken := &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}
	if refreshToken, ok := result["refresh_token"].(string); ok {
		newToken.RefreshToken = refreshToken
	} else {
		newToken.RefreshToken = token.RefreshToken
	}
	if expiresIn, ok := result["expires_in"].(float64); ok {
		newToken.Expiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}

	return newToken, nil
}

// isNearExpiry checks if a token is within 5 minutes of expiry.
func (r *RefreshableTokenSource) isNearExpiry(token *oauth2.Token) bool {
	if token.Expiry.IsZero() {
		return false
	}
	return time.Until(token.Expiry) < 5*time.Minute
}
