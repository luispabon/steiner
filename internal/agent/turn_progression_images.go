package agent

import (
	"crypto/sha256"
	"encoding/hex"
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
// binary data has been stripped. The wording is frozen before the image's first
// provider delivery and never depends on vision capability or discovery state:
// the message text a stateful provider has already been sent must stay
// byte-identical when its image bytes are removed.
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
		return descriptive + " — use read tool to re-examine]"
	}

	// Legacy format for backward compat (when ID/FilePath are not set).
	if sizeStr != "" {
		return fmt.Sprintf("[image: %s %s %s]", dims, fmtStr, sizeStr)
	}
	return fmt.Sprintf("[image: %s %s]", dims, fmtStr)
}

// imageFingerprint is the provider-side identity of one delivered image:
// SHA-256 over its media type, a NUL separator, and its base64 payload. It is
// the value a stateful provider records as sent, and it never exposes the
// payload itself.
func imageFingerprint(img ImageBlock) string {
	sum := sha256.Sum256([]byte(img.MediaType + "\x00" + img.Data))
	return hex.EncodeToString(sum[:])
}

// freezeImagePlaceholders appends each image's placeholder token to the
// containing message's Content before the message first reaches a provider,
// and records the image fingerprints separately when the provider will receive
// the images themselves. Freezing the text up front means image cleanup can
// later drop only the bytes: the sent text never changes, so a stateful
// provider's append-only record still matches. The placeholder check makes a
// second pass a no-op.
func freezeImagePlaceholders(msgs []Message, imagesDelivered bool) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		for _, placeholder := range imagePlaceholders(out[i]) {
			for strings.Count(out[i].Content, placeholder.token) < placeholder.count {
				out[i].Content = appendContentToken(out[i].Content, placeholder.token)
			}
		}
		if imagesDelivered {
			out[i].SentImageFingerprints = deliveredImageFingerprints(out[i].SentImageFingerprints, out[i].Images)
		}
	}
	return out
}

// deliveredImageFingerprints returns recorded extended with one fingerprint per
// image carrying data. It keeps multiplicity, so the same image pasted twice
// records two fingerprints, and repeated freeze passes add nothing.
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
// the payload is not re-sent on later turns. Content is left alone: placeholder
// tokens were frozen before delivery, and the only text this adds is a
// placeholder for an image that never reached a provider (a legacy in-memory
// state or a vision-disabled request).
func stripImagesFromMessages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if len(out[i].Images) == 0 {
			continue
		}
		recorded := false
		for _, img := range out[i].Images {
			if img.Data == "" {
				continue
			}
			if !recorded {
				recordImageRewrite(&out[i])
				recorded = true
			}
		}
		for _, placeholder := range imagePlaceholders(out[i]) {
			for strings.Count(out[i].Content, placeholder.token) < placeholder.count {
				out[i].Content = appendContentToken(out[i].Content, placeholder.token)
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

// placeholderCount is one placeholder token and how many times the message's
// images need it in the content.
type placeholderCount struct {
	token string
	count int
}

// imagePlaceholders returns the placeholder tokens of the message's images that
// still carry data, in image order and with duplicates counted. Image order
// keeps the appended text deterministic.
func imagePlaceholders(msg Message) []placeholderCount {
	index := make(map[string]int, len(msg.Images))
	out := make([]placeholderCount, 0, len(msg.Images))
	for _, img := range msg.Images {
		if img.Data == "" {
			continue
		}
		token := imageBlockPlaceholder(img)
		if i, ok := index[token]; ok {
			out[i].count++
			continue
		}
		index[token] = len(out)
		out = append(out, placeholderCount{token: token, count: 1})
	}
	return out
}

// appendContentToken appends token to content on its own line.
func appendContentToken(content, token string) string {
	if content == "" {
		return token
	}
	return content + "\n" + token
}
