package agent

import (
	"fmt"
	"strings"
)

// formatSize returns a human-readable size string like "2KB" or "1.5MB".
func formatSize(sizeBytes int) string {
	if sizeBytes <= 0 {
		return ""
	}
	if sizeBytes >= 1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(sizeBytes)/1024/1024)
	}
	return fmt.Sprintf("%dKB", sizeBytes/1024)
}

// imageBlockPlaceholder returns a compact text token describing an image whose
// binary data has been stripped. Wording adapts based on whether the model can see images.
// visionState: the capability state for the current alias (Capable/Incapable/Unknown).
// subAgentConfigured: whether a vision sub-agent is configured (for routing).
func imageBlockPlaceholder(img ImageBlock, visionState VisionState, subAgentConfigured bool) string {
	var dims string
	if img.Width > 0 && img.Height > 0 {
		dims = fmt.Sprintf("%dx%d", img.Width, img.Height)
	} else {
		dims = "?"
	}
	var fmtStr string
	if mt := img.MediaType; strings.Contains(mt, "/") {
		fmtStr = mt[strings.LastIndex(mt, "/")+1:]
	} else {
		fmtStr = mt
	}
	sizeStr := formatSize(img.SizeBytes)

	// New format when ID and FilePath are both set.
	if img.ID != "" && img.FilePath != "" {
		descriptive := ""
		if sizeStr != "" {
			descriptive = fmt.Sprintf("[image %s: %s %s %s %s",
				img.ID, img.FilePath, dims, fmtStr, sizeStr)
		} else {
			descriptive = fmt.Sprintf("[image %s: %s %s %s",
				img.ID, img.FilePath, dims, fmtStr)
		}

		var suffix string
		switch {
		case visionState == VisionIncapable && subAgentConfigured:
			// Non-vision with sub-agent: advertise follow_up, not read.
			suffix = " — use follow_up with the agent_id from the image analysis]"
		case visionState == VisionIncapable:
			// Non-vision without sub-agent: no re-examine hint.
			suffix = "]"
		default:
			suffix = fmt.Sprintf(" — use vision tool with image_id \"%s\" or read tool to re-examine]", img.ID)
		}
		return descriptive + suffix
	}

	// Legacy format for backward compat (when ID/FilePath are not set).
	if sizeStr != "" {
		return fmt.Sprintf("[image: %s %s %s]", dims, fmtStr, sizeStr)
	}
	return fmt.Sprintf("[image: %s %s]", dims, fmtStr)
}

// stripImagesFromMessages clears the Data field of every ImageBlock in msgs
// whose Data is non-empty and appends a placeholder token to the containing
// message's Content so the model retains awareness of the image without the
// full base64 payload being re-sent on subsequent turns.
// visionState: the capability state for the current alias (Capable/Incapable/Unknown).
// subAgentConfigured: whether a vision sub-agent is configured (for routing).
func stripImagesFromMessages(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		imgs := make([]ImageBlock, len(out[i].Images))
		copy(imgs, out[i].Images)
		for j := range imgs {
			if imgs[j].Data == "" {
				continue
			}
			placeholder := imageBlockPlaceholder(imgs[j], visionState, subAgentConfigured)
			imgs[j].Data = ""
			if out[i].Content == "" {
				out[i].Content = placeholder
			} else {
				out[i].Content = out[i].Content + "\n" + placeholder
			}
		}
		out[i].Images = nil
	}
	return out
}

// stripImagesFromMessagesExceptDeferredRead strips all image data except read
// tool results, which remain available for the immediately following model request.
func stripImagesFromMessagesExceptDeferredRead(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionState, subAgentConfigured)[0]
	}
	return out
}

// stripDeferredReadImages removes read image data after the next model request
// has consumed it. It does not affect pasted images or other tool messages.
func stripDeferredReadImages(msgs []Message, visionState VisionState, subAgentConfigured bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if !isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionState, subAgentConfigured)[0]
	}
	return out
}

func isDeferredReadImageMessage(msg Message) bool {
	return msg.Role == MessageRoleTool && msg.Name == "read" && len(msg.Images) > 0 && msg.Images[0].Data != ""
}
