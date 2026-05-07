package builtin

import (
	"testing"

	"github.com/luispabon/steiner/internal/tool/builtin/patchdoc"
)

func TestNewApplyPatchResultCopiesPathsAndSummarizesChanges(t *testing.T) {
	t.Parallel()

	got := newApplyPatchResult(patchdoc.ApplyResult{
		Added:    []string{"added.txt"},
		Modified: []string{"modified.txt"},
		Deleted:  []string{"deleted.txt"},
		Moved:    []patchdoc.MoveResult{{From: "from.txt", To: "to.txt"}},
	})

	if got == nil {
		t.Fatal("newApplyPatchResult() = nil, want result")
	}
	if got.DryRun {
		t.Fatal("newApplyPatchResult() DryRun = true, want false")
	}
	if got.HunksApplied != 4 {
		t.Fatalf("newApplyPatchResult() HunksApplied = %d, want 4", got.HunksApplied)
	}
	if got.HunksFailed != 0 {
		t.Fatalf("newApplyPatchResult() HunksFailed = %d, want 0", got.HunksFailed)
	}

	wantPaths := []string{"added.txt", "modified.txt", "deleted.txt", "to.txt"}
	if len(got.Paths) != len(wantPaths) {
		t.Fatalf("newApplyPatchResult() Paths = %#v, want %#v", got.Paths, wantPaths)
	}
	for i, want := range wantPaths {
		if got.Paths[i] != want {
			t.Fatalf("newApplyPatchResult() Paths[%d] = %q, want %q", i, got.Paths[i], want)
		}
	}

	if len(got.Added) != 1 || got.Added[0] != "added.txt" {
		t.Fatalf("newApplyPatchResult() Added = %#v, want [added.txt]", got.Added)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "modified.txt" {
		t.Fatalf("newApplyPatchResult() Modified = %#v, want [modified.txt]", got.Modified)
	}
	if len(got.Deleted) != 1 || got.Deleted[0] != "deleted.txt" {
		t.Fatalf("newApplyPatchResult() Deleted = %#v, want [deleted.txt]", got.Deleted)
	}
	if len(got.Moved) != 1 || got.Moved[0].From != "from.txt" || got.Moved[0].To != "to.txt" {
		t.Fatalf("newApplyPatchResult() Moved = %#v, want [{from.txt to.txt}]", got.Moved)
	}

	wantOutput := "Success.\nUpdated the following files:\nA added.txt\nM modified.txt\nD deleted.txt\nR from.txt -> to.txt"
	if got.Output != wantOutput {
		t.Fatalf("newApplyPatchResult() Output = %q, want %q", got.Output, wantOutput)
	}
}

func TestNewApplyPatchResultDryRunSummarizesAsDryRun(t *testing.T) {
	t.Parallel()

	got := newApplyPatchResult(patchdoc.ApplyResult{
		Added:  []string{"added.txt"},
		DryRun: true,
	})

	if got == nil {
		t.Fatal("newApplyPatchResult() = nil, want result")
	}
	if !got.DryRun {
		t.Fatal("newApplyPatchResult() DryRun = false, want true")
	}
	if got.WasMutated() {
		t.Fatal("ApplyPatchResult.WasMutated() = true, want false for dry run")
	}

	wantOutput := "Dry run succeeded.\nUpdated the following files:\nA added.txt"
	if got.Output != wantOutput {
		t.Fatalf("newApplyPatchResult() Output = %q, want %q", got.Output, wantOutput)
	}
}

func TestApplyPatchResultWasMutatedRejectsFailedResult(t *testing.T) {
	t.Parallel()

	got := &ApplyPatchResult{
		Paths:        []string{"failed.txt"},
		HunksApplied: 1,
		HunksFailed:  1,
	}

	if got.WasMutated() {
		t.Fatal("ApplyPatchResult.WasMutated() = true, want false when hunks failed")
	}
}
