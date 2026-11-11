package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"
)

// The attempt ledger is test-only. This file holds its protocol: the pinned
// values, the record shapes, and the checks every reserved or stored record must
// pass. The implementation is in token_estimator_claude_ledger_test.go.

const (
	// claudeLedgerSchemaVersion is the only ledger schema this code reads.
	claudeLedgerSchemaVersion = 1
	// claudeLedgerMaxAttempts caps lifetime attempts. It equals one attempt per
	// (model, slot) pair, which TestClaudeTokenLedgerProtocolEnumsArePinned checks.
	claudeLedgerMaxAttempts = 18
	// claudeLedgerMaxRequestBytes bounds one normalized request. A larger request
	// is refused, never truncated, because a truncated request cannot be verified.
	claudeLedgerMaxRequestBytes = 1 << 20
	claudeLedgerFileName        = "attempts.json"
	claudeLedgerLockName        = "attempts.lock"
)

// claudeLedgerSlot names one payload slot of the token estimation run.
type claudeLedgerSlot string

const (
	claudeLedgerSlotBaseline   claudeLedgerSlot = "baseline"
	claudeLedgerSlotCodeJSON   claudeLedgerSlot = "codeJSON"
	claudeLedgerSlotMarkdown   claudeLedgerSlot = "markdown"
	claudeLedgerSlotUnicode    claudeLedgerSlot = "Unicode"
	claudeLedgerSlotTools      claudeLedgerSlot = "tools"
	claudeLedgerSlotMixedTools claudeLedgerSlot = "mixedtools"
)

// claudeLedgerSlots is the slot allowlist.
var claudeLedgerSlots = []claudeLedgerSlot{
	claudeLedgerSlotBaseline, claudeLedgerSlotCodeJSON, claudeLedgerSlotMarkdown,
	claudeLedgerSlotUnicode, claudeLedgerSlotTools, claudeLedgerSlotMixedTools,
}

// claudeLedgerModels is the exact candidate model allowlist.
var claudeLedgerModels = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}

// claudeLedgerStatus is the state of one attempt record.
type claudeLedgerStatus string

const (
	// claudeLedgerStatusReserved is written before the launch. It stays in place
	// when the outcome is never written, and it still counts.
	claudeLedgerStatusReserved  claudeLedgerStatus = "reserved"
	claudeLedgerStatusSucceeded claudeLedgerStatus = "succeeded"
	claudeLedgerStatusFailed    claudeLedgerStatus = "failed"
)

// Failure codes are the only failure text a record may hold. Launch error text
// is never stored because it can carry private paths or credentials.
const (
	claudeLedgerFailLaunch        = "launch failed"
	claudeLedgerFailNoObservation = "launch returned no observation"
	claudeLedgerFailModel         = "observed model differs from requested"
	claudeLedgerFailUsage         = "observed usage is invalid"
)

// claudeLedgerFailures is the failure code allowlist.
var claudeLedgerFailures = []string{claudeLedgerFailLaunch, claudeLedgerFailNoObservation, claudeLedgerFailModel, claudeLedgerFailUsage}

var (
	errClaudeLedgerExists     = errors.New("attempt ledger already exists")
	errClaudeLedgerMissing    = errors.New("attempt ledger missing on resume")
	errClaudeLedgerLocked     = errors.New("attempt ledger locked by another session")
	errClaudeLedgerCorrupt    = errors.New("attempt ledger corrupt")
	errClaudeLedgerCap        = errors.New("attempt ledger cap reached")
	errClaudeLedgerDuplicate  = errors.New("attempt ledger already holds this model and slot")
	errClaudeLedgerInput      = errors.New("invalid attempt reservation")
	errClaudeLedgerClosed     = errors.New("attempt ledger session is closed")
	errClaudeLedgerDurability = errors.New("attempt ledger durability is unknown; no further attempts run in this session")
)

// claudeLedgerUsage is the presence-aware usage of the terminal assistant message.
// A nil cache field was not reported by the CLI, which differs from an explicit
// zero. PromptTotal is the production PromptTokens for that message, counted once.
// The CLI result usage is never added. OutputTokens is the message's output count
// from the same stream usage. Nil means the stream did not report it, which differs
// from an explicit zero. It is audit only: no gate reads it, and it does not prove
// the output cap applied.
type claudeLedgerUsage struct {
	Input         *int `json:"input_tokens,omitempty"`
	CacheCreation *int `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     *int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens  *int `json:"output_tokens,omitempty"`
	PromptTotal   int  `json:"prompt_total_tokens"`
}

// claudeLedgerRecord is one attempt. Its request and hashes identify the launch
// without holding credentials or private paths.
type claudeLedgerRecord struct {
	AttemptID      int                `json:"attempt_id"`
	TimestampUTC   string             `json:"timestamp_utc"`
	Slot           claudeLedgerSlot   `json:"slot"`
	RequestedModel string             `json:"requested_model"`
	ObservedModel  string             `json:"observed_model,omitempty"`
	Status         claudeLedgerStatus `json:"status"`
	Failure        string             `json:"failure,omitempty"`
	CLIVersion     string             `json:"cli_version"`
	LibraryCommit  string             `json:"library_commit"`
	PayloadSHA256  string             `json:"payload_sha256"`
	ToolsSHA256    string             `json:"tools_sha256"`
	Request        json.RawMessage    `json:"request"`
	Usage          *claudeLedgerUsage `json:"usage,omitempty"`
	// ThinkingBlocksObserved is set on succeeded records only. It is audit only.
	ThinkingBlocksObserved bool `json:"thinking_blocks_observed,omitempty"`
}

// claudeLedgerFile is the on-disk ledger document.
type claudeLedgerFile struct {
	SchemaVersion int                  `json:"schema_version"`
	Attempts      []claudeLedgerRecord `json:"attempts"`
}

// claudeLedgerReserveInput names one attempt before its launch. The caller
// supplies the known, authored normalized request. C2 verifies the full request
// and its hash against the fixture.
type claudeLedgerReserveInput struct {
	Slot           claudeLedgerSlot
	RequestedModel string
	CLIVersion     string
	LibraryCommit  string
	PayloadSHA256  string
	ToolsSHA256    string
	Request        ChatRequest
}

// check validates the fields a reservation and a stored record share.
func (in claudeLedgerReserveInput) check() error {
	switch {
	case !slices.Contains(claudeLedgerModels, in.RequestedModel):
		return fmt.Errorf("%w: model %q is not a candidate", errClaudeLedgerInput, in.RequestedModel)
	case !slices.Contains(claudeLedgerSlots, in.Slot):
		return fmt.Errorf("%w: slot %q is not in the protocol", errClaudeLedgerInput, in.Slot)
	case in.CLIVersion == "" || in.LibraryCommit == "":
		return fmt.Errorf("%w: cli version and library commit are required", errClaudeLedgerInput)
	case !claudeLedgerIsSHA256(in.PayloadSHA256) || !claudeLedgerIsSHA256(in.ToolsSHA256):
		return fmt.Errorf("%w: payload and tools hashes must be SHA-256 hex", errClaudeLedgerInput)
	}
	return nil
}

// claudeLedgerValidateUsage checks one usage value. Every present component must
// be non-negative, at least one component must be present, PromptTotal must be
// positive, and PromptTotal must equal the sum of the present components. Each
// component is checked on its own, so a negative value cannot hide behind a
// positive sum. An overflowing sum fails too. OutputTokens, when present, must be
// non-negative and is not part of the sum.
func claudeLedgerValidateUsage(u claudeLedgerUsage) error {
	if u.OutputTokens != nil && *u.OutputTokens < 0 {
		return fmt.Errorf("output tokens are negative: %d", *u.OutputTokens)
	}
	present, sum := false, 0
	for _, v := range []*int{u.Input, u.CacheCreation, u.CacheRead} {
		if v == nil {
			continue
		}
		if *v < 0 {
			return fmt.Errorf("usage component is negative: %d", *v)
		}
		// Check before adding, so the sum cannot wrap around.
		if *v > math.MaxInt-sum {
			return errors.New("usage components overflow int")
		}
		present = true
		sum += *v
	}
	switch {
	case !present:
		return errors.New("usage has no components")
	case u.PromptTotal <= 0:
		return fmt.Errorf("prompt %d is not positive", u.PromptTotal)
	case u.PromptTotal != sum:
		return fmt.Errorf("prompt %d differs from component sum %d", u.PromptTotal, sum)
	}
	return nil
}

// claudeLedgerValidateRequest checks a reserved or stored normalized request. It
// must be one ChatRequest object with no unknown fields, name requested as its
// model, and carry at least one message. A request over the size limit fails.
func claudeLedgerValidateRequest(raw []byte, requested string) error {
	// The limit applies to the compact form, which reserve writes. Compacting
	// first makes an indented copy of the same request measure the same.
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return fmt.Errorf("request is not JSON: %w", err)
	}
	if compact.Len() > claudeLedgerMaxRequestBytes {
		return fmt.Errorf("request is %d bytes compact, limit is %d", compact.Len(), claudeLedgerMaxRequestBytes)
	}
	raw = compact.Bytes()
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("request is null")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req ChatRequest
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("request has data after the object")
	}
	switch {
	case req.Model != requested:
		return fmt.Errorf("request model %q differs from %q", req.Model, requested)
	case len(req.Messages) == 0:
		return errors.New("request has no messages")
	}
	return nil
}

// claudeLedgerValidateRecord checks one stored record. The status decides which
// outcome fields may be set, a succeeded record must pass the usage checks, and
// the failure code decides which observed model a failed record may name.
func claudeLedgerValidateRecord(rec claudeLedgerRecord) error {
	in := claudeLedgerReserveInput{
		Slot:           rec.Slot,
		RequestedModel: rec.RequestedModel,
		CLIVersion:     rec.CLIVersion,
		LibraryCommit:  rec.LibraryCommit,
		PayloadSHA256:  rec.PayloadSHA256,
		ToolsSHA256:    rec.ToolsSHA256,
	}
	if err := in.check(); err != nil {
		return err
	}
	if ts, err := time.Parse(time.RFC3339Nano, rec.TimestampUTC); err != nil || ts.Location() != time.UTC {
		return errors.New("timestamp is not RFC 3339 UTC")
	}
	if len(rec.Request) == 0 {
		return errors.New("request is missing")
	}
	if err := claudeLedgerValidateRequest(rec.Request, rec.RequestedModel); err != nil {
		return err
	}
	switch rec.Status {
	case claudeLedgerStatusReserved:
		if rec.ObservedModel != "" || rec.Usage != nil || rec.Failure != "" || rec.ThinkingBlocksObserved {
			return errors.New("reserved record carries an outcome")
		}
	case claudeLedgerStatusSucceeded:
		if rec.ObservedModel != rec.RequestedModel || rec.Usage == nil || rec.Failure != "" {
			return errors.New("succeeded record lacks a matching observation")
		}
		if err := claudeLedgerValidateUsage(*rec.Usage); err != nil {
			return fmt.Errorf("succeeded record: %w", err)
		}
	case claudeLedgerStatusFailed:
		if !slices.Contains(claudeLedgerFailures, rec.Failure) || rec.Usage != nil || rec.ThinkingBlocksObserved {
			return errors.New("failed record lacks a known failure code or carries usage or thinking audit")
		}
		switch rec.Failure {
		case claudeLedgerFailLaunch, claudeLedgerFailNoObservation:
			if rec.ObservedModel != "" {
				return errors.New("failed record names an observed model without an observation")
			}
		case claudeLedgerFailModel:
			if !slices.Contains(claudeLedgerModels, rec.ObservedModel) || rec.ObservedModel == rec.RequestedModel {
				return errors.New("model failure lacks a candidate observed model that differs from requested")
			}
		case claudeLedgerFailUsage:
			if rec.ObservedModel != rec.RequestedModel {
				return errors.New("usage failure lacks the requested observed model")
			}
		}
	default:
		return fmt.Errorf("unknown status %q", rec.Status)
	}
	return nil
}

// claudeLedgerIsSHA256 reports whether s is a hex SHA-256 digest.
func claudeLedgerIsSHA256(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}

// claudeLedgerIntPtr returns a pointer to v for presence-aware usage fields.
func claudeLedgerIntPtr(v int) *int {
	return &v
}
