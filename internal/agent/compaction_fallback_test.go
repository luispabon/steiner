package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

func TestCompactionCacheCold(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	recorded := LastMainRequest{At: last, ProviderAlias: "openai", BackendModelID: "gpt-test", PromptCacheKey: "cache-key"}
	const after = 5 * time.Minute

	tests := []struct {
		name  string
		now   time.Time
		last  LastMainRequest
		after time.Duration
		want  bool
	}{
		{name: "zero threshold is never cold", now: last.Add(time.Hour), last: recorded, after: 0, want: false},
		{name: "negative threshold is never cold", now: last.Add(time.Hour), last: recorded, after: -time.Second, want: false},
		{name: "non-positive threshold ignores missing request", now: last, last: LastMainRequest{}, after: 0, want: false},
		{name: "missing request is cold", now: last, last: LastMainRequest{}, after: after, want: true},
		{name: "one nanosecond before threshold is warm", now: last.Add(after - time.Nanosecond), last: recorded, after: after, want: false},
		{name: "exactly at threshold is warm", now: last.Add(after), last: recorded, after: after, want: false},
		{name: "one nanosecond past threshold is cold", now: last.Add(after + time.Nanosecond), last: recorded, after: after, want: true},
		{name: "well past threshold is cold", now: last.Add(time.Hour), last: recorded, after: after, want: true},
		{name: "now before the request is warm", now: last.Add(-time.Minute), last: recorded, after: after, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compactionCacheCold(tt.now, tt.last, tt.after); got != tt.want {
				t.Errorf("compactionCacheCold() = %v, want %v", got, tt.want)
			}
		})
	}
}

// fixedCompactionClock reports one fixed time from Now.
type fixedCompactionClock struct {
	realClock
	now time.Time
}

func (c fixedCompactionClock) Now() time.Time { return c.now }

func TestSelectCompactionFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	const after = 5 * time.Minute
	mainModel := provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"}
	resolveErr := errors.New("resolve failed")
	target := CompactionTarget{
		Provider:      &fakeProvider{},
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
	}
	fixed := fixedCompactionClock{now: now}
	cold := LastMainRequest{At: now.Add(-time.Hour)}
	warm := LastMainRequest{At: now.Add(-time.Minute)}

	tests := []struct {
		name       string
		after      time.Duration
		resolve    bool
		target     CompactionTarget
		resolveErr error
		clock      Clock
		last       LastMainRequest
		wantOK     bool
		wantErr    bool
		wantErrIs  error
		wantCalls  int
	}{
		{name: "nil resolve disables", after: after, resolve: false, clock: fixed, last: cold},
		{name: "zero after disables without resolving", after: 0, resolve: true, target: target, clock: fixed, last: cold},
		{name: "negative after disables without resolving", after: -time.Second, resolve: true, target: target, clock: fixed, last: cold},
		{name: "warm cache skips resolution", after: after, resolve: true, target: target, clock: fixed, last: warm},
		{name: "cold cache uses target", after: after, resolve: true, target: target, clock: fixed, last: cold, wantOK: true, wantCalls: 1},
		{name: "missing main request is cold", after: after, resolve: true, target: target, clock: fixed, last: LastMainRequest{}, wantOK: true, wantCalls: 1},
		{name: "nil clock reads real time when warm", after: after, resolve: true, target: target, last: LastMainRequest{At: time.Now()}},
		{name: "nil clock reads real time when cold", after: after, resolve: true, target: target, last: LastMainRequest{At: time.Now().Add(-time.Hour)}, wantOK: true, wantCalls: 1},
		{name: "resolution error is returned", after: after, resolve: true, resolveErr: resolveErr, clock: fixed, last: cold, wantErr: true, wantErrIs: resolveErr, wantCalls: 1},
		{name: "nil target provider is an error", after: after, resolve: true, target: CompactionTarget{ResolvedModel: target.ResolvedModel}, clock: fixed, last: cold, wantErr: true, wantCalls: 1},
		{name: "same alias and backend model is skipped", after: after, resolve: true, target: CompactionTarget{Provider: &fakeProvider{}, ResolvedModel: mainModel}, clock: fixed, last: cold, wantCalls: 1},
		{name: "same backend model under other alias is used", after: after, resolve: true, target: CompactionTarget{Provider: &fakeProvider{}, ResolvedModel: provider.ResolvedModel{ProviderAlias: "other", BackendModelID: "gpt-main"}}, clock: fixed, last: cold, wantOK: true, wantCalls: 1},
		{name: "same alias under other backend model is used", after: after, resolve: true, target: CompactionTarget{Provider: &fakeProvider{}, ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-other"}}, clock: fixed, last: cold, wantOK: true, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			req := RunRequest{ResolvedModel: mainModel, Clock: tt.clock, CompactionFallback: CompactionFallback{After: tt.after}}
			if tt.resolve {
				req.CompactionFallback.Resolve = func(context.Context) (CompactionTarget, error) {
					calls++
					return tt.target, tt.resolveErr
				}
			}

			got, ok, err := selectCompactionFallback(context.Background(), req, tt.last)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("error = %v, want wrapping %v", err, tt.wantErrIs)
			}
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if calls != tt.wantCalls {
				t.Errorf("resolve calls = %d, want %d", calls, tt.wantCalls)
			}
			if !ok && got.Provider != nil {
				t.Error("skipped selection returned a target provider")
			}
			if ok && (got.ResolvedModel.ProviderAlias != tt.target.ResolvedModel.ProviderAlias || got.ResolvedModel.BackendModelID != tt.target.ResolvedModel.BackendModelID) {
				t.Errorf("target = %s/%s, want %s/%s", got.ResolvedModel.ProviderAlias, got.ResolvedModel.BackendModelID, tt.target.ResolvedModel.ProviderAlias, tt.target.ResolvedModel.BackendModelID)
			}
		})
	}
}

func TestCompactionFallbackIdentity(t *testing.T) {
	t.Parallel()
	openai := provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-x"}

	tests := []struct {
		name   string
		main   string
		target provider.ResolvedModel
		want   string
	}{
		{name: "empty main value is empty", main: "", target: openai, want: ""},
		{name: "openai target", main: "main", target: openai, want: "02d4ec6fe06e63903e3ad7236e6348cb"},
		{name: "provider alias changes identity", main: "main", target: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "gpt-x"}, want: "c731f26f5384275775c3d1722d08b9be"},
		{name: "backend model changes identity", main: "main", target: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-y"}, want: "b8f6e469d64aa0aa8561f317b2109f2a"},
		{name: "main value changes identity", main: "other", target: openai, want: "a4cc2182cb8164a845fd7443d9e64f9d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compactionFallbackIdentity(tt.main, tt.target); got != tt.want {
				t.Errorf("compactionFallbackIdentity() = %q, want %q", got, tt.want)
			}
		})
	}
}

// compactionFallbackTestTool returns a tool whose parameters are a nested map.
func compactionFallbackTestTool() provider.ToolSpec {
	return provider.ToolSpec{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Parameters: map[string]any{"type": "object"}}}
}

// compactionFallbackTestTarget returns a target that differs from the main
// request in provider, model, budget, params and compaction prompt.
func compactionFallbackTestTarget() CompactionTarget {
	return CompactionTarget{
		Provider: &fakeProvider{},
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:  "anthropic",
			BackendModelID: "claude-small",
			Params:         map[string]any{"temperature": 0.2},
			ExtraParams:    map[string]any{"top_k": "target"},
			Prompts:        config.ModelPrompts{Compaction: "target compaction"},
		},
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 4000},
	}
}

// compactionFallbackTestRequest returns a main request with mutable prompt
// state, a parent transport session, a cache baseline and a fallback.
func compactionFallbackTestRequest(prov provider.Provider) RunRequest {
	return RunRequest{
		Provider:      prov,
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main", Params: map[string]any{"temperature": 0.7}, ExtraParams: map[string]any{"top_k": "main"}},
		ModelBudget:   prompt.ModelTokenBudget{ContextSize: 8000},
		Tools:         []provider.ToolSpec{compactionFallbackTestTool()},
		Prompt: prompt.AssemblyOptions{
			ModelBudget:               prompt.ModelTokenBudget{ContextSize: 8000},
			PromptOverrides:           config.ModelPrompts{System: "main system", Compaction: "main compaction"},
			Tools:                     []provider.ToolSpec{compactionFallbackTestTool()},
			SkillsRoots:               []string{"skills"},
			SkillNames:                []string{"skill"},
			ProjectContextExtraFiles:  []string{"extra.md"},
			ProjectContextIgnoreFiles: []string{"ignore.md"},
			SandboxWritableMounts:     []string{"/mnt/work"},
		},
		PromptCacheKey:         "main-cache",
		TransportSession:       "main-transport",
		ParentTransportSession: "parent-transport",
		CacheBaseline:          NewCacheBaselineStore(),
		UsageSource:            usagestats.SourceSubAgent,
		CompactionFallback: CompactionFallback{
			After:   time.Minute,
			Ref:     "anthropic/claude-small",
			Resolve: func(context.Context) (CompactionTarget, error) { return CompactionTarget{}, nil },
		},
	}
}

func TestCompactionFallbackRequestReplacesTarget(t *testing.T) {
	t.Parallel()
	req := compactionFallbackTestRequest(&fakeProvider{})
	target := compactionFallbackTestTarget()

	got, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	if got.Provider != target.Provider {
		t.Error("Provider is not the target provider")
	}
	if got.ResolvedModel.ProviderAlias != "anthropic" || got.ResolvedModel.BackendModelID != "claude-small" {
		t.Errorf("ResolvedModel = %s/%s, want anthropic/claude-small", got.ResolvedModel.ProviderAlias, got.ResolvedModel.BackendModelID)
	}
	if got.ModelBudget != target.ModelBudget || got.Prompt.ModelBudget != target.ModelBudget {
		t.Errorf("budgets = %+v and %+v, want %+v", got.ModelBudget, got.Prompt.ModelBudget, target.ModelBudget)
	}
	if got.Prompt.PromptOverrides.Compaction != "target compaction" {
		t.Errorf("compaction prompt = %q, want target compaction", got.Prompt.PromptOverrides.Compaction)
	}
	if got.Prompt.PromptOverrides.System != "main system" {
		t.Errorf("system prompt = %q, want main system", got.Prompt.PromptOverrides.System)
	}
	if got.UsageSource != usagestats.SourceSubAgent {
		t.Errorf("UsageSource = %v, want SourceSubAgent", got.UsageSource)
	}
	if got.ParentTransportSession != "" || got.CacheBaseline != nil {
		t.Errorf("ParentTransportSession = %q, CacheBaseline = %v, want both cleared", got.ParentTransportSession, got.CacheBaseline)
	}
	if got.CompactionFallback.Resolve != nil || got.CompactionFallback.After != 0 {
		t.Error("fallback copy still carries a compaction fallback")
	}
	if !got.compactionFallbackTarget {
		t.Error("fallback copy is not marked as a fallback target")
	}
}

func TestCompactionFallbackRequestIdentities(t *testing.T) {
	t.Parallel()
	target := compactionFallbackTestTarget()

	tests := []struct {
		name          string
		cacheKey      string
		transport     string
		wantCacheKey  string
		wantTransport string
	}{
		{
			name:          "both main values derive identities",
			cacheKey:      "main-cache",
			transport:     "main-transport",
			wantCacheKey:  compactionFallbackIdentity("main-cache", target.ResolvedModel),
			wantTransport: compactionFallbackIdentity("main-transport", target.ResolvedModel),
		},
		{
			name:          "empty cache key stays empty",
			cacheKey:      "",
			transport:     "main-transport",
			wantCacheKey:  "",
			wantTransport: compactionFallbackIdentity("main-transport", target.ResolvedModel),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := compactionFallbackTestRequest(&fakeProvider{})
			req.PromptCacheKey = tt.cacheKey
			req.TransportSession = tt.transport

			got, err := compactionFallbackRequest(req, target)
			if err != nil {
				t.Fatalf("compactionFallbackRequest() error = %v", err)
			}
			if got.PromptCacheKey != tt.wantCacheKey {
				t.Errorf("PromptCacheKey = %q, want %q", got.PromptCacheKey, tt.wantCacheKey)
			}
			if got.TransportSession != tt.wantTransport {
				t.Errorf("TransportSession = %q, want %q", got.TransportSession, tt.wantTransport)
			}
		})
	}
}

func TestCompactionFallbackRequestFreshTransport(t *testing.T) {
	t.Parallel()
	req := compactionFallbackTestRequest(&fakeProvider{})
	req.TransportSession = ""
	target := compactionFallbackTestTarget()

	first, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	second, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	if first.TransportSession == "" || second.TransportSession == "" {
		t.Fatalf("fresh TransportSession is empty: %q and %q", first.TransportSession, second.TransportSession)
	}
	if first.TransportSession == second.TransportSession {
		t.Errorf("TransportSession %q repeats across operations", first.TransportSession)
	}
	if first.TransportSession == compactionFallbackIdentity("", target.ResolvedModel) {
		t.Error("fresh TransportSession is not a fresh key")
	}
}

func TestCompactionFallbackRequestIsolation(t *testing.T) {
	t.Parallel()
	req := compactionFallbackTestRequest(&fakeProvider{})
	target := compactionFallbackTestTarget()

	got, err := compactionFallbackRequest(req, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}

	// Changing the memoized target after the copy is built must not reach the copy.
	target.ResolvedModel.Params["temperature"] = 8.8
	target.ResolvedModel.ExtraParams["top_k"] = "target-mutated"
	if got.ResolvedModel.Params["temperature"] != 0.2 || got.ResolvedModel.ExtraParams["top_k"] != "target" {
		t.Errorf("copy params = %v and %v, want target values", got.ResolvedModel.Params, got.ResolvedModel.ExtraParams)
	}

	// Changing the copy must not reach the main request.
	got.Tools[0].Function.Name = "changed"
	got.Tools[0].Function.Parameters["type"] = "changed"
	got.Prompt.Tools[0].Function.Parameters["type"] = "changed"
	got.Prompt.SkillsRoots[0] = "changed"
	got.Prompt.SkillNames[0] = "changed"
	got.Prompt.ProjectContextExtraFiles[0] = "changed"
	got.Prompt.ProjectContextIgnoreFiles[0] = "changed"
	got.Prompt.SandboxWritableMounts[0] = "changed"
	got.ResolvedModel.Params["temperature"] = 9.9
	got.ResolvedModel.ExtraParams["top_k"] = "changed"

	if req.Tools[0].Function.Name != "read" || req.Tools[0].Function.Parameters["type"] != "object" {
		t.Errorf("main Tools = %+v, want unchanged", req.Tools)
	}
	if req.Prompt.Tools[0].Function.Parameters["type"] != "object" {
		t.Errorf("main Prompt.Tools = %+v, want unchanged", req.Prompt.Tools)
	}
	if req.Prompt.SkillsRoots[0] != "skills" || req.Prompt.SkillNames[0] != "skill" {
		t.Errorf("main skills = %v and %v, want unchanged", req.Prompt.SkillsRoots, req.Prompt.SkillNames)
	}
	if req.Prompt.ProjectContextExtraFiles[0] != "extra.md" || req.Prompt.ProjectContextIgnoreFiles[0] != "ignore.md" || req.Prompt.SandboxWritableMounts[0] != "/mnt/work" {
		t.Error("main project context or sandbox mounts changed")
	}
	if req.ResolvedModel.Params["temperature"] != 0.7 || req.ResolvedModel.ExtraParams["top_k"] != "main" {
		t.Errorf("main params = %v and %v, want unchanged", req.ResolvedModel.Params, req.ResolvedModel.ExtraParams)
	}
}

func TestCompactionFallbackRequestLeavesOriginal(t *testing.T) {
	t.Parallel()
	main := &fakeProvider{}
	req := compactionFallbackTestRequest(main)
	target := compactionFallbackTestTarget()

	if _, err := compactionFallbackRequest(req, target); err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}

	if req.Provider != main {
		t.Error("main Provider changed")
	}
	if req.ResolvedModel.ProviderAlias != "openai" || req.ResolvedModel.BackendModelID != "gpt-main" {
		t.Errorf("main ResolvedModel = %s/%s, want openai/gpt-main", req.ResolvedModel.ProviderAlias, req.ResolvedModel.BackendModelID)
	}
	if req.ModelBudget.ContextSize != 8000 || req.Prompt.ModelBudget.ContextSize != 8000 {
		t.Error("main budgets changed")
	}
	if req.Prompt.PromptOverrides.Compaction != "main compaction" {
		t.Errorf("main compaction prompt = %q, want unchanged", req.Prompt.PromptOverrides.Compaction)
	}
	if req.PromptCacheKey != "main-cache" || req.TransportSession != "main-transport" || req.ParentTransportSession != "parent-transport" {
		t.Error("main cache identities changed")
	}
	if req.CacheBaseline == nil || req.CompactionFallback.Resolve == nil || req.CompactionFallback.After != time.Minute {
		t.Error("main CacheBaseline or CompactionFallback changed")
	}
	if req.compactionFallbackTarget {
		t.Error("main request is marked as a fallback target")
	}
}
