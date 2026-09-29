package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
)

// phaseDriverHost is what a phase needs from its runtime to host a
// ConversationDriver.
type phaseDriverHost struct {
	run        agent.DriverRunFunc
	record     *driverRunRecord
	background agent.BackgroundAgents
	// setSink installs the driver as the sink of background completions.
	setSink func(agent.CompletionSink)
	// shutdown cancels every background sub-agent and waits for them.
	shutdown            func(ctx context.Context, cause delegation.CancelCause)
	events              output.EventSink
	maxTokensPerEpisode int
	// steers is the queue the driver reads user steers from; nil when headless.
	steers *agent.SteerQueue
	// canceller cancels sub-agents on the user's behalf.
	canceller phaseCanceller
}

// phaseCanceller cancels a phase's sub-agents.
type phaseCanceller interface {
	CancelAgent(agentID string, discard bool) error
	CancelAll() error
}

// phaseControl is the user's handle on a running phase: its driver takes
// prompts, steers and stop-turn, its supervisor takes sub-agent cancellation.
type phaseControl struct {
	driver    *agent.ConversationDriver
	canceller phaseCanceller
}

func (c phaseControl) Submit(text string, images []agent.ImageBlock) {
	c.driver.Submit(text, images, agent.SubmitMeta{})
}

func (c phaseControl) NotifySteer() { c.driver.NotifySteer() }

func (c phaseControl) StopTurn() { c.driver.StopTurn() }

func (c phaseControl) CancelAgent(agentID string, discard bool) error {
	if c.canceller == nil {
		return errors.New("no active delegate cancellation available")
	}
	return c.canceller.CancelAgent(agentID, discard)
}

func (c phaseControl) CancelAll() error {
	if c.canceller == nil {
		return errors.New("no active delegate cancellation available")
	}
	return c.canceller.CancelAll()
}

// runPhaseOnDriver runs one phase on a ConversationDriver and returns once the
// conversation is quiescent: idle with no sub-agent pending. Every failure
// path shuts the sub-agents down before the driver settles their results, and
// the driver is always closed, saving a final snapshot, before this returns.
func runPhaseOnDriver(ctx context.Context, in oneshot.PhaseRunInput, host phaseDriverHost) (oneshot.RunResult, error) {
	history, prompt, ok := splitPhasePrompt(in.Conversation)
	if !ok {
		return oneshot.RunResult{}, errors.New("run phase: conversation must end with a user message")
	}

	hostCtx, failHost := context.WithCancelCause(ctx)
	defer failHost(nil)
	host.record.onFailure = failHost

	driver := agent.NewConversationDriver(agent.DriverOptions{
		Run:                 host.run,
		Background:          host.background,
		Steers:              host.steers,
		Save:                in.Session.Save,
		Events:              phaseDriverEvents(host.events),
		MaxTokensPerEpisode: host.maxTokensPerEpisode,
	}, history, agent.ConversationLineage{})
	if host.setSink != nil {
		host.setSink(driver)
	}
	// The loop outlives phase cancellation so the shutdown below can still hand
	// it the children's cancelled results; Close is what stops it.
	driver.Start(context.WithoutCancel(ctx))
	if in.RegisterControl != nil {
		// The release runs when this function returns, after Close, so control stays with
		// this phase until its driver has settled.
		release := in.RegisterControl(phaseControl{driver: driver, canceller: host.canceller})
		defer release()
	}
	driver.Submit(prompt.Content, prompt.Images, agent.SubmitMeta{})

	waitErr := driver.WaitQuiescent(hostCtx)
	runErr := host.record.runFailure()
	var failure error
	switch {
	case errors.Is(waitErr, agent.ErrEpisodeBudgetExhausted):
		failure = fmt.Errorf("run phase: %w", waitErr)
	case runErr != nil:
		failure = runErr
	case ctx.Err() != nil:
		failure = ctx.Err()
	case waitErr != nil:
		failure = waitErr
	}

	settleCtx := context.WithoutCancel(ctx)
	if failure != nil {
		host.shutdown(settleCtx, delegation.CancelCauseSystem)
	}
	driver.Close(settleCtx)
	if failure != nil {
		return oneshot.RunResult{}, failure
	}

	snap := driver.Snapshot()
	tokens, stop := host.record.totals()
	return oneshot.RunResult{
		Conversation: snap.Conversation,
		Reply:        lastAssistantReply(snap.Conversation),
		Lineage:      snap.Lineage,
		TokenCount:   tokens,
		StopReason:   stop,
	}, nil
}

// splitPhasePrompt separates the trailing user message, which the driver
// submits, from the history before it.
func splitPhasePrompt(conversation []agent.Message) ([]agent.Message, agent.Message, bool) {
	if len(conversation) == 0 || conversation[len(conversation)-1].Role != agent.MessageRoleUser {
		return nil, agent.Message{}, false
	}
	last := len(conversation) - 1
	return conversation[:last], conversation[last], true
}

// phaseDriverEvents forwards driver events except conversation state, which
// describes the phase's private conversation and must not be mistaken for the
// launching session's.
func phaseDriverEvents(sink output.EventSink) output.EventSink {
	if sink == nil {
		return nil
	}
	return output.SinkFunc(func(event output.Event) {
		if event.Type == output.EventTypeConversationState {
			return
		}
		sink.Emit(event)
	})
}
