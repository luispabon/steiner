package agent

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// releaseCountingFakeProvider counts provider.ConnectionReleaser calls on top
// of the shared fake provider.
type releaseCountingFakeProvider struct {
	*fakeProvider
	releases atomic.Int32
}

func (p *releaseCountingFakeProvider) ReleaseConnection() { p.releases.Add(1) }

func TestRunCompactionOnTargetReleasesTargetProvider(t *testing.T) {
	target := CompactionTarget{
		Provider:      &releaseCountingFakeProvider{fakeProvider: &fakeProvider{}},
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
	}
	prov := target.Provider.(*releaseCountingFakeProvider)

	runCalled := false
	over, err := runCompactionOnTarget(RunRequest{PromptCacheKey: "main-key"}, target, func(summaryReq *RunRequest) (CompactionOutcome, error) {
		runCalled = true
		if summaryReq == nil {
			t.Fatal("summary request is nil, want the fallback request")
		}
		if summaryReq.Provider != target.Provider {
			t.Errorf("summary provider = %T, want the fallback target provider", summaryReq.Provider)
		}
		if got := prov.releases.Load(); got != 0 {
			t.Errorf("releases during the operation = %d, want 0", got)
		}
		return CompactionOutcome{Applied: true}, nil
	})
	if err != nil {
		t.Fatalf("runCompactionOnTarget: %v", err)
	}
	if !runCalled {
		t.Fatal("compaction run was not called")
	}
	if got := prov.releases.Load(); got != 1 {
		t.Errorf("releases = %d, want 1 after the operation", got)
	}
	if !over.Applied {
		t.Errorf("outcome not returned: %+v", over)
	}
}

func TestRunCompactionOnTargetReleasesTargetProviderOnError(t *testing.T) {
	target := CompactionTarget{
		Provider:      &releaseCountingFakeProvider{fakeProvider: &fakeProvider{}},
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
	}
	prov := target.Provider.(*releaseCountingFakeProvider)

	wantErr := errors.New("fallback failed")
	_, err := runCompactionOnTarget(RunRequest{PromptCacheKey: "main-key"}, target, func(*RunRequest) (CompactionOutcome, error) {
		return CompactionOutcome{}, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got := prov.releases.Load(); got != 1 {
		t.Errorf("releases = %d, want 1 after a failed operation", got)
	}
}
