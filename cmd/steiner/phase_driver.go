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
