package agent

import (
	"context"
	"slices"
)

// Submit queues a user prompt. It lifts a hold and wakes the loop; the prompt
// is delivered together with everything else buffered. Empty prompts and
// prompts after Close are ignored.
func (d *ConversationDriver) Submit(text string, images []ImageBlock, _ SubmitMeta) {
	if text == "" && len(images) == 0 {
		return
	}
	d.mu.Lock()
	if d.closing {
		d.unlockEmit()
		return
	}
	d.users = append(d.users, SteerMessage{Text: text, Images: images})
	d.held = false
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
	}
	d.unlockEmit()
	d.signalWake()
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
	items := d.users
	d.users = nil
	items = append(items, d.opts.Steers.Drain()...)
	completions := d.completions
	d.completions = nil
	if len(items) == 0 && len(completions) == 0 {
		return InboxDrain{}
	}

	parts := prefix
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
		return InboxDrain{}
	}
	d.disarmWindowLocked()
	wake := len(items) > 0
	if !d.exhausted {
		wake = wake || slices.ContainsFunc(completions, func(c SubAgentCompletion) bool { return !c.Quiet })
	}
	return InboxDrain{Message: &msg, Wake: wake, UserText: parts.UserText}
}
