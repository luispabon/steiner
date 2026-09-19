package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// httpClient is the HTTP client used for all HTTP requests in this package.
// It is set to http.DefaultClient by default and can be replaced in tests.
var httpClient = http.DefaultClient

const (
	// maxReleaseJSONBytes limits the size of GitHub release JSON responses to protect
	// against unbounded memory consumption from malicious or streaming responses.
	// Release JSON is typically a few KB; this 5 MB limit accommodates even large
	// releases with many assets.
	maxReleaseJSONBytes = 5 * 1024 * 1024
)

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
	return fetchRelease(ctx, url, token)
}

// fetchReleaseByTag fetches a GitHub release by its tag name. If token is
// non-empty, it is passed as a Bearer token in the Authorization header.
func fetchReleaseByTag(ctx context.Context, owner, repo, tag, token string) (*release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", owner, repo, tag)
	return fetchRelease(ctx, url, token)
}

// fetchRelease performs a GET request to the given GitHub API URL and returns
// the parsed release. If token is non-empty, it is passed as a Bearer token.
func fetchRelease(ctx context.Context, url, token string) (*release, error) {
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseJSONBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if len(body) > maxReleaseJSONBytes {
		return nil, fmt.Errorf("release JSON exceeded maximum size of %d bytes", maxReleaseJSONBytes)
	}

	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	return &rel, nil
}
