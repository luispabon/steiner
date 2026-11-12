package advisor

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// releaseCountingProvider records provider.ConnectionReleaser calls on top of
// the shared fake provider.
type releaseCountingProvider struct {
	*fakeProvider
	releases int
}

func (p *releaseCountingProvider) ReleaseConnection() { p.releases++ }

func TestAdviseWithMessagesReleasesProvider(t *testing.T) {
	tests := []struct {
		name      string
		fake      *fakeProvider
		reasoning bool
	}{
		{
			name: "chat completion",
			fake: &fakeProvider{response: provider.ChatResponse{Message: provider.Message{Content: "guidance"}}},
		},
		{
			name: "chat error",
			fake: &fakeProvider{err: errors.New("backend failed")},
		},
		{
			name:      "reasoning stream",
			reasoning: true,
			fake: &fakeProvider{streamChunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "guidance"}},
				{Done: true, Delta: provider.Message{Content: "guidance"}, FinishReason: "stop"},
			}},
		},
		{
			name:      "stream setup error",
			reasoning: true,
			fake:      &fakeProvider{streamErr: errors.New("stream broke")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &releaseCountingProvider{fakeProvider: tt.fake}
			rm := provider.ResolvedModel{BackendModelID: "advisor-model"}
			if tt.reasoning {
				rm.ReasoningEffectiveEffort = "high"
			}

			_, _ = adviseWithMessages(context.Background(), prov, rm, buildMessages(nil, "", nil), nil, nil, "", nil, "", "")

			if prov.releases != 1 {
				t.Errorf("releases = %d, want 1 after the advisor call", prov.releases)
			}
		})
	}
}
