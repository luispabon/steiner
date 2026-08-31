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
func saveFetchedContent(workDir, content, contentType string, truncated bool, maxSize int) (*FetchURLResult, error) {
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
	if len(runes) > savedContentPreviewRunes {
		previewRunes = runes[:savedContentPreviewRunes]
	}
	preview := string(previewRunes)

	nextOffset := strings.Count(preview, "\n") + 1
	totalLines := strings.Count(content, "\n") + 1

	message := fmt.Sprintf(
		"Saved %d bytes (%d lines). Use the read tool with offset and limit to page through it.",
		len(content), totalLines,
	)
	if truncated {
		message = truncatedSaveMessage(len(content), maxSize)
	}

	return &FetchURLResult{
		Content:       preview,
		ContentLength: len(runes),
		FilePath:      relPath,
		NextOffset:    nextOffset,
		Message:       message,
		TotalLines:    totalLines,
	}, nil
}

// saveFetchedImage writes raw image bytes to a content-addressed file under
// .steiner/tmp/fetched/ within workDir. Image files are kept out of the tool
// result so the caller can request them with the read tool.
func saveFetchedImage(workDir string, image *fetchedImage) (*FetchURLResult, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256(image.data))
	relPath := filepath.Join(".steiner", "tmp", "fetched", hash[:12]+image.extension)
	absPath := filepath.Join(workDir, relPath)
	absDir := filepath.Dir(absPath)

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("save fetched image: %w", err)
	}
	if err := writeFileAtomic(absPath, image.data, 0o600); err != nil {
		return nil, fmt.Errorf("save fetched image: %w", err)
	}

	return &FetchURLResult{
		ContentLength: len(image.data),
		FilePath:      relPath,
		Message:       "Image saved. Call the read tool with this path to inspect it.",
	}, nil
}
