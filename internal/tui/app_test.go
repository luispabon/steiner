package tui

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

func TestSetInitialModeSeedsModel(t *testing.T) {
	tests := []struct {
		name          string
		initialConfig string
		want          string
	}{
		{name: "seed build on empty config", want: "build"},
		{name: "override plan", initialConfig: "build", want: "plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := NewApp(Config{InitialMode: tt.initialConfig})
			app.SetInitialMode(tt.want)

			m := newModel(app.cfg, nil)
			if m.mode != tt.want {
				t.Fatalf("model mode = %q, want %q", m.mode, tt.want)
			}
			if m.sidebar.execMode != tt.want {
				t.Fatalf("sidebar mode = %q, want %q", m.sidebar.execMode, tt.want)
			}
			if m.status.execMode != tt.want {
				t.Fatalf("status mode = %q, want %q", m.status.execMode, tt.want)
			}
		})
	}
}

func TestSetInitialEnabledSkillsSeedsModel(t *testing.T) {
	tests := []struct {
		name    string
		config  []string
		initial []string
		want    map[string]bool
	}{
		{
			name:    "seed empty on empty config",
			config:  []string{},
			initial: []string{},
			want:    map[string]bool{},
		},
		{
			name:    "seed skills when present in config",
			config:  []string{"sql", "python", "bash"},
			initial: []string{"sql", "bash"},
			want: map[string]bool{
				"sql":    true,
				"python": false,
				"bash":   true,
			},
		},
		{
			name:    "ignore unknown skills",
			config:  []string{"sql", "python"},
			initial: []string{"sql", "unknown"},
			want: map[string]bool{
				"sql":    true,
				"python": false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := NewApp(Config{
				SkillNames:           tt.config,
				InitialEnabledSkills: tt.initial,
			})

			m := newModel(app.cfg, nil)
			if len(m.enabledSkills) != len(tt.want) {
				t.Fatalf("model enabledSkills len = %d, want %d", len(m.enabledSkills), len(tt.want))
			}
			for name, wantEnabled := range tt.want {
				if got, ok := m.enabledSkills[name]; !ok {
					t.Errorf("skill %q missing from enabledSkills", name)
				} else if got != wantEnabled {
					t.Errorf("skill %q enabled = %v, want %v", name, got, wantEnabled)
				}
			}
		})
	}
}

func TestResumeSeedsInitialModeBeforeToggle(t *testing.T) {
	store := tuiTestSessionStore{}
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents:   output.NoopSink{},
		SessionStore: store,
		Config: config.Config{
			Modes: config.ModesConfig{Default: config.ExecutionModeBuild},
		},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	app := NewApp(Config{Controller: sess, InitialMode: "build"})
	sess.DisplaySink().Set(app.EventSink())
	if err := sess.LoadSessionByID(context.Background(), "id"); err != nil {
		t.Fatalf("LoadSessionByID() error = %v", err)
	}
	if got := sess.Mode(); got != config.ExecutionModePlan {
		t.Fatalf("session mode = %q, want %q", got, config.ExecutionModePlan)
	}

	app.SetInitialMode(string(sess.Mode()))
	m := newModel(app.cfg, app.bridge.Messages())
	if m.mode != "plan" {
		t.Fatalf("model mode = %q, want %q", m.mode, "plan")
	}
	m.executeToggleModeAction()
	if got := sess.Mode(); got != config.ExecutionModeBuild {
		t.Fatalf("session mode after toggle = %q, want %q", got, config.ExecutionModeBuild)
	}
}

type tuiTestSessionStore struct{}

func (tuiTestSessionStore) Save(session.Session) error {
	return nil
}

func (tuiTestSessionStore) Load(string) (session.Session, error) {
	return session.Session{
		ID:   "id",
		Mode: "plan",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{{
				ID:       1,
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "previous message"}},
			}},
			NextGenerationID: 2,
		},
	}, nil
}

func (tuiTestSessionStore) List() ([]session.IndexEntry, error) {
	return nil, nil
}
