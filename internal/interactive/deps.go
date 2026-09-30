package interactive

import (
	"context"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/internal/tool"
)

// RunResult captures the outcome of an interactive session run.
type RunResult struct {
	Conversation    []agent.Message
	WorkflowHandoff *tool.WorkflowHandoffTransition
	// Lineage is the run's own lineage; it may be empty, in which case the
	// session derives one from Conversation.
	Lineage    agent.ConversationLineage
	TokenCount int
	StopReason agent.StopReason
}

// RunInput is what the session hands a runExecutor for one turn sequence.
type RunInput struct {
	Conversation []agent.Message
	// DrainInbox returns the next boundary delivery for the run.
	DrainInbox func() agent.InboxDrain
	// OnToolBatchDone is called after every tool batch; nil when the session
	// has no sub-agents.
	OnToolBatchDone func(batchID string)
	// PendingSubAgents lists the sub-agents still running; nil when the session
	// has no sub-agents.
	PendingSubAgents func() []agent.PendingSubAgent
	// MaxTokens caps the run's tokens; 0 means the runner default.
	MaxTokens int
}

// runExecutor starts and manages model-in-the-loop runs. Consumer-defined to
// avoid coupling to internal/agent or cmd/steiner.
type runExecutor interface {
	// Run executes one model run over in.Conversation and returns the updated
	// conversation.
	Run(ctx context.Context, in RunInput) (RunResult, error)

	// Compact reduces the conversation through the same runner seam used by
	// normal runs.
	Compact(ctx context.Context, conversation []agent.Message, tools []provider.ToolSpec, steering string) ([]agent.Message, error)
}

// skillLoader loads a skill document by name for submit-time injection.
// Consumer-defined to avoid coupling to internal/skill or cmd/steiner.
type skillLoader interface {
	// Load returns the skill document with the given name.
	Load(context.Context, string) (skill.Skill, error)
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

// imageSessionStore scopes the image store to the active conversation.
type imageSessionStore interface {
	// BindSession switches the image store to sessionID's folder, with the
	// next img-N at least minNext.
	BindSession(sessionID string, minNext int) error
	// CopySession copies fromID's image folder to toID (used for forks).
	CopySession(fromID, toID string) error
}

// DelegateCanceller cancels active delegated agents.
type DelegateCanceller interface {
	CancelAgent(agentID string, discard bool) error
	CancelAll() error
}

// Dependencies groups the external dependencies and initial configuration
// required by an interactive session. Each field uses a consumer-defined
// interface to avoid premature coupling to concrete implementations.
type Dependencies struct {
	BaseEvents    output.EventSink
	Runner        runExecutor
	HistoryWriter historyWriter
	SessionStore  sessionStore
	SkillNames    []string
	// SkillLoader loads skill documents so an enabled skill's content can be
	// injected into the next submitted user message. Nil disables delivery:
	// enable/disable still toggles the tracked set, but no content is injected.
	// cmd/steiner wires it.
	SkillLoader       skillLoader
	Config            config.Config
	HomeDir           string
	WorkDir           string
	CompactionLogPath string
	// RecordModelSwitch records a successful model switch for popularity stats.
	RecordModelSwitch func(providerAlias, modelID string) error
	// OnEffectiveAssignmentsChanged is called after a successful profile switch
	// with the new effective assignments.
	OnEffectiveAssignmentsChanged func(config.EffectiveModelAssignments)
	DelegateCanceller             DelegateCanceller
	// ImageStore scopes image registrations to the active conversation. cmd/steiner
	// wires it; nil disables session-scoped image storage.
	ImageStore imageSessionStore
	// ResolveModel resolves a model alias to its provider and model metadata,
	// backed by the session's shared Resolver (memoized, single-flight).
	// Required wherever a resolved model's metadata (e.g. context window) is
	// needed outside the main run loop.
	ResolveModel func(alias string) (provider.ResolvedModel, error)
	// Background is the sub-agent supervisor the conversation driver consults.
	// Nil when the session has no sub-agents.
	Background agent.BackgroundAgents
	// SetCompletionSink installs the driver as the supervisor's completion
	// sink each time the session builds one, replacing a retired driver.
	SetCompletionSink func(agent.CompletionSink)
	// Clock drives the driver's completion coalescing window; nil is real time.
	Clock agent.Clock
	// MaxTokensPerEpisode caps completion tokens per conversation episode; 0 is unlimited.
	MaxTokensPerEpisode int
}
