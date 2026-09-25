package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// staleImageDirAge is how long an image folder (session or ephemeral) or legacy
// loose file may go untouched before the next process prunes it.
const staleImageDirAge = 30 * 24 * time.Hour

// imageIndexFilename is the per-session image index written under the
// session's folder.
const imageIndexFilename = "index.json"

// imageIDPattern matches the numeric part of an img-N identifier.
var imageIDPattern = regexp.MustCompile(`img-(\d+)`)

// imageRefPattern matches an image ID inside a rendered placeholder
// ("[image img-N:") or a composer marker ("[img-N]"). A bare "img-N" in prose
// or a file path (e.g. /shots/img-20240101.png) must not raise the ID floor.
var imageRefPattern = regexp.MustCompile(`\[image img-(\d+):|\[img-(\d+)\]`)

// ImageRef describes a registered image with ID, file location, and metadata.
type ImageRef struct {
	ID        string `json:"id"`
	FilePath  string `json:"file_path"`
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int    `json:"size_bytes"`
}

// imageIndex is the on-disk shape of a session's index.json.
type imageIndex struct {
	Next   int        `json:"next"`
	Images []ImageRef `json:"images"`
}

// ImageStore tracks conversation-scoped image registrations, assigning
// auto-incrementing IDs like img-1, img-2 to each image stored on disk. It is
// bound to one folder at a time: an ephemeral per-process folder until
// BindSession switches it to a session folder.
type ImageStore struct {
	mu        sync.Mutex
	root      string
	dir       string
	procDir   string
	bound     bool
	sessionID string
	refs      map[string]ImageRef
	order     []string
	next      int
}

// NewImageStore returns an ImageStore rooted at root (e.g.
// "<workDir>/.steiner/tmp/images"). It starts unbound, using an ephemeral
// per-process folder under root, and best-effort prunes stale folders under
// root.
func NewImageStore(root string) *ImageStore {
	s := &ImageStore{
		root: root,
		dir:  filepath.Join(root, fmt.Sprintf("proc-%d-%s", os.Getpid(), randomProcSuffix())),
		refs: make(map[string]ImageRef),
		next: 1,
	}
	s.procDir = s.dir
	s.pruneStale()
	return s
}

// randomProcSuffix returns a short random hex suffix for the ephemeral folder.
func randomProcSuffix() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand only fails on a broken platform RNG; a time-derived
		// suffix still yields a per-process-unique folder name.
		return fmt.Sprintf("%06x", uint32(time.Now().UnixNano())&0xffffff)
	}
	return hex.EncodeToString(b[:])
}

// pruneStale removes direct subdirectories and legacy loose files under the
// root whose mtime is older than staleImageDirAge. Pruning is best-effort.
func (s *ImageStore) pruneStale() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		// No root yet: nothing to prune.
		return
	}
	cutoff := time.Now().Add(-staleImageDirAge)
	for _, entry := range entries {
		path := filepath.Join(s.root, entry.Name())
		if path == s.dir {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		// Best-effort: an unremovable stale entry is not worth failing startup over.
		_ = os.RemoveAll(path)
	}
}

// BindSession switches the store to the folder for sessionID, loading its
// index (if any). The next img-N is max(index next, highest indexed ID+1,
// minNext, 1). In-memory refs from the previous binding are dropped. sessionID
// must be a single safe path element. The folder is not created here; Register
// creates it lazily.
func (s *ImageStore) BindSession(sessionID string, minNext int) error {
	if err := validateSessionID(sessionID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	newDir := filepath.Join(s.root, sessionID)
	idx, hasIndex, err := loadSessionIndex(newDir, sessionID)
	if err != nil {
		return err
	}

	next := max(minNext, 1)
	refs := make(map[string]ImageRef)
	order := make([]string, 0, len(idx.Images))
	if hasIndex {
		next = max(next, idx.Next, highestImageID(idx.Images)+1)
		refs, order = refsFromIndex(idx)
	}

	prevDir, prevBound := s.dir, s.bound
	s.dir = newDir
	s.bound = true
	s.sessionID = sessionID
	s.refs = refs
	s.order = order
	s.next = next

	// Reset the prune clock for a resumed session. Touch only when the folder
	// already exists; BindSession never creates it.
	if info, err := os.Stat(newDir); err == nil && info.IsDir() {
		now := time.Now()
		// Best-effort: a failed touch only leaves the folder on its old prune clock.
		_ = os.Chtimes(newDir, now, now)
	}

	// Drop an empty ephemeral folder left by the previous unbound binding; a
	// non-empty one is cleaned up by Cleanup.
	if !prevBound && prevDir != "" && isProcDirName(prevDir) {
		// Best-effort: os.Remove fails for a non-empty or missing folder.
		_ = os.Remove(prevDir)
	}
	return nil
}

// CopySession copies the image folder of fromID to toID (for forks), rewriting
// index file paths that point inside the source folder to the destination. A
// missing source folder is not an error. A non-empty destination is refused.
func (s *ImageStore) CopySession(fromID, toID string) error {
	if err := validateSessionID(fromID); err != nil {
		return err
	}
	if err := validateSessionID(toID); err != nil {
		return err
	}

	srcDir := filepath.Join(s.root, fromID)
	dstDir := filepath.Join(s.root, toID)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("copy session images: read %s: %w", fromID, err)
	}
	if dstEntries, derr := os.ReadDir(dstDir); derr == nil && len(dstEntries) > 0 {
		return fmt.Errorf("copy session images: destination %s is not empty", toID)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("copy session images: create %s: %w", toID, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copySessionEntry(entry.Name(), srcDir, dstDir); err != nil {
			return err
		}
	}
	return nil
}

// loadSessionIndex reads dir/index.json into an imageIndex. It reports whether
// an index was present; a missing folder or index is not an error.
func loadSessionIndex(dir, sessionID string) (imageIndex, bool, error) {
	var idx imageIndex
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return idx, false, nil
	}
	data, rerr := os.ReadFile(filepath.Join(dir, imageIndexFilename))
	switch {
	case rerr == nil:
		if uerr := json.Unmarshal(data, &idx); uerr != nil {
			return imageIndex{}, false, fmt.Errorf("bind session %s: read image index: %w", sessionID, uerr)
		}
		return idx, true, nil
	case os.IsNotExist(rerr):
		// No index yet: a fresh or pre-upgrade session folder.
		return idx, false, nil
	default:
		return imageIndex{}, false, fmt.Errorf("bind session %s: read image index: %w", sessionID, rerr)
	}
}

// refsFromIndex returns the unique, non-empty refs of idx keyed by ID, plus
// their registration order.
func refsFromIndex(idx imageIndex) (map[string]ImageRef, []string) {
	refs := make(map[string]ImageRef)
	order := make([]string, 0, len(idx.Images))
	for _, ref := range idx.Images {
		if ref.ID == "" {
			continue
		}
		if _, ok := refs[ref.ID]; ok {
			continue
		}
		refs[ref.ID] = ref
		order = append(order, ref.ID)
	}
	return refs, order
}

// copySessionEntry copies one non-directory entry from srcDir to dstDir,
// rewriting index file paths that point inside srcDir to sit under dstDir.
func copySessionEntry(name, srcDir, dstDir string) error {
	data, err := os.ReadFile(filepath.Join(srcDir, name))
	if err != nil {
		return fmt.Errorf("copy session images: read %s: %w", name, err)
	}
	if name == imageIndexFilename {
		data = rewriteIndexPaths(data, srcDir, dstDir)
	}
	if err := os.WriteFile(filepath.Join(dstDir, name), data, 0o644); err != nil {
		return fmt.Errorf("copy session images: write %s: %w", name, err)
	}
	return nil
}

// rewriteIndexPaths rewrites index paths under srcDir to sit under dstDir,
// leaving the original bytes when the index cannot be parsed or re-encoded.
func rewriteIndexPaths(data []byte, srcDir, dstDir string) []byte {
	var idx imageIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return data
	}
	for i := range idx.Images {
		idx.Images[i].FilePath = rewriteSessionPath(idx.Images[i].FilePath, srcDir, dstDir)
	}
	rewritten, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return data
	}
	return rewritten
}

// Register assigns the next img-N ID in the current binding, records the ref,
// and persists the index when session-bound. filePath may be empty when the
// caller failed to write the file; the ID is still assigned.
func (s *ImageStore) Register(filePath, mediaType string, w, h, sizeBytes int) ImageRef {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := fmt.Sprintf("img-%d", s.next)
	s.next++
	ref := ImageRef{
		ID:        id,
		FilePath:  filePath,
		MediaType: mediaType,
		Width:     w,
		Height:    h,
		SizeBytes: sizeBytes,
	}
	s.refs[id] = ref
	s.order = append(s.order, id)
	s.persistIndexLocked()
	return ref
}

// Remove forgets id in the current binding, deletes its file only if the file
// lies inside the store's current folder, and persists the index. Unknown IDs
// are a no-op.
func (s *ImageStore) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref, ok := s.refs[id]
	if !ok {
		return
	}
	delete(s.refs, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	if ref.FilePath != "" && pathWithin(s.dir, ref.FilePath) {
		// Best-effort: the file may already be gone (pruned or removed by hand).
		_ = os.Remove(ref.FilePath)
	}
	s.persistIndexLocked()
}

// Get returns the ImageRef for the given ID in the current binding, or
// (ImageRef{}, false) if not found.
func (s *ImageStore) Get(id string) (ImageRef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.refs[id]
	return ref, ok
}

// All returns all registered ImageRefs in registration order for the current
// binding.
func (s *ImageStore) All() []ImageRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orderedRefsLocked()
}

// Dir returns the store's current folder (the bound session folder, or the
// ephemeral per-process folder while unbound).
func (s *ImageStore) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// Cleanup removes only the ephemeral per-process folder, if one exists. Session
// folders persist across restarts.
func (s *ImageStore) Cleanup() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.procDir == "" {
		return nil
	}
	return os.RemoveAll(s.procDir)
}

// orderedRefsLocked returns the current refs in registration order. Callers
// must hold s.mu.
func (s *ImageStore) orderedRefsLocked() []ImageRef {
	refs := make([]ImageRef, 0, len(s.order))
	for _, id := range s.order {
		if ref, ok := s.refs[id]; ok {
			refs = append(refs, ref)
		}
	}
	return refs
}

// persistIndexLocked writes the session index atomically. Ephemeral folders
// have no index and a write failure never fails Register or Remove. Callers
// must hold s.mu.
func (s *ImageStore) persistIndexLocked() {
	if !s.bound {
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("persist image index", "session", s.sessionID, "error", err)
		return
	}
	idx := imageIndex{Next: s.next, Images: s.orderedRefsLocked()}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		slog.Warn("persist image index", "session", s.sessionID, "error", err)
		return
	}
	tmp := filepath.Join(s.dir, imageIndexFilename+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Warn("persist image index", "session", s.sessionID, "error", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, imageIndexFilename)); err != nil {
		slog.Warn("persist image index", "session", s.sessionID, "error", err)
	}
}

// NextImageIDFloor returns 1 + the highest N in image placeholders or composer
// markers across the lineage's message contents and summary prefixes, or 1 when
// there are none.
func NextImageIDFloor(l ConversationLineage) int {
	highest := 0
	for _, gen := range l.Generations {
		highest = max(highest, highestImageRefInMessages(gen.SummaryPrefix), highestImageRefInMessages(gen.Messages))
	}
	return highest + 1
}

// validateSessionID rejects IDs that are not a single safe path element.
func validateSessionID(sessionID string) error {
	if sessionID == "" || sessionID == "." || sessionID == ".." {
		return fmt.Errorf("bind session: invalid session id %q", sessionID)
	}
	if filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\`) {
		return fmt.Errorf("bind session: invalid session id %q", sessionID)
	}
	return nil
}

// isProcDirName reports whether dir names an ephemeral per-process folder.
func isProcDirName(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), "proc-")
}

// pathWithin reports whether path lies inside dir.
func pathWithin(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// rewriteSessionPath rewrites a path under srcDir to sit under dstDir.
func rewriteSessionPath(path, srcDir, dstDir string) string {
	if path == "" {
		return path
	}
	if pathWithin(srcDir, path) {
		rel, err := filepath.Rel(srcDir, path)
		if err == nil {
			return filepath.Join(dstDir, rel)
		}
	}
	return path
}

// highestImageID returns the highest N among the refs' img-N IDs, or 0.
func highestImageID(refs []ImageRef) int {
	highest := 0
	for _, ref := range refs {
		if n, ok := parseImageIDNumber(ref.ID); ok && n > highest {
			highest = n
		}
	}
	return highest
}

// highestImageRefInMessages returns the highest N among image references in the
// messages' content, or 0. Only rendered placeholders ("[image img-N:") and
// composer markers ("[img-N]") count; a bare "img-N" in prose or a file path is
// not an image reference.
func highestImageRefInMessages(messages []Message) int {
	highest := 0
	for _, msg := range messages {
		for _, match := range imageRefPattern.FindAllStringSubmatch(msg.Content, -1) {
			digits := match[1]
			if digits == "" {
				digits = match[2]
			}
			n, err := strconv.Atoi(digits)
			if err == nil && n > highest {
				highest = n
			}
		}
	}
	return highest
}

// parseImageIDNumber extracts N from an "img-N" identifier.
func parseImageIDNumber(id string) (int, bool) {
	match := imageIDPattern.FindStringSubmatch(id)
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return n, true
}
