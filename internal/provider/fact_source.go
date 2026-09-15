package provider

import (
	"context"

	"github.com/luispabon/steiner/internal/config"
)

// factField identifies a single fact within ModelFacts, for use as a bitset
// element and as a notes-map key.
type factField uint8

// fieldSet is a bitset of factField values.
type fieldSet uint8

const (
	fieldContextWindow factField = 1 << iota
	fieldMaxOutput
	fieldVision
	fieldEfforts
	fieldEchoBack
	fieldTransport
)

// modelRef carries everything a fact source needs to answer facts for one
// model.
type modelRef struct {
	Alias          string
	IsAlias        bool
	ProviderAlias  string
	Provider       config.ProviderConfig
	Profile        providerProfile
	BackendModelID string
	ModelConfig    config.ModelConfig
}

// factSource answers any subset of ModelFacts for one model.
type factSource interface {
	name() FactSource
	// resolve returns facts for fields in want. Fields not in want must be
	// left !Known. Fields in want that this source cannot answer must also
	// be left !Known (that's normal, not an error).
	resolve(ctx context.Context, ref modelRef, want fieldSet) sourceResult
}

// sourceResult is what a factSource returns for one resolve call.
type sourceResult struct {
	facts ModelFacts
	// notes carries provenance/degradation notes keyed by field, for fields
	// the source was asked about. Unused until stage B2 wires models.dev
	// degradation warnings.
	notes     map[factField]string
	sourceErr string
}
