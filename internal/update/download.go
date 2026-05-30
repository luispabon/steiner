package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// downloadURL downloads the content at url with optional bearer token auth.
func downloadURL(ctx context.Context, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return body, nil
}

// downloadAsset downloads a release asset binary.
func downloadAsset(ctx context.Context, url, token string) ([]byte, error) {
	return downloadURL(ctx, url, token)
}

// downloadChecksums downloads the checksums file for a release.
func downloadChecksums(ctx context.Context, url, token string) ([]byte, error) {
	return downloadURL(ctx, url, token)
}
