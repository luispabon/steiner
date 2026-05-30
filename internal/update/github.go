package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// httpClient is the HTTP client used for all HTTP requests in this package.
// It is set to http.DefaultClient by default and can be replaced in tests.
var httpClient = http.DefaultClient

// asset represents a downloadable release asset from a GitHub release.
type asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

// release represents a GitHub release.
type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

// fetchLatestRelease fetches the latest non-draft, non-prerelease release from
// the given GitHub repository. If token is non-empty, it is passed as a Bearer
// token in the Authorization header.
func fetchLatestRelease(ctx context.Context, owner, repo, token string) (*release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	return &rel, nil
}
