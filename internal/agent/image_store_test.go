package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func TestImageStoreRegisterAssignsSequentialIDs(t *testing.T) {
	store := NewImageStore(t.TempDir())
	first := store.Register("/tmp/one.png", "image/png", 10, 20, 30)
	second := store.Register("/tmp/two.jpg", "image/jpeg", 40, 50, 60)

	if first.ID != "img-1" {
		t.Errorf("first ID = %q, want img-1", first.ID)
	}
	if second.ID != "img-2" {
		t.Errorf("second ID = %q, want img-2", second.ID)
	}
	all := store.All()
	if len(all) != 2 || all[0].ID != "img-1" || all[1].ID != "img-2" {
		t.Fatalf("All() = %#v, want img-1 then img-2", all)
	}
	if got := store.Dir(); got == "" {
		t.Error("Dir() is empty for an unbound store")
	}
}

func TestImageStoreRegisterEmptyPathStillAssignsID(t *testing.T) {
	store := NewImageStore(t.TempDir())
	ref := store.Register("", "image/png", 1, 1, 1)
	if ref.ID != "img-1" {
		t.Fatalf("ID = %q, want img-1", ref.ID)
	}
	if _, ok := store.Get(ref.ID); !ok {
		t.Errorf("Get(%q) = not found, want found", ref.ID)
	}
}

func TestImageStoreIDsResumeFromIndex(t *testing.T) {
	root := t.TempDir()

	first := NewImageStore(root)
	if err := first.BindSession("sess", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	ref1 := first.Register(filepath.Join(first.Dir(), "one.png"), "image/png", 1, 1, 1)
	ref2 := first.Register(filepath.Join(first.Dir(), "two.png"), "image/png", 1, 1, 1)
	if ref1.ID != "img-1" || ref2.ID != "img-2" {
		t.Fatalf("IDs = %q, %q, want img-1, img-2", ref1.ID, ref2.ID)
	}

	second := NewImageStore(root)
	if err := second.BindSession("sess", 1); err != nil {
		t.Fatalf("re-bind: %v", err)
	}
	got, ok := second.Get("img-1")
	if !ok {
		t.Fatal("Get(img-1) after rebind = not found")
	}
	if got.FilePath != ref1.FilePath {
		t.Errorf("resumed FilePath = %q, want %q", got.FilePath, ref1.FilePath)
	}
	third := second.Register(filepath.Join(second.Dir(), "three.png"), "image/png", 1, 1, 1)
	if third.ID != "img-3" {
		t.Errorf("third ID = %q, want img-3 (counter resumes from index)", third.ID)
	}
}

func TestImageStoreGetIsScopedToCurrentBinding(t *testing.T) {
	root := t.TempDir()
	store := NewImageStore(root)
	if err := store.BindSession("one", 1); err != nil {
		t.Fatalf("bind one: %v", err)
	}
	store.Register(filepath.Join(store.Dir(), "a.png"), "image/png", 1, 1, 1)

	if err := store.BindSession("two", 1); err != nil {
		t.Fatalf("bind two: %v", err)
	}
	if _, ok := store.Get("img-1"); ok {
		t.Error("Get(img-1) = found after binding another session, want not found")
	}
	if len(store.All()) != 0 {
		t.Errorf("All() = %d refs after binding another session, want 0", len(store.All()))
	}
}

func TestImageStoreRemove(t *testing.T) {
	root := t.TempDir()
	outOfFolder := filepath.Join(t.TempDir(), "read-source.png")
	mustWrite(t, outOfFolder, "data")

	store := NewImageStore(root)
	if err := store.BindSession("sess", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	inFolder := filepath.Join(store.Dir(), "pasted.png")
	mustWrite(t, inFolder, "data")
	inRef := store.Register(inFolder, "image/png", 1, 1, 4)
	outRef := store.Register(outOfFolder, "image/png", 1, 1, 4)

	store.Remove(inRef.ID)
	if _, err := os.Stat(inFolder); !os.IsNotExist(err) {
		t.Errorf("in-folder file still exists after Remove: %v", err)
	}

	store.Remove(outRef.ID)
	if _, err := os.Stat(outOfFolder); err != nil {
		t.Errorf("out-of-folder file removed by Remove: %v", err)
	}

	store.Remove("img-999")
	if len(store.All()) != 0 {
		t.Errorf("All() = %d after removals, want 0", len(store.All()))
	}
}

func TestImageStoreBindSessionLazyFolder(t *testing.T) {
	root := t.TempDir()
	store := NewImageStore(root)
	if err := store.BindSession("sess", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sess")); !os.IsNotExist(err) {
		t.Fatalf("BindSession created the folder: %v", err)
	}

	store.Register(filepath.Join(store.Dir(), "a.png"), "image/png", 1, 1, 1)
	if _, err := os.Stat(filepath.Join(root, "sess")); err != nil {
		t.Errorf("Register did not create the folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sess", imageIndexFilename)); err != nil {
		t.Errorf("Register did not write the index: %v", err)
	}
}

func TestImageStoreBindAndCleanupLeavesNoFolders(t *testing.T) {
	root := t.TempDir()
	store := NewImageStore(root)
	if err := store.BindSession("sess", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := store.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("root has %d entries after bind+cleanup, want 0: %v", len(entries), entries)
	}
}

func TestImageStoreCleanupRemovesEphemeralOnly(t *testing.T) {
	root := t.TempDir()

	unbound := NewImageStore(root)
	mustMkdir(t, unbound.Dir())
	mustMkdir(t, filepath.Join(root, "session-keep"))
	if err := unbound.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(unbound.Dir()); !os.IsNotExist(err) {
		t.Errorf("ephemeral folder still exists after Cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "session-keep")); err != nil {
		t.Errorf("session folder removed by Cleanup: %v", err)
	}

	bound := NewImageStore(root)
	if err := bound.BindSession("sess", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	mustMkdir(t, bound.Dir())
	if err := bound.Cleanup(); err != nil {
		t.Fatalf("Cleanup bound: %v", err)
	}
	if _, err := os.Stat(bound.Dir()); err != nil {
		t.Errorf("session folder removed by Cleanup: %v", err)
	}
}

func TestImageStorePrunesStaleEntries(t *testing.T) {
	root := t.TempDir()
	staleDir := filepath.Join(root, "stale-session")
	staleFile := filepath.Join(root, "legacy.png")
	freshDir := filepath.Join(root, "fresh-session")
	freshFile := filepath.Join(root, "fresh.png")
	mustMkdir(t, staleDir)
	mustWrite(t, staleFile, "x")
	mustMkdir(t, freshDir)
	mustWrite(t, freshFile, "x")

	old := time.Now().Add(-31 * 24 * time.Hour)
	for _, path := range []string{staleDir, staleFile} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("Chtimes %s: %v", path, err)
		}
	}

	store := NewImageStore(root)

	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("stale dir not pruned: %v", err)
	}
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Errorf("stale loose file not pruned: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("fresh dir pruned: %v", err)
	}
	if _, err := os.Stat(freshFile); err != nil {
		t.Errorf("fresh loose file pruned: %v", err)
	}
	if err := store.BindSession("fresh-session", 1); err != nil {
		t.Errorf("BindSession after prune: %v", err)
	}
}

func TestImageStoreBindSessionRejectsUnsafeIDs(t *testing.T) {
	store := NewImageStore(t.TempDir())
	for _, id := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := store.BindSession(id, 1); err == nil {
			t.Errorf("BindSession(%q) = nil, want error", id)
		}
	}
}

func TestImageStoreCounterFloor(t *testing.T) {
	tests := []struct {
		name    string
		index   *imageIndex
		minNext int
		wantID  string
	}{
		{
			name:    "pre-upgrade session uses minNext",
			minNext: 8,
			wantID:  "img-8",
		},
		{
			name:    "index next below minNext uses minNext",
			index:   &imageIndex{Next: 3},
			minNext: 8,
			wantID:  "img-8",
		},
		{
			name:    "index next above minNext wins",
			index:   &imageIndex{Next: 10},
			minNext: 8,
			wantID:  "img-10",
		},
		{
			name:    "highest indexed ID wins over stale next",
			index:   &imageIndex{Next: 1, Images: []ImageRef{{ID: "img-5"}}},
			minNext: 1,
			wantID:  "img-6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.index != nil {
				data, err := json.Marshal(tt.index)
				if err != nil {
					t.Fatalf("marshal index: %v", err)
				}
				mustWrite(t, filepath.Join(root, "sess", imageIndexFilename), string(data))
			}
			store := NewImageStore(root)
			if err := store.BindSession("sess", tt.minNext); err != nil {
				t.Fatalf("BindSession: %v", err)
			}
			if got := store.Register("", "image/png", 1, 1, 1).ID; got != tt.wantID {
				t.Errorf("Register ID = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestImageStoreCorruptIndexKeepsBinding(t *testing.T) {
	root := t.TempDir()
	store := NewImageStore(root)
	if err := store.BindSession("good", 1); err != nil {
		t.Fatalf("bind good: %v", err)
	}
	store.Register("", "image/png", 1, 1, 1)

	mustWrite(t, filepath.Join(root, "broken", imageIndexFilename), "not json")
	if err := store.BindSession("broken", 1); err == nil {
		t.Fatal("BindSession with corrupt index = nil, want error")
	}
	if _, ok := store.Get("img-1"); !ok {
		t.Error("previous binding refs dropped after corrupt-index error")
	}
	if got := store.Register("", "image/png", 1, 1, 1).ID; got != "img-2" {
		t.Errorf("Register after failed bind = %q, want img-2 (previous binding retained)", got)
	}
}

func TestImageStoreCopySession(t *testing.T) {
	root := t.TempDir()
	fromDir := filepath.Join(root, "from")
	mustWrite(t, filepath.Join(fromDir, "img-1.png"), "data")
	idx := imageIndex{Next: 2, Images: []ImageRef{{ID: "img-1", FilePath: filepath.Join(fromDir, "img-1.png")}}}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal index: %v", err)
	}
	mustWrite(t, filepath.Join(fromDir, imageIndexFilename), string(data))

	store := NewImageStore(root)
	if err := store.CopySession("from", "to"); err != nil {
		t.Fatalf("CopySession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "to", "img-1.png")); err != nil {
		t.Errorf("copied file missing: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(root, "to", imageIndexFilename))
	if err != nil {
		t.Fatalf("read copied index: %v", err)
	}
	var gotIdx imageIndex
	if err := json.Unmarshal(copied, &gotIdx); err != nil {
		t.Fatalf("unmarshal copied index: %v", err)
	}
	wantPath := filepath.Join(root, "to", "img-1.png")
	if len(gotIdx.Images) != 1 || gotIdx.Images[0].FilePath != wantPath {
		t.Errorf("copied index paths = %#v, want %q", gotIdx.Images, wantPath)
	}
}

func TestImageStoreCopySessionMissingSourceIsNoop(t *testing.T) {
	root := t.TempDir()
	store := NewImageStore(root)
	if err := store.CopySession("missing", "to"); err != nil {
		t.Fatalf("CopySession missing source = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(root, "to")); !os.IsNotExist(err) {
		t.Errorf("destination created for missing source: %v", err)
	}
}

func TestImageStoreCopySessionRejectsNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "from", "a.png"), "data")
	mustWrite(t, filepath.Join(root, "to", "b.png"), "data")

	store := NewImageStore(root)
	if err := store.CopySession("from", "to"); err == nil {
		t.Fatal("CopySession onto non-empty destination = nil, want error")
	}
}

func TestNextImageIDFloor(t *testing.T) {
	tests := []struct {
		name    string
		lineage ConversationLineage
		want    int
	}{
		{
			name:    "empty lineage",
			lineage: ConversationLineage{},
			want:    1,
		},
		{
			name: "placeholder reference",
			lineage: ConversationLineage{Generations: []ConversationGeneration{{ID: 1, Messages: []Message{
				{Role: MessageRoleUser, Content: "see [image img-7: /x.png 8x8 png 1KB]"},
			}}}},
			want: 8,
		},
		{
			name: "composer marker reference",
			lineage: ConversationLineage{Generations: []ConversationGeneration{{ID: 1, Messages: []Message{
				{Role: MessageRoleUser, Content: "see [img-4] before submit"},
			}}}},
			want: 5,
		},
		{
			name: "summary prefix and earlier generations counted",
			lineage: ConversationLineage{Generations: []ConversationGeneration{
				{ID: 1, SummaryPrefix: []Message{{Role: MessageRoleSummary, Content: "saw [img-3] earlier"}}},
				{ID: 2, Messages: []Message{{Role: MessageRoleUser, Content: "[image img-5: /y.png 8x8 png 1KB]"}}},
			}},
			want: 6,
		},
		{
			name: "file path img-N ignored",
			lineage: ConversationLineage{Generations: []ConversationGeneration{{ID: 1, Messages: []Message{
				{Role: MessageRoleUser, Content: "read /shots/img-20240101.png and /tmp/img-9.png"},
			}}}},
			want: 1,
		},
		{
			name: "non-matching text ignored",
			lineage: ConversationLineage{Generations: []ConversationGeneration{{ID: 1, Messages: []Message{
				{Role: MessageRoleUser, Content: "img-x and image-3 and img-"},
			}}}},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextImageIDFloor(tt.lineage); got != tt.want {
				t.Errorf("NextImageIDFloor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestImageStoreConcurrentAccess(t *testing.T) {
	store := NewImageStore(t.TempDir())
	if err := store.BindSession("concurrent", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				store.Register("", "image/png", 1, 1, 1)
			}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if got := len(store.All()); got != 100 {
		t.Errorf("All() = %d, want 100", got)
	}
}
