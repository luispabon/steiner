package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"

	"github.com/luispabon/steiner/internal/agent"
)

// phaseImageMarker matches the exact numeric composer marker the TUI writes
// into input text, such as [img-3].
var phaseImageMarker = regexp.MustCompile(`\[img-(\d+)\]`)

// phaseUnavailableMarker replaces the marker of an attachment that did not
// reach the phase. It does not match the image reference patterns, so it never
// names a store ID.
const phaseUnavailableMarker = "[image unavailable]"

// importPhaseSteer copies each pasted image in steer into store and rewrites
// the marker of every attachment to its new local ID. An attachment that cannot
// be imported becomes phaseUnavailableMarker and is reported to warn; the steer
// text and the other images are still delivered. Any other [img-N] reference is
// left alone: IDs are phase-local, so a reference the phase cannot resolve
// fails its lookup. The source file path is never used.
func importPhaseSteer(steer agent.SteerMessage, store *agent.ImageStore, warn func(string)) agent.SteerMessage {
	images := make([]agent.ImageBlock, 0, len(steer.Images))
	replacements := make(map[string]string, len(steer.Images))
	for _, block := range steer.Images {
		sourceID := block.ID
		ref, err := importPhaseImage(store, block)
		if err != nil {
			replacements[sourceID] = ""
			if warn != nil {
				warn(phaseImageFailure(sourceID, err))
			}
			continue
		}
		replacements[sourceID] = ref.ID
		block.ID = ref.ID
		block.FilePath = ref.FilePath
		images = append(images, block)
	}
	return agent.SteerMessage{Text: rewritePhaseMarkers(steer.Text, replacements), Images: images}
}

// importPhaseImage writes one block's decoded bytes into store.
func importPhaseImage(store *agent.ImageStore, block agent.ImageBlock) (agent.ImageRef, error) {
	if store == nil {
		return agent.ImageRef{}, errors.New("image store is unavailable")
	}
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil || len(data) == 0 {
		return agent.ImageRef{}, errors.New("image data is missing or invalid")
	}
	return store.Import(data, block.MediaType, block.Width, block.Height)
}

// rewritePhaseMarkers applies the marker rule to each exact composer marker.
// The marker of an attachment is replaced with its new local ID, or with
// phaseUnavailableMarker when that attachment failed to import. Every other
// marker is left unchanged: IDs are phase-local, and a reference the phase does
// not hold simply fails its lookup. Text outside markers is left alone.
func rewritePhaseMarkers(text string, replacements map[string]string) string {
	return phaseImageMarker.ReplaceAllStringFunc(text, func(marker string) string {
		id := "img-" + marker[len("[img-"):len(marker)-1]
		replacement, ok := replacements[id]
		if !ok {
			return marker
		}
		if replacement == "" {
			return phaseUnavailableMarker
		}
		return "[" + replacement + "]"
	})
}

// phaseImageFailure is the warning text for a pasted image that did not reach
// the phase.
func phaseImageFailure(id string, err error) string {
	if id == "" {
		return "oneshot steer image was not delivered: " + err.Error()
	}
	return fmt.Sprintf("oneshot steer image %s was not delivered: %v", id, err)
}

// phaseSteerImporter returns the driver hook that imports queued steer images
// into store. The hook runs under the driver lock and writes the pasted image
// files there; the clipboard paste path caps an image at 5 MiB and resizes it
// when it is oversized, so the write is bounded. Warnings come back to the
// driver because the hook must not emit events or do work that waits on the
// user or the network.
func phaseSteerImporter(store *agent.ImageStore) func([]agent.SteerMessage) ([]agent.SteerMessage, []string) {
	return func(steers []agent.SteerMessage) ([]agent.SteerMessage, []string) {
		processed := make([]agent.SteerMessage, len(steers))
		var warnings []string
		for i, steer := range steers {
			processed[i] = importPhaseSteer(steer, store, func(warning string) {
				warnings = append(warnings, warning)
			})
		}
		return processed, warnings
	}
}
