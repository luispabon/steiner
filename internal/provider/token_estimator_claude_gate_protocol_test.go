package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
)

// The Claude token gate protocol is test-only and offline. It holds the pinned
// launch controls, the evidence document, the request builder shared by every
// slot, and the checks that bind recorded attempts to the fixtures. The
// measurement and acceptance rules are in token_estimator_claude_validation_test.go.

const (
	claudeGateEvidenceName = "observations.json"
	// claudeGateSchemaVersion is the only evidence schema this code reads.
	claudeGateSchemaVersion = 1
	// claudeGateMaxOutputTokens is the output cap of every launch.
	claudeGateMaxOutputTokens = 64
	// claudeGateMaxTokens bounds every token count the gate compares, observed or
	// predicted. With this bound, differences and products of two counts fit int64.
	claudeGateMaxTokens = 1 << 40
)

// Failure codes. A report holds only these fixed strings, never launch error text.
const (
	claudeGateFailRecord    = "attempt record invalid"
	claudeGateFailState     = "attempt not succeeded"
	claudeGateFailMissing   = "required slot missing"
	claudeGateFailDuplicate = "duplicate attempt"
	claudeGateFailPins      = "pins mismatch"
	claudeGateFailRequest   = "recorded request mismatch"
	claudeGateFailBudget    = "attempt budget exceeded"
	claudeGateFailBounds    = "token count out of gate bounds"
	claudeGateFailObserved  = "observed delta not positive"
	claudeGateFailText      = "text delta gate failed"
	claudeGateFailCategory  = "text category gate failed"
	claudeGateFailTool      = "tool delta gate failed"
	claudeGateFailFull      = "full request gate failed"
)

// claudeGateSlotPayload names the fixture payload each slot adds to the system
// message. Slots without a payload map to "".
var claudeGateSlotPayload = map[claudeLedgerSlot]string{
	claudeLedgerSlotBaseline:   "",
	claudeLedgerSlotCodeJSON:   "code_json",
	claudeLedgerSlotMarkdown:   "markdown_prose",
	claudeLedgerSlotUnicode:    "unicode",
	claudeLedgerSlotTools:      "",
	claudeLedgerSlotMixedTools: "mixed",
}

// claudeGateTextSlots lists the three text-delta slots in report order.
var claudeGateTextSlots = []claudeLedgerSlot{claudeLedgerSlotCodeJSON, claudeLedgerSlotMarkdown, claudeLedgerSlotUnicode}

// claudeGateLaunch holds the launch controls of every attempt. ChatRequest's JSON
// omits the controls named in JSONOmitted, so they are recorded here. The recorded
// request does not prove that step 5 applied them at launch.
type claudeGateLaunch struct {
	Reasoning       string   `json:"reasoning"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	Turns           int      `json:"turns"`
	History         string   `json:"history"`
	JSONOmitted     []string `json:"json_omitted"`
}

// claudeGateLaunchPinned returns the only launch protocol the gate accepts. The
// JSONOmitted names are ChatRequest Go field names whose tag is json:"-".
func claudeGateLaunchPinned() claudeGateLaunch {
	return claudeGateLaunch{
		Reasoning:       "off",
		MaxOutputTokens: claudeGateMaxOutputTokens,
		Turns:           1,
		History:         "none",
		JSONOmitted: []string{
			"TransportSession", "ParentTransportSession", "Params", "ExtraParams",
			"PromptCacheKey", "IncludeEmptyReasoning", "Reasoning", "AdvisorCacheProfile",
			"BackendState", "CompactionSummary", "CompactionSourceMessages",
		},
	}
}

// claudeGateEvidence is the single versioned evidence document. Step 5 writes it
// and this package reads it. It stores no CLI result usage and no computed
// results. The gate recomputes every value from the attempts and the fixtures.
type claudeGateEvidence struct {
	SchemaVersion int                  `json:"schema_version"`
	CLIVersion    string               `json:"cli_version"`
	LibraryCommit string               `json:"library_commit"`
	PayloadSHA256 map[string]string    `json:"payload_sha256"`
	ToolsSHA256   string               `json:"tools_sha256"`
	Launch        claudeGateLaunch     `json:"launch"`
	Attempts      []claudeLedgerRecord `json:"attempts"`
}

// claudeGateRequest returns the normalized request for model and slot. The payload
// is concatenated to the system text with no separator. The user message is
// claudeFixtureUser, and there is no history. The CLI system prompt later appends
// claudeSubToolNote to every slot. That note is constant, so predictions leave it
// out and its effect cancels in deltas. Step 5 must render this request through
// claudeSubSystemPrompt and claudeSubMessages, not a hand-built argument list.
func claudeGateRequest(model string, slot claudeLedgerSlot, set claudeFixtureSet) (ChatRequest, error) {
	name, ok := claudeGateSlotPayload[slot]
	if !ok {
		return ChatRequest{}, fmt.Errorf("claude gate slot %q is not in the protocol", slot)
	}
	system := claudeFixtureSystem
	if name != "" {
		p, err := claudeGatePayload(set, name)
		if err != nil {
			return ChatRequest{}, err
		}
		system += string(p.Bytes)
	}
	var tools []ToolSpec
	if claudeGateHasTools(slot) {
		tools = set.Tools
	}
	maxTokens := claudeGateMaxOutputTokens
	return ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: MessageRoleSystem, Content: system},
			{Role: MessageRoleUser, Content: claudeFixtureUser},
		},
		MaxTokens: &maxTokens,
		Tools:     tools,
	}, nil
}

// claudeGateHasTools reports whether slot sends the fixture tool schemas.
func claudeGateHasTools(slot claudeLedgerSlot) bool {
	return slot == claudeLedgerSlotTools || slot == claudeLedgerSlotMixedTools
}

// claudeGatePayload returns the verified fixture payload called name.
func claudeGatePayload(set claudeFixtureSet, name string) (claudeFixturePayload, error) {
	for _, p := range set.Payloads {
		if p.Name == name {
			return p, nil
		}
	}
	return claudeFixturePayload{}, fmt.Errorf("claude fixture payload %q is missing", name)
}

// claudeGateSHA256 returns the lowercase hex SHA-256 of b.
func claudeGateSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// claudeGatePayloadSHA returns the payload hash a slot records. A slot without a
// payload records the hash of empty bytes. An unknown name returns "", which
// fails the pin check.
func claudeGatePayloadSHA(set claudeFixtureSet, slot claudeLedgerSlot) string {
	name := claudeGateSlotPayload[slot]
	if name == "" {
		return claudeGateSHA256(nil)
	}
	for _, p := range set.Payloads {
		if p.Name == name {
			return p.SHA256
		}
	}
	return ""
}

// claudeGateToolsSHA returns the tools hash a slot records: the pinned tools file
// for slots that send tools, and the hash of empty bytes otherwise.
func claudeGateToolsSHA(set claudeFixtureSet, slot claudeLedgerSlot) string {
	if claudeGateHasTools(slot) {
		return set.Manifest.Tools.SHA256
	}
	return claudeGateSHA256(nil)
}

// claudeGateLoadEvidence reads the evidence file at path. Duplicate keys, unknown
// fields, trailing data, a schema other than claudeGateSchemaVersion, and a
// missing attempts array fail.
func claudeGateLoadEvidence(path string) (claudeGateEvidence, error) {
	var ev claudeGateEvidence
	data, err := os.ReadFile(path)
	if err != nil {
		return ev, fmt.Errorf("read claude gate evidence: %w", err)
	}
	if err := claudeLedgerRejectDuplicateKeys(data); err != nil {
		return ev, fmt.Errorf("parse claude gate evidence: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ev); err != nil {
		return ev, fmt.Errorf("parse claude gate evidence: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ev, errors.New("parse claude gate evidence: data after the document")
	}
	if ev.SchemaVersion != claudeGateSchemaVersion {
		return ev, fmt.Errorf("claude gate evidence schema_version %d, want %d", ev.SchemaVersion, claudeGateSchemaVersion)
	}
	if ev.Attempts == nil {
		return ev, errors.New("claude gate evidence attempts array missing")
	}
	return ev, nil
}

// claudeGatePinsMatch reports whether the evidence pins equal the fixture pins and
// the launch protocol is the pinned one.
func claudeGatePinsMatch(set claudeFixtureSet, ev claudeGateEvidence) bool {
	if ev.LibraryCommit != set.Manifest.Library.Commit || ev.ToolsSHA256 != set.Manifest.Tools.SHA256 ||
		len(ev.PayloadSHA256) != len(set.Manifest.Payloads) || !reflect.DeepEqual(ev.Launch, claudeGateLaunchPinned()) {
		return false
	}
	for _, pin := range set.Manifest.Payloads {
		if ev.PayloadSHA256[pin.Name] != pin.SHA256 {
			return false
		}
	}
	return true
}

// claudeGateEvidenceFailures returns the faults that apply to every model:
// pins, the lifetime budget, and records that are invalid, out of sequence, or
// duplicated.
func claudeGateEvidenceFailures(set claudeFixtureSet, ev claudeGateEvidence) []string {
	var fails []string
	add := func(code string) {
		if !slices.Contains(fails, code) {
			fails = append(fails, code)
		}
	}
	if !claudeGatePinsMatch(set, ev) {
		add(claudeGateFailPins)
	}
	if len(ev.Attempts) > claudeLedgerMaxAttempts {
		add(claudeGateFailBudget)
	}
	seen := map[string]bool{}
	for i, rec := range ev.Attempts {
		if rec.AttemptID != i+1 || claudeLedgerValidateRecord(rec) != nil ||
			(rec.Usage != nil && int64(rec.Usage.PromptTotal) > claudeGateMaxTokens) {
			add(claudeGateFailRecord)
		}
		if rec.CLIVersion != ev.CLIVersion {
			add(claudeGateFailPins)
		}
		key := rec.RequestedModel + "\x00" + string(rec.Slot)
		if seen[key] {
			add(claudeGateFailDuplicate)
		}
		seen[key] = true
	}
	return fails
}

// claudeGateModelRecords returns the recorded attempt of each slot of model, and a
// failure for every record or slot that is not proven. A record is proven when it
// succeeded, its pins equal the fixtures, and its request equals the rebuilt one.
func claudeGateModelRecords(set claudeFixtureSet, ev claudeGateEvidence, model string) (map[claudeLedgerSlot]claudeLedgerRecord, []string) {
	recs := map[claudeLedgerSlot]claudeLedgerRecord{}
	var fails []string
	add := func(code string) {
		if !slices.Contains(fails, code) {
			fails = append(fails, code)
		}
	}
	for _, rec := range ev.Attempts {
		if rec.RequestedModel != model {
			continue
		}
		if rec.Status != claudeLedgerStatusSucceeded {
			add(claudeGateFailState)
			continue
		}
		if rec.LibraryCommit != set.Manifest.Library.Commit || rec.PayloadSHA256 != claudeGatePayloadSHA(set, rec.Slot) ||
			rec.ToolsSHA256 != claudeGateToolsSHA(set, rec.Slot) {
			add(claudeGateFailPins)
		}
		want, err := claudeGateRequest(model, rec.Slot, set)
		wantJSON, mErr := json.Marshal(want)
		if err != nil || mErr != nil || !claudeGateSameRequest(rec.Request, wantJSON) {
			add(claudeGateFailRequest)
		}
		recs[rec.Slot] = rec
	}
	for _, slot := range claudeLedgerSlots {
		if _, ok := recs[slot]; !ok {
			add(claudeGateFailMissing)
		}
	}
	return recs, fails
}

// claudeGateSameRequest reports whether recorded, in compact form, equals want.
func claudeGateSameRequest(recorded json.RawMessage, want []byte) bool {
	var compact bytes.Buffer
	if err := json.Compact(&compact, recorded); err != nil {
		return false
	}
	return bytes.Equal(compact.Bytes(), want)
}
