package tui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// clipboardImageMsg is returned by the async clipboard read command.
type clipboardImageMsg struct {
	block agent.ImageBlock
	err   error // non-nil means paste failed (silently ignored for no-image case)
}

// pasteImageCmd returns a tea.Cmd that reads the clipboard asynchronously.
// Returns nil (no-op) when no image and no image file path found.
func pasteImageCmd(store *agent.ImageStore) tea.Cmd {
	return func() tea.Msg {
		// 1. Try clipboard image
		data, mimeType, err := ReadClipboardImage()
		if err == nil && len(data) > 0 {
			return buildClipboardImageMsg(data, mimeType, store)
		}

		// 2. Try clipboard text as file path
		text, err := ReadClipboardText()
		if err != nil || text == "" {
			return nil
		}
		text = strings.TrimSpace(text)
		if !builtin.IsImageExtension(filepath.Ext(text)) {
			return nil
		}
		fileData, err := os.ReadFile(text)
		if err != nil {
			return nil
		}
		// detect MIME from file content
		mimeType = http.DetectContentType(fileData)
		return buildClipboardImageMsg(fileData, mimeType, store)
	}
}

func buildClipboardImageMsg(data []byte, mimeType string, store *agent.ImageStore) tea.Msg {
	resized, w, h, err := builtin.ResizeImageIfNeeded(data)
	if err != nil {
		return clipboardImageMsg{err: err}
	}
	encoded := base64.StdEncoding.EncodeToString(resized)
	block := agent.ImageBlock{
		MediaType: mimeType,
		Data:      encoded,
		Width:     w,
		Height:    h,
		SizeBytes: len(resized),
	}

	if store != nil {
		ext := extFromMIME(mimeType)
		filename := fmt.Sprintf("%s_%s.%s", time.Now().Format("20060102_150405"), randomHex(4), ext)
		path := filepath.Join(store.Dir(), filename)
		if mkErr := os.MkdirAll(store.Dir(), 0o755); mkErr == nil {
			if writeErr := os.WriteFile(path, resized, 0o644); writeErr == nil {
				ref := store.Register(path, mimeType, w, h, len(resized))
				block.ID = ref.ID
				block.FilePath = ref.FilePath
			}
		}
	}

	return clipboardImageMsg{block: block}
}

// randomHex returns n random bytes hex-encoded (2n characters).
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// extFromMIME maps common image MIME types to file extensions.
func extFromMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}
