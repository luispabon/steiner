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
// binary data has been stripped. The wording never depends on the model's vision
// capability or discovery state: the message text a stateful provider has
// already been sent must stay byte-identical when its image bytes are removed,
// so the token is frozen before the image's first delivery. visionAvailable
// names the vision sub-agent route only when one is actually configured.
func imageBlockPlaceholder(img ImageBlock, visionAvailable bool) string {
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
		if visionAvailable {
			return descriptive + fmt.Sprintf("; use the read tool, or sub_agent type \"vision\" with image_id \"%s\", to re-examine]", img.ID)
		}
		return descriptive + "; use the read tool to re-examine]"
	}

	// Legacy format for backward compat (when ID/FilePath are not set).
	if sizeStr != "" {
		return fmt.Sprintf("[image: %s %s %s]", dims, fmtStr, sizeStr)
	}
	return fmt.Sprintf("[image: %s %s]", dims, fmtStr)
}

// imageBlockPlaceholders returns the tokens the message's data-carrying images
// still need, given the tokens already frozen into it. Tokens are claimed in
// image order, one per data image, so the recorded list alone decides coverage
// and user text can never be mistaken for a frozen token.
func imageBlockPlaceholders(msg Message, visionAvailable bool) []string {
	var out []string
	frozen := len(msg.ImagePlaceholders)
	seen := 0
	for _, img := range msg.Images {
		if img.Data == "" {
			continue
		}
		seen++
		if seen <= frozen {
			continue
		}
		out = append(out, imageBlockPlaceholder(img, visionAvailable))
	}
	return out
}

// freezeImagePlaceholders appends each image's placeholder token to the
// containing message's Content before the message first reaches a provider,
// recording the tokens in Message.ImagePlaceholders. Cleanup later removes only
// the bytes, so the text a stateful provider was sent never changes. The
// recorded list makes a second pass a no-op and keeps user text that matches a
// token from being mistaken for one.
func freezeImagePlaceholders(msgs []Message, visionAvailable bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		needed := imageBlockPlaceholders(out[i], visionAvailable)
		if len(needed) == 0 {
			continue
		}
		// Record the user's own words before the content starts carrying
		// model-facing tokens. Tool and sourced messages stay unmarked.
		recordImageRewrite(&out[i])
		frozen := append([]string(nil), out[i].ImagePlaceholders...)
		for _, token := range needed {
			out[i].Content = appendContentToken(out[i].Content, token)
			frozen = append(frozen, token)
		}
		out[i].ImagePlaceholders = frozen
	}
	return out
}

// stripImagesFromMessages clears the Data field of every ImageBlock in msgs so
// the payload is not re-sent on later turns. Placeholder tokens frozen before
// delivery are left untouched; a message that never went through the freeze
// (a legacy in-memory state or an image no request delivered) gets its
// placeholders and request record here, before the data is dropped.
// visionAvailable names the vision sub-agent route only for a caller that knows
// one is configured; a child with delegation disabled passes false.
func stripImagesFromMessages(msgs []Message, visionAvailable bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		// A message that never went through the freeze gets its tokens here, and
		// records the user's words first. Frozen messages append nothing.
		needed := imageBlockPlaceholders(out[i], visionAvailable)
		if len(needed) > 0 && len(out[i].ImagePlaceholders) == 0 {
			recordImageRewrite(&out[i])
		}
		for _, token := range needed {
			out[i].Content = appendContentToken(out[i].Content, token)
		}
		out[i].Images = nil
	}
	return out
}

// stripImagesFromMessagesExceptDeferredRead strips all image data except read
// tool results, which remain available for the immediately following model request.
func stripImagesFromMessagesExceptDeferredRead(msgs []Message, visionAvailable bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionAvailable)[0]
	}
	return out
}

// stripDeferredReadImages removes read image data after the next model request
// has consumed it. It does not affect pasted images or other tool messages.
func stripDeferredReadImages(msgs []Message, visionAvailable bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if !isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]}, visionAvailable)[0]
	}
	return out
}

func isDeferredReadImageMessage(msg Message) bool {
	return msg.Role == MessageRoleTool && msg.Name == "read" && len(msg.Images) > 0 && msg.Images[0].Data != ""
}

// appendContentToken appends token to content on its own line.
func appendContentToken(content, token string) string {
	if content == "" {
		return token
	}
	return content + "\n" + token
}
