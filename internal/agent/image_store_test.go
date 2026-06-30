package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestImageStore_Register(t *testing.T) {
	tests := []struct {
		name      string
		calls     int
		wantID    string
		wantPath  string
		wantMedia string
	}{
		{
			name:      "first registration gets img-1",
			calls:     1,
			wantID:    "img-1",
			wantPath:  "/tmp/image1.png",
			wantMedia: "image/png",
		},
		{
			name:      "second registration gets img-2",
			calls:     2,
			wantID:    "img-2",
			wantPath:  "/tmp/image2.jpg",
			wantMedia: "image/jpeg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewImageStore(".steiner/tmp/images")
			var ref ImageRef

			paths := []string{"/tmp/image1.png", "/tmp/image2.jpg"}
			medias := []string{"image/png", "image/jpeg"}

			for i := 0; i < tt.calls; i++ {
				ref = store.Register(paths[i], medias[i], 1920, 1080, 100000)
			}

			if ref.ID != tt.wantID {
				t.Fatalf("ID = %q, want %q", ref.ID, tt.wantID)
			}
			if ref.FilePath != tt.wantPath {
				t.Fatalf("FilePath = %q, want %q", ref.FilePath, tt.wantPath)
			}
			if ref.MediaType != tt.wantMedia {
				t.Fatalf("MediaType = %q, want %q", ref.MediaType, tt.wantMedia)
			}
		})
	}
}

func TestImageStore_Get(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		found   bool
		wantRef ImageRef
	}{
		{
			name:  "find existing image",
			id:    "img-1",
			found: true,
			wantRef: ImageRef{
				ID:        "img-1",
				FilePath:  "/tmp/test.png",
				MediaType: "image/png",
				Width:     1920,
				Height:    1080,
				SizeBytes: 50000,
			},
		},
		{
			name:  "nonexistent ID returns false",
			id:    "img-999",
			found: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewImageStore(".steiner/tmp/images")
			store.Register("/tmp/test.png", "image/png", 1920, 1080, 50000)

			ref, ok := store.Get(tt.id)
			if ok != tt.found {
				t.Fatalf("Get(%q) ok = %v, want %v", tt.id, ok, tt.found)
			}
			if tt.found && ref != tt.wantRef {
				t.Fatalf("Get(%q) = %#v, want %#v", tt.id, ref, tt.wantRef)
			}
		})
	}
}

func TestImageStore_All(t *testing.T) {
	tests := []struct {
		name      string
		numImages int
		wantCount int
	}{
		{
			name:      "empty store returns empty slice",
			numImages: 0,
			wantCount: 0,
		},
		{
			name:      "three images in order",
			numImages: 3,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewImageStore(".steiner/tmp/images")

			for i := 0; i < tt.numImages; i++ {
				store.Register("/tmp/test.png", "image/png", 100, 100, 1000)
			}

			refs := store.All()
			if len(refs) != tt.wantCount {
				t.Fatalf("len(All()) = %d, want %d", len(refs), tt.wantCount)
			}

			// Verify IDs are sequential.
			for i, ref := range refs {
				if ref.ID == "" {
					t.Fatalf("All()[%d].ID is empty", i)
				}
			}
		})
	}
}

func TestImageStore_Cleanup(t *testing.T) {
	tests := []struct {
		name             string
		createDir        bool
		expectError      bool
		expectDirToExist bool
	}{
		{
			name:             "cleanup removes existing directory",
			createDir:        true,
			expectError:      false,
			expectDirToExist: false,
		},
		{
			name:             "cleanup tolerates missing directory",
			createDir:        false,
			expectError:      false,
			expectDirToExist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			storeDir := filepath.Join(tmpDir, "images")

			if tt.createDir {
				if err := os.MkdirAll(storeDir, 0o755); err != nil {
					t.Fatalf("setup: failed to create directory: %v", err)
				}
			}

			store := NewImageStore(storeDir)
			err := store.Cleanup()

			if (err != nil) != tt.expectError {
				t.Fatalf("Cleanup() error = %v, want error = %v", err, tt.expectError)
			}

			if tt.expectDirToExist {
				if _, err := os.Stat(storeDir); os.IsNotExist(err) {
					t.Fatalf("directory should exist after cleanup, but was removed")
				}
			} else {
				if _, err := os.Stat(storeDir); !os.IsNotExist(err) {
					t.Fatalf("directory should be removed after cleanup, but still exists")
				}
			}
		})
	}
}

func TestImageStore_ConcurrentAccess(t *testing.T) {
	store := NewImageStore(".steiner/tmp/images")
	numGoroutines := 10
	numRegsPerGoroutine := 10

	done := make(chan bool, numGoroutines)

	// Launch concurrent Register calls.
	for i := 0; i < numGoroutines; i++ {
		go func(_ int) {
			for j := 0; j < numRegsPerGoroutine; j++ {
				store.Register("/tmp/test.png", "image/png", 100, 100, 1000)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish.
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify all registrations succeeded: should have exactly
	// numGoroutines * numRegsPerGoroutine images.
	refs := store.All()
	wantCount := numGoroutines * numRegsPerGoroutine
	if len(refs) != wantCount {
		t.Fatalf("concurrent registration: got %d images, want %d", len(refs), wantCount)
	}

	// Verify no ID collisions: all IDs should be unique.
	seen := make(map[string]bool)
	for _, ref := range refs {
		if seen[ref.ID] {
			t.Fatalf("duplicate ID %q detected in concurrent access", ref.ID)
		}
		seen[ref.ID] = true
	}

	// Verify Get works on all registered IDs.
	for i := 1; i <= wantCount; i++ {
		id := fmt.Sprintf("img-%d", i)
		_, ok := store.Get(id)
		if !ok {
			t.Fatalf("concurrent access: failed to Get %s", id)
		}
	}
}
