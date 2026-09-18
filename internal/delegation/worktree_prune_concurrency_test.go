package delegation

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestPruneCodeWorktreeConcurrentCallsSerialize drives the public prune helpers
// concurrently. It guards two regressions at once: the prune sweep's public
// helpers must not re-enter worktreeMu (which would deadlock), and concurrent
// prunes must not race on the git metadata store.
func TestPruneCodeWorktreeConcurrentCallsSerialize(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	delegationBase := filepath.Join(repo, ".steiner", "worktrees")
	var relIDs []string
	for i := 0; i < 3; i++ {
		wt, err := ProvisionCodeWorktree(ctx, repo, fmt.Sprintf("agent-%d", i))
		if err != nil {
			t.Fatalf("provision worktree %d: %v", i, err)
		}
		rel, err := filepath.Rel(delegationBase, wt.Path)
		if err != nil {
			t.Fatalf("relative worktree path: %v", err)
		}
		relIDs = append(relIDs, rel)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(relIDs)+2)
	for _, rel := range relIDs {
		wg.Add(1)
		go func(rel string) {
			defer wg.Done()
			if _, err := PruneCodeWorktree(ctx, repo, rel); err != nil {
				errCh <- err
			}
		}(rel)
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := PruneAllCodeWorktrees(ctx, repo); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := PruneProcessCodeWorktrees(ctx, repo); err != nil {
			errCh <- err
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent prune calls deadlocked")
	}
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent prune: %v", err)
	}
}
