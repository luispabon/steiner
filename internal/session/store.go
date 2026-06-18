package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
)

const (
	indexFile   = "index.json"
	maxSessions = 25
	sessionExt  = ".json"
)

// Store manages session persistence to disk with atomic writes and eviction.
type Store struct {
	dir   string
	mu    sync.Mutex
	index []IndexEntry
}

// NewStore creates a store for the given directory, creating it if absent.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	store := &Store{dir: dir}
	if err := store.loadIndex(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return store, nil
}

// Save writes a session to disk atomically and updates the index.
func (s *Store) Save(session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionPath := filepath.Join(s.dir, session.ID+sessionExt)
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

	sessionPath := filepath.Join(s.dir, id+sessionExt)
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
	var legacy legacySessionEnvelope
	if err := json.Unmarshal(data, &legacy); err != nil {
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

	sessionPath := filepath.Join(s.dir, id+sessionExt)
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

// writeAtomic writes data to a temporary file then renames it.
func (s *Store) writeAtomic(path string, data interface{}) error {
	data64, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data64, 0o644); err != nil {
		return fmt.Errorf("write tmp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename tmp file: %w", err)
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

	if err := json.Unmarshal(data, &s.index); err != nil {
		return err
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

// evictOldestLocked removes the oldest session if more than maxSessions exist.
func (s *Store) evictOldestLocked() error {
	if len(s.index) <= maxSessions {
		return nil
	}

	toRemove := s.index[len(s.index)-1]
	s.index = s.index[:len(s.index)-1]

	sessionPath := filepath.Join(s.dir, toRemove.ID+sessionExt)
	if err := os.Remove(sessionPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete evicted session: %w", err)
	}

	return s.saveIndexLocked()
}

// saveIndexLocked writes the index file atomically.
func (s *Store) saveIndexLocked() error {
	indexPath := filepath.Join(s.dir, indexFile)
	return s.writeAtomic(indexPath, s.index)
}
