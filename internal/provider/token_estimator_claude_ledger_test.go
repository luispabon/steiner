package provider

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/fsatomic"
)

// The attempt ledger is test-only. It counts the Claude token estimation launches
// of one run against a lifetime cap. Reserve persists a reservation before the
// launch and RunAttempt persists the outcome after it. Every reservation counts,
// failures included, and the count never shrinks. Create and Open hold an
// exclusive lock until Close. Open fails closed on anything it cannot verify.
// Stream observations come from claudeLedgerObserveStream.

const (
	// claudeLedgerSchemaVersion is the only ledger schema this code reads.
	claudeLedgerSchemaVersion = 1
	// claudeLedgerMaxAttempts caps lifetime attempts. It equals one attempt per
	// (model, slot) pair, which TestClaudeTokenLedgerProtocolEnumsArePinned checks.
	claudeLedgerMaxAttempts = 18
	claudeLedgerFileName    = "attempts.json"
	claudeLedgerLockName    = "attempts.lock"
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

var (
	errClaudeLedgerExists    = errors.New("attempt ledger already exists")
	errClaudeLedgerMissing   = errors.New("attempt ledger missing on resume")
	errClaudeLedgerLocked    = errors.New("attempt ledger locked by another session")
	errClaudeLedgerCorrupt   = errors.New("attempt ledger corrupt")
	errClaudeLedgerCap       = errors.New("attempt ledger cap reached")
	errClaudeLedgerDuplicate = errors.New("attempt ledger already holds this model and slot")
	errClaudeLedgerInput     = errors.New("invalid attempt reservation")
)

// claudeLedgerUsage is the presence-aware usage of the terminal assistant message.
// A nil cache field was not reported by the CLI, which differs from an explicit
// zero. PromptTotal is the production PromptTokens for that message, counted once.
// The CLI result usage is never added.
type claudeLedgerUsage struct {
	Input         *int `json:"input_tokens,omitempty"`
	CacheCreation *int `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     *int `json:"cache_read_input_tokens,omitempty"`
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
}

// claudeLedgerFile is the on-disk ledger document.
type claudeLedgerFile struct {
	SchemaVersion int                  `json:"schema_version"`
	Attempts      []claudeLedgerRecord `json:"attempts"`
}

// claudeLedgerReserveInput names one attempt before its launch.
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

// claudeLedger is one exclusive session over a ledger directory.
type claudeLedger struct {
	dir      string
	lock     *os.File
	attempts []claudeLedgerRecord
}

// claudeLedgerCreate starts an empty ledger in dir. It refuses a dir that already
// holds a ledger, so a fresh start is always an explicit call.
func claudeLedgerCreate(dir string) (*claudeLedger, error) {
	lock, err := claudeLedgerLock(dir)
	if err != nil {
		return nil, err
	}
	l := &claudeLedger{dir: dir, lock: lock}
	_, err = os.Lstat(claudeLedgerPath(dir))
	switch {
	case err == nil:
		return nil, l.fail(errClaudeLedgerExists)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, l.fail(fmt.Errorf("stat attempt ledger: %w", err))
	}
	if err := l.commit(nil); err != nil {
		return nil, l.fail(err)
	}
	return l, nil
}

// claudeLedgerOpen resumes the ledger in dir and holds its lock until Close. A
// missing, unreadable, or unverifiable ledger fails closed and nothing is created.
func claudeLedgerOpen(dir string) (*claudeLedger, error) {
	lock, err := claudeLedgerLock(dir)
	if err != nil {
		return nil, err
	}
	l := &claudeLedger{dir: dir, lock: lock}
	attempts, err := claudeLedgerLoad(dir)
	if err != nil {
		return nil, l.fail(err)
	}
	l.attempts = attempts
	return l, nil
}

// claudeLedgerLock takes the exclusive session lock. O_EXCL makes a second holder
// fail. A lock left by a crashed session must be removed by hand after checking
// that no session is running.
func claudeLedgerLock(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, claudeLedgerLockName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, errClaudeLedgerLocked
	}
	if err != nil {
		return nil, fmt.Errorf("take attempt ledger lock: %w", err)
	}
	return f, nil
}

// Close ends the session and removes the lock. It is idempotent.
func (l *claudeLedger) Close() error {
	if l.lock == nil {
		return nil
	}
	closeErr := l.lock.Close()
	l.lock = nil
	if err := os.Remove(filepath.Join(l.dir, claudeLedgerLockName)); err != nil {
		return errors.Join(closeErr, fmt.Errorf("remove attempt ledger lock: %w", err))
	}
	return closeErr
}

// fail releases the lock and returns err with any release error joined.
func (l *claudeLedger) fail(err error) error {
	return errors.Join(err, l.Close())
}

// Total returns the number of counted attempts, failures and orphaned
// reservations included.
func (l *claudeLedger) Total() int {
	return len(l.attempts)
}

// Reserve persists a reservation for in. It runs before any launch, and the
// reservation counts from then on even if the launch fails or the outcome is
// never written.
func (l *claudeLedger) Reserve(in claudeLedgerReserveInput) (claudeLedgerRecord, error) {
	if err := in.check(); err != nil {
		return claudeLedgerRecord{}, err
	}
	if in.Request.Model != in.RequestedModel {
		return claudeLedgerRecord{}, fmt.Errorf("%w: request model %q differs from %q", errClaudeLedgerInput, in.Request.Model, in.RequestedModel)
	}
	if len(l.attempts) >= claudeLedgerMaxAttempts {
		return claudeLedgerRecord{}, errClaudeLedgerCap
	}
	for _, rec := range l.attempts {
		if rec.RequestedModel == in.RequestedModel && rec.Slot == in.Slot {
			return claudeLedgerRecord{}, errClaudeLedgerDuplicate
		}
	}
	request, err := json.Marshal(in.Request)
	if err != nil {
		return claudeLedgerRecord{}, fmt.Errorf("encode attempt request: %w", err)
	}
	rec := claudeLedgerRecord{
		AttemptID:      len(l.attempts) + 1,
		TimestampUTC:   time.Now().UTC().Format(time.RFC3339Nano),
		Slot:           in.Slot,
		RequestedModel: in.RequestedModel,
		Status:         claudeLedgerStatusReserved,
		CLIVersion:     in.CLIVersion,
		LibraryCommit:  in.LibraryCommit,
		PayloadSHA256:  in.PayloadSHA256,
		ToolsSHA256:    in.ToolsSHA256,
		Request:        request,
	}
	if err := l.commit(append(slices.Clone(l.attempts), rec)); err != nil {
		return claudeLedgerRecord{}, err
	}
	return rec, nil
}

// RunAttempt reserves in, persists the reservation, then calls launch with the
// reserved record. A launch error, a missing observation, or an observed model
// other than the requested one is recorded as a failed attempt, and the attempt
// still counts. The returned error covers ledger failures only: a refused
// reservation (launch does not run) or an outcome that could not be persisted.
func (l *claudeLedger) RunAttempt(in claudeLedgerReserveInput, launch func(claudeLedgerRecord) (*claudeLedgerObservation, error)) (claudeLedgerRecord, error) {
	rec, err := l.Reserve(in)
	if err != nil {
		return claudeLedgerRecord{}, err
	}
	obs, launchErr := launch(rec)
	return l.finish(rec.AttemptID, obs, launchErr)
}

// finish persists the outcome of attempt id, which must still be reserved.
func (l *claudeLedger) finish(id int, obs *claudeLedgerObservation, launchErr error) (claudeLedgerRecord, error) {
	idx := id - 1
	if idx < 0 || idx >= len(l.attempts) || l.attempts[idx].Status != claudeLedgerStatusReserved {
		return claudeLedgerRecord{}, fmt.Errorf("attempt %d is not reserved", id)
	}
	rec := l.attempts[idx]
	if obs != nil {
		rec.ObservedModel = obs.ObservedModel
	}
	switch {
	case launchErr != nil:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, launchErr.Error()
	case obs == nil:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, "launch returned no observation"
	case obs.ObservedModel != rec.RequestedModel:
		rec.Status = claudeLedgerStatusFailed
		rec.Failure = fmt.Sprintf("observed model %q, requested %q", obs.ObservedModel, rec.RequestedModel)
	default:
		usage := obs.claudeLedgerUsage
		rec.Status, rec.Usage = claudeLedgerStatusSucceeded, &usage
	}
	attempts := slices.Clone(l.attempts)
	attempts[idx] = rec
	if err := l.commit(attempts); err != nil {
		return claudeLedgerRecord{}, err
	}
	return rec, nil
}

// commit writes attempts as the whole ledger file and syncs the directory so the
// rename survives a crash. fsatomic syncs the file but not its directory. The
// in-memory state moves only after the file write succeeds.
func (l *claudeLedger) commit(attempts []claudeLedgerRecord) error {
	data, err := claudeLedgerEncode(attempts)
	if err != nil {
		return err
	}
	if err := fsatomic.WriteFile(claudeLedgerPath(l.dir), data, 0o600); err != nil {
		return fmt.Errorf("write attempt ledger: %w", err)
	}
	l.attempts = attempts
	return claudeLedgerSyncDir(l.dir)
}

// claudeLedgerSyncDir fsyncs dir so a rename inside it is durable.
func claudeLedgerSyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open attempt ledger dir: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return fmt.Errorf("sync attempt ledger dir: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close attempt ledger dir: %w", err)
	}
	return nil
}

// claudeLedgerLoad reads and verifies the ledger file in dir.
func claudeLedgerLoad(dir string) ([]claudeLedgerRecord, error) {
	path := claudeLedgerPath(dir)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errClaudeLedgerMissing
	}
	if err != nil {
		return nil, fmt.Errorf("stat attempt ledger: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: want a regular file with mode 600, got %v", errClaudeLedgerCorrupt, info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read attempt ledger: %w", err)
	}
	return claudeLedgerDecode(data)
}

// claudeLedgerDecode parses and validates ledger bytes. Unknown fields, trailing
// data, a missing attempts array, and any record outside the protocol fail closed.
func claudeLedgerDecode(data []byte) ([]claudeLedgerRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file claudeLedgerFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%w: %w", errClaudeLedgerCorrupt, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the ledger document", errClaudeLedgerCorrupt)
	}
	if file.SchemaVersion != claudeLedgerSchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d, want %d", errClaudeLedgerCorrupt, file.SchemaVersion, claudeLedgerSchemaVersion)
	}
	if file.Attempts == nil {
		return nil, fmt.Errorf("%w: attempts array missing", errClaudeLedgerCorrupt)
	}
	if len(file.Attempts) > claudeLedgerMaxAttempts {
		return nil, fmt.Errorf("%w: %d attempts exceed the cap of %d", errClaudeLedgerCorrupt, len(file.Attempts), claudeLedgerMaxAttempts)
	}
	seen := make(map[string]bool, len(file.Attempts))
	for i, rec := range file.Attempts {
		if rec.AttemptID != i+1 {
			return nil, fmt.Errorf("%w: attempt at position %d has attempt_id %d", errClaudeLedgerCorrupt, i+1, rec.AttemptID)
		}
		if err := claudeLedgerValidateRecord(rec); err != nil {
			return nil, fmt.Errorf("%w: attempt %d: %w", errClaudeLedgerCorrupt, rec.AttemptID, err)
		}
		key := rec.RequestedModel + "\x00" + string(rec.Slot)
		if seen[key] {
			return nil, fmt.Errorf("%w: attempt %d repeats a model and slot", errClaudeLedgerCorrupt, rec.AttemptID)
		}
		seen[key] = true
	}
	return file.Attempts, nil
}

// claudeLedgerValidateRecord checks one stored record. The status decides which
// outcome fields may be set.
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
	switch rec.Status {
	case claudeLedgerStatusReserved:
		if rec.ObservedModel != "" || rec.Usage != nil || rec.Failure != "" {
			return errors.New("reserved record carries an outcome")
		}
	case claudeLedgerStatusSucceeded:
		if rec.ObservedModel != rec.RequestedModel || rec.Usage == nil || rec.Failure != "" {
			return errors.New("succeeded record lacks a matching observation")
		}
	case claudeLedgerStatusFailed:
		if rec.Failure == "" || rec.Usage != nil {
			return errors.New("failed record lacks a failure or carries usage")
		}
	default:
		return fmt.Errorf("unknown status %q", rec.Status)
	}
	return nil
}

// claudeLedgerEncode renders the ledger document. An empty ledger keeps an
// explicit empty attempts array so a truncated file is not read as empty.
func claudeLedgerEncode(attempts []claudeLedgerRecord) ([]byte, error) {
	if attempts == nil {
		attempts = []claudeLedgerRecord{}
	}
	data, err := json.MarshalIndent(claudeLedgerFile{SchemaVersion: claudeLedgerSchemaVersion, Attempts: attempts}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode attempt ledger: %w", err)
	}
	return append(data, '\n'), nil
}

func claudeLedgerPath(dir string) string {
	return filepath.Join(dir, claudeLedgerFileName)
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

// claudeLedgerTestInput returns a valid reservation for model and slot. The pins
// come from the loaded fixture.
func claudeLedgerTestInput(set claudeFixtureSet, model string, slot claudeLedgerSlot) claudeLedgerReserveInput {
	return claudeLedgerReserveInput{
		Slot:           slot,
		RequestedModel: model,
		CLIVersion:     "test-cli 1.0.0",
		LibraryCommit:  set.Manifest.Library.Commit,
		PayloadSHA256:  set.Payloads[0].SHA256,
		ToolsSHA256:    set.Manifest.Tools.SHA256,
		Request: ChatRequest{
			Model:    model,
			Messages: []Message{{Role: MessageRoleUser, Content: claudeFixtureUser}},
		},
	}
}

// claudeLedgerFakeObservation returns the observation a fake launch reports for
// model. It stands in for a stream, so the tests never start a CLI.
func claudeLedgerFakeObservation(model string) *claudeLedgerObservation {
	return &claudeLedgerObservation{
		ObservedModel:     model,
		claudeLedgerUsage: claudeLedgerUsage{Input: claudeLedgerIntPtr(3), PromptTotal: 3},
	}
}

// claudeLedgerOnDisk reads the ledger file in dir through the production validator.
func claudeLedgerOnDisk(t *testing.T, dir string) []claudeLedgerRecord {
	t.Helper()
	data, err := os.ReadFile(claudeLedgerPath(dir))
	if err != nil {
		t.Fatalf("read attempt ledger: %v", err)
	}
	recs, err := claudeLedgerDecode(data)
	if err != nil {
		t.Fatalf("decode attempt ledger: %v", err)
	}
	return recs
}

// claudeLedgerCreateForTest creates a ledger in a fresh temp dir. Cleanup closes
// it, which is a no-op when the test already closed it.
func claudeLedgerCreateForTest(t *testing.T) (string, *claudeLedger) {
	t.Helper()
	dir := t.TempDir()
	l, err := claudeLedgerCreate(dir)
	if err != nil {
		t.Fatalf("create attempt ledger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return dir, l
}

// claudeLedgerValidFile returns the bytes of a ledger holding one succeeded attempt.
func claudeLedgerValidFile(t *testing.T, set claudeFixtureSet) []byte {
	t.Helper()
	dir, l := claudeLedgerCreateForTest(t)
	in := claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline)
	if _, err := l.RunAttempt(in, func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return claudeLedgerFakeObservation(rec.RequestedModel), nil
	}); err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close attempt ledger: %v", err)
	}
	data, err := os.ReadFile(claudeLedgerPath(dir))
	if err != nil {
		t.Fatalf("read attempt ledger: %v", err)
	}
	return data
}

func claudeLedgerModeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", filepath.Base(path), err)
	}
	return info.Mode().Perm()
}

func TestClaudeTokenLedgerReservesBeforeLaunch(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	dir, l := claudeLedgerCreateForTest(t)
	in := claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline)
	launched := false
	rec, err := l.RunAttempt(in, func(reserved claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launched = true
		onDisk := claudeLedgerOnDisk(t, dir)
		if len(onDisk) != 1 || onDisk[0].Status != claudeLedgerStatusReserved || onDisk[0].AttemptID != reserved.AttemptID {
			t.Errorf("on disk before launch = %+v, want one reserved attempt %d", onDisk, reserved.AttemptID)
		}
		return claudeLedgerFakeObservation(reserved.RequestedModel), nil
	})
	if err != nil {
		t.Fatalf("run attempt: %v", err)
	}
	if !launched {
		t.Fatal("launch callback did not run")
	}
	if rec.AttemptID != 1 || rec.Status != claudeLedgerStatusSucceeded {
		t.Errorf("record = attempt %d status %q, want attempt 1 succeeded", rec.AttemptID, rec.Status)
	}
	if rec.RequestedModel != "claude-haiku-5-5" || rec.ObservedModel != "claude-haiku-5-5" {
		t.Errorf("identity = requested %q observed %q", rec.RequestedModel, rec.ObservedModel)
	}
	if rec.CLIVersion != in.CLIVersion || rec.LibraryCommit != set.Manifest.Library.Commit || rec.PayloadSHA256 != in.PayloadSHA256 || rec.ToolsSHA256 != set.Manifest.Tools.SHA256 {
		t.Errorf("pins = %q %q %q %q, want the reserved pins", rec.CLIVersion, rec.LibraryCommit, rec.PayloadSHA256, rec.ToolsSHA256)
	}
	if !strings.Contains(string(rec.Request), `"claude-haiku-5-5"`) {
		t.Errorf("request = %s, want the normalized request", rec.Request)
	}
	if _, err := time.Parse(time.RFC3339Nano, rec.TimestampUTC); err != nil || !strings.HasSuffix(rec.TimestampUTC, "Z") {
		t.Errorf("timestamp = %q, want RFC 3339 UTC", rec.TimestampUTC)
	}
	if rec.Usage == nil || rec.Usage.PromptTotal != 3 {
		t.Errorf("usage = %+v, want prompt total 3", rec.Usage)
	}
	if got := claudeLedgerModeOf(t, claudeLedgerPath(dir)); got != 0o600 {
		t.Errorf("ledger mode = %o, want 600", got)
	}
	onDisk := claudeLedgerOnDisk(t, dir)
	if len(onDisk) != 1 || onDisk[0].Status != claudeLedgerStatusSucceeded {
		t.Errorf("on disk after outcome = %+v, want one succeeded attempt", onDisk)
	}
}

func TestClaudeTokenLedgerProtocolEnumsArePinned(t *testing.T) {
	wantModels := []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}
	if !slices.Equal(claudeLedgerModels, wantModels) {
		t.Errorf("models = %v, want %v", claudeLedgerModels, wantModels)
	}
	wantSlots := []claudeLedgerSlot{"baseline", "codeJSON", "markdown", "Unicode", "tools", "mixedtools"}
	if !slices.Equal(claudeLedgerSlots, wantSlots) {
		t.Errorf("slots = %v, want %v", claudeLedgerSlots, wantSlots)
	}
	if claudeLedgerMaxAttempts != len(wantModels)*len(wantSlots) {
		t.Errorf("cap = %d, want one attempt per pair = %d", claudeLedgerMaxAttempts, len(wantModels)*len(wantSlots))
	}
	if claudeLedgerSchemaVersion != 1 {
		t.Errorf("schema version = %d, want 1", claudeLedgerSchemaVersion)
	}
}

func TestClaudeTokenLedgerRefusesUnlistedInputs(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	tests := []struct {
		name string
		edit func(*claudeLedgerReserveInput)
	}{
		{"model outside allowlist", func(in *claudeLedgerReserveInput) {
			in.RequestedModel, in.Request.Model = "claude-haiku-4-5", "claude-haiku-4-5"
		}},
		{"model with suffix", func(in *claudeLedgerReserveInput) {
			in.RequestedModel, in.Request.Model = "claude-opus-5-5-preview", "claude-opus-5-5-preview"
		}},
		{"slot outside protocol", func(in *claudeLedgerReserveInput) { in.Slot = "markdown_prose" }},
		{"request model differs", func(in *claudeLedgerReserveInput) { in.Request.Model = "claude-sonnet-5-5" }},
		{"payload hash not hex", func(in *claudeLedgerReserveInput) { in.PayloadSHA256 = "not-a-hash" }},
		{"missing cli version", func(in *claudeLedgerReserveInput) { in.CLIVersion = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, l := claudeLedgerCreateForTest(t)
			in := claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline)
			tt.edit(&in)
			launched := false
			_, err := l.RunAttempt(in, func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
				launched = true
				return nil, nil
			})
			if !errors.Is(err, errClaudeLedgerInput) {
				t.Errorf("err = %v, want errClaudeLedgerInput", err)
			}
			if launched || l.Total() != 0 {
				t.Errorf("launched = %t total = %d, want no launch and no count", launched, l.Total())
			}
		})
	}
}

func TestClaudeTokenLedgerCapCountsEveryPair(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	_, l := claudeLedgerCreateForTest(t)
	launches, n := 0, 0
	for _, model := range claudeLedgerModels {
		for _, slot := range claudeLedgerSlots {
			n++
			fail := n%2 == 0
			_, err := l.RunAttempt(claudeLedgerTestInput(set, model, slot), func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
				launches++
				if fail {
					return nil, errors.New("fake launch failed")
				}
				return claudeLedgerFakeObservation(rec.RequestedModel), nil
			})
			if err != nil {
				t.Fatalf("attempt %d: %v", n, err)
			}
		}
	}
	if l.Total() != claudeLedgerMaxAttempts || launches != claudeLedgerMaxAttempts {
		t.Fatalf("total = %d launches = %d, want %d each", l.Total(), launches, claudeLedgerMaxAttempts)
	}
	_, err := l.RunAttempt(claudeLedgerTestInput(set, claudeLedgerModels[0], claudeLedgerSlots[0]), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launches++
		return nil, nil
	})
	if !errors.Is(err, errClaudeLedgerCap) {
		t.Errorf("19th attempt err = %v, want errClaudeLedgerCap", err)
	}
	if launches != claudeLedgerMaxAttempts {
		t.Errorf("19th attempt launched; launches = %d", launches)
	}
}

func TestClaudeTokenLedgerFailuresConsumeAttempts(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	_, l := claudeLedgerCreateForTest(t)
	const haiku = "claude-haiku-5-5"
	launches := 0
	failing := func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launches++
		return nil, errors.New("cli exited 1")
	}
	rec, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), failing)
	if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != "cli exited 1" {
		t.Fatalf("failed launch = %+v, err %v; want a failed record", rec, err)
	}
	if _, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), failing); !errors.Is(err, errClaudeLedgerDuplicate) || launches != 1 {
		t.Errorf("retry after failure: err = %v launches = %d, want duplicate refusal with one launch", err, launches)
	}
	rec, err = l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotMarkdown), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return claudeLedgerFakeObservation("claude-sonnet-5-5"), nil
	})
	if err != nil || rec.Status != claudeLedgerStatusFailed || !strings.Contains(rec.Failure, "claude-sonnet-5-5") || rec.ObservedModel != "claude-sonnet-5-5" {
		t.Errorf("wrong observed model = %+v, err %v; want a failed record naming it", rec, err)
	}
	rec, err = l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotCodeJSON), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return nil, nil
	})
	if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != "launch returned no observation" {
		t.Errorf("no observation = %+v, err %v; want a failed record", rec, err)
	}
	if l.Total() != 3 {
		t.Errorf("total = %d, want 3 counted failures", l.Total())
	}
}

func TestClaudeTokenLedgerResumeKeepsCount(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	const haiku = "claude-haiku-5-5"
	dir, l := claudeLedgerCreateForTest(t)
	launch := func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return claudeLedgerFakeObservation(rec.RequestedModel), nil
	}
	for _, slot := range claudeLedgerSlots[:2] {
		if _, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, slot), launch); err != nil {
			t.Fatalf("run attempt: %v", err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	resumed, err := claudeLedgerOpen(dir)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	if resumed.Total() != 2 {
		t.Fatalf("resumed total = %d, want 2", resumed.Total())
	}
	if _, err := resumed.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlots[0]), launch); !errors.Is(err, errClaudeLedgerDuplicate) {
		t.Errorf("duplicate after resume: err = %v, want errClaudeLedgerDuplicate", err)
	}
	rec, err := resumed.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlots[2]), launch)
	if err != nil || rec.AttemptID != 3 {
		t.Fatalf("attempt after resume = %+v, err %v; want attempt 3", rec, err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("close resumed: %v", err)
	}
	again, err := claudeLedgerOpen(dir)
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	if again.Total() != 3 {
		t.Errorf("second resumed total = %d, want 3", again.Total())
	}
	if got := claudeLedgerOnDisk(t, dir); len(got) != 3 || got[2].AttemptID != 3 {
		t.Errorf("on disk = %+v, want three attempts ending at 3", got)
	}
}

func TestClaudeTokenLedgerOpenFailsClosed(t *testing.T) {
	t.Run("missing on resume", func(t *testing.T) {
		dir := t.TempDir()
		l, err := claudeLedgerOpen(dir)
		if !errors.Is(err, errClaudeLedgerMissing) || l != nil {
			t.Fatalf("open empty dir = %v, err %v; want errClaudeLedgerMissing", l, err)
		}
		if _, err := os.Lstat(claudeLedgerPath(dir)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("open created a ledger: stat err = %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, claudeLedgerLockName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("lock left behind: stat err = %v", err)
		}
	})

	set := loadClaudeFixtureForTest(t)
	valid := claudeLedgerValidFile(t, set)
	swap := func(from, to string) func([]byte) []byte {
		return func(b []byte) []byte { return []byte(strings.Replace(string(b), from, to, 1)) }
	}
	tests := []struct {
		name   string
		mode   fs.FileMode
		tamper func([]byte) []byte
	}{
		{"truncated", 0o600, func(b []byte) []byte { return b[:len(b)/2] }},
		{"trailing data", 0o600, func(b []byte) []byte { return append(slices.Clone(b), "{}"...) }},
		{"unknown field", 0o600, swap(`"schema_version": 1`, `"schema_version": 1, "extra": true`)},
		{"schema version 2", 0o600, swap(`"schema_version": 1`, `"schema_version": 2`)},
		{"attempt id gap", 0o600, swap(`"attempt_id": 1`, `"attempt_id": 2`)},
		{"unknown status", 0o600, swap(`"status": "succeeded"`, `"status": "bogus"`)},
		{"mode 644", 0o644, func(b []byte) []byte { return b }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := claudeLedgerPath(dir)
			data := tt.tamper(valid)
			if err := os.WriteFile(path, data, tt.mode); err != nil {
				t.Fatalf("write ledger: %v", err)
			}
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatalf("chmod ledger: %v", err)
			}
			l, err := claudeLedgerOpen(dir)
			if !errors.Is(err, errClaudeLedgerCorrupt) || l != nil {
				t.Errorf("open = %v, err %v; want errClaudeLedgerCorrupt", l, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("open changed the corrupt ledger: read err %v", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, claudeLedgerLockName)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("lock left behind after corrupt open: stat err = %v", err)
			}
		})
	}
}

func TestClaudeTokenLedgerLockIsExclusive(t *testing.T) {
	dir, l := claudeLedgerCreateForTest(t)
	if _, err := claudeLedgerOpen(dir); !errors.Is(err, errClaudeLedgerLocked) {
		t.Fatalf("second open err = %v, want errClaudeLedgerLocked", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, claudeLedgerLockName)); err != nil {
		t.Errorf("refused open removed the holder's lock: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	resumed, err := claudeLedgerOpen(dir)
	if err != nil {
		t.Fatalf("open after close: %v", err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatalf("close resumed: %v", err)
	}
}

func TestClaudeTokenLedgerCreateRefusesExisting(t *testing.T) {
	dir, l := claudeLedgerCreateForTest(t)
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := claudeLedgerCreate(dir); !errors.Is(err, errClaudeLedgerExists) {
		t.Errorf("create over existing ledger err = %v, want errClaudeLedgerExists", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, claudeLedgerLockName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("failed create left its lock: stat err = %v", err)
	}
}

func TestClaudeTokenLedgerWriteFailurePreventsLaunch(t *testing.T) {
	set := loadClaudeFixtureForTest(t)
	dir, l := claudeLedgerCreateForTest(t)
	// A directory where the ledger file belongs makes the reservation write fail.
	path := claudeLedgerPath(dir)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove ledger: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("mkdir in place of ledger: %v", err)
	}
	launched := false
	_, err := l.RunAttempt(claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launched = true
		return nil, nil
	})
	if err == nil {
		t.Error("RunAttempt succeeded with an unwritable ledger")
	}
	if launched {
		t.Error("launch ran without a persisted reservation")
	}
	if l.Total() != 0 {
		t.Errorf("total = %d, want 0 after failed reservation", l.Total())
	}
}
