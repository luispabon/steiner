package session

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// Session represents a saved conversation session with metadata and lineage.
type Session struct {
	ID        string                    `json:"id"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
	Title     string                    `json:"title"`
	Model     string                    `json:"model"`
	Group     string                    `json:"group,omitempty"`
	Mode      string                    `json:"mode,omitempty"`
	Lineage   agent.ConversationLineage `json:"lineage"`
	// PromptCacheKey identifies the prefix family this session's payload belongs
	// to, for provider-side cache routing. NOT an identifier: a fork deliberately
	// shares its parent's value, so two session files can carry the same key.
	// Empty on records written before this field existed; always read via CacheKey.
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
}

// CacheKey returns the session's prompt cache key, falling back to the session ID
// for records written before the field existed.
func (s Session) CacheKey() string {
	if s.PromptCacheKey != "" {
		return s.PromptCacheKey
	}
	return s.ID
}

// IndexEntry is a lightweight version of Session without Lineage for index listings.
type IndexEntry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	Group     string    `json:"group,omitempty"`
}

// generateID creates a random hex ID.
func generateID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return fmt.Sprintf("%032x", b), nil
}

// TitleFromPrompt truncates a prompt to 80 characters and collapses whitespace.
func TitleFromPrompt(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}

// NewSession creates a new session with the given model, lineage, and optional group.
func NewSession(model string, lineage agent.ConversationLineage, group ...string) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	sessionGroup := ""
	if len(group) > 0 {
		sessionGroup = strings.TrimSpace(group[0])
	}
	return Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Title:     "",
		Model:     model,
		Group:     sessionGroup,
		Lineage:   lineage,
	}, nil
}

// WithTitle returns a copy of the session with an updated title.
func (s Session) WithTitle(title string) Session {
	s.Title = TitleFromPrompt(title)
	s.UpdatedAt = time.Now().UTC()
	return s
}

// WithLineage returns a copy of the session with an updated lineage.
func (s Session) WithLineage(lineage agent.ConversationLineage) Session {
	s.Lineage = lineage
	s.UpdatedAt = time.Now().UTC()
	return s
}

// Fork creates a new session as a fork of the given session.
// The fork has a new ID, cloned lineage, same model, and title prefixed with "Fork of: ".
func Fork(s Session) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, fmt.Errorf("fork: %w", err)
	}
	now := time.Now().UTC()
	forkTitle := TitleFromPrompt("Fork of: " + s.Title)
	return Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Title:     forkTitle,
		Model:     s.Model,
		Group:     strings.TrimSpace(s.Group),
		Lineage:   s.Lineage.Clone(),
		// The fork deliberately shares the parent's prompt cache key so the
		// warm prefix carries over; CacheKey() heals pre-change records that
		// have no stored key instead of propagating an empty one.
		PromptCacheKey: s.CacheKey(),
	}, nil
}
