package tui

import (
	"strings"
	"testing"
)

func TestLSPRowEmptyWhenNoActiveServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		servers []LSPServerStatus
	}{
		{"none configured", nil},
		{"all stopped", []LSPServerStatus{{Name: "gopls", Status: "stopped"}}},
		{"unknown status", []LSPServerStatus{{Name: "gopls", Status: "declared"}}},
		{"disabled", []LSPServerStatus{{Name: "gopls", Status: "disabled"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{lspServers: tc.servers}
			s.recomputeLSPAggregate()
			spinner, text := s.lspRow(80)
			if spinner != "" || text != "" {
				t.Errorf("lspRow() = (%q, %q), want (\"\", \"\")", spinner, text)
			}
		})
	}
}

func TestLSPRowJoinsNamesWhenFits(t *testing.T) {
	t.Parallel()
	s := sidebarState{lspServers: []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "ready"},
		{Name: "ts-ls", Root: "/repo", Status: "ready"},
	}}
	s.recomputeLSPAggregate()
	spinner, text := s.lspRow(80)
	if text != "gopls,ts-ls" {
		t.Errorf("lspRow() text = %q, want %q", text, "gopls,ts-ls")
	}
	if spinner != "" {
		t.Errorf("lspRow() spinner = %q, want empty when no server is starting", spinner)
	}
	if strings.Contains(spinner, "\x1b[") || strings.Contains(text, "\x1b[") {
		t.Errorf("lspRow() must not contain ANSI escapes; styling is the caller's job (spinner=%q, text=%q)", spinner, text)
	}
}

func TestLSPRowFallsBackToCountWhenTooNarrow(t *testing.T) {
	t.Parallel()
	s := sidebarState{lspServers: []LSPServerStatus{
		{Name: "gopls", Root: "/repo", Status: "ready"},
		{Name: "ts-ls", Root: "/repo", Status: "starting"},
	}}
	s.recomputeLSPAggregate()
	_, text := s.lspRow(3)
	if text != "1/2" {
		t.Errorf("lspRow() text = %q, want %q", text, "1/2")
	}
}

func TestLSPRowSpinnerOnlyWhenStarting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		status      string
		wantSpinner bool
	}{
		{"starting", "starting", true},
		{"ready", "ready", false},
		{"failed", "failed", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := sidebarState{lspServers: []LSPServerStatus{{Name: "gopls", Status: tc.status}}}
			s.recomputeLSPAggregate()
			spinner, _ := s.lspRow(80)
			if tc.wantSpinner && spinner == "" {
				t.Errorf("lspRow() spinner = %q, want non-empty when %s", spinner, tc.status)
			}
			if !tc.wantSpinner && spinner != "" {
				t.Errorf("lspRow() spinner = %q, want empty when %s", spinner, tc.status)
			}
		})
	}
}

func TestLSPRowStoppedSessionNotActive(t *testing.T) {
	t.Parallel()
	s := sidebarState{lspServers: []LSPServerStatus{
		{Name: "gopls", Root: "/a", Status: "stopped"},
		{Name: "gopls", Root: "/b", Status: "ready"},
	}}
	s.recomputeLSPAggregate()
	_, text := s.lspRow(80)
	if text != "gopls" {
		t.Errorf("lspRow() text = %q, want %q (stopped session should not duplicate or exclude the server)", text, "gopls")
	}
	if s.lspTotalKnown != 1 {
		t.Errorf("lspTotalKnown = %d, want 1 (deduped by name, stopped session ignored)", s.lspTotalKnown)
	}
}

func TestLSPRowFailedSessionMakesServerFailed(t *testing.T) {
	t.Parallel()
	s := sidebarState{lspServers: []LSPServerStatus{
		{Name: "gopls", Root: "/a", Status: "ready"},
		{Name: "gopls", Root: "/b", Status: "failed"},
	}}
	s.recomputeLSPAggregate()
	if !s.lspFailed {
		t.Error("lspFailed = false, want true when any session for a server failed")
	}
	if s.lspActive != 1 {
		t.Errorf("lspActive = %d, want 1: N counts servers with >=1 ready session, regardless of other failed sessions", s.lspActive)
	}
}

func TestLSPRowActiveCountsReadyServers(t *testing.T) {
	t.Parallel()
	s := sidebarState{lspServers: []LSPServerStatus{
		{Name: "gopls", Root: "/a", Status: "ready"},
		{Name: "ts-ls", Root: "/a", Status: "starting"},
	}}
	s.recomputeLSPAggregate()
	if s.lspActive != 1 {
		t.Errorf("lspActive = %d, want 1", s.lspActive)
	}
	if s.lspTotalKnown != 2 {
		t.Errorf("lspTotalKnown = %d, want 2", s.lspTotalKnown)
	}
}
