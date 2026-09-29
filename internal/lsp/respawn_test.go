package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// TestEntryForRespawnsExitedSession pins that a Ready entry whose server
// process has exited is retired and respawned on the next lookup instead of
// handing back the dead session forever.
func TestEntryForRespawnsExitedSession(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(""), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:      true,
		IdleTimeout:  config.MustDuration("30s"),
		ReadyTimeout: config.MustDuration("500ms"),
		CacheDir:     filepath.Join(tmpdir, "cache"),
		Servers: map[string]config.LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        os.Args[0],
				Args:           []string{"-test.run=TestLSPHelperProcess"},
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				Env:            map[string]string{helperEnv: "lsp"},
			},
		},
	}
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	released := make(chan struct{})
	close(released)
	entered := make(chan struct{}, 1)
	dead := newBlockingSession(entered, released)
	close(dead.exited)
	key := sessionKey{server: "go", root: tmpdir}
	m.sessions[key] = &entry{
		state: ServerState{
			Name: "go", Root: tmpdir, Status: ServerStatusReady,
			StartedAt: time.Now(), LastUsed: time.Now(),
		},
		session: dead,
	}

	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTestTimeout)
	defer cancel()
	ent, sess, err := m.entryFor(ctx, filepath.Join(tmpdir, "file.go"))
	requireSessionStarted(t, sess, err)
	if sess == session(dead) {
		t.Fatal("entryFor returned the exited session instead of respawning")
	}
	select {
	case <-entered:
	default:
		t.Error("exited session was not closed")
	}
	select {
	case <-sess.Exited():
		t.Error("respawned session is already exited")
	default:
	}
	ent.mu.Lock()
	status := ent.state.Status
	ent.mu.Unlock()
	if status != ServerStatusReady {
		t.Errorf("status = %v, want ready", status)
	}
}
