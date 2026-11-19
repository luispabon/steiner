package interactive

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

func restoreTestConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"local":  {Type: config.ProviderTypeOllama, BaseURL: "http://localhost:11434/v1"},
			"remote": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://remote.invalid/v1", APIKeyEnv: "STEINER_TEST_MISSING_KEY"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"small":        {Provider: "local", ID: "small-backend"},
				"large":        {Provider: "local", ID: "large-backend"},
				"twin-a":       {Provider: "local", ID: "twin"},
				"twin-b":       {Provider: "local", ID: "twin"},
				"remote-model": {Provider: "remote", ID: "remote-backend"},
			},
			Effective: config.EffectiveModelAssignments{
				DefaultModel:            "small",
				ActiveOrchestratorModel: "small",
			},
		},
	}
}

func restoreTestLineage() agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:       1,
			Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}},
		}},
		NextGenerationID: 2,
	}
}

func warningMessages(events []output.Event) []string {
	var warnings []string
	for _, event := range events {
		if payload, ok := event.Payload.(output.ConfigWarningEvent); ok {
			warnings = append(warnings, payload.Message)
		}
	}
	return warnings
}

func TestLoadSessionRestoresSavedModelAndReasoning(t *testing.T) {
	t.Setenv("STEINER_TEST_MISSING_KEY", "")
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["saved"] = session.Session{
		ID:                 "saved",
		Model:              "large-backend",
		ModelRef:           "large",
		ModelProvider:      "local",
		ReasoningOverrides: map[string]session.ReasoningOverride{"large": {Kind: "effort", Effort: "high"}},
		Lineage:            restoreTestLineage(),
	}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents:         output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore:       store,
		Config:             cfg,
		StartupModel:       "small",
		StartupModelSource: ModelSourceProfile,
	})

	if err := s.loadSession(context.Background(), "saved"); err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	if got := s.CurrentModelAlias(); got != "large" {
		t.Fatalf("CurrentModelAlias() = %q, want large", got)
	}
	if got := s.CurrentReasoningOverride(); got.Kind != provider.ReasoningOverrideEffort || got.Effort != "high" {
		t.Fatalf("CurrentReasoningOverride() = %+v, want effort high", got)
	}
	if warnings := warningMessages(events); len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
}

func TestLoadSessionWarnsWhenSavedModelOverridesExplicitFlag(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["saved"] = session.Session{
		ID: "saved", Model: "large-backend", ModelRef: "large", ModelProvider: "local", Lineage: restoreTestLineage(),
	}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents:         output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore:       store,
		Config:             cfg,
		StartupModel:       "small",
		StartupModelSource: ModelSourceFlag,
	})

	if err := s.loadSession(context.Background(), "saved"); err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	warnings := warningMessages(events)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "overrides --model \"small\"") {
		t.Fatalf("warnings = %v, want one --model override warning", warnings)
	}
}

func TestLoadSessionFallsBackWhenSavedProviderHasNoCredentials(t *testing.T) {
	t.Setenv("STEINER_TEST_MISSING_KEY", "")
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["saved"] = session.Session{
		ID: "saved", Model: "remote-backend", ModelRef: "remote-model", ModelProvider: "remote", Lineage: restoreTestLineage(),
	}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents:         output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore:       store,
		Config:             cfg,
		StartupModel:       "small",
		StartupModelSource: ModelSourceProfile,
	})

	if err := s.loadSession(context.Background(), "saved"); err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	if got := s.CurrentModelAlias(); got != "small" {
		t.Fatalf("CurrentModelAlias() = %q, want the startup model small", got)
	}
	warnings := warningMessages(events)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "STEINER_TEST_MISSING_KEY is unset") || !strings.Contains(warnings[0], "startup model \"small\"") {
		t.Fatalf("warnings = %v, want missing-credential fallback warning", warnings)
	}
}

func TestLegacyBackendModelResolution(t *testing.T) {
	tests := []struct {
		name          string
		backend       string
		startup       string
		wantAlias     string
		wantWarning   string
		wantNoWarning bool
	}{
		{name: "unique backend", backend: "large-backend", startup: "small", wantAlias: "large", wantNoWarning: true},
		{name: "ambiguous prefers matching startup alias", backend: "twin", startup: "twin-a", wantAlias: "twin-a", wantNoWarning: true},
		{name: "ambiguous without match falls back", backend: "twin", startup: "small", wantAlias: "small", wantWarning: "matches several configured models"},
		{name: "unknown backend falls back", backend: "gone", startup: "small", wantAlias: "small", wantWarning: "no longer matches a configured model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := restoreTestConfig(t)
			store := newMockSessionStore()
			store.loadedSessions["legacy"] = session.Session{ID: "legacy", Model: tt.backend, Lineage: restoreTestLineage()}
			var events []output.Event
			s := testNewSession(t, Dependencies{
				BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
				SessionStore: store,
				Config:       cfg,
				StartupModel: tt.startup,
			})

			if err := s.loadSession(context.Background(), "legacy"); err != nil {
				t.Fatalf("loadSession() error = %v", err)
			}
			if got := s.CurrentModelAlias(); got != tt.wantAlias {
				t.Fatalf("CurrentModelAlias() = %q, want %q", got, tt.wantAlias)
			}
			warnings := warningMessages(events)
			if tt.wantNoWarning {
				if len(warnings) != 0 {
					t.Fatalf("warnings = %v, want none", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tt.wantWarning) {
				t.Fatalf("warnings = %v, want one containing %q", warnings, tt.wantWarning)
			}
		})
	}
}

func TestSaveSessionPersistsModelIdentityAndReasoning(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: cfg})
	if err := s.handleSwitchModel("large", &provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "medium"}); err != nil {
		t.Fatalf("handleSwitchModel() error = %v", err)
	}
	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession() error = %v", err)
	}
	saved, ok := store.savedSessions[s.SessionID()]
	if !ok {
		t.Fatal("session not saved")
	}
	if saved.ModelRef != "large" || saved.ModelProvider != "local" || saved.Model != "large-backend" {
		t.Fatalf("saved identity = (%q, %q, %q), want (large, local, large-backend)", saved.ModelRef, saved.ModelProvider, saved.Model)
	}
	want := session.ReasoningOverride{Kind: "effort", Effort: "medium"}
	if got := saved.ReasoningOverrides["large"]; got != want {
		t.Fatalf("saved override = %+v, want %+v", got, want)
	}
}

func TestRetiredDriverSaveKeepsModelIdentity(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: cfg})
	meta := runSessionMeta{
		id:                 "retired",
		cacheKey:           "retired",
		modelID:            "large-backend",
		modelRef:           "large",
		modelProvider:      "local",
		reasoningOverrides: map[string]session.ReasoningOverride{"large": {Kind: "effort", Effort: "low"}},
	}
	snap := agent.DriverSnapshot{Lineage: restoreTestLineage()}
	if err := s.writeLineageAs(meta, snap); err != nil {
		t.Fatalf("writeLineageAs() error = %v", err)
	}
	saved := store.savedSessions["retired"]
	if saved.ModelRef != "large" || saved.ModelProvider != "local" || saved.Model != "large-backend" {
		t.Fatalf("saved identity = (%q, %q, %q), want retired driver identity", saved.ModelRef, saved.ModelProvider, saved.Model)
	}
	if got := saved.ReasoningOverrides["large"]; got.Kind != "effort" || got.Effort != "low" {
		t.Fatalf("saved override = %+v, want effort low", got)
	}
}

func TestModelRejectionGuidance(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		fallback string
		want     string
	}{
		{
			name:     "model not found with fallback",
			err:      &provider.HTTPError{StatusCode: 404, Body: `{"error":{"message":"model gpt-x does not exist"}}`},
			fallback: "small",
			want:     `configured compaction fallback "small"`,
		},
		{
			name: "unsupported model without fallback",
			err:  &provider.HTTPError{StatusCode: 400, Body: `{"message":"unsupported model"}`},
			want: "/model",
		},
		{
			name: "unrelated bad request",
			err:  &provider.HTTPError{StatusCode: 400, Body: `{"message":"invalid temperature"}`},
		},
		{
			name: "non-http error",
			err:  errors.New("model not found"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := modelRejectionGuidance("gpt-x", tt.fallback, tt.err)
			if tt.want == "" {
				if got != "" {
					t.Fatalf("modelRejectionGuidance() = %q, want no guidance", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Fatalf("modelRejectionGuidance() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func newRecordingSink(events *[]output.Event, mu *sync.Mutex) output.SinkFunc {
	return func(event output.Event) {
		mu.Lock()
		*events = append(*events, event)
		mu.Unlock()
	}
}

func TestReportedModelRejectionEmitsConversationNotice(t *testing.T) {
	cfg := restoreTestConfig(t)
	cfg.Models.Effective.CompactionFallback = "small"
	var events []output.Event
	var mu sync.Mutex
	finished := make(chan struct{})
	s := testNewSession(t, Dependencies{
		BaseEvents: newRecordingSink(&events, &mu),
		Config:     cfg,
		Runner: newRunExecutorFunc(func(context.Context, []agent.Message) (RunResult, error) {
			defer close(finished)
			return RunResult{}, &agent.ReportedError{Err: &provider.HTTPError{
				StatusCode: 400,
				Body:       `{"error":{"message":"invalid model gpt-x"}}`,
			}}
		}),
	})
	ctx := context.Background()
	if err := s.Handle(ctx, SubmitPrompt{Text: "hello"}); err != nil {
		t.Fatalf("Handle(SubmitPrompt) error = %v", err)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish")
	}
	s.WaitRuns(ctx)

	mu.Lock()
	defer mu.Unlock()
	notices := warningMessages(events)
	if len(notices) != 1 || !strings.Contains(notices[0], "Change it with /model") || !strings.Contains(notices[0], `"small"`) {
		t.Fatalf("conversation notices = %v, want one model rejection notice naming the fallback", notices)
	}
}

func TestSequentialResumesFallBackToStartupModel(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["saved-large"] = session.Session{
		ID: "saved-large", Model: "large-backend", ModelRef: "large", ModelProvider: "local", Lineage: restoreTestLineage(),
	}
	store.loadedSessions["saved-none"] = session.Session{
		ID: "saved-none", Model: "", Lineage: restoreTestLineage(),
	}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore: store,
		Config:       cfg,
		StartupModel: "small",
	})
	if err := s.loadSession(context.Background(), "saved-large"); err != nil {
		t.Fatalf("first loadSession() error = %v", err)
	}
	if got := s.CurrentModelAlias(); got != "large" {
		t.Fatalf("after first resume CurrentModelAlias() = %q, want large", got)
	}
	// Simulate a model switch between resumes: the next resume must still
	// return to the startup model, not this one.
	if err := s.handleSwitchModel("twin-a", nil); err != nil {
		t.Fatalf("handleSwitchModel() error = %v", err)
	}
	if err := s.loadSession(context.Background(), "saved-none"); err != nil {
		t.Fatalf("second loadSession() error = %v", err)
	}
	if got := s.CurrentModelAlias(); got != "small" {
		t.Fatalf("after record without identity CurrentModelAlias() = %q, want startup small", got)
	}
}

func TestResumeFallbackWarningMatchesAppliedModel(t *testing.T) {
	t.Setenv("STEINER_TEST_MISSING_KEY", "")
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["saved"] = session.Session{
		ID: "saved", Model: "remote-backend", ModelRef: "remote-model", ModelProvider: "remote", Lineage: restoreTestLineage(),
	}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore: store,
		Config:       cfg,
		StartupModel: "twin-b",
	})
	if err := s.loadSession(context.Background(), "saved"); err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	if got := s.CurrentModelAlias(); got != "twin-b" {
		t.Fatalf("CurrentModelAlias() = %q, want startup twin-b", got)
	}
	warnings := warningMessages(events)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `startup model "twin-b"`) {
		t.Fatalf("warnings = %v, want the applied startup model named", warnings)
	}
}

func TestResumeReplacesReasoningOverrides(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	store.loadedSessions["with-override"] = session.Session{
		ID: "with-override", Model: "large-backend", ModelRef: "large", ModelProvider: "local",
		ReasoningOverrides: map[string]session.ReasoningOverride{"large": {Kind: "effort", Effort: "high"}},
		Lineage:            restoreTestLineage(),
	}
	store.loadedSessions["without-override"] = session.Session{
		ID: "without-override", Model: "large-backend", ModelRef: "large", ModelProvider: "local",
		Lineage: restoreTestLineage(),
	}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: cfg, StartupModel: "small"})
	if err := s.loadSession(context.Background(), "with-override"); err != nil {
		t.Fatalf("first loadSession() error = %v", err)
	}
	if got := s.CurrentReasoningOverride(); got.Kind != provider.ReasoningOverrideEffort || got.Effort != "high" {
		t.Fatalf("after first resume override = %+v, want effort high", got)
	}
	if err := s.loadSession(context.Background(), "without-override"); err != nil {
		t.Fatalf("second loadSession() error = %v", err)
	}
	if got := s.CurrentReasoningOverride(); got.Kind != "" {
		t.Fatalf("after second resume override = %+v, want none", got)
	}
}

func TestForkCopiesLiveModelIdentityAndReasoning(t *testing.T) {
	cfg := restoreTestConfig(t)
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store, Config: cfg})
	if err := s.handleSwitchModel("twin-b", &provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "low"}); err != nil {
		t.Fatalf("handleSwitchModel() error = %v", err)
	}
	originalID := s.SessionID()
	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() error = %v", err)
	}
	forkID := s.SessionID()
	if forkID == originalID {
		t.Fatal("fork did not change the session id")
	}
	forked, ok := store.savedSessions[forkID]
	if !ok {
		t.Fatalf("fork %q not saved", forkID)
	}
	if forked.ModelRef != "twin-b" || forked.ModelProvider != "local" || forked.Model != "twin" {
		t.Fatalf("fork identity = (%q, %q, %q), want (twin-b, local, twin)", forked.ModelRef, forked.ModelProvider, forked.Model)
	}
	if got := forked.ReasoningOverrides["twin-b"]; got.Kind != "effort" || got.Effort != "low" {
		t.Fatalf("fork override = %+v, want effort low", got)
	}
	// Reloading the fork must resolve the exact alias, not the ambiguous twin.
	reloadable := forked
	reloadable.ID = "fork-under-test"
	store.loadedSessions["fork-under-test"] = reloadable
	reloaded := testNewSession(t, Dependencies{SessionStore: store, Config: cfg, StartupModel: "twin-a"})
	if err := reloaded.loadSession(context.Background(), "fork-under-test"); err != nil {
		t.Fatalf("reload fork error = %v", err)
	}
	if got := reloaded.CurrentModelAlias(); got != "twin-b" {
		t.Fatalf("reloaded fork model = %q, want twin-b", got)
	}
}

func TestLocalModelUsageErrorClaudeSubscriptionAndCodex(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"claude": {Type: config.ProviderTypeClaudeSubscription},
			"codex":  {Type: config.ProviderTypeCodex, APIKeyEnv: "STEINER_TEST_CODEX_KEY"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"claudemodel": {Provider: "claude", ID: "claude-model"},
			"codexmodel":  {Provider: "codex", ID: "codex-model"},
		}},
	}
	t.Setenv("PATH", "")
	if err := localModelUsageError(cfg, "claudemodel", "claude", "claude-model"); err == nil || !strings.Contains(err.Error(), "claude CLI on PATH") {
		t.Fatalf("localModelUsageError(claude_subscription) = %v, want missing CLI error", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("STEINER_TEST_CODEX_KEY", "sk-test")
	if err := localModelUsageError(cfg, "codexmodel", "codex", "codex-model"); err != nil {
		t.Fatalf("localModelUsageError(codex API key) = %v, want nil", err)
	}
}
