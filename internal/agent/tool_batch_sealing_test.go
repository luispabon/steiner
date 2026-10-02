package agent

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func toolBatchProvider() *fakeProvider {
	call := func(id string) provider.ChatResponse {
		return provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read", Arguments: map[string]any{}}}},
			FinishReason: "tool_calls",
			Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
		}
	}
	return &fakeProvider{responses: []provider.ChatResponse{call("a"), call("b"), completeResponse("done")}}
}

func TestRunnerSealsOnlyWithSealerAndScope(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		useSealer  bool
		wantSealed int
	}{
		{name: "sealer and scope seal every batch", scope: "scope-1", useSealer: true, wantSealed: 2},
		{name: "no scope seals nothing", scope: "", useSealer: true, wantSealed: 0},
		{name: "no sealer does not panic", scope: "scope-1", useSealer: false, wantSealed: 0},
		{name: "neither", wantSealed: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealer := &recordingSealer{}
			req := inboxRunRequest(toolBatchProvider(), nil)
			req.GroupScope = tt.scope
			if tt.useSealer {
				req.Sealer = sealer
			}
			if _, err := NewRunner().Run(context.Background(), req); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(sealer.calls) != tt.wantSealed {
				t.Fatalf("seal calls = %v, want %d", sealer.calls, tt.wantSealed)
			}
			for _, c := range sealer.calls {
				if c.scope != tt.scope {
					t.Fatalf("sealed scope %q, want %q", c.scope, tt.scope)
				}
			}
		})
	}
}

// Two concurrent streams share one sealer but each seals only its own scope.
func TestRunnerConcurrentStreamsSealOnlyTheirOwnScope(t *testing.T) {
	sealer := &recordingSealer{}
	scopes := []string{"scope-a", "scope-b"}
	var wg sync.WaitGroup
	for _, scope := range scopes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := inboxRunRequest(toolBatchProvider(), nil)
			req.Sealer, req.GroupScope = sealer, scope
			if _, err := NewRunner().Run(context.Background(), req); err != nil {
				t.Errorf("Run(%s) error = %v", scope, err)
			}
		}()
	}
	wg.Wait()
	counts := map[string]int{}
	for _, c := range sealer.calls {
		counts[c.scope]++
	}
	for _, scope := range scopes {
		if counts[scope] != 2 {
			t.Fatalf("scope %s sealed %d batches, want 2 (all calls %v)", scope, counts[scope], sealer.calls)
		}
	}
	if len(counts) != len(scopes) || !slices.Equal(slices.Sorted(maps.Keys(counts)), scopes) {
		t.Fatalf("sealed scopes = %v, want exactly %v", counts, scopes)
	}
}
