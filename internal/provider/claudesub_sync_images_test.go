package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

// deliveredImageMessage returns a user message whose image was sent and whose
// placeholder text was frozen before delivery. The agent records the delivered
// fingerprint separately because cleanup later drops the bytes.
func deliveredImageMessage(t *testing.T, data string, fingerprintRecorded bool) Message {
	t.Helper()
	image := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: data}
	msg := Message{Role: MessageRoleUser, Content: "look\n[image img-1: /tmp/a.png 1x1 png 1KB]", Images: []ImageBlock{image}}
	if fingerprintRecorded {
		msg.SentImageFingerprints = claudeSubImageFingerprints(msg)
	}
	return msg
}

// strippedImageMessage returns the same message after image cleanup removed
// only the bytes: text and the separate fingerprint record stay.
func strippedImageMessage(t *testing.T, sent Message) Message {
	t.Helper()
	stripped := sent
	stripped.Images = nil
	return stripped
}

// TestClaudeSubLiveReusesRecordAfterImageStrip proves a pasted-image turn keeps
// its live process: the request after cleanup matches the recorded digest and
// the recorded fingerprints, so nothing reseeds and no flattened replay happens.
func TestClaudeSubLiveReusesRecordAfterImageStrip(t *testing.T) {
	sent := deliveredImageMessage(t, "AAAA", true)
	if len(sent.SentImageFingerprints) != 1 {
		t.Fatalf("setup recorded %d fingerprints, want 1", len(sent.SentImageFingerprints))
	}
	s := &claudeSubSync{}
	first := []Message{sent}
	delta, err := s.plan(ChatRequest{Messages: first})
	if err != nil {
		t.Fatalf("setup plan: %v", err)
	}
	claudeSubSyncCommit(s, delta)

	stripped := strippedImageMessage(t, sent)
	next := Message{Role: MessageRoleUser, Content: "next"}
	delta, err = s.plan(ChatRequest{Messages: []Message{stripped, next}})
	if err != nil {
		t.Fatalf("plan after image strip = %v, want the live record to accept the append", err)
	}
	if len(delta.User) != 1 || delta.User[0].Content != "next" {
		t.Fatalf("delta user = %q, want only the new message", claudeSubSyncContents(delta.User))
	}

	changed := stripped
	changed.Content = "look differently\n[image img-1: /tmp/a.png 1x1 png 1KB]"
	if _, err := s.plan(ChatRequest{Messages: []Message{changed, next}}); !errors.Is(err, errClaudeSubHistoryChanged) {
		t.Fatalf("rewritten history err = %v, want errClaudeSubHistoryChanged", err)
	}
}

// TestClaudeSubResumeRebuildsImageFingerprints proves a stored checkpoint can be
// rebuilt from request history after cleanup, because the fingerprints live in
// message metadata rather than in the stripped image bytes.
func TestClaudeSubResumeRebuildsImageFingerprints(t *testing.T) {
	sent := deliveredImageMessage(t, "AAAA", true)
	assistant := Message{Role: MessageRoleAssistant, Content: "ok"}
	s := &claudeSubSync{started: true}
	s.commitSent(claudeSubDelta{User: []Message{sent}})
	s.commitAssistant(assistant, "u1")
	s.fingerprint = "fp"
	s.cliSessionID = "cli-1"

	stored, ok, err := s.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("checkpoint = (%+v, %t, %v), want a state", stored, ok, err)
	}
	stripped := []Message{strippedImageMessage(t, sent), assistant}
	if _, restorable, reason, err := claudeSubRestoreFor(stripped, stored, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("restore = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}

	// Without the separate record the same history cannot reproduce the stored
	// prefix, which is exactly the case that reseeded before this change.
	lossy := []Message{strippedImageMessage(t, deliveredImageMessage(t, "AAAA", false)), assistant}
	if _, restorable, reason, err := claudeSubRestoreFor(lossy, stored, true, "key", "fp"); err != nil || restorable || reason != claudeSubReseedPrefix {
		t.Fatalf("lossy restore = (%t, %q, %v), want reseed reason %q", restorable, reason, err, claudeSubReseedPrefix)
	}
}

// TestClaudeSubImageFingerprintRecordWins proves the separate record is used
// verbatim and falls back to the image bytes only for messages that carry none.
func TestClaudeSubImageFingerprintRecordWins(t *testing.T) {
	image := ImageBlock{MediaType: "image/png", Data: "AAAA"}
	sum := sha256.Sum256([]byte("image/png\x00AAAA"))
	want := hex.EncodeToString(sum[:])

	withRecord := Message{Role: MessageRoleUser, Images: []ImageBlock{image}, SentImageFingerprints: []string{"recorded"}}
	if got := claudeSubImageFingerprints(withRecord); len(got) != 1 || got[0] != "recorded" {
		t.Fatalf("recorded fingerprints = %v, want the separate record", got)
	}
	withoutRecord := Message{Role: MessageRoleUser, Images: []ImageBlock{image}}
	if got := claudeSubImageFingerprints(withoutRecord); len(got) != 1 || got[0] != want {
		t.Fatalf("computed fingerprints = %v, want %q", got, want)
	}
	if got := claudeSubImageFingerprints(Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", FilePath: "/tmp/a.png"}}}); len(got) != 0 {
		t.Fatalf("path-only image fingerprints = %v, want none", got)
	}
}
