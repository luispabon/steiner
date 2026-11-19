package session

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/agent"
)

// Session represents a saved conversation session with metadata and lineage.
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	// ModelRef is the configured model reference (alias or provider/model-id)
	// active when the session was saved. Empty on records written before it
	// existed, where Model still carries the backend id.
	ModelRef string `json:"model_ref,omitempty"`
	// ModelProvider is the provider alias that served ModelRef when the session
	// was saved. It lets a resumed session tell a redefined alias from the one
	// the conversation was talking to.
	ModelProvider string `json:"model_provider,omitempty"`
	// ReasoningOverrides records session-time reasoning effort overrides by
	// model reference, so a resume restores the same effort.
	ReasoningOverrides map[string]ReasoningOverride `json:"reasoning_overrides,omitempty"`
	Group              string                       `json:"group,omitempty"`
	Mode               string                       `json:"mode,omitempty"`
	Skills             []string                     `json:"skills,omitempty"`
	Lineage            agent.ConversationLineage    `json:"lineage"`
	// PromptCacheKey identifies the prefix family this session's payload belongs
	// to, for provider-side cache routing. NOT an identifier: a fork deliberately
	// shares its parent's value, so two session files can carry the same key.
	// Empty on records written before this field existed; always read via CacheKey.
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
	// SubAgentLedger records sub-agents still outstanding when the session was saved.
	SubAgentLedger []agent.SubAgentLedgerEntry `json:"sub_agent_ledger,omitempty"`
	// DelegationGroups records reserved group names for the session scope.
	DelegationGroups *agent.DelegationGroupLedger `json:"delegation_groups,omitempty"`
	// SessionDate records the local date the session started, as rendered parts,
	// so a resumed or forked session reproduces the exact date line including the
	// zone abbreviation. Nil on records written before this field existed.
	SessionDate *StartDate `json:"session_date,omitempty"`
	// LastMainRequest records the most recent successful main model request, so
	// a resumed session keeps its prompt-cache age. Nil on records written
	// before this field existed.
	LastMainRequest *LastMainRequest `json:"last_main_request,omitempty"`
}

// StartDate is a session's recorded start date, stored as rendered parts
// rather than an instant so the zone abbreviation survives a reload on a later
// day or in another zone.
type StartDate struct {
	Date   string `json:"date"`
	Zone   string `json:"zone"`
	Offset string `json:"offset"`
}

func cloneSessionDate(d *StartDate) *StartDate {
	if d == nil {
		return nil
	}
	clone := *d
	return &clone
}

// ReasoningOverride is a persisted session-time reasoning override. Kind is one
// of the provider package's override kinds ("provider_default" or "effort");
// Effort is set for the "effort" kind.
type ReasoningOverride struct {
	Kind   string `json:"kind"`
	Effort string `json:"effort,omitempty"`
}

func cloneReasoningOverrides(src map[string]ReasoningOverride) map[string]ReasoningOverride {
	if src == nil {
		return nil
	}
	clone := make(map[string]ReasoningOverride, len(src))
	for ref, override := range src {
		clone[ref] = override
	}
	return clone
}

// LastMainRequest is the persisted form of a session's most recent successful
// main model request: when it started, the model identity that served it, and
// the prompt cache key it was sent with. A resumed run drops one whose identity
// does not match its own request.
type LastMainRequest struct {
	At             time.Time `json:"at"`
	ProviderAlias  string    `json:"provider_alias"`
	BackendModelID string    `json:"backend_model_id"`
	PromptCacheKey string    `json:"prompt_cache_key"`
}

func cloneLastMainRequest(last *LastMainRequest) *LastMainRequest {
	if last == nil {
		return nil
	}
	clone := *last
	return &clone
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
	if utf8.RuneCountInString(title) > 80 {
		runes := []rune(title)
		title = string(runes[:80])
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
// The fork has a new ID, same model, and title prefixed with "Fork of: ".
// Its lineage is a deep copy that keeps only the latest generation.
func Fork(s Session) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, fmt.Errorf("fork: %w", err)
	}
	now := time.Now().UTC()
	forkTitle := TitleFromPrompt("Fork of: " + s.Title)
	lineage := s.Lineage.OnlyLatest()
	return Session{
		ID:                 id,
		CreatedAt:          now,
		UpdatedAt:          now,
		Title:              forkTitle,
		Model:              s.Model,
		ModelRef:           s.ModelRef,
		ModelProvider:      s.ModelProvider,
		ReasoningOverrides: cloneReasoningOverrides(s.ReasoningOverrides),
		Mode:               s.Mode,
		Group:              strings.TrimSpace(s.Group),
		Skills:             s.Skills,
		Lineage:            lineage,
		DelegationGroups:   agent.CloneDelegationGroupLedger(s.DelegationGroups),
		// The fork deliberately shares the parent's prompt cache key so the
		// warm prefix carries over; CacheKey() heals pre-change records that
		// have no stored key instead of propagating an empty one.
		PromptCacheKey:  s.CacheKey(),
		SessionDate:     cloneSessionDate(s.SessionDate),
		LastMainRequest: cloneLastMainRequest(s.LastMainRequest),
	}, nil
}
