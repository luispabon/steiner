package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
)

// recordingPhaseControl counts the prompts a router routes to it.
type recordingPhaseControl struct {
	mu      sync.Mutex
	submits int
}

func (c *recordingPhaseControl) Submit(string, []agent.ImageBlock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submits++
}

func (c *recordingPhaseControl) submitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.submits
}

func (c *recordingPhaseControl) NotifySteer() {}

func (c *recordingPhaseControl) StopTurn() {}

func (c *recordingPhaseControl) CancelAgent(string, bool) error { return nil }

func (c *recordingPhaseControl) CancelAll() error { return nil }

// mustSeedImage registers an image in store so the next import takes a
// different ID.
func mustSeedImage(t *testing.T, store *agent.ImageStore, content string) agent.ImageRef {
	t.Helper()
	ref, err := store.Import([]byte(content), "image/png", 2, 3)
	if err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return ref
}

// pastedImage returns a steer image block carrying the launching session's ID
// and path plus the pasted bytes.
func pastedImage(id, path, content string) agent.ImageBlock {
	return agent.ImageBlock{
		ID:        id,
		FilePath:  path,
		MediaType: "image/png",
		Data:      base64.StdEncoding.EncodeToString([]byte(content)),
	}
}

func TestImportPhaseSteerUsesReceivingStoreID(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	seeded := mustSeedImage(t, store, "phase image")
	if seeded.ID != "img-1" {
		t.Fatalf("seed ID = %q, want img-1", seeded.ID)
	}

	got := importPhaseSteer(agent.SteerMessage{
		Text:   "inspect [img-1]",
		Images: []agent.ImageBlock{pastedImage("img-1", "/launch/pasted.png", "pasted bytes")},
	}, store, func(message string) { t.Errorf("unexpected warning: %s", message) })

	if got.Text != "inspect [img-2]" {
		t.Fatalf("text = %q, want inspect [img-2]", got.Text)
	}
	if len(got.Images) != 1 {
		t.Fatalf("images = %+v, want one block", got.Images)
	}
	block := got.Images[0]
	if block.ID != "img-2" {
		t.Fatalf("image ID = %q, want img-2", block.ID)
	}
	if block.FilePath == "/launch/pasted.png" {
		t.Fatal("imported block kept the launching session's file path")
	}

	// The vision path resolves an ID through the store, then reads that file.
	ref, ok := store.Get(block.ID)
	if !ok {
		t.Fatalf("image %q absent from the receiving store", block.ID)
	}
	data, err := os.ReadFile(ref.FilePath)
	if err != nil || !bytes.Equal(data, []byte("pasted bytes")) {
		t.Fatalf("stored bytes = %q, err = %v", data, err)
	}
	seedRef, ok := store.Get(seeded.ID)
	if !ok {
		t.Fatalf("receiving store lost %q", seeded.ID)
	}
	seedData, err := os.ReadFile(seedRef.FilePath)
	if err != nil || string(seedData) != "phase image" {
		t.Fatalf("receiving store's own img-1 = %q, err = %v", seedData, err)
	}
}

func TestImportPhaseSteerDropsFailedImageOnly(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	mustSeedImage(t, store, "one")
	mustSeedImage(t, store, "two")

	var warnings []string
	got := importPhaseSteer(agent.SteerMessage{
		Text: "keep [img-1] and [img-2] and nowhere",
		Images: []agent.ImageBlock{
			pastedImage("img-1", "/launch/good.png", "good bytes"),
			{ID: "img-2", FilePath: "/launch/bad.png", MediaType: "image/png", Data: "not base64"},
		},
	}, store, func(message string) { warnings = append(warnings, message) })

	if got.Text != "keep [img-3] and [image unavailable] and nowhere" {
		t.Fatalf("text = %q, want the failed marker marked unavailable", got.Text)
	}
	if len(got.Images) != 1 || got.Images[0].ID != "img-3" {
		t.Fatalf("images = %+v, want one img-3", got.Images)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "img-2") {
		t.Fatalf("warnings = %v, want one naming img-2", warnings)
	}
}

func TestImportPhaseSteerRewritesAttachmentMarkers(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	mustSeedImage(t, store, "phase image")

	var warnings []string
	got := importPhaseSteer(agent.SteerMessage{
		Text:   "keep [img-1] and attach [img-7]",
		Images: []agent.ImageBlock{pastedImage("img-7", "/launch/seven.png", "seven bytes")},
	}, store, func(message string) { warnings = append(warnings, message) })

	if got.Text != "keep [img-1] and attach [img-2]" {
		t.Fatalf("text = %q, want only the attachment marker rewritten", got.Text)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	assertStoreBytes(t, store, "img-1", "phase image")
	assertImportedImage(t, store, got.Images, "seven bytes")
}

func TestImportPhaseSteerLeavesOtherMarkersUnchanged(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())

	var warnings []string
	got := importPhaseSteer(agent.SteerMessage{Text: "see [img-1] and [img-9]"}, store, func(message string) { warnings = append(warnings, message) })

	if got.Text != "see [img-1] and [img-9]" {
		t.Fatalf("text = %q, want the references left alone", got.Text)
	}
	if len(got.Images) != 0 {
		t.Fatalf("images = %+v, want none", got.Images)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if all := store.All(); len(all) != 0 {
		t.Fatalf("store = %+v, want no registrations", all)
	}
}

func assertStoreBytes(t *testing.T, store *agent.ImageStore, id, want string) {
	t.Helper()
	ref, ok := store.Get(id)
	if !ok {
		t.Fatalf("image %q absent from the phase store", id)
	}
	data, err := os.ReadFile(ref.FilePath)
	if err != nil || string(data) != want {
		t.Fatalf("image %q bytes = %q, err = %v, want %q", id, data, err, want)
	}
}

func TestPhaseDriverImportsSteerQueuedBeforePhase(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	mustSeedImage(t, store, "phase image")
	queue := agent.NewSteerQueue()
	queue.Add(agent.SteerMessage{Text: "pre-phase [img-1]", Images: []agent.ImageBlock{pastedImage("img-1", "/launch/pasted.png", "pasted bytes")}})

	h := &phaseHarness{bg: &phaseBackground{}}
	delivered := make(chan agent.Message, 1)
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		delivered <- in.Conversation[len(in.Conversation)-1]
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.images = store
	host.steers = queue

	if _, err := runPhaseOnDriver(context.Background(), h.input(), host); err != nil {
		t.Fatalf("runPhaseOnDriver: %v", err)
	}
	msg := <-delivered
	if !strings.Contains(msg.Content, "pre-phase [img-2]") || strings.Contains(msg.Content, "[img-1]") {
		t.Fatalf("delivered content = %q, want the rewritten pre-phase marker", msg.Content)
	}
	assertImportedImage(t, store, msg.Images, "pasted bytes")
}

func TestPhaseDriverImportsBoundaryDrainedSteerImage(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	queue := agent.NewSteerQueue()

	h := &phaseHarness{bg: &phaseBackground{}}
	delivered := make(chan agent.Message, 1)
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		queue.Add(agent.SteerMessage{Text: "during [img-7]", Images: []agent.ImageBlock{pastedImage("img-7", "/launch/seven.png", "seven bytes")}})
		delivered <- *in.DrainInbox().Message
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.images = store
	host.steers = queue

	if _, err := runPhaseOnDriver(context.Background(), h.input(), host); err != nil {
		t.Fatalf("runPhaseOnDriver: %v", err)
	}
	msg := <-delivered
	if msg.Content != "during [img-1]" {
		t.Fatalf("delivered content = %q, want during [img-1]", msg.Content)
	}
	assertImportedImage(t, store, msg.Images, "seven bytes")
}

func TestPhaseDriverKeepsEarlierSteerImageReference(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	mustSeedImage(t, store, "phase image")
	queue := agent.NewSteerQueue()

	h := &phaseHarness{bg: &phaseBackground{}}
	first := make(chan agent.Message, 1)
	second := make(chan agent.Message, 1)
	warnings := make(chan string, 8)
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		queue.Add(agent.SteerMessage{Text: "first [img-1]", Images: []agent.ImageBlock{pastedImage("img-1", "/launch/one.png", "first bytes")}})
		first <- *in.DrainInbox().Message
		queue.Add(agent.SteerMessage{Text: "reuse [img-2]"})
		second <- *in.DrainInbox().Message
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.images = store
	host.steers = queue
	host.events = output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.ConversationWarningEvent); ok {
			warnings <- payload.Message
		}
	})

	if _, err := runPhaseOnDriver(context.Background(), h.input(), host); err != nil {
		t.Fatalf("runPhaseOnDriver: %v", err)
	}
	gotFirst := <-first
	if gotFirst.Content != "first [img-2]" {
		t.Fatalf("first steer content = %q, want the rewritten import", gotFirst.Content)
	}
	assertImportedImage(t, store, gotFirst.Images, "first bytes")

	gotSecond := <-second
	if gotSecond.Content != "reuse [img-2]" {
		t.Fatalf("second steer content = %q, want the earlier steer reference kept", gotSecond.Content)
	}
	if len(gotSecond.Images) != 0 {
		t.Fatalf("second steer images = %+v, want none", gotSecond.Images)
	}
	assertStoreBytes(t, store, "img-2", "first bytes")
	assertStoreBytes(t, store, "img-1", "phase image")
	select {
	case warning := <-warnings:
		t.Fatalf("unexpected warning for a resolvable phase reference: %q", warning)
	default:
	}
}

func TestPhaseDriverDeliversSteerTextWhenImageImportFails(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	queue := agent.NewSteerQueue()

	h := &phaseHarness{bg: &phaseBackground{}}
	delivered := make(chan agent.Message, 1)
	warnings := make(chan string, 4)
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		queue.Add(agent.SteerMessage{Text: "keep this [img-1]", Images: []agent.ImageBlock{{ID: "img-1", FilePath: "/launch/broken.png", MediaType: "image/png", Data: "not base64"}}})
		delivered <- *in.DrainInbox().Message
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.images = store
	host.steers = queue
	host.events = output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.ConversationWarningEvent); ok {
			warnings <- payload.Message
		}
	})

	if _, err := runPhaseOnDriver(context.Background(), h.input(), host); err != nil {
		t.Fatalf("runPhaseOnDriver: %v", err)
	}
	msg := <-delivered
	if !strings.Contains(msg.Content, "keep this") || strings.Contains(msg.Content, "[img-1]") {
		t.Fatalf("delivered content = %q, want the text without the stale marker", msg.Content)
	}
	if len(msg.Images) != 0 {
		t.Fatalf("delivered images = %+v, want none", msg.Images)
	}
	select {
	case warning := <-warnings:
		if !strings.Contains(warning, "img-1") {
			t.Fatalf("warning = %q, want it to name img-1", warning)
		}
	default:
		t.Fatal("no warning emitted for the failed steer image")
	}
}

func TestPhaseControlSubmitImportsImageAndCloses(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	mustSeedImage(t, store, "phase image")

	h := &phaseHarness{bg: &phaseBackground{}}
	var mu sync.Mutex
	var conversations [][]agent.Message
	started := make(chan struct{})
	release := make(chan struct{})
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		mu.Lock()
		conversations = append(conversations, in.Conversation)
		run := len(conversations)
		mu.Unlock()
		if run == 1 {
			close(started)
			<-release
		}
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.images = store
	warnings := make(chan string, 8)
	host.events = output.SinkFunc(func(event output.Event) {
		if payload, ok := event.Payload.(output.ConversationWarningEvent); ok {
			warnings <- payload.Message
		}
	})

	var control oneshot.PhaseControl
	in := h.input()
	in.RegisterControl = func(pc oneshot.PhaseControl) func() {
		control = pc
		return func() {}
	}
	done := make(chan error, 1)
	go func() {
		_, err := runPhaseOnDriver(context.Background(), in, host)
		done <- err
	}()
	<-started
	control.Submit("see [img-1]", []agent.ImageBlock{pastedImage("img-1", "/launch/one.png", "submitted bytes")})
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runPhaseOnDriver: %v", err)
		}
	case <-time.After(phaseDriverTestTimeout):
		t.Fatal("phase did not finish")
	}

	mu.Lock()
	runs := len(conversations)
	var second []agent.Message
	if runs > 1 {
		second = conversations[1]
	}
	mu.Unlock()
	if runs != 2 {
		t.Fatalf("runs = %d, want the phase prompt and the submitted steer", runs)
	}
	last := second[len(second)-1]
	if !strings.Contains(last.Content, "[img-2]") || strings.Contains(last.Content, "[img-1]") {
		t.Fatalf("submitted content = %q, want the rewritten marker", last.Content)
	}
	assertImportedImage(t, store, last.Images, "submitted bytes")

	before := len(store.All())
	control.Submit("late [img-2]", []agent.ImageBlock{pastedImage("img-2", "/launch/late.png", "late bytes")})
	if after := len(store.All()); after != before {
		t.Fatalf("closed phase imported %d late images", after-before)
	}
	select {
	case warning := <-warnings:
		if warning != phaseSteerClosedWarning {
			t.Fatalf("late submit warning = %q, want %q", warning, phaseSteerClosedWarning)
		}
	default:
		t.Fatal("late submit was dropped without a warning")
	}
}

func TestPhaseRouterReleaseWithUncomparableSink(t *testing.T) {
	// A phaseControl carries an output.SinkFunc, whose func type cannot be
	// compared. Registering the control by value made the router's release
	// compare two such values and panic, so a phase registers a pointer.
	router := &interactive.PhaseRouter{}
	h := &phaseHarness{bg: &phaseBackground{}}
	host := h.host(func(_ context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.events = output.SinkFunc(func(output.Event) {})
	in := h.input()
	in.RegisterControl = func(pc oneshot.PhaseControl) func() { return router.Register(pc) }

	if _, err := runPhaseOnDriver(context.Background(), in, host); err != nil {
		t.Fatalf("runPhaseOnDriver: %v", err)
	}

	// The phase's release cleared the router, so a later target gets control.
	target := &recordingPhaseControl{}
	defer router.Register(target)()
	router.Submit("late", nil)
	if got := target.submitCount(); got != 1 {
		t.Fatalf("later target received %d submits, want 1", got)
	}
}

func assertImportedImage(t *testing.T, store *agent.ImageStore, images []agent.ImageBlock, want string) {
	t.Helper()
	if len(images) != 1 {
		t.Fatalf("images = %+v, want one block", images)
	}
	ref, ok := store.Get(images[0].ID)
	if !ok {
		t.Fatalf("image %q absent from the receiving store", images[0].ID)
	}
	data, err := os.ReadFile(ref.FilePath)
	if err != nil || string(data) != want {
		t.Fatalf("stored bytes = %q, err = %v, want %q", data, err, want)
	}
}
