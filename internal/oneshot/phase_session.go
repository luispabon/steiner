package oneshot

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// phaseSession is the single session a phase persists to. It is created before
// the phase runs; every later save updates the same record.
type phaseSession struct {
	store SessionStore
	mu    sync.Mutex
	sess  session.Session
}

func (o *Orchestrator) newPhaseSession(phase Phase, modelAlias string) (*phaseSession, error) {
	sess, err := session.NewSession(strings.TrimSpace(modelAlias), agent.ConversationLineage{}, o.deps.Identity.ID)
	if err != nil {
		return nil, fmt.Errorf("create phase session: %w", err)
	}
	sess = sess.WithTitle(fmt.Sprintf("%s phase: %s", phase, o.deps.Task))
	ps := &phaseSession{store: o.deps.SessionStore, sess: sess}
	if err := ps.store.Save(sess); err != nil {
		return nil, fmt.Errorf("save phase session: %w", err)
	}
	return ps, nil
}

func (p *phaseSession) id() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sess.ID
}

// save persists a driver snapshot to the phase session.
func (p *phaseSession) save(_ context.Context, snap agent.DriverSnapshot) error {
	return p.saveLineage(lineageOf(snap.Lineage, snap.Conversation))
}

// saveResult does the final save from a finished run. A result without any
// conversation leaves what the driver already saved untouched.
func (p *phaseSession) saveResult(result RunResult) error {
	lineage := lineageOf(result.Lineage, result.Conversation)
	if lineage.Empty() {
		return nil
	}
	return p.saveLineage(lineage)
}

func (p *phaseSession) saveLineage(lineage agent.ConversationLineage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.sess.WithLineage(lineage)
	if err := p.store.Save(next); err != nil {
		return fmt.Errorf("save phase session: %w", err)
	}
	p.sess = next
	return nil
}

func lineageOf(lineage agent.ConversationLineage, conversation []agent.Message) agent.ConversationLineage {
	if !lineage.Empty() || len(conversation) == 0 {
		return lineage
	}
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{ID: 1, Messages: cloneAgentMessages(conversation)},
		},
		NextGenerationID: 2,
	}
}
