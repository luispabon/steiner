package builtin

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// saveFetchedContent writes content to a content-addressed file under
// .steiner/tmp/fetched/ within workDir and returns a FetchURLResult
// describing the saved file plus a bounded preview. URL and StatusCode are
// left zero-value for the caller to set.
func saveFetchedContent(workDir, content, contentType string, truncated bool) (*FetchURLResult, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	hash12 := hash[:12]
	ext := extensionFromContentType(contentType)

	relPath := filepath.Join(".steiner", "tmp", "fetched", hash12+ext)
	absPath := filepath.Join(workDir, relPath)
	absDir := filepath.Dir(absPath)

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("save fetched content: %w", err)
	}

	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("save fetched content: %w", err)
	}

	runes := []rune(content)
	previewRunes := runes
	if len(runes) > inlineThreshold {
		previewRunes = runes[:inlineThreshold]
	}
	preview := string(previewRunes)

	nextOffset := strings.Count(preview, "\n") + 1

	message := "Use the read tool with offset and limit to continue reading."
	if truncated {
		message = fmt.Sprintf(
			"Content was truncated to %d characters (original is larger). The saved file does not contain the complete content. Use the read tool with offset and limit to continue reading the saved portion.",
			len(runes),
		)
	}

	return &FetchURLResult{
		Content:       preview,
		ContentLength: len(runes),
		FilePath:      relPath,
		NextOffset:    nextOffset,
		Truncated:     truncated,
		Message:       message,
	}, nil
}
