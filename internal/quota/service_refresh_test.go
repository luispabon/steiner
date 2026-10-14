package quota

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRefreshAllRunsFetchesConcurrently(t *testing.T) {
	svc, _ := newTestService(t, nil)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()

	// Both fetches block until release closes, so both can only start while
	// each other is still running if RefreshAll runs them concurrently.
	fa := &fakeFetcher{snap: sampleSnapshot("a", testBase, 1), started: make(chan struct{}, 1), block: release}
	fb := &fakeFetcher{snap: sampleSnapshot("b", testBase, 2), started: make(chan struct{}, 1), block: release}
	svc.Register(Account{Key: "a", Aliases: []string{"a"}, Fetcher: fa})
	svc.Register(Account{Key: "b", Aliases: []string{"b"}, Fetcher: fb})

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RefreshAll(context.Background())
	}()

	for name, f := range map[string]*fakeFetcher{"a": fa, "b": fb} {
		select {
		case <-f.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("fetch %s did not start while the other fetch was blocked: RefreshAll is sequential", name)
		}
	}
	unblock()
	<-done

	for name, f := range map[string]*fakeFetcher{"a": fa, "b": fb} {
		if got := f.callCount(); got != 1 {
			t.Errorf("fetch %s calls = %d, want 1", name, got)
		}
	}
	for _, v := range svc.Views("") {
		if v.Latest == nil || v.Latest.Source != SourceFetch {
			t.Errorf("account %s not stored after RefreshAll returned: %+v", v.Key, v.Latest)
		}
	}
}
