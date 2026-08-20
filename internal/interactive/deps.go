package interactive

import (
	"context"
	"net/http"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// RunResult captures the outcome of an interactive session run.
type RunResult struct {
	Conversation    []agent.Message
	WorkflowHandoff *tool.WorkflowHandoffTransition
}

// runExecutor starts and manages model-in-the-loop runs. Consumer-defined to
// avoid coupling to internal/agent or cmd/steiner.
type runExecutor interface {
	// Run executes a model run with the given conversation and skills.
	// drainSteers drains all queued between-turn steering messages; pass nil when unavailable.
	// Returns the updated conversation on success.
	Run(ctx context.Context, conversation []agent.Message, skillNames []string, drainSteers func() []agent.SteerMessage) (RunResult, error)

	// Compact reduces the conversation through the same runner seam used by
	// normal runs.
	Compact(ctx context.Context, conversation []agent.Message, skillNames []string, tools []provider.ToolSpec) ([]agent.Message, error)
}

// historyWriter persists and loads prompt history for an interactive session.
// Consumer-defined to avoid coupling to cmd/steiner or internal/history.
type historyWriter interface {
	// Record persists a prompt string to history.
	Record(prompt string) error
	// Load returns all previously recorded prompts.
	Load() ([]string, error)
}

// sessionStore persists and loads conversation sessions with lineage metadata.
// Consumer-defined to avoid coupling to cmd/steiner or internal/history.
type sessionStore interface {
	// Save persists a session to disk.
	Save(session.Session) error
	// Load reads a session by ID from disk.
	Load(id string) (session.Session, error)
	// List returns all sessions sorted newest-first.
	List() ([]session.IndexEntry, error)
}

// Dependencies groups the external dependencies and initial configuration
// required by an interactive session. Each field uses a consumer-defined
// interface to avoid premature coupling to concrete implementations.
type Dependencies struct {
	BaseEvents        output.EventSink
	Runner            runExecutor
	HistoryWriter     historyWriter
	SessionStore      sessionStore
	SkillNames        []string
	Config            config.Config
	HTTPClient        *http.Client
	HomeDir           string
	WorkDir           string
	CompactionLogPath string
}
