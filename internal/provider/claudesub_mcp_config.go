package provider

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// writeConfig writes the claude CLI --mcp-config file into dir (which the caller
// provides private and 0o700) as dir/mcp.json with mode 0o600.
func (h *claudeSubMCPHost) writeConfig(dir string) (string, error) {
	cfg := claudeSubMCPConfig{MCPServers: map[string]claudeSubMCPServerConfig{
		claudeSubMCPServerName: {
			Type:    "http",
			URL:     h.url,
			Headers: map[string]string{"Authorization": "Bearer " + h.token},
		},
	}}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal claude_subscription mcp config: %w", err)
	}
	const name = "mcp.json"
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open claude_subscription mcp dir: %w", err)
	}
	defer root.Close()
	if err := claudeSubMCPReplaceConfig(root, name, data); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// claudeSubMCPReplaceConfig writes data to name beneath root as an exact-0o600
// regular file, replacing any existing regular file. Every step (inspect, temp
// create, rename, cleanup) uses a root-relative operation, so the write stays
// anchored to the directory the Root was opened on even if that path is later
// swapped for a symlink; the token is never written through a symlink. A symlink
// or non-regular target is refused.
func claudeSubMCPReplaceConfig(root *os.Root, name string, data []byte) error {
	if info, err := root.Lstat(name); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to write claude_subscription mcp config: %s is a symlink", name)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to write claude_subscription mcp config: %s is not a regular file", name)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect claude_subscription mcp config: %w", err)
	}
	// Write a fresh 0o600 temp file and atomically rename it into place, so any
	// pre-existing permissive file is replaced with the exact mode.
	tmpName, tmp, err := claudeSubMCPCreateTemp(root, name)
	if err != nil {
		return fmt.Errorf("create claude_subscription mcp config: %w", err)
	}
	defer func() { _ = root.Remove(tmpName) }() // no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set claude_subscription mcp config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write claude_subscription mcp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close claude_subscription mcp config: %w", err)
	}
	if err := root.Rename(tmpName, name); err != nil {
		return fmt.Errorf("replace claude_subscription mcp config: %w", err)
	}
	return nil
}

// claudeSubMCPCreateTemp creates a fresh 0o600 file inside root named after the
// target, using only root-relative operations so the temp file cannot be
// redirected outside the directory. os.Root has no CreateTemp in Go 1.26, so it
// retries an O_EXCL open with a random suffix rather than falling back to raw
// os.CreateTemp.
func claudeSubMCPCreateTemp(root *os.Root, name string) (string, *os.File, error) {
	for i := 0; i < 100; i++ {
		tmpName := name + ".tmp-" + rand.Text()
		f, err := root.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return tmpName, f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("claude_subscription mcp config: could not allocate a temp file")
}

// claudeSubMCPConfig is the exact JSON shape of the CLI's --mcp-config file.
type claudeSubMCPConfig struct {
	MCPServers map[string]claudeSubMCPServerConfig `json:"mcpServers"`
}

type claudeSubMCPServerConfig struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}
