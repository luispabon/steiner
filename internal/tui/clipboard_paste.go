package tui

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
func pasteImageCmd() tea.Cmd {
	return func() tea.Msg {
		// 1. Try clipboard image
		data, mimeType, err := ReadClipboardImage()
		if err == nil && len(data) > 0 {
			return buildClipboardImageMsg(data, mimeType)
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
		return buildClipboardImageMsg(fileData, mimeType)
	}
}

func buildClipboardImageMsg(data []byte, mimeType string) tea.Msg {
	resized, w, h, err := builtin.ResizeImageIfNeeded(data)
	if err != nil {
		return clipboardImageMsg{err: err}
	}
	encoded := base64.StdEncoding.EncodeToString(resized)
	return clipboardImageMsg{
		block: agent.ImageBlock{
			MediaType: mimeType,
			Data:      encoded,
			Width:     w,
			Height:    h,
			SizeBytes: len(resized),
		},
	}
}
