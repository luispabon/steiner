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
	Lineage   agent.ConversationLineage `json:"lineage"`
}

// IndexEntry is a lightweight version of Session without Lineage for index listings.
type IndexEntry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
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

// NewSession creates a new session with the given model and lineage.
func NewSession(model string, lineage agent.ConversationLineage) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	return Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Title:     "",
		Model:     model,
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
