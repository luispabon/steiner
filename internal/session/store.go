package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/fsatomic"
)

const (
	indexFile   = "index.json"
	maxSessions = 25
	sessionExt  = ".json"
	historyFile = "history.md"

	// Transcripts may contain sensitive prompts and tool output.
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
)

// chmod is os.Chmod, swappable so tests can simulate ownership failures.
var chmod = os.Chmod

// removeAll is os.RemoveAll, swappable so tests can simulate history removal failures.
var removeAll = os.RemoveAll

// secureExistingFiles tightens permissions on session and index files
// created by older versions with world-readable modes. It is best-effort:
// files we cannot chmod (root-owned after a sudo run, root-squash mounts) are
// logged and skipped so the store still opens; new writes use fileMode anyway.
func secureExistingFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("session store: cannot list directory to tighten permissions", "dir", dir, "error", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (filepath.Ext(name) != sessionExt && !strings.HasSuffix(name, sessionExt+".tmp")) {
			continue
		}
		if err := chmod(filepath.Join(dir, name), fileMode); err != nil && !os.IsNotExist(err) {
			slog.Warn("session store: cannot tighten file permissions", "file", name, "error", err)
		}
	}
}

// Store manages session persistence to disk with atomic writes and eviction.
type Store struct {
	dir   string
	mu    sync.Mutex
	index []IndexEntry
}

// NewStore creates a store for the given directory, creating it if absent.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	if err := chmod(dir, dirMode); err != nil {
		slog.Warn("session store: cannot tighten directory permissions", "dir", dir, "error", err)
	}
	secureExistingFiles(dir)
	store := &Store{dir: dir}
	if err := store.loadIndex(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return store, nil
}

func safeSessionPath(dir, id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("invalid session id %q", id)
	}
	return filepath.Join(dir, id+sessionExt), nil
}

// safeSessionDir returns the per-session directory that holds the session's
// history archive. It applies the same id validation as safeSessionPath.
func safeSessionDir(dir, id string) (string, error) {
	if _, err := safeSessionPath(dir, id); err != nil {
		return "", err
	}
	return filepath.Join(dir, id), nil
}

// Save writes a session to disk atomically and updates the index.
func (s *Store) Save(session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionPath, err := safeSessionPath(s.dir, session.ID)
	if err != nil {
		return fmt.Errorf("validate session id: %w", err)
	}
	// Never persist image payloads: strip them from a clone so the caller's
	// lineage is left untouched.
	session.Lineage = agent.LineageWithoutImageData(session.Lineage)
	if err := s.writeAtomic(sessionPath, session); err != nil {
		return fmt.Errorf("write session: %w", err)
	}

	if err := s.updateIndexLocked(session); err != nil {
		return fmt.Errorf("update index: %w", err)
	}

	if err := s.evictOldestLocked(); err != nil {
		return fmt.Errorf("evict oldest: %w", err)
	}

	return nil
}

// Load reads a session by ID from disk.
func (s *Store) Load(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionPath, err := safeSessionPath(s.dir, id)
	if err != nil {
		return Session{}, fmt.Errorf("validate session id: %w", err)
	}
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, fmt.Errorf("unmarshal session: %w", err)
	}
	if session.Lineage.Empty() {
		session = restoreLegacyLineage(session, data)
	}

	return session, nil
}

type legacySessionEnvelope struct {
	Conversation []agent.Message `json:"conversation"`
	Messages     []agent.Message `json:"messages"`
}

func restoreLegacyLineage(session Session, data []byte) Session {
	// TODO(legacy-cleanup): remove once legacy session files created before 2026-06-19
	// are no longer supported.
	var legacy legacySessionEnvelope
	if err := json.Unmarshal(data, &legacy); err != nil {
		slog.Warn("session store: cannot decode legacy conversation, keeping session without restored lineage", "session_id", session.ID, "error", err)
		return session
	}

	messages := legacy.Conversation
	if len(messages) == 0 {
		messages = legacy.Messages
	}
	if len(messages) == 0 {
		return session
	}

	session.Lineage = agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: messages,
			},
		},
		NextGenerationID: 2,
	}
	return session
}

// List returns all sessions sorted newest-first.
func (s *Store) List() ([]IndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]IndexEntry, len(s.index))
	copy(out, s.index)
	return out, nil
}

// Delete removes a session file and its index entry.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionPath, err := safeSessionPath(s.dir, id)
	if err != nil {
		return fmt.Errorf("validate session id: %w", err)
	}
	sessionDir, err := safeSessionDir(s.dir, id)
	if err != nil {
		return fmt.Errorf("validate session id: %w", err)
	}
	if err := removeAll(sessionDir); err != nil {
		return fmt.Errorf("delete session history: %w", err)
	}
	if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete session file: %w", err)
	}

	for i, entry := range s.index {
		if entry.ID == id {
			s.index = append(s.index[:i], s.index[i+1:]...)
			break
		}
	}

	if err := s.saveIndexLocked(); err != nil {
		return fmt.Errorf("save index: %w", err)
	}

	return nil
}

// HistoryPath returns the absolute path of the history archive for session id.
// The file is created by AppendHistory and may not exist yet.
func (s *Store) HistoryPath(id string) (string, error) {
	sessionDir, err := safeSessionDir(s.dir, id)
	if err != nil {
		return "", fmt.Errorf("validate session id: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(sessionDir, historyFile))
	if err != nil {
		return "", fmt.Errorf("resolve history path: %w", err)
	}
	return path, nil
}

// AppendHistory appends chunk to the history archive for session id, creating
// the session directory and file as needed. header is written before chunk only
// when the file is new or empty. It returns the archive's absolute path.
func (s *Store) AppendHistory(id, header, chunk string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.HistoryPath(id)
	if err != nil {
		return "", fmt.Errorf("append history: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return "", fmt.Errorf("append history: create session directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return "", fmt.Errorf("append history: open file: %w", err)
	}
	err = writeHistoryChunk(f, header, chunk)
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("append history: %w", err)
	}
	return path, nil
}

// writeHistoryChunk writes header then chunk to f and syncs f to disk before
// returning. The header is skipped when f already has content.
func writeHistoryChunk(f *os.File, header, chunk string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat history file: %w", err)
	}
	if info.Size() == 0 {
		if _, err := f.WriteString(header); err != nil {
			return fmt.Errorf("write history header: %w", err)
		}
	}
	if _, err := f.WriteString(chunk); err != nil {
		return fmt.Errorf("write history chunk: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync history file: %w", err)
	}
	return nil
}

// ReadHistory returns the contents of the history archive for session id. A
// missing archive returns an error that matches os.ErrNotExist.
func (s *Store) ReadHistory(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path, err := s.HistoryPath(id)
	if err != nil {
		return "", fmt.Errorf("read history: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read history: %w", err)
	}
	return string(data), nil
}

// writeAtomic writes data to a temporary file then renames it.
func (s *Store) writeAtomic(path string, data interface{}) error {
	data64, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := fsatomic.WriteFile(path, data64, fileMode); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// loadIndex reads the index file if it exists.
func (s *Store) loadIndex() error {
	indexPath := filepath.Join(s.dir, indexFile)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.index = []IndexEntry{}
			return nil
		}
		return err
	}

	var entries []IndexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	// Drop entries whose IDs could escape the store directory.
	s.index = make([]IndexEntry, 0, len(entries))
	for _, e := range entries {
		if _, err := safeSessionPath(s.dir, e.ID); err != nil {
			continue
		}
		s.index = append(s.index, e)
	}

	return nil
}

// updateIndexLocked adds or updates an index entry and sorts newest-first.
func (s *Store) updateIndexLocked(session Session) error {
	entry := IndexEntry{
		ID:        session.ID,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
		Title:     session.Title,
		Model:     session.Model,
		Group:     session.Group,
	}

	for i, e := range s.index {
		if e.ID == session.ID {
			s.index[i] = entry
			sort.Slice(s.index, func(i, j int) bool {
				return s.index[i].UpdatedAt.After(s.index[j].UpdatedAt)
			})
			return s.saveIndexLocked()
		}
	}

	s.index = append(s.index, entry)
	sort.Slice(s.index, func(i, j int) bool {
		return s.index[i].UpdatedAt.After(s.index[j].UpdatedAt)
	})

	return s.saveIndexLocked()
}

// evictOldestLocked removes the oldest sessions while more than maxSessions
// exist. Each eviction removes the history directory, then the JSON, then
// persists the index, so a failed removal leaves the index matching the files
// still on disk.
func (s *Store) evictOldestLocked() error {
	for len(s.index) > maxSessions {
		toRemove := s.index[len(s.index)-1]

		sessionPath, err := safeSessionPath(s.dir, toRemove.ID)
		if err != nil {
			return fmt.Errorf("validate evicted session id: %w", err)
		}
		sessionDir, err := safeSessionDir(s.dir, toRemove.ID)
		if err != nil {
			return fmt.Errorf("validate evicted session id: %w", err)
		}
		if err := removeAll(sessionDir); err != nil {
			return fmt.Errorf("delete evicted session history: %w", err)
		}
		if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete evicted session: %w", err)
		}

		s.index = s.index[:len(s.index)-1]
		if err := s.saveIndexLocked(); err != nil {
			return fmt.Errorf("save index after eviction: %w", err)
		}
	}

	return nil
}

// saveIndexLocked writes the index file atomically.
func (s *Store) saveIndexLocked() error {
	indexPath := filepath.Join(s.dir, indexFile)
	return s.writeAtomic(indexPath, s.index)
}
