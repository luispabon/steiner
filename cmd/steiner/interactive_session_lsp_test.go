package main

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/lsp"
)

func TestLSPTUIStates(t *testing.T) {
	cfg := config.Config{
		LSP: config.LSPConfig{
			Servers: map[string]config.LSPServerConfig{
				"gopls":   {Enabled: true},
				"pyright": {Enabled: true},
			},
		},
	}

	t.Run("nil manager with configured servers yields not started rows", func(t *testing.T) {
		states := lspTUIStates(cfg, nil)
		if len(states) != 2 {
			t.Fatalf("len(states) = %d, want 2", len(states))
		}
		for _, s := range states {
			if s.Status != "not started" {
				t.Errorf("server %q status = %q, want %q", s.Name, s.Status, "not started")
			}
		}
	})

	t.Run("nil manager with no configured servers yields empty slice", func(t *testing.T) {
		states := lspTUIStates(config.Config{}, nil)
		if len(states) != 0 {
			t.Fatalf("len(states) = %d, want 0", len(states))
		}
	})

	t.Run("untouched manager synthesizes not started rows same as nil", func(t *testing.T) {
		mgr := lsp.NewManager(cfg.LSP, t.TempDir(), nil, func(string) {}, nil)
		t.Cleanup(func() { _ = mgr.Close() })

		states := lspTUIStates(cfg, mgr)
		if len(states) != 2 {
			t.Fatalf("len(states) = %d, want 2", len(states))
		}
		for _, s := range states {
			if s.Status != "not started" {
				t.Errorf("server %q status = %q, want %q", s.Name, s.Status, "not started")
			}
		}
	})
}
