package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

	if !p.conversationHasPastedImages(state.Conversation) {
		return false
	}

	newMessages := make([]Message, len(state.Conversation))
	copy(newMessages, state.Conversation)

	for i := range newMessages {
		if newMessages[i].Role != MessageRoleUser {
			continue
		}
		if len(newMessages[i].Images) == 0 {
			continue
		}

		p.processImagesInMessage(ctx, &newMessages[i], vc.SubAgentConfigured())
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

// processImagesInMessage processes (routes or strips) all images in a message.
// Captures the original message content once to avoid subsequent images inheriting
// earlier images' appended descriptions in the vision task prompt.
func (p *turnProgressor) processImagesInMessage(ctx context.Context, msg *Message, subAgentConfigured bool) {
	originalContent := msg.Content
	newImages := make([]ImageBlock, len(msg.Images))
	copy(newImages, msg.Images)

	for j := range newImages {
		if newImages[j].Data == "" {
			continue
		}

		if subAgentConfigured {
			if err := p.routeImageToVision(ctx, msg, &newImages[j], originalContent); err != nil {
				p.stripImageFallback(msg, &newImages[j])
			}
		} else {
			p.stripImageFallback(msg, &newImages[j])
		}
	}

	msg.Images = nil
}

// routeImageToVision calls the vision tool for a single image and appends
// the result as an inline description block to the message's Content.
// originalContent is the message's original content before any images were processed.
func (p *turnProgressor) routeImageToVision(ctx context.Context, msg *Message, img *ImageBlock, originalContent string) error {
	if img.ID == "" {
		return fmt.Errorf("image has no ID")
	}

	task := wrapVisionTask(originalContent)

	raw, err := p.request.Executor.Execute(ctx, "vision", map[string]any{
		"task":     task,
		"image_id": img.ID,
	})
	if err != nil {
		return fmt.Errorf("vision call: %w", err)
	}

	env := normalizeToolResult(raw)
	if env.Content == "" {
		return fmt.Errorf("vision tool returned empty result")
	}

	var result struct {
		AgentID string `json:"agent_id"`
		Output  string `json:"output"`
	}
	if err := json.Unmarshal([]byte(env.Content), &result); err != nil {
		return fmt.Errorf("unmarshal vision result: %w", err)
	}

	if result.Output == "" {
		return fmt.Errorf("vision tool output is empty")
	}

	description := fmt.Sprintf("[Image %s — you cannot view images directly. A vision assistant examined it and reports:\n%s\nFor further detail about this image, call follow_up with agent_id \"%s\".]",
		img.ID, result.Output, result.AgentID)

	if msg.Content == "" {
		msg.Content = description
	} else {
		msg.Content = msg.Content + "\n" + description
	}

	img.Data = ""

	return nil
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
"%s"
Bias your detail toward that request, but describe comprehensively.`

	return fmt.Sprintf(template, userText)
}
