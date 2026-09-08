package main

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/lsp"
)

func TestLSPTUIStatesFrom(t *testing.T) {
	cfg := config.Config{
		LSP: config.LSPConfig{
			Servers: map[string]config.LSPServerConfig{
				"gopls":   {Enabled: true},
				"pyright": {Enabled: true},
			},
		},
	}
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lastUsed := startedAt.Add(time.Minute)

	tests := []struct {
		name       string
		cfg        config.Config
		live       []lsp.ServerState
		wantNames  []string
		wantStatus map[string]string
	}{
		{
			name:      "no config and no live state yields empty slice",
			cfg:       config.Config{},
			live:      nil,
			wantNames: nil,
		},
		{
			name:      "configured servers with no live state all synthesize not started",
			cfg:       cfg,
			live:      nil,
			wantNames: []string{"gopls", "pyright"},
		},
		{
			name: "live session for a configured server suppresses its synthesized row",
			cfg:  cfg,
			live: []lsp.ServerState{
				{Name: "gopls", Root: "/repo", Status: lsp.ServerStatusReady, StartedAt: startedAt, LastUsed: lastUsed},
			},
			wantNames: []string{"gopls", "pyright"},
		},
		{
			name: "two live sessions for the same server at different roots both kept",
			cfg:  config.Config{},
			live: []lsp.ServerState{
				{Name: "gopls", Root: "/repo-a", Status: lsp.ServerStatusReady},
				{Name: "gopls", Root: "/repo-b", Status: lsp.ServerStatusStarting},
			},
			wantNames: []string{"gopls", "gopls"},
		},
		{
			name: "disabled configured server with no live state synthesizes disabled",
			cfg: config.Config{
				LSP: config.LSPConfig{
					Servers: map[string]config.LSPServerConfig{
						"gopls": {Enabled: false},
					},
				},
			},
			live:       nil,
			wantNames:  []string{"gopls"},
			wantStatus: map[string]string{"gopls": "disabled"},
		},
		{
			name: "enabled configured server with no live state synthesizes not started",
			cfg: config.Config{
				LSP: config.LSPConfig{
					Servers: map[string]config.LSPServerConfig{
						"gopls": {Enabled: true},
					},
				},
			},
			live:       nil,
			wantNames:  []string{"gopls"},
			wantStatus: map[string]string{"gopls": "not started"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lspTUIStatesFrom(tc.cfg, tc.live)
			names := make([]string, 0, len(got))
			for _, s := range got {
				names = append(names, s.Name)
			}
			sort.Strings(names)
			wantSorted := append([]string(nil), tc.wantNames...)
			sort.Strings(wantSorted)
			if !equalStrings(names, wantSorted) {
				t.Fatalf("names = %v, want %v", names, wantSorted)
			}
			for _, s := range got {
				if wantStatus, ok := tc.wantStatus[s.Name]; ok && s.Status != wantStatus {
					t.Fatalf("status for %s = %q, want %q", s.Name, s.Status, wantStatus)
				}
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLSPTUIStatesFromPreservesLiveFields(t *testing.T) {
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lastUsed := startedAt.Add(time.Minute)
	live := []lsp.ServerState{
		{
			Name:      "gopls",
			Root:      "/repo",
			Status:    lsp.ServerStatusFailed,
			Err:       errors.New("boom"),
			StartedAt: startedAt,
			LastUsed:  lastUsed,
		},
	}

	got := lspTUIStatesFrom(config.Config{}, live)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	s := got[0]
	if s.Name != "gopls" || s.Root != "/repo" || s.Status != string(lsp.ServerStatusFailed) {
		t.Errorf("got = %+v, want name=gopls root=/repo status=failed", s)
	}
	if s.Error != "boom" {
		t.Errorf("s.Error = %q, want %q", s.Error, "boom")
	}
	if !s.StartedAt.Equal(startedAt) || !s.LastUsed.Equal(lastUsed) {
		t.Errorf("s.StartedAt/LastUsed = %v/%v, want %v/%v", s.StartedAt, s.LastUsed, startedAt, lastUsed)
	}
}

func TestLSPTUIStatesNilManager(t *testing.T) {
	cfg := config.Config{
		LSP: config.LSPConfig{
			Servers: map[string]config.LSPServerConfig{"gopls": {Enabled: true}},
		},
	}
	states := lspTUIStates(cfg, nil)
	if len(states) != 1 || states[0].Name != "gopls" || states[0].Status != "not started" {
		t.Fatalf("states = %+v, want one not-started gopls row", states)
	}
}
