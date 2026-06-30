package agent

import (
	"fmt"
	"os"
	"sync"
)

// ImageRef describes a registered image with ID, file location, and metadata.
type ImageRef struct {
	ID        string
	FilePath  string
	MediaType string
	Width     int
	Height    int
	SizeBytes int
}

// ImageStore tracks session-scoped image registrations, assigning auto-incrementing
// IDs like img-1, img-2 to each image stored on disk.
type ImageStore struct {
	mu      sync.Mutex
	refs    map[string]ImageRef
	counter int
	dir     string
}

// NewImageStore returns an ImageStore bound to the given directory (e.g. ".steiner/tmp/images").
func NewImageStore(dir string) *ImageStore {
	return &ImageStore{
		refs:    make(map[string]ImageRef),
		counter: 0,
		dir:     dir,
	}
}

// Register assigns an auto-incrementing ID to an image and returns the ImageRef.
// IDs are formatted as img-1, img-2, etc., starting from img-1 on the first call.
func (s *ImageStore) Register(filePath, mediaType string, w, h, sizeBytes int) ImageRef {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.counter++
	id := fmt.Sprintf("img-%d", s.counter)
	ref := ImageRef{
		ID:        id,
		FilePath:  filePath,
		MediaType: mediaType,
		Width:     w,
		Height:    h,
		SizeBytes: sizeBytes,
	}
	s.refs[id] = ref
	return ref
}

// Get returns the ImageRef for the given ID, or (ImageRef{}, false) if not found.
func (s *ImageStore) Get(id string) (ImageRef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.refs[id]
	return ref, ok
}

// All returns a slice of all registered ImageRefs in the order they were registered.
func (s *ImageStore) All() []ImageRef {
	s.mu.Lock()
	defer s.mu.Unlock()

	refs := make([]ImageRef, 0, len(s.refs))
	for i := 1; i <= s.counter; i++ {
		id := fmt.Sprintf("img-%d", i)
		if ref, ok := s.refs[id]; ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

// Dir returns the directory used to store image files.
func (s *ImageStore) Dir() string {
	return s.dir
}

// Cleanup removes the image store directory and all files within it.
// It tolerates the directory already being removed (os.RemoveAll returns nil for missing paths).
func (s *ImageStore) Cleanup() error {
	return os.RemoveAll(s.dir)
}
