package provider

import (
	"errors"
	"reflect"
	"testing"
)

// deliveredImageMessage returns a user message whose image was sent and whose
// placeholder text was frozen before delivery. The agent records the delivered
// fingerprint separately because cleanup later drops the bytes.
func deliveredImageMessage(data string) Message {
	image := ImageBlock{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: data}
	msg := Message{Role: MessageRoleUser, Content: "look\n[image img-1: /tmp/a.png 1x1 png 1KB]", Images: []ImageBlock{image}}
	return msg
}

// strippedImageMessage returns the same message after image cleanup removed
// only the bytes: text and the separate fingerprint record stay.
func strippedImageMessage(sent Message) Message {
	stripped := sent
	stripped.Images = nil
	return stripped
}

// TestClaudeSubLiveReusesRecordAfterImageStrip proves a pasted-image turn keeps
// its live process: the request after cleanup matches the recorded digest and
// the recorded fingerprints, so nothing reseeds and no flattened replay happens.
func TestClaudeSubLiveReusesRecordAfterImageStrip(t *testing.T) {
	sent := deliveredImageMessage("AAAA")
	s := &claudeSubSync{}
	first := []Message{sent}
	delta, err := s.plan(ChatRequest{Messages: first})
	if err != nil {
		t.Fatalf("setup plan: %v", err)
	}
	claudeSubSyncCommit(s, delta)

	stripped := strippedImageMessage(sent)
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
	sent := deliveredImageMessage("AAAA")
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
	stripped := []Message{strippedImageMessage(sent), assistant}
	if _, restorable, reason, err := claudeSubRestoreFor(stripped, stored, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("restore = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}

	// The provider's own record carries the delivered fingerprints, so a history
	// whose agent metadata is missing still restores: the CLI transcript is the
	// authority, and a failed or cancelled turn never leaves metadata behind.
	lossy := []Message{strippedImageMessage(deliveredImageMessage("AAAA")), assistant}
	if _, restorable, reason, err := claudeSubRestoreFor(lossy, stored, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("lossy restore = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}

	// Adding an image to an already-sent message is a rewrite, not cleanup.
	added := []Message{{Role: MessageRoleUser, Content: stripped[0].Content, Images: []ImageBlock{{MediaType: "image/png", Data: "BBBB"}}}, assistant}
	if _, restorable, reason, err := claudeSubRestoreFor(added, stored, true, "key", "fp"); err != nil || restorable || reason != claudeSubReseedFinalUUID {
		t.Fatalf("added-image restore = (%t, %q, %v), want reseed reason %q", restorable, reason, err, claudeSubReseedFinalUUID)
	}
}

// TestClaudeSubImageClaimUsesOnlyBytes proves only bytes present on a message
// are a delivery claim: a stripped message claims nothing, path-only images
// claim nothing, and cleanup never leaves a claim behind.
func TestClaudeSubImageClaimUsesOnlyBytes(t *testing.T) {
	image := ImageBlock{MediaType: "image/png", Data: "AAAA"}
	want := ImageFingerprint("image/png", "AAAA")

	if got, ok := claudeSubImageClaim(Message{Role: MessageRoleUser, Images: []ImageBlock{image}}); !ok || len(got) != 1 || got[0] != want {
		t.Fatalf("computed claim = (%v, %t), want %q", got, ok, want)
	}
	if got, ok := claudeSubImageClaim(Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", FilePath: "/tmp/a.png"}}}); !ok || len(got) != 0 {
		t.Fatalf("path-only claim = (%v, %t), want none", got, ok)
	}
	if got, ok := claudeSubImageClaim(Message{Role: MessageRoleUser, Content: "look"}); !ok || len(got) != 0 {
		t.Fatalf("stripped claim = (%v, %t), want none", got, ok)
	}
}

// TestClaudeSubReseedRecordsOnlyDeliveredImages proves a flattened reseed claims
// only the images it actually carries: metadata for a stripped image is not
// replayed, so the record matches the transcript the CLI received.
func TestClaudeSubReseedRecordsOnlyDeliveredImages(t *testing.T) {
	stripped := deliveredImageMessage("AAAA")
	stripped.Images = nil
	carried := deliveredImageMessage("BBBB")
	msgs := []Message{stripped, carried}

	delta := claudeSubReseedDelta(msgs)
	if len(delta.keep) != 2 {
		t.Fatalf("keep entries = %d, want 2", len(delta.keep))
	}
	if len(delta.keep[0].Images) != 0 {
		t.Fatalf("stripped entry images = %v, want none", delta.keep[0].Images)
	}
	if want := claudeSubDeliveryImageFingerprints(carried); len(delta.keep[1].Images) != 1 || delta.keep[1].Images[0] != want[0] {
		t.Fatalf("carried entry images = %v, want %v", delta.keep[1].Images, want)
	}
	if len(delta.User) != 1 || len(delta.User[0].Images) != 1 {
		t.Fatalf("transcript images = %+v, want only the carried image", delta.User)
	}
}

// TestClaudeSubReseedThenLiveAppendCheckpointRestore reproduces the reseed loop:
// a stripped history message still carries its old delivery record, but the
// reseed transcript did not deliver it. The live record must accept the append,
// the checkpoint must describe only what was delivered, and a restore from the
// same history must succeed instead of reseeding forever.
func TestClaudeSubReseedThenLiveAppendCheckpointRestore(t *testing.T) {
	stripped := deliveredImageMessage("AAAA")
	stripped.Images = nil
	firstAssistant := Message{Role: MessageRoleAssistant, Content: "ok"}
	newUser := Message{Role: MessageRoleUser, Content: "next"}
	secondAssistant := Message{Role: MessageRoleAssistant, Content: "done"}

	history := []Message{stripped, firstAssistant}
	s := &claudeSubSync{}
	reseeded := claudeSubReseedDelta(history)
	if len(reseeded.keep[0].Images) != 0 {
		t.Fatalf("reseed claim = %v, want the stripped image excluded", reseeded.keep[0].Images)
	}
	s.adopt(reseeded)
	s.commitSent(reseeded)
	s.fingerprint = "fp"
	s.cliSessionID = "cli-1"

	delta, err := s.plan(ChatRequest{Messages: []Message{stripped, firstAssistant, newUser}})
	if err != nil {
		t.Fatalf("live plan after reseed = %v, want the append accepted", err)
	}
	if len(delta.User) != 1 || delta.User[0].Content != "next" {
		t.Fatalf("live delta user = %q, want only the new message", claudeSubSyncContents(delta.User))
	}
	claudeSubSyncCommit(s, delta)
	s.commitAssistant(secondAssistant, "u2")

	stored, ok, err := s.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("checkpoint = (%+v, %t, %v), want a state", stored, ok, err)
	}
	if got := stored.SyncEntryImagesAt(0); len(got) != 0 {
		t.Fatalf("stored entry images = %v, want none for the stripped history", got)
	}
	full := []Message{stripped, firstAssistant, newUser, secondAssistant}
	if _, restorable, reason, err := claudeSubRestoreFor(full, stored, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("restore after reseed = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}
}

// TestClaudeSubUpgradeFromPriorStateReseedsOnce proves a checkpoint written
// before per-entry image fingerprints existed reseeds once and then stabilizes:
// the rebuilt record no longer claims the stripped image, so later restores are
// silent.
func TestClaudeSubUpgradeFromPriorStateReseedsOnce(t *testing.T) {
	stripped := deliveredImageMessage("AAAA")
	stripped.Images = nil
	assistant := Message{Role: MessageRoleAssistant, Content: "ok"}
	history := []Message{stripped, assistant}

	live := &claudeSubSync{started: true}
	live.commitSent(claudeSubDelta{User: []Message{stripped}})
	live.commitAssistant(assistant, "u1")
	live.fingerprint = "fp"
	live.cliSessionID = "cli-1"
	stored, ok, err := live.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("checkpoint = (%+v, %t, %v), want a state", stored, ok, err)
	}
	// A prior-version state stores no per-entry image fingerprints.
	stored.SyncEntryImages = nil
	stored.SyncPrefixDigest = "prior-version"
	if _, restorable, reason, err := claudeSubRestoreFor(history, stored, true, "key", "fp"); err != nil || restorable || reason != claudeSubReseedPrefix {
		t.Fatalf("prior state restore = (%t, %q, %v), want reseed reason %q", restorable, reason, err, claudeSubReseedPrefix)
	}

	upgraded := &claudeSubSync{}
	delta := claudeSubReseedDelta(history)
	upgraded.adopt(delta)
	upgraded.commitSent(delta)
	upgraded.fingerprint = "fp"
	upgraded.cliSessionID = "cli-1"
	// The reseeded CLI reports no per-message uuids for the flattened history, so
	// the record becomes resumable once the next turn's assistant arrives.
	newUser := Message{Role: MessageRoleUser, Content: "next"}
	delta, err = upgraded.plan(ChatRequest{Messages: append(append([]Message{}, history...), newUser)})
	if err != nil {
		t.Fatalf("live plan after upgrade reseed = %v", err)
	}
	claudeSubSyncCommit(upgraded, delta)
	newAssistant := Message{Role: MessageRoleAssistant, Content: "done"}
	upgraded.commitAssistant(newAssistant, "u2")
	rebuilt, ok, err := upgraded.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("rebuilt checkpoint = (%+v, %t, %v), want a state", rebuilt, ok, err)
	}
	full := append(append([]Message{}, history...), newUser, newAssistant)
	if _, restorable, reason, err := claudeSubRestoreFor(full, rebuilt, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("restore after upgrade = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}
}

// TestClaudeSubTamperedEntryImagesRejected proves a checkpoint whose stored
// per-entry images disagree with the history it claims to describe reseeds
// instead of being trusted.
func TestClaudeSubTamperedEntryImagesRejected(t *testing.T) {
	stripped := deliveredImageMessage("AAAA")
	stripped.Images = nil
	assistant := Message{Role: MessageRoleAssistant, Content: "ok"}
	history := []Message{stripped, assistant}

	live := &claudeSubSync{started: true}
	live.commitSent(claudeSubDelta{User: []Message{stripped}})
	live.commitAssistant(assistant, "u1")
	live.fingerprint = "fp"
	live.cliSessionID = "cli-1"
	stored, ok, err := live.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("checkpoint = (%+v, %t, %v), want a state", stored, ok, err)
	}
	// A tampered state adds an image the history never delivered.
	stored.SyncEntryImages = [][]string{{ImageFingerprint("image/png", "BBBB")}}
	if _, restorable, reason, err := claudeSubRestoreFor(history, stored, true, "key", "fp"); err != nil || restorable || reason != claudeSubReseedPrefix {
		t.Fatalf("tampered restore = (%t, %q, %v), want reseed reason %q", restorable, reason, err, claudeSubReseedPrefix)
	}
}

// TestClaudeSubSecondReseedIsStable proves reseeding the same history twice
// converges: the second record equals the first and a later live turn checkpoints
// and restores silently.
func TestClaudeSubSecondReseedIsStable(t *testing.T) {
	stripped := deliveredImageMessage("AAAA")
	stripped.Images = nil
	assistant := Message{Role: MessageRoleAssistant, Content: "ok"}
	history := []Message{stripped, assistant}

	build := func() *claudeSubSync {
		s := &claudeSubSync{}
		delta := claudeSubReseedDelta(history)
		s.adopt(delta)
		s.commitSent(delta)
		s.fingerprint = "fp"
		s.cliSessionID = "cli-1"
		return s
	}
	first := build()
	second := build()
	if !reflect.DeepEqual(first.entries, second.entries) {
		t.Fatalf("second reseed entries = %+v, want %+v", second.entries, first.entries)
	}
	if got := second.entries[0].Images; len(got) != 0 {
		t.Fatalf("second reseed images = %v, want none", got)
	}

	newUser := Message{Role: MessageRoleUser, Content: "next"}
	delta, err := second.plan(ChatRequest{Messages: append(append([]Message{}, history...), newUser)})
	if err != nil {
		t.Fatalf("live plan after second reseed = %v", err)
	}
	claudeSubSyncCommit(second, delta)
	newAssistant := Message{Role: MessageRoleAssistant, Content: "done"}
	second.commitAssistant(newAssistant, "u2")
	stored, ok, err := second.checkpoint("key")
	if err != nil || !ok {
		t.Fatalf("checkpoint = (%+v, %t, %v), want a state", stored, ok, err)
	}
	full := append(append([]Message{}, history...), newUser, newAssistant)
	if _, restorable, reason, err := claudeSubRestoreFor(full, stored, true, "key", "fp"); err != nil || !restorable || reason != "" {
		t.Fatalf("restore after second reseed = (%t, %q, %v), want a silent restore", restorable, reason, err)
	}
}
