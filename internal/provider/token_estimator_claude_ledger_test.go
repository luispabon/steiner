package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/fsatomic"
)

// The attempt ledger is test-only. It counts the Claude token estimation launches
// of one run against a lifetime cap. Reserve persists a reservation before the
// launch and RunAttempt persists the outcome after it. Every reservation counts,
// failures included, and the count never shrinks. Create and Open hold an
// exclusive lock file until Close, and a mutex serializes calls within the
// process. Open fails closed on anything it cannot verify. The protocol is in
// token_estimator_claude_ledger_protocol_test.go.

// claudeLedger is one exclusive session over a ledger directory.
type claudeLedger struct {
	// mu serializes calls within this process. The lock file excludes other processes.
	mu       sync.Mutex
	dir      string
	lock     *os.File
	attempts []claudeLedgerRecord
	// syncDir fsyncs the ledger directory after each write. Tests replace it to
	// inject a failure.
	syncDir func(dir string) error
	// poisoned is set when the directory fsync fails after a write. The new state
	// may or may not be durable, so the session refuses further attempts.
	poisoned error
}

// newClaudeLedger returns a session over dir that holds lock.
func newClaudeLedger(dir string, lock *os.File) *claudeLedger {
	return &claudeLedger{dir: dir, lock: lock, syncDir: claudeLedgerSyncDir}
}

// claudeLedgerCreate starts an empty ledger in dir. It refuses a dir that already
// holds a ledger, so a fresh start is always an explicit call.
func claudeLedgerCreate(dir string) (*claudeLedger, error) {
	lock, err := claudeLedgerLock(dir)
	if err != nil {
		return nil, err
	}
	l := newClaudeLedger(dir, lock)
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
	l := newClaudeLedger(dir, lock)
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

// Close ends the session and removes the lock. It is idempotent, and it still
// works after a poisoned write.
func (l *claudeLedger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.attempts)
}

// Reserve persists a reservation for in. It runs before any launch, and the
// reservation counts from then on even if the launch fails or the outcome is
// never written.
func (l *claudeLedger) Reserve(in claudeLedgerReserveInput) (claudeLedgerRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reserve(in)
}

// reserve is Reserve without locking. The caller holds l.mu.
func (l *claudeLedger) reserve(in claudeLedgerReserveInput) (claudeLedgerRecord, error) {
	switch {
	case l.lock == nil:
		return claudeLedgerRecord{}, errClaudeLedgerClosed
	case l.poisoned != nil:
		return claudeLedgerRecord{}, l.poisoned
	}
	if err := in.check(); err != nil {
		return claudeLedgerRecord{}, err
	}
	if in.Request.Model != in.RequestedModel {
		return claudeLedgerRecord{}, fmt.Errorf("%w: request model %q differs from %q", errClaudeLedgerInput, in.Request.Model, in.RequestedModel)
	}
	request, err := json.Marshal(in.Request)
	if err != nil {
		return claudeLedgerRecord{}, fmt.Errorf("encode attempt request: %w", err)
	}
	if err := claudeLedgerValidateRequest(request, in.RequestedModel); err != nil {
		return claudeLedgerRecord{}, fmt.Errorf("%w: %w", errClaudeLedgerInput, err)
	}
	if len(l.attempts) >= claudeLedgerMaxAttempts {
		return claudeLedgerRecord{}, errClaudeLedgerCap
	}
	for _, rec := range l.attempts {
		if rec.RequestedModel == in.RequestedModel && rec.Slot == in.Slot {
			return claudeLedgerRecord{}, errClaudeLedgerDuplicate
		}
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
// reserved record. A launch error, a missing observation, an observed model other
// than the requested one, or invalid usage is recorded as a failed attempt with a
// fixed failure code, and the attempt still counts. The returned error covers
// ledger failures only: a refused reservation (launch does not run) or an outcome
// that could not be persisted.
//
// launch runs while the ledger mutex is held. It must not call Reserve,
// RunAttempt, Total, or Close, which would deadlock. Reading the ledger file on
// disk is allowed.
func (l *claudeLedger) RunAttempt(in claudeLedgerReserveInput, launch func(claudeLedgerRecord) (*claudeLedgerObservation, error)) (claudeLedgerRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, err := l.reserve(in)
	if err != nil {
		return claudeLedgerRecord{}, err
	}
	obs, launchErr := launch(rec)
	return l.complete(rec.AttemptID, obs, launchErr)
}

// complete persists the outcome of attempt id, which must still be reserved. The
// caller holds l.mu. Launch error text is dropped: only a fixed failure code is
// stored.
func (l *claudeLedger) complete(id int, obs *claudeLedgerObservation, launchErr error) (claudeLedgerRecord, error) {
	idx := id - 1
	if idx < 0 || idx >= len(l.attempts) || l.attempts[idx].Status != claudeLedgerStatusReserved {
		return claudeLedgerRecord{}, fmt.Errorf("attempt %d is not reserved", id)
	}
	rec := l.attempts[idx]
	if obs != nil && slices.Contains(claudeLedgerModels, obs.ObservedModel) {
		rec.ObservedModel = obs.ObservedModel
	}
	switch {
	case launchErr != nil:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, claudeLedgerFailLaunch
	case obs == nil:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, claudeLedgerFailNoObservation
	case obs.ObservedModel != rec.RequestedModel:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, claudeLedgerFailModel
	case claudeLedgerValidateUsage(obs.claudeLedgerUsage) != nil:
		rec.Status, rec.Failure = claudeLedgerStatusFailed, claudeLedgerFailUsage
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

// commit writes attempts as the whole ledger file and then syncs the directory so
// the rename survives a crash. fsatomic syncs the file but not its directory. A
// failed file write leaves the in-memory state unchanged. A failed directory sync
// happens after the rename, so the new state is kept, counted, and the session is
// poisoned: the write may or may not be durable, and no attempt may be burned or
// reused on that basis.
func (l *claudeLedger) commit(attempts []claudeLedgerRecord) error {
	data, err := claudeLedgerEncode(attempts)
	if err != nil {
		return err
	}
	if err := fsatomic.WriteFile(claudeLedgerPath(l.dir), data, 0o600); err != nil {
		return fmt.Errorf("write attempt ledger: %w", err)
	}
	l.attempts = attempts
	if err := l.syncDir(l.dir); err != nil {
		l.poisoned = fmt.Errorf("%w: %w", errClaudeLedgerDurability, err)
		return l.poisoned
	}
	return nil
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

// claudeLedgerDecode parses and validates ledger bytes. A repeated key anywhere
// fails closed before decoding, because encoding/json keeps the last of two equal
// keys. Unknown fields, trailing data, a missing attempts array, and any record
// outside the protocol also fail closed.
func claudeLedgerDecode(data []byte) ([]claudeLedgerRecord, error) {
	if err := claudeLedgerRejectDuplicateKeys(data); err != nil {
		return nil, fmt.Errorf("%w: %w", errClaudeLedgerCorrupt, err)
	}
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

// claudeLedgerRejectDuplicateKeys walks every JSON value in data and fails on a
// repeated object key. encoding/json keeps the last of two equal keys, so a second
// key could silently replace an earlier count or request field.
func claudeLedgerRejectDuplicateKeys(data []byte) error {
	return claudeLedgerWalk(json.NewDecoder(bytes.NewReader(data)))
}

// claudeLedgerWalk consumes one JSON value from dec, recursing into objects and arrays.
func claudeLedgerWalk(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = true
			if err := claudeLedgerWalk(dec); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for dec.More() {
			if err := claudeLedgerWalk(dec); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token() // the closing delimiter
	return err
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
