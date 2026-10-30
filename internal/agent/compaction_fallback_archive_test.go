package agent_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// archiveSessionID is the session the real history archive writes to.
const archiveSessionID = "archive-discard"

// archiveProvider answers chat calls in order from responses. From call number
// failFrom on (1-based, zero never fails) it returns an error instead.
type archiveProvider struct {
	calls     int
	failFrom  int
	responses []provider.ChatResponse
}

func (p *archiveProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	p.calls++
	if p.failFrom > 0 && p.calls >= p.failFrom {
		return provider.ChatResponse{}, errors.New("target emergency failed")
	}
	if p.calls > len(p.responses) {
		return provider.ChatResponse{}, errors.New("no response configured")
	}
	return p.responses[p.calls-1], nil
}

func (p *archiveProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("stream not used")
}

func (p *archiveProvider) SupportsUsageStats() bool { return true }

// archiveBudget returns the token budget of the archive fixtures at contextSize.
func archiveBudget(contextSize int) prompt.ModelTokenBudget {
	return prompt.ModelTokenBudget{
		ContextSize:               contextSize,
		MaxCompletionTokens:       128,
		SummaryMaxTokens:          64,
		NormalSummaryMaxTokens:    64,
		EmergencySummaryMaxTokens: 16,
	}
}

// archiveMessages is four turns. The retained window keeps the last turns, so
// the compaction archives the earlier ones.
func archiveMessages() []agent.Message {
	var msgs []agent.Message
	for turn := 1; turn <= 4; turn++ {
		name := fmt.Sprintf("turn %d", turn)
		msgs = append(msgs,
			agent.Message{Role: agent.MessageRoleUser, Content: strings.Repeat(name+" user detail ", 4)},
			agent.Message{Role: agent.MessageRoleAssistant, Content: strings.Repeat(name+" assistant detail ", 4)},
		)
	}
	return msgs
}

// compactForArchive runs one compaction of archiveMessages with the real session
// history archive and returns the archive file contents.
func compactForArchive(t *testing.T, main provider.Provider, fallback agent.CompactionFallback) string {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	req := agent.RunRequest{
		Provider:           main,
		ResolvedModel:      provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
		ModelBudget:        archiveBudget(6000),
		Events:             output.SinkFunc(func(output.Event) {}),
		HistoryArchive:     session.NewHistoryArchive(store, func() string { return archiveSessionID }),
		CompactionFallback: fallback,
	}
	_, _, applied, err := agent.NewRunner().CompactWithLineage(context.Background(), req, agent.ConversationLineage{}, archiveMessages())
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	path, err := store.HistoryPath(archiveSessionID)
	if err != nil {
		t.Fatalf("HistoryPath() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	return string(data)
}

// TestCompactionFallbackDiscardLeavesOnlyMainArchiveWrites checks that a fallback
// attempt failing after its normal stage writes nothing to the real session
// archive. The archive must match a run without fallback, so the discarded
// target stage leaves no rows behind.
func TestCompactionFallbackDiscardLeavesOnlyMainArchiveWrites(t *testing.T) {
	t.Parallel()
	normal := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: strings.Repeat("normal summary detail ", 1200)},
		FinishReason: "stop",
	}
	emergency := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "emergency handoff summary"},
		FinishReason: "stop",
	}
	mainResponses := []provider.ChatResponse{normal, emergency}

	want := compactForArchive(t, &archiveProvider{responses: mainResponses}, agent.CompactionFallback{})
	if !strings.Contains(want, "turn 1 user detail") {
		t.Fatalf("disabled archive = %q, want the archived first turn", want)
	}

	target := &archiveProvider{responses: []provider.ChatResponse{normal}, failFrom: 2}
	main := &archiveProvider{responses: mainResponses}
	got := compactForArchive(t, main, agent.CompactionFallback{
		After: 5 * time.Minute,
		Ref:   "anthropic/claude-small",
		Resolve: func(context.Context) (agent.CompactionTarget, error) {
			return agent.CompactionTarget{
				Provider:      target,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
				ModelBudget:   archiveBudget(100000),
			}, nil
		},
	})
	if target.calls != 2 {
		t.Errorf("target calls = %d, want 2 (normal stage applied, emergency stage failed)", target.calls)
	}
	if main.calls != 2 {
		t.Errorf("main calls = %d, want 2 (rerun on main)", main.calls)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("archive after discarded fallback = %q, want the main-only archive %q", got, want)
	}
}
