package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// copyToClipboard returns a tea.Cmd that writes text to the system clipboard.
// Tries wl-copy (Wayland), xclip, xsel, then falls back to OSC52.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if clipboardExec(text) {
			return nil
		}
		_, _ = fmt.Fprint(os.Stdout, ansi.SetSystemClipboard(text))
		return nil
	}
}

func clipboardExec(text string) bool {
	type candidate struct {
		name string
		args []string
		env  string
	}
	candidates := []candidate{
		{"wl-copy", nil, "WAYLAND_DISPLAY"},
		{"xclip", []string{"-selection", "clipboard"}, "DISPLAY"},
		{"xsel", []string{"--clipboard", "--input"}, "DISPLAY"},
	}
	for _, c := range candidates {
		if c.env != "" && os.Getenv(c.env) == "" {
			continue
		}
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		if runClipboardCandidate(path, c.args, text) {
			return true
		}
	}
	return false
}

func runClipboardCandidate(path string, args []string, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}
