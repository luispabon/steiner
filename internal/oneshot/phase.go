package oneshot

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

const (
	advisorPlanBudget      = 4
	advisorImplementBudget = 3
	advisorReviewBudget    = 4
)

// PhaseRunnerFactory builds a fresh phase runner for each oneshot phase.
type PhaseRunnerFactory interface {
	NewPhaseRunner(ctx context.Context, phase Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (PhaseRunner, error)
}

// PhaseRunnerFactoryFunc adapts a function to PhaseRunnerFactory.
type PhaseRunnerFactoryFunc func(ctx context.Context, phase Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (PhaseRunner, error)

// NewPhaseRunner adapts f to the PhaseRunnerFactory interface.
func (f PhaseRunnerFactoryFunc) NewPhaseRunner(ctx context.Context, phase Phase, modelAlias string, approver tool.ApprovalResponder, advisorCfg config.AdvisorConfig) (PhaseRunner, error) {
	return f(ctx, phase, modelAlias, approver, advisorCfg)
}

// SessionStore persists phase sessions for later resume and review.
type SessionStore interface {
	Save(session.Session) error
}

// InterruptFactory derives the run-wide interrupt context.
type InterruptFactory func(parent context.Context) (context.Context, context.CancelFunc)

// Dependencies captures the orchestrator inputs for an autonomous run.
type Dependencies struct {
	ProjectRoot      string
	Identity         RunIdentity
	Task             string
	Config           config.Config
	ManifestStore    *ManifestStore
	SessionStore     SessionStore
	RunnerFactory    PhaseRunnerFactory
	Events           output.EventSink
	DrainSteers      func() []agent.SteerMessage
	InterruptFactory InterruptFactory
}

// Orchestrator owns the outer oneshot phase loop.
type Orchestrator struct {
	deps Dependencies
}

// NewOrchestrator validates deps and constructs an orchestrator.
func NewOrchestrator(deps Dependencies) (*Orchestrator, error) {
	if strings.TrimSpace(deps.ProjectRoot) == "" {
		return nil, fmt.Errorf("orchestrator project root is required")
	}
	if strings.TrimSpace(deps.Identity.ID) == "" {
		return nil, fmt.Errorf("orchestrator run id is required")
	}
	if strings.TrimSpace(deps.Task) == "" {
		return nil, fmt.Errorf("orchestrator task is required")
	}
	if deps.ManifestStore == nil {
		deps.ManifestStore = NewManifestStore(deps.Identity.ManifestPath(deps.ProjectRoot))
	}
	if deps.SessionStore == nil {
		return nil, fmt.Errorf("orchestrator session store is required")
	}
	if deps.RunnerFactory == nil {
		return nil, fmt.Errorf("orchestrator runner factory is required")
	}
	if deps.InterruptFactory == nil {
		deps.InterruptFactory = defaultInterruptFactory
	}
	return &Orchestrator{deps: deps}, nil
}

func phaseOrder() []Phase {
	return []Phase{PhasePlan, PhaseImplement, PhaseReview}
}

func phaseSpec(phase Phase) (PhaseSpec, bool) {
	switch phase {
	case PhasePlan:
		return PhaseSpec{Phase: phase, AdvisorBudget: advisorPlanBudget}, true
	case PhaseImplement:
		return PhaseSpec{Phase: phase, AdvisorBudget: advisorImplementBudget}, true
	case PhaseReview:
		return PhaseSpec{Phase: phase, AdvisorBudget: advisorReviewBudget}, true
	default:
		return PhaseSpec{}, false
	}
}

// PhaseSpec captures the bounded execution policy for a phase.
type PhaseSpec struct {
	Phase         Phase
	AdvisorBudget int
}

func phaseModelAlias(cfg config.Config, phase Phase) string {
	alias := strings.TrimSpace(cfg.OneShot.Models[string(phase)])
	if alias == "" {
		return strings.TrimSpace(cfg.DefaultModel)
	}
	return alias
}

func phaseAdvisorConfig(cfg config.Config, phase Phase) config.AdvisorConfig {
	advisor := cfg.Advisor
	advisor.Enabled = true
	if spec, ok := phaseSpec(phase); ok {
		advisor.MaxUsesPerRun = spec.AdvisorBudget
	}
	return advisor
}

func phaseModelSnapshot(cfg config.Config) ModelSnapshot {
	snapshot := ModelSnapshot{
		DefaultModel: strings.TrimSpace(cfg.DefaultModel),
		PhaseModels:  make(map[Phase]string, len(phaseOrder())),
	}
	for _, phase := range phaseOrder() {
		snapshot.PhaseModels[phase] = phaseModelAlias(cfg, phase)
	}
	return snapshot
}

func phaseSeedConversation(identity RunIdentity, task, phase, worktreePath, planningPath string) []string {
	parts := []string{
		fmt.Sprintf("Task: %s", strings.TrimSpace(task)),
		fmt.Sprintf("Phase: %s", phase),
		fmt.Sprintf("Run ID: %s", identity.ID),
		fmt.Sprintf("Branch: %s", identity.BranchName()),
		fmt.Sprintf("Worktree: %s", worktreePath),
		fmt.Sprintf("Planning artifacts: %s/overview.md, %s/plan.yaml", planningPath, planningPath),
	}
	return parts
}

func defaultInterruptFactory(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}
