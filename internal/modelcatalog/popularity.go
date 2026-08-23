package modelcatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const popularityFilename = "model-popularity.json"

// Key identifies a provider's canonical backend model ID.
type Key struct {
	ProviderAlias string
	ModelID       string
}

type popularityEntry struct {
	ProviderAlias string    `json:"provider_alias"`
	ModelID       string    `json:"model_id"`
	Count         int       `json:"count"`
	LastUsed      time.Time `json:"last_used"`
}

type popularityFile struct {
	Entries []popularityEntry `json:"entries"`
}

// Store persists model popularity counts and last-used timestamps.
type Store struct {
	mu   sync.Mutex
	path string
}

// DefaultStatePath returns the default path for persisted model popularity.
func DefaultStatePath() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "steiner", popularityFilename)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".local", "state", "steiner", popularityFilename)
}

// NewStore creates a popularity Store. An empty path uses DefaultStatePath.
func NewStore(path string) *Store {
	if path == "" {
		path = DefaultStatePath()
	}
	return &Store{path: path}
}

// Record increments the count for providerAlias and modelID and updates its
// last-used timestamp.
func (s *Store) Record(providerAlias, modelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create popularity store dir: %w", err)
	}
	release, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return fmt.Errorf("acquire popularity store lock: %w", err)
	}
	defer release()

	entries, err := readEntries(s.path)
	if err != nil {
		return err
	}
	key := Key{ProviderAlias: providerAlias, ModelID: modelID}
	entry := entries[key]
	entry.ProviderAlias = providerAlias
	entry.ModelID = modelID
	entry.Count++
	entry.LastUsed = time.Now().UTC()
	entries[key] = entry

	if err := writeEntries(s.path, entries); err != nil {
		return fmt.Errorf("write popularity store: %w", err)
	}
	return nil
}

// Snapshot returns persisted popularity counts keyed by canonical provider and
// model identity. Missing or corrupt state returns an empty map.
func (s *Store) Snapshot() map[Key]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return map[Key]int{}
	}
	release, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return map[Key]int{}
	}
	defer release()

	entries, err := readEntries(s.path)
	if err != nil {
		return map[Key]int{}
	}
	counts := make(map[Key]int, len(entries))
	for key, entry := range entries {
		counts[key] = entry.Count
	}
	return counts
}

func readEntries(path string) (map[Key]popularityEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[Key]popularityEntry), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read popularity store: %w", err)
	}

	var stored popularityFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return make(map[Key]popularityEntry), nil
	}
	entries := make(map[Key]popularityEntry, len(stored.Entries))
	for _, entry := range stored.Entries {
		key := Key{ProviderAlias: entry.ProviderAlias, ModelID: entry.ModelID}
		entries[key] = entry
	}
	return entries, nil
}

func writeEntries(path string, entries map[Key]popularityEntry) error {
	ordered := make([]popularityEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ProviderAlias != ordered[j].ProviderAlias {
			return ordered[i].ProviderAlias < ordered[j].ProviderAlias
		}
		return ordered[i].ModelID < ordered[j].ModelID
	})
	data, err := json.Marshal(popularityFile{Entries: ordered})
	if err != nil {
		return fmt.Errorf("marshal popularity store: %w", err)
	}

	dir := filepath.Dir(path)
	return atomicWriteFile(dir, ".tmp-model-popularity-*", path, "popularity", data)
}
