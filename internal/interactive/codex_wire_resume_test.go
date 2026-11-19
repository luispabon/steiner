package interactive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// codexResumeProvider records the requests the agent actually builds. It never
// talks to a network: the Codex wire below is serialized by the provider's own
// fingerprint path, which does not send.
type codexResumeProvider struct {
	requests []provider.ChatRequest
}

func (p *codexResumeProvider) ChatCompletion(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.requests = append(p.requests, req)
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "ok"},
		FinishReason: "stop",
	}, nil
}

func (p *codexResumeProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("the Codex prefix regression does not stream")
}

func (p *codexResumeProvider) SupportsUsageStats() bool { return true }

// codexResumeExecutor satisfies the runner without executing tool calls: the
// scripted response finishes on the first turn.
type codexResumeExecutor struct{}

func (codexResumeExecutor) Execute(context.Context, string, string, map[string]any) (any, error) {
	return nil, nil
}

func codexResumeTools() []provider.ToolSpec {
	return []provider.ToolSpec{
		{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        "read",
				Description: "Read a file from the project.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
					"required":   []any{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        "bash",
				Description: "Run a shell command.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"command": map[string]any{"type": "string"}},
					"required":   []any{"command"},
				},
			},
		},
	}
}

func codexResumeConfig() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex, BaseURL: "https://codex.example.test/v1"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"codex-mini": {Provider: "codex", ID: "gpt-5.4-mini"},
			},
			Effective: config.EffectiveModelAssignments{
				DefaultModel:            "codex-mini",
				ActiveOrchestratorModel: "codex-mini",
			},
		},
	}
}

// codexResumeRequest runs the real agent turn loop over conversation, so the
// request is assembled by prompt.Assemble and built by prepareTurn rather than
// reconstructed by the test. source is the agent-facing message list, which
// keeps internal roles (such as compaction summaries) intact.
func codexResumeRequest(t *testing.T, homeDir, projectRoot string, source []agent.Message, date prompt.SessionDate, cacheKey string) provider.ChatRequest {
	t.Helper()
	prov := &codexResumeProvider{}
	run := agent.RunRequest{
		Provider:           prov,
		Executor:           codexResumeExecutor{},
		Tools:              codexResumeTools(),
		SourceConversation: source,
		Prompt: prompt.AssemblyOptions{
			HomeDir:     homeDir,
			ProjectRoot: projectRoot,
			SessionDate: date,
		},
		ResolvedModel: provider.ResolvedModel{
			Alias:                 "codex-mini",
			ProviderAlias:         "codex",
			BackendModelID:        "gpt-5.4-mini",
			EffectiveProviderType: config.ProviderTypeCodex,
			EffectiveTransport:    provider.TransportConfigured,
		},
		PromptCacheKey:     cacheKey,
		Events:             output.NoopSink{},
		StreamingPreferred: false,
	}
	if _, err := agent.NewRunner().Run(context.Background(), run); err != nil {
		t.Fatalf("run request: %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider received %d requests, want 1", len(prov.requests))
	}
	return prov.requests[0]
}

// codexResumeInstructions mirrors the Responses wire: system messages are
// trimmed and joined into the request's instructions field.
func codexResumeInstructions(request provider.ChatRequest) string {
	var parts []string
	for _, msg := range request.Messages {
		if msg.Role == provider.MessageRoleSystem {
			if text := strings.TrimSpace(msg.Content); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// codexResumePrefixText joins the content of the messages before the newly
// added user turn. The session date is user-scoped context, so it lives in the
// input prefix rather than the Codex instructions field.
func codexResumePrefixText(messages []provider.Message) string {
	var parts []string
	for _, msg := range messages {
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n\n")
}

func codexResumeSnippet(text string) string {
	const limit = 200
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func codexResumeFingerprint(t *testing.T, client *provider.Client, request provider.ChatRequest) provider.WireCacheDiagnostics {
	t.Helper()
	cacheable := len(request.Messages) - 1
	if cacheable < 0 {
		cacheable = 0
	}
	fingerprint, err := client.CacheFingerprint(context.Background(), request, false, cacheable, 0)
	if err != nil {
		t.Fatalf("CacheFingerprint() error = %v", err)
	}
	return fingerprint
}

// TestCodexWirePrefixStableAcrossOvernightResume captures the Codex request a
// normal turn would send, persists the session, restarts on the next day in a
// different zone, resumes, and adds another user turn. The serialized prefix
// up to that new turn, including the shared instructions, the user-scoped date
// line, tools, and cache affinity headers, must be unchanged. The test fails if
// the resume recaptures the date: the date line and the Codex wire hashes then
// move to day two.
func TestCodexWirePrefixStableAcrossOvernightResume(t *testing.T) {
	dayOne := time.Date(2026, 9, 13, 23, 55, 0, 0, time.FixedZone("BST", 3600))
	dayTwo := time.Date(2026, 9, 14, 8, 5, 0, 0, time.FixedZone("CEST", 2*3600))
	ctx := context.Background()

	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	homeDir := t.TempDir()
	projectRoot := t.TempDir()

	history := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first prompt"},
		{Role: agent.MessageRoleAssistant, Content: "first reply"},
	}
	lineage := agent.ConversationLineage{
		Generations:      []agent.ConversationGeneration{{ID: 1, Messages: cloneMessages(history)}},
		NextGenerationID: 2,
	}

	// Day one: the last request of the night carries the history plus the
	// prompt that is being answered.
	first := testNewSession(t, Dependencies{SessionStore: store, Config: codexResumeConfig()})
	first.mu.Lock()
	first.now = func() time.Time { return dayOne }
	first.sessionDate = prompt.NewSessionDate(dayOne)
	first.mu.Unlock()
	seedConversation(first, history, lineage)

	beforeSource := append(cloneMessages(history), agent.Message{Role: agent.MessageRoleUser, Content: "what changed overnight?"})
	before := codexResumeRequest(t, homeDir, projectRoot, beforeSource, first.SessionDate(), first.PromptCacheKey())
	if err := first.saveSession(); err != nil {
		t.Fatalf("saveSession() error = %v", err)
	}

	// Next morning: a fresh process resumes the same on-disk record with a
	// different zone and day, then appends a new user turn.
	resumed := testNewSession(t, Dependencies{SessionStore: store, Config: codexResumeConfig()})
	resumed.mu.Lock()
	resumed.now = func() time.Time { return dayTwo }
	resumed.mu.Unlock()
	if err := resumed.LoadSessionByID(ctx, first.SessionID()); err != nil {
		t.Fatalf("LoadSessionByID() error = %v", err)
	}

	date, zone, offset := resumed.SessionDate().Parts()
	if date != "2026-09-13" || zone != "BST" || offset != "+01:00" {
		t.Fatalf("resumed date = (%q, %q, %q), want the day-one (2026-09-13, BST, +01:00)", date, zone, offset)
	}
	if got := resumed.Conversation(); !reflect.DeepEqual(got, history) {
		t.Fatalf("resumed conversation = %#v, want %#v", got, history)
	}

	afterSource := append(cloneMessages(history), agent.Message{Role: agent.MessageRoleUser, Content: "second prompt after restart"})
	after := codexResumeRequest(t, homeDir, projectRoot, afterSource, resumed.SessionDate(), resumed.PromptCacheKey())

	beforeInstructions := codexResumeInstructions(before)
	afterInstructions := codexResumeInstructions(after)
	if afterInstructions == "" {
		t.Fatal("resumed request carries no Codex instructions")
	}
	if afterInstructions != beforeInstructions {
		t.Fatalf("shared instructions changed across the restart:\nbefore=%s\nafter=%s", codexResumeSnippet(beforeInstructions), codexResumeSnippet(afterInstructions))
	}

	// The only difference between the two requests is the newly added user
	// turn, so everything before it must be byte-identical, including the
	// user-scoped session date line.
	if len(before.Messages) == 0 || len(after.Messages) != len(before.Messages) {
		t.Fatalf("message counts = %d before, %d after; want equal non-zero counts", len(before.Messages), len(after.Messages))
	}
	beforePrefix := before.Messages[:len(before.Messages)-1]
	afterPrefix := after.Messages[:len(after.Messages)-1]
	if !reflect.DeepEqual(beforePrefix, afterPrefix) {
		t.Fatalf("input prefix changed across the restart:\nbefore=%#v\nafter=%#v", beforePrefix, afterPrefix)
	}
	prefixText := codexResumePrefixText(afterPrefix)
	if !strings.Contains(prefixText, "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started.") {
		t.Fatalf("resumed prefix does not carry the captured date line:\n%s", codexResumeSnippet(prefixText))
	}
	if strings.Contains(prefixText, "2026-09-14") || strings.Contains(prefixText, "CEST") {
		t.Fatalf("resumed prefix recaptured the resume-day date:\n%s", codexResumeSnippet(prefixText))
	}
	if !reflect.DeepEqual(before.Tools, after.Tools) {
		t.Fatalf("tool definitions changed across the restart")
	}
	if len(after.Tools) == 0 {
		t.Fatal("request carries no tools")
	}
	if before.Model != "gpt-5.4-mini" || after.Model != "gpt-5.4-mini" {
		t.Fatalf("model ids = %q before, %q after; want gpt-5.4-mini", before.Model, after.Model)
	}
	if before.PromptCacheKey == "" || after.PromptCacheKey != before.PromptCacheKey {
		t.Fatalf("prompt cache keys = %q before, %q after; want the same non-empty key", before.PromptCacheKey, after.PromptCacheKey)
	}

	// Serialize both requests through the real Codex Responses wire. The
	// fingerprint hashes the payload and the affinity headers without sending.
	client, err := provider.NewCodexResponses(provider.ClientConfig{
		BaseURL: "https://codex.example.test/v1",
		APIKey:  "test-key",
		Model:   "gpt-5.4-mini",
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}
	beforeWire := codexResumeFingerprint(t, client, before)
	afterWire := codexResumeFingerprint(t, client, after)

	if beforeWire.CacheablePrefixHash != afterWire.CacheablePrefixHash {
		t.Fatalf("serialized Codex prefix changed across the restart: %q vs %q", beforeWire.CacheablePrefixHash, afterWire.CacheablePrefixHash)
	}
	if !afterWire.AffinityKeysMatch {
		t.Fatalf("affinity diagnostics = %#v, want body key, session-id, and thread-id to match", afterWire)
	}
	if afterWire.BodyKeyHash != beforeWire.BodyKeyHash ||
		afterWire.SessionIDHash != beforeWire.SessionIDHash ||
		afterWire.ThreadIDHash != beforeWire.ThreadIDHash ||
		afterWire.OriginatorHash != beforeWire.OriginatorHash {
		t.Fatalf("cache affinity headers changed across the restart:\nbefore=%#v\nafter=%#v", beforeWire, afterWire)
	}
	if afterWire.SessionIDHash == "" || afterWire.ThreadIDHash == "" || afterWire.OriginatorHash == "" {
		t.Fatalf("affinity header hashes = %#v, want non-empty hashes", afterWire)
	}
	if !afterWire.HasTools || afterWire.ToolCount != len(after.Tools) {
		t.Fatalf("wire tools = %t/%d, want %d tools", afterWire.HasTools, afterWire.ToolCount, len(after.Tools))
	}

	// Sensitivity control: the hash covers the instructions and the prefix.
	mutated := after
	mutated.Messages = provider.CloneMessages(after.Messages)
	mutated.Messages[0].Content += " changed"
	if codexResumeFingerprint(t, client, mutated).CacheablePrefixHash == afterWire.CacheablePrefixHash {
		t.Fatal("serialized prefix hash does not change when the instructions change")
	}

	// Negative control for the regression itself: a resume that recaptured the
	// second day's date would not share the day-one Codex prefix.
	recaptured := codexResumeRequest(t, homeDir, projectRoot, afterSource, prompt.NewSessionDate(dayTwo), resumed.PromptCacheKey())
	if codexResumeFingerprint(t, client, recaptured).CacheablePrefixHash == beforeWire.CacheablePrefixHash {
		t.Fatal("serialized prefix hash does not detect a recaptured date")
	}
}
