package agent

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
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
// binary data has been stripped. The wording never depends on vision capability
// or discovery state: the message text a stateful provider has already been
// sent must stay byte-identical when its image bytes are removed, so the token
// is frozen before the image's first delivery and both routes for looking at
// the image again stay available.
func imageBlockPlaceholder(img ImageBlock) string {
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
		return descriptive + fmt.Sprintf(" — use the read tool, or sub_agent type \"vision\" with image_id \"%s\", to re-examine]", img.ID)
	}

	// Legacy format for backward compat (when ID/FilePath are not set).
	if sizeStr != "" {
		return fmt.Sprintf("[image: %s %s %s]", dims, fmtStr, sizeStr)
	}
	return fmt.Sprintf("[image: %s %s]", dims, fmtStr)
}

// imageFingerprint is the provider-side identity of one delivered image. Both
// sides of the provider boundary share provider.ImageFingerprint so a recorded
// fingerprint can be checked against the bytes a message still carries.
func imageFingerprint(img ImageBlock) string {
	return provider.ImageFingerprint(img.MediaType, img.Data)
}

// imagePlaceholders returns the placeholder tokens the message's data-carrying
// images need, in image order and with duplicates counted. Image order keeps
// the appended text deterministic.
func imagePlaceholders(msg Message) (order []string, counts map[string]int) {
	counts = make(map[string]int, len(msg.Images))
	for _, img := range msg.Images {
		if img.Data == "" {
			continue
		}
		token := imageBlockPlaceholder(img)
		if counts[token] == 0 {
			order = append(order, token)
		}
		counts[token]++
	}
	return order, counts
}

// countTokens counts the occurrences of each token.
func countTokens(tokens []string) map[string]int {
	counts := make(map[string]int, len(tokens))
	for _, token := range tokens {
		counts[token]++
	}
	return counts
}

// freezeImagePlaceholders appends each image's placeholder token to the
// containing message's Content before the message first reaches a provider. The
// tokens are recorded in Message.ImagePlaceholders, so cleanup never appends
// twice and user text that happens to match a token is never mistaken for a
// frozen one. This runs before delivery because the sent text must not change
// when image bytes are later removed.
func freezeImagePlaceholders(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		order, want := imagePlaceholders(out[i])
		if len(order) == 0 {
			continue
		}
		// Record the user's own words before the content starts carrying
		// model-facing tokens. Tool and sourced messages stay unmarked.
		recordImageRewrite(&out[i])
		have := countTokens(out[i].ImagePlaceholders)
		frozen := append([]string(nil), out[i].ImagePlaceholders...)
		for _, token := range order {
			for have[token] < want[token] {
				out[i].Content = appendContentToken(out[i].Content, token)
				frozen = append(frozen, token)
				have[token]++
			}
		}
		out[i].ImagePlaceholders = frozen
	}
	return out
}

// deliveredImageFingerprints returns recorded extended with one fingerprint per
// image carrying data. It keeps multiplicity, so the same image pasted twice
// records two fingerprints, and repeated calls add nothing.
func deliveredImageFingerprints(recorded []string, images []ImageBlock) []string {
	want := make(map[string]int, len(images))
	order := make([]string, 0, len(images))
	for _, img := range images {
		if img.Data == "" {
			continue
		}
		fingerprint := imageFingerprint(img)
		if want[fingerprint] == 0 {
			order = append(order, fingerprint)
		}
		want[fingerprint]++
	}
	if len(want) == 0 {
		return recorded
	}
	have := make(map[string]int, len(recorded))
	for _, fingerprint := range recorded {
		have[fingerprint]++
	}
	out := recorded
	for _, fingerprint := range order {
		for have[fingerprint] < want[fingerprint] {
			if out == nil {
				out = make([]string, 0, len(order))
			}
			out = append(out, fingerprint)
			have[fingerprint]++
		}
	}
	return out
}

// stripImagesFromMessages clears the Data field of every ImageBlock in msgs so
// the payload is not re-sent on later turns. Placeholder tokens frozen before
// delivery are left untouched; a message that never went through the freeze
// (a legacy in-memory state) gets its placeholders and request record here,
// before the data is dropped.
func stripImagesFromMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		order, want := imagePlaceholders(out[i])
		if len(order) == 0 {
			out[i].Images = nil
			continue
		}
		if len(out[i].ImagePlaceholders) == 0 {
			recordImageRewrite(&out[i])
		}
		have := countTokens(out[i].ImagePlaceholders)
		for _, token := range order {
			for have[token] < want[token] {
				out[i].Content = appendContentToken(out[i].Content, token)
				have[token]++
			}
		}
		out[i].Images = nil
	}
	return out
}

// stripImagesFromMessagesExceptDeferredRead strips all image data except read
// tool results, which remain available for the immediately following model request.
func stripImagesFromMessagesExceptDeferredRead(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]})[0]
	}
	return out
}

// stripDeferredReadImages removes read image data after the next model request
// has consumed it. It does not affect pasted images or other tool messages.
func stripDeferredReadImages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if !isDeferredReadImageMessage(out[i]) {
			continue
		}
		out[i] = stripImagesFromMessages([]Message{out[i]})[0]
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
