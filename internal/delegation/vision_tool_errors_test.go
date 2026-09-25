package delegation

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestLoadVisionImageBlockErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := agent.NewImageStore(dir)

	if _, err := loadVisionImageBlock("img-99", store); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("unknown ID error = %v, want 'not registered'", err)
	}

	emptyPathRef := store.Register("", "image/png", 1, 1, 1)
	if _, err := loadVisionImageBlock(emptyPathRef.ID, store); err == nil || !strings.Contains(err.Error(), "paste it again") {
		t.Errorf("empty path error = %v, want 'paste it again'", err)
	}

	missingRef := store.Register(filepath.Join(dir, "gone.png"), "image/png", 1, 1, 1)
	if _, err := loadVisionImageBlock(missingRef.ID, store); err == nil || !strings.Contains(err.Error(), "paste it again") {
		t.Errorf("missing file error = %v, want 'paste it again'", err)
	}

	if _, err := loadVisionImageBlock("img-1", nil); err == nil {
		t.Error("nil store = nil error, want error")
	}
}
