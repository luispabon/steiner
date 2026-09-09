package advisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/provider"
)

type cacheDiagnosticRecord struct {
	Kind    diagnostics.Kind   `json:"kind"`
	Source  diagnostics.Source `json:"source"`
	Payload cachePayload       `json:"payload"`
}

func TestAdvisorCacheDiagnosticsFieldsAndSharedPrefix(t *testing.T) {
	dir := t.TempDir()
	writer, err := diagnostics.New(diagnostics.Options{Dir: dir, Streams: diagnostics.Streams{Cache: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	defer func() { _ = writer.Close() }()
	providerStub := &fakeProvider{response: provider.ChatResponse{
		Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "advice"},
		Usage:   &provider.UsageStats{PromptTokens: 100, CacheReadInputTokens: 70, CacheCreationInputTokens: 10, CompletionTokens: 8},
	}}
	handler := NewHandler(HandlerDeps{Provider: providerStub, Model: provider.ResolvedModel{ProviderAlias: "alias", BackendModelID: "model"}, Config: Config{MaxUsesPerRun: 2}, CacheKey: "opaque-cache-key", Diagnostics: writer})
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{{Role: provider.MessageRoleUser, Content: "context"}})
	for _, question := range []string{"first", "second"} {
		if _, err := handler(ctx, map[string]any{"question": question}); err != nil {
			t.Fatalf("handler() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cache.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("diagnostic lines = %d, want 2", len(lines))
	}
	var records []cacheDiagnosticRecord
	for _, line := range lines {
		var record cacheDiagnosticRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		records = append(records, record)
	}
	first, second := records[0].Payload, records[1].Payload
	if records[0].Kind != diagnostics.KindCache || records[0].Source != diagnostics.SourceAdvisor {
		t.Fatalf("record identity = %#v, want cache/advisor", records[0])
	}
	if first.ProviderAlias != "alias" || first.BackendModelID != "model" || first.PromptTokens != 100 || first.CacheReadTokens != 70 || first.CacheCreateTokens != 10 || first.CompletionTokens != 8 {
		t.Fatalf("metadata = %#v", first)
	}
	if first.CacheKeyHash == "" || first.PrefixHash == "" || first.PrefixMessageCount == 0 {
		t.Fatalf("opaque prefix fields = %#v", first)
	}
	if first.CacheKeyHash == "opaque-cache-key" || strings.Contains(string(data), "opaque-cache-key") {
		t.Fatal("diagnostics exposed raw cache key")
	}
	if second.SharedPrefixMessages != first.PrefixMessageCount {
		t.Fatalf("second shared prefix = %d, want %d", second.SharedPrefixMessages, first.PrefixMessageCount)
	}
}

func TestAdvisorCacheDiagnosticsGatingAndNilWriter(t *testing.T) {
	state := &handlerState{cacheKey: "key"}
	state.emitCacheDiagnostic(nil, provider.ResolvedModel{}, nil, []provider.Message{{Role: provider.MessageRoleUser, Content: "x"}})
	writer, err := diagnostics.New(diagnostics.Options{Dir: filepath.Join(t.TempDir(), "diagnostics"), Streams: diagnostics.Streams{Cache: false}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	if writer != nil {
		t.Fatalf("disabled writer = %#v, want nil", writer)
	}
}

func TestAdvisorCacheDiagnosticsDistinctHandlersDoNotSharePrefix(t *testing.T) {
	dir := t.TempDir()
	w, err := diagnostics.New(diagnostics.Options{Dir: dir, Streams: diagnostics.Streams{Cache: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	a := &handlerState{cacheKey: "same"}
	b := &handlerState{cacheKey: "same"}
	messages := []provider.Message{{Role: provider.MessageRoleSystem, Content: "stable"}, {Role: provider.MessageRoleUser, Content: "suffix"}}
	a.emitCacheDiagnostic(w, provider.ResolvedModel{}, nil, messages)
	b.emitCacheDiagnostic(w, provider.ResolvedModel{}, nil, messages)
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cache.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("diagnostic lines = %d, want 2", len(lines))
	}
	var second cacheDiagnosticRecord
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if second.Payload.SharedPrefixMessages != 0 {
		t.Fatalf("distinct handler shared prefix = %d, want 0", second.Payload.SharedPrefixMessages)
	}
}
