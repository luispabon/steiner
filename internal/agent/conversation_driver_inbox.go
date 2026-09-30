package agent

import (
	"context"
	"slices"

	"github.com/luispabon/steiner/internal/output"
)

// Submit queues a user prompt. It lifts a hold and wakes the loop; the prompt
// is delivered together with everything else buffered. Empty prompts and
// prompts after Close are ignored.
func (d *ConversationDriver) Submit(text string, images []ImageBlock, meta SubmitMeta) {
	if text == "" && len(images) == 0 {
		return
	}
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return
	}
	d.users = append(d.users, SteerMessage{Text: text, Images: images})
	for _, block := range meta.SkillBlocks {
		if !slices.Contains(d.userBlocks, block) {
			d.userBlocks = append(d.userBlocks, block)
		}
	}
	d.held = false
	d.steersParked = false
	d.resetEpisodeLocked()
	d.unlockEmit()
	d.signalWake()
}

// NotifySteer wakes the loop after text was added to the steer queue. Like a
// prompt, a steer lifts a hold. The text stays in the queue until drained.
func (d *ConversationDriver) NotifySteer() {
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return
	}
	if d.opts.Steers.Len() > 0 {
		d.held = false
		d.steersParked = false
	}
	d.unlockEmit()
	d.signalWake()
}

// DetachSteers stops the driver reading the steer queue. A replaced driver
// calls it so it cannot take steers meant for its successor or a oneshot run.
func (d *ConversationDriver) DetachSteers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opts.Steers = nil
}

// AttachSteers makes the driver read q again after a DetachSteers.
func (d *ConversationDriver) AttachSteers(q *SteerQueue) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opts.Steers = q
}

// DeliverCompletions implements CompletionSink. Completions are buffered until
// the next drain; while held they accumulate without waking the model.
func (d *ConversationDriver) DeliverCompletions(cs []SubAgentCompletion) {
	if len(cs) == 0 {
		return
	}
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return
	}
	d.completions = append(d.completions, cs...)
	d.armWindowLocked(cs)
	d.unlockEmit()
	d.signalWake()
}

// RequestCompaction queues fn to run once the driver is not generating.
// Completions arriving meanwhile wait until it finishes.
func (d *ConversationDriver) RequestCompaction(fn func(ctx context.Context, conv []Message) ([]Message, ConversationLineage, error)) {
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return
	}
	d.compactions = append(d.compactions, fn)
	d.unlockEmit()
	d.signalWake()
}

// drainForRun is the DrainInbox handed to the runner. While held or closing
// nothing is delivered: items stay buffered for the next sequence or Close.
func (d *ConversationDriver) drainForRun() InboxDrain {
	d.mu.Lock()
	defer d.unlockEmit()
	if d.held || d.closing {
		return InboxDrain{}
	}
	return d.drainLocked(DeliveryParts{})
}

// drainLocked takes every buffered prompt, steer and completion into one
// message, prefixed by prefix. It returns the zero value when nothing was
// buffered. Steers.Drain is atomic, so a take-back racing this drain gets each
// steer or the drain does, never both.
//
// MarkDelivered runs before the runner appends the message. That is safe
// because the runner appends synchronously on the same goroutine before any
// other inbox access, and Pending is read after MarkDelivered so an agent
// whose result is in this message is never also listed as pending.
func (d *ConversationDriver) drainLocked(prefix DeliveryParts) InboxDrain {
	drain, _ := d.drainItemsLocked(prefix)
	return drain
}

// drainItemsLocked is drainLocked that also returns the text of the steers
// taken from the queue, for hosts that must announce them.
func (d *ConversationDriver) drainItemsLocked(prefix DeliveryParts) (InboxDrain, string) {
	items := d.users
	blocks := d.userBlocks
	d.users = nil
	d.userBlocks = nil
	steers := d.opts.Steers.Drain()
	items = append(items, steers...)
	completions := d.completions
	d.completions = nil
	if len(items) == 0 && len(completions) == 0 {
		return InboxDrain{}, ""
	}

	parts := prefix
	if len(blocks) > 0 {
		parts.SkillBlocks = blocks
	}
	parts.Completions = completions
	if bg := d.opts.Background; bg != nil {
		if len(completions) > 0 {
			ids := make([]string, len(completions))
			for i, c := range completions {
				ids[i] = c.ParentCallID
			}
			bg.MarkDelivered(ids)
		}
		parts.Pending = bg.Pending()
	}
	if len(items) > 0 {
		merged := MergeSteers(items)
		parts.UserText = merged.Content
		parts.Images = merged.Images
	}
	msg, ok := BuildDeliveryMessage(parts)
	if !ok {
		return InboxDrain{}, ""
	}
	d.disarmWindowLocked()
	if len(completions) > 0 {
		delivered := make([]output.DeliveredSubAgent, len(completions))
		for i, c := range completions {
			delivered[i] = output.DeliveredSubAgent{
				AgentID:      c.AgentID,
				AgentType:    c.AgentType,
				Status:       c.Status,
				ParentCallID: c.ParentCallID,
				DurationMs:   c.Duration.Milliseconds(),
			}
		}
		d.pendingEvents = append(d.pendingEvents, output.NewSubAgentsDeliveredEvent(delivered))
	}
	wake := len(items) > 0
	if !d.exhausted {
		wake = wake || slices.ContainsFunc(completions, func(c SubAgentCompletion) bool { return !c.Quiet })
	}
	steerText := ""
	if len(steers) > 0 {
		steerText = MergeSteers(steers).Content
	}
	return InboxDrain{Message: &msg, Wake: wake, UserText: parts.UserText}, steerText
}
