package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/luispabon/steiner/internal/output"
)

// handleImagesForVision routes or strips pasted images for a model that
// cannot view images, mutating state in place. Returns mutated=true if any
// change was made to state.Conversation/state.Lineage.
func (p *turnProgressor) handleImagesForVision(ctx context.Context, state *RunState) bool {
	vc := p.request.VisionCapabilities
	if vc == nil {
		return false
	}

	alias := p.request.ResolvedModel.Alias
	if vc.Get(alias) != VisionIncapable {
		return false
	}

	if !p.conversationHasPastedImages(state.Conversation) && !p.conversationHasDeferredReadImages(state.Conversation) {
		return false
	}

	if vc.TakeNotify(alias) {
		disposition := "routed"
		if !vc.SubAgentConfigured() {
			disposition = "stripped"
		}
		emitEvent(p.request.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
			Severity: "info",
			Kind:     "vision_discovery",
			Message:  fmt.Sprintf("model %s cannot view images; images will be %s", alias, disposition),
		}))
	}

	// Deliberately copies state.Conversation rather than following the
	// SummaryPrefixStrippedMessages() pattern used elsewhere: this is a fresh
	// user turn with no summary prefix to worry about double-counting, and the
	// images must survive the copy so routing/stripping can use them. Revisit
	// if images ever need to survive behind a post-compaction summary.
	newMessages := make([]Message, len(state.Conversation))
	copy(newMessages, state.Conversation)
	originalMessages := make([]Message, len(state.Conversation))
	copy(originalMessages, state.Conversation)

	for i := range newMessages {
		if !eligibleVisionMessage(newMessages[i]) {
			continue
		}

		p.processImagesInMessage(ctx, &newMessages[i], vc.SubAgentConfigured(), visionTaskContent(originalMessages, i))
	}
	state.Conversation = newMessages
	state.Lineage = state.Lineage.WithCurrentMessages(newMessages)

	return true
}

// conversationHasPastedImages scans for user-role messages with unstripped images.
func (p *turnProgressor) conversationHasPastedImages(msgs []Message) bool {
	for _, msg := range msgs {
		if msg.Role != MessageRoleUser {
			continue
		}
		for _, img := range msg.Images {
			if img.Data != "" {
				return true
			}
		}
	}
	return false
}

func (p *turnProgressor) conversationHasDeferredReadImages(msgs []Message) bool {
	for _, msg := range msgs {
		if isDeferredReadImageMessage(msg) {
			return true
		}
	}
	return false
}

func eligibleVisionMessage(msg Message) bool {
	return len(msg.Images) > 0 && (msg.Role == MessageRoleUser || (msg.Role == MessageRoleTool && msg.Name == "read"))
}

// processImagesInMessage processes (routes or strips) all images in a message.
// Captures the task content once to avoid subsequent images inheriting earlier
// images' appended descriptions in the vision task prompt.
func (p *turnProgressor) processImagesInMessage(ctx context.Context, msg *Message, subAgentConfigured bool, userText string) {
	originalContent := userText
	newImages := make([]ImageBlock, len(msg.Images))
	copy(newImages, msg.Images)

	for j := range newImages {
		if newImages[j].Data == "" {
			continue
		}

		if subAgentConfigured {
			if err := p.routeImageToVision(ctx, msg, &newImages[j], originalContent); err != nil {
				slog.Error("vision routing failed", "image_id", newImages[j].ID, "error", err)
				emitEvent(p.request.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
					Severity: "warn",
					Kind:     "vision_routing_failed",
					Message:  fmt.Sprintf("image %s routing failed: %s", newImages[j].ID, err),
				}))
				p.stripImageFallback(msg, &newImages[j])
			}
		} else {
			p.stripImageFallback(msg, &newImages[j])
		}
	}

	msg.Images = nil
}

// visionTaskContent returns the user request associated with a message. Read
// tool results contain image data but their content is tool output, so use the
// nearest preceding user message as the vision task context.
func visionTaskContent(messages []Message, index int) string {
	message := messages[index]
	if message.Role == MessageRoleTool && message.Name == "read" {
		if user, ok := latestUserMessage(messages[:index]); ok {
			return user.Content
		}
	}
	return message.Content
}

// routeImageToVision calls the vision tool for a single image and appends
// the result as an inline description block to the message's Content.
// originalContent is the relevant parent user request, or the message content
// when no parent user request is available.
func (p *turnProgressor) routeImageToVision(ctx context.Context, msg *Message, img *ImageBlock, originalContent string) error {
	if img.ID == "" {
		return fmt.Errorf("image has no ID")
	}

	task := wrapVisionTask(originalContent)
	args := map[string]any{
		"task":     task,
		"image_id": img.ID,
	}
	callID := fmt.Sprintf("vision-auto-%s", img.ID)

	emitEvent(p.request.Events, output.NewToolCallStartedEvent(0, "vision", callID, args))

	raw, err := p.request.Executor.Execute(ctx, "vision", callID, args)
	if err != nil {
		return p.failVisionCall(callID, err)
	}

	env := normalizeToolResult(raw)
	if projected, ok := projectedToolResult(resultValue(raw)); ok {
		env.Content = projected
		env.Projected = true
	}
	if env.Content == "" {
		return p.failVisionCall(callID, fmt.Errorf("vision tool returned empty result"))
	}

	var result struct {
		Continuation *struct {
			AgentID string `json:"agent_id"`
		} `json:"continuation"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(env.Content), &result); err != nil {
		emitEvent(p.request.Events, output.NewToolCallFinishedEvent(0, "vision", callID, "", err))
		return fmt.Errorf("unmarshal vision result: %w", err)
	}

	if result.Output == "" {
		return p.failVisionCall(callID, fmt.Errorf("vision tool output is empty"))
	}

	description := fmt.Sprintf("[Image %s — you cannot view images directly. A vision assistant examined it and reports:\n%s",
		img.ID, result.Output)
	if result.Continuation != nil && result.Continuation.AgentID != "" {
		description += fmt.Sprintf("\nFor further detail about this image, call follow_up with agent_id \"%s\".", result.Continuation.AgentID)
	}
	description += "]"

	if msg.Content == "" {
		msg.Content = description
	} else {
		msg.Content = msg.Content + "\n" + description
	}

	img.Data = ""

	emitEvent(p.request.Events, output.NewToolCallFinishedEvent(0, "vision", callID, env.Content, nil))

	emitEvent(p.request.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Severity: "info",
		Kind:     "vision_routed",
		Message:  fmt.Sprintf("image %s routed to vision assistant: %s", img.ID, truncateForLog(result.Output, 200)),
	}))

	return nil
}

// failVisionCall emits a finished event for the failed vision call and
// returns the wrapped error.
func (p *turnProgressor) failVisionCall(callID string, err error) error {
	emitEvent(p.request.Events, output.NewToolCallFinishedEvent(0, "vision", callID, "", err))
	return fmt.Errorf("vision call: %w", err)
}

// truncateForLog shortens s to at most max runes for a compact diagnostic
// line, appending an ellipsis marker when truncated.
func truncateForLog(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// stripImageFallback clears the image data and appends a minimal fallback note.
func (p *turnProgressor) stripImageFallback(msg *Message, img *ImageBlock) {
	fallback := "[Image could not be processed for a non-vision model]"
	if msg.Content == "" {
		msg.Content = fallback
	} else {
		msg.Content = msg.Content + "\n" + fallback
	}
	img.Data = ""
}

// wrapVisionTask wraps the user's request into a task string for the vision
// sub-agent, instructing it to describe the image in detail.
func wrapVisionTask(userText string) string {
	const template = `The main agent cannot see images and will rely entirely on your description.
Describe the attached image in full, exact detail — layout, text (quoted verbatim),
colours, dimensions, and anything notable. The user's request to the main agent was:
%s
Bias your detail toward that request, but describe comprehensively.`

	return fmt.Sprintf(template, strconv.Quote(userText))
}
