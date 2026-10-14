//go:build !unix

package quota

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendFailsWithoutFileLocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	if err := NewStore(path).Append(sampleSnapshot("a", testBase, 10), testBase); err == nil {
		t.Fatal("Append without file locking: want error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("history file written without locking: stat err %v", err)
	}
}
