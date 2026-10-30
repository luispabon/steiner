package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

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

// claudeLedgerEditRecord returns data re-encoded with its first record changed by
// edit. The edit may break the protocol; the reader under test must reject it.
func claudeLedgerEditRecord(t *testing.T, data []byte, edit func(*claudeLedgerRecord)) []byte {
	t.Helper()
	recs, err := claudeLedgerDecode(data)
	if err != nil || len(recs) == 0 {
		t.Fatalf("decode valid ledger: %v", err)
	}
	edit(&recs[0])
	out, err := claudeLedgerEncode(recs)
	if err != nil {
		t.Fatalf("encode edited ledger: %v", err)
	}
	return out
}

// claudeLedgerDirWith returns a fresh dir whose ledger file holds data at mode 0o600.
func claudeLedgerDirWith(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(claudeLedgerPath(dir), data, 0o600); err != nil {
		t.Fatalf("write ledger: %v", err)
	}
	return dir
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		{"request without messages", func(in *claudeLedgerReserveInput) { in.Request.Messages = nil }},
		{"request over size limit", func(in *claudeLedgerReserveInput) {
			in.Request.Messages = []Message{{Role: MessageRoleUser, Content: strings.Repeat("a", claudeLedgerMaxRequestBytes)}}
		}},
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
	t.Parallel()
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
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	_, l := claudeLedgerCreateForTest(t)
	const haiku = "claude-haiku-5-5"
	launches := 0
	failing := func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launches++
		return nil, errors.New("cli exited 1")
	}
	rec, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), failing)
	if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != claudeLedgerFailLaunch {
		t.Fatalf("failed launch = %+v, err %v; want a failed record with the launch code", rec, err)
	}
	if _, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), failing); !errors.Is(err, errClaudeLedgerDuplicate) || launches != 1 {
		t.Errorf("retry after failure: err = %v launches = %d, want duplicate refusal with one launch", err, launches)
	}
	rec, err = l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotMarkdown), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return claudeLedgerFakeObservation("claude-sonnet-5-5"), nil
	})
	if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != claudeLedgerFailModel || rec.ObservedModel != "claude-sonnet-5-5" {
		t.Errorf("wrong observed model = %+v, err %v; want a failed record with the model code", rec, err)
	}
	rec, err = l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotCodeJSON), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return nil, nil
	})
	if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != claudeLedgerFailNoObservation {
		t.Errorf("no observation = %+v, err %v; want a failed record", rec, err)
	}
	if l.Total() != 3 {
		t.Errorf("total = %d, want 3 counted failures", l.Total())
	}
}

// Launch error text can carry private paths or credentials. Only the fixed failure
// code may reach the file.
func TestClaudeTokenLedgerPersistsOnlyFailureCodes(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	dir, l := claudeLedgerCreateForTest(t)
	rec, err := l.RunAttempt(claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return nil, errors.New("exec /home/luis/.config/steiner/key sk-ant-api03-SECRET failed")
	})
	if err != nil || rec.Failure != claudeLedgerFailLaunch {
		t.Fatalf("record = %+v, err %v; want the launch failure code", rec, err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(claudeLedgerPath(dir))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, private := range []string{"/home/luis", "sk-ant", "SECRET"} {
		if bytes.Contains(data, []byte(private)) {
			t.Errorf("ledger holds %q from launch error text", private)
		}
	}
}

// An observation that does not prove the requested model and usage fails the
// attempt and persists a failed record, never a success.
func TestClaudeTokenLedgerInvalidObservationPersistsFailed(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	const haiku, opus = "claude-haiku-5-5", "claude-opus-5-5"
	tests := []struct {
		name     string
		obs      *claudeLedgerObservation
		want     string
		observed string
	}{
		{"nil observation", nil, claudeLedgerFailNoObservation, ""},
		{"empty observation", &claudeLedgerObservation{}, claudeLedgerFailNoObservation, ""},
		{"model outside candidates", claudeLedgerFakeObservation("claude-haiku-4-5"), claudeLedgerFailNoObservation, ""},
		{"other candidate model", claudeLedgerFakeObservation(opus), claudeLedgerFailModel, opus},
		{"model without usage", &claudeLedgerObservation{ObservedModel: haiku}, claudeLedgerFailUsage, haiku},
		{"negative component", &claudeLedgerObservation{ObservedModel: haiku, claudeLedgerUsage: claudeLedgerUsage{Input: claudeLedgerIntPtr(-2), CacheRead: claudeLedgerIntPtr(5), PromptTotal: 3}}, claudeLedgerFailUsage, haiku},
		// The wrapped sum is MaxInt-2, so only the overflow check rejects this.
		{"components overflow", &claudeLedgerObservation{ObservedModel: haiku, claudeLedgerUsage: claudeLedgerUsage{Input: claudeLedgerIntPtr(math.MaxInt), CacheCreation: claudeLedgerIntPtr(math.MaxInt), CacheRead: claudeLedgerIntPtr(math.MaxInt), PromptTotal: math.MaxInt - 2}}, claudeLedgerFailUsage, haiku},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, l := claudeLedgerCreateForTest(t)
			rec, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
				return tt.obs, nil
			})
			if err != nil || rec.Status != claudeLedgerStatusFailed || rec.Failure != tt.want || rec.ObservedModel != tt.observed || rec.Usage != nil {
				t.Errorf("record = %+v, err %v; want failed with %q, observed %q and no usage", rec, err, tt.want, tt.observed)
			}
			// The stored record must also pass the production validator on reload.
			if got := claudeLedgerOnDisk(t, dir); len(got) != 1 || got[0].Failure != tt.want || got[0].ObservedModel != tt.observed {
				t.Errorf("on disk = %+v, want the failure above", got)
			}
		})
	}
}

func TestClaudeTokenLedgerUsageValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		u    claudeLedgerUsage
		ok   bool
	}{
		{"input only", claudeLedgerUsage{Input: claudeLedgerIntPtr(3), PromptTotal: 3}, true},
		{"cache read without input", claudeLedgerUsage{CacheRead: claudeLedgerIntPtr(7), PromptTotal: 7}, true},
		{"zero components", claudeLedgerUsage{Input: claudeLedgerIntPtr(0), PromptTotal: 0}, false},
		{"no components", claudeLedgerUsage{PromptTotal: 0}, false},
		{"negative input, positive sum", claudeLedgerUsage{Input: claudeLedgerIntPtr(-1), CacheCreation: claudeLedgerIntPtr(4), CacheRead: claudeLedgerIntPtr(7), PromptTotal: 10}, false},
		{"negative cache creation, positive sum", claudeLedgerUsage{Input: claudeLedgerIntPtr(200), CacheCreation: claudeLedgerIntPtr(-100), CacheRead: claudeLedgerIntPtr(7), PromptTotal: 107}, false},
		{"negative cache read, positive sum", claudeLedgerUsage{Input: claudeLedgerIntPtr(200), CacheRead: claudeLedgerIntPtr(-7), PromptTotal: 193}, false},
		{"prompt differs from sum", claudeLedgerUsage{Input: claudeLedgerIntPtr(3), PromptTotal: 4}, false},
		{"sum equals MaxInt", claudeLedgerUsage{Input: claudeLedgerIntPtr(math.MaxInt), PromptTotal: math.MaxInt}, true},
		// The wrapped sum of three MaxInt values is MaxInt-2, so PromptTotal matches it.
		// Only the overflow check rejects this value.
		{"three MaxInt components overflow", claudeLedgerUsage{Input: claudeLedgerIntPtr(math.MaxInt), CacheCreation: claudeLedgerIntPtr(math.MaxInt), CacheRead: claudeLedgerIntPtr(math.MaxInt), PromptTotal: math.MaxInt - 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := claudeLedgerValidateUsage(tt.u); (err == nil) != tt.ok {
				t.Errorf("err = %v, want ok %t", err, tt.ok)
			}
		})
	}
}

func TestClaudeTokenLedgerResumeKeepsCount(t *testing.T) {
	t.Parallel()
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

// A resumed ledger keeps failed records, an orphaned reservation, and the cap. It
// refuses every further attempt, including a duplicate.
func TestClaudeTokenLedgerResumeAtCapKeepsEveryAttempt(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	dir, l := claudeLedgerCreateForTest(t)
	n := 0
	for _, model := range claudeLedgerModels {
		for _, slot := range claudeLedgerSlots {
			in := claudeLedgerTestInput(set, model, slot)
			if n == claudeLedgerMaxAttempts-1 {
				// The last reservation is never finished, as after a crash.
				if _, err := l.Reserve(in); err != nil {
					t.Fatalf("reserve orphan: %v", err)
				}
			} else {
				fail := n%3 == 0
				if _, err := l.RunAttempt(in, func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
					if fail {
						return nil, errors.New("fake launch failed")
					}
					return claudeLedgerFakeObservation(rec.RequestedModel), nil
				}); err != nil {
					t.Fatalf("attempt %d: %v", n+1, err)
				}
			}
			n++
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	resumed, err := claudeLedgerOpen(dir)
	if err != nil {
		t.Fatalf("resume at cap: %v", err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	if resumed.Total() != claudeLedgerMaxAttempts {
		t.Fatalf("resumed total = %d, want %d", resumed.Total(), claudeLedgerMaxAttempts)
	}
	if _, err := resumed.Reserve(claudeLedgerTestInput(set, claudeLedgerModels[0], claudeLedgerSlots[0])); !errors.Is(err, errClaudeLedgerCap) {
		t.Errorf("reserve at cap err = %v, want errClaudeLedgerCap", err)
	}
	counts := map[claudeLedgerStatus]int{}
	for _, rec := range claudeLedgerOnDisk(t, dir) {
		counts[rec.Status]++
	}
	want := map[claudeLedgerStatus]int{claudeLedgerStatusFailed: 6, claudeLedgerStatusSucceeded: 11, claudeLedgerStatusReserved: 1}
	for status, w := range want {
		if counts[status] != w {
			t.Errorf("on disk %q = %d, want %d", status, counts[status], w)
		}
	}
}

// A duplicate of a failed or orphaned pair is refused on resume below the cap, and
// the count is kept.
func TestClaudeTokenLedgerResumeRefusesDuplicatesBelowCap(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	const haiku = "claude-haiku-5-5"
	dir, l := claudeLedgerCreateForTest(t)
	failedIn := claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline)
	orphanIn := claudeLedgerTestInput(set, haiku, claudeLedgerSlotMarkdown)
	if _, err := l.RunAttempt(failedIn, func(claudeLedgerRecord) (*claudeLedgerObservation, error) {
		return nil, errors.New("fake launch failed")
	}); err != nil {
		t.Fatalf("failed attempt: %v", err)
	}
	if _, err := l.Reserve(orphanIn); err != nil {
		t.Fatalf("orphan reservation: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	resumed, err := claudeLedgerOpen(dir)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	launches := 0
	launch := func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
		launches++
		return claudeLedgerFakeObservation(rec.RequestedModel), nil
	}
	for _, in := range []claudeLedgerReserveInput{failedIn, orphanIn} {
		if _, err := resumed.RunAttempt(in, launch); !errors.Is(err, errClaudeLedgerDuplicate) {
			t.Errorf("duplicate of slot %q: err = %v, want errClaudeLedgerDuplicate", in.Slot, err)
		}
	}
	if launches != 0 || resumed.Total() != 2 {
		t.Errorf("launches = %d total = %d, want 0 and 2", launches, resumed.Total())
	}
}

// Each record is checked on resume. A malformed request, a bad outcome, or a
// failure text outside the code list makes the whole ledger corrupt.
func TestClaudeTokenLedgerResumeRejectsBadRecords(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	valid := claudeLedgerValidFile(t, set)
	const haiku, opus = "claude-haiku-5-5", "claude-opus-5-5"
	// failedWith turns the valid succeeded record into a failed one with code and observed.
	failedWith := func(code, observed string) func(*claudeLedgerRecord) {
		return func(r *claudeLedgerRecord) {
			r.Status, r.Failure, r.Usage, r.ObservedModel = claudeLedgerStatusFailed, code, nil, observed
		}
	}
	sonnetRequest := json.RawMessage(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"x"}]}`)
	tests := []struct {
		name string
		edit func(*claudeLedgerRecord)
	}{
		{"request null", func(r *claudeLedgerRecord) { r.Request = json.RawMessage("null") }},
		{"request empty object", func(r *claudeLedgerRecord) { r.Request = json.RawMessage("{}") }},
		{"request model differs", func(r *claudeLedgerRecord) { r.Request = sonnetRequest }},
		{"request without messages", func(r *claudeLedgerRecord) { r.Request = json.RawMessage(`{"model":"claude-haiku-5-5"}`) }},
		{"request unknown field", func(r *claudeLedgerRecord) {
			r.Request = json.RawMessage(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"x"}],"bogus":true}`)
		}},
		{"succeeded usage negative", func(r *claudeLedgerRecord) {
			r.Usage = &claudeLedgerUsage{Input: claudeLedgerIntPtr(-2), CacheRead: claudeLedgerIntPtr(5), PromptTotal: 3}
		}},
		{"succeeded prompt differs from sum", func(r *claudeLedgerRecord) { r.Usage.PromptTotal = 99 }},
		{"succeeded without usage", func(r *claudeLedgerRecord) { r.Usage = nil }},
		{"succeeded usage overflows", func(r *claudeLedgerRecord) {
			r.Usage = &claudeLedgerUsage{Input: claudeLedgerIntPtr(math.MaxInt), CacheCreation: claudeLedgerIntPtr(math.MaxInt), CacheRead: claudeLedgerIntPtr(math.MaxInt), PromptTotal: math.MaxInt - 2}
		}},
		{"failed with raw error text", func(r *claudeLedgerRecord) {
			r.Status, r.Failure, r.Usage, r.ObservedModel = claudeLedgerStatusFailed, "exec /home/luis/key failed", nil, ""
		}},
		{"launch failure names observed model", failedWith(claudeLedgerFailLaunch, haiku)},
		{"no-observation failure names observed model", failedWith(claudeLedgerFailNoObservation, haiku)},
		{"model failure without observed model", failedWith(claudeLedgerFailModel, "")},
		{"model failure observed equals requested", failedWith(claudeLedgerFailModel, haiku)},
		{"model failure observed outside candidates", failedWith(claudeLedgerFailModel, "claude-haiku-4-5")},
		{"usage failure observed differs", failedWith(claudeLedgerFailUsage, opus)},
		{"usage failure without observed model", failedWith(claudeLedgerFailUsage, "")},
		{"failed record carries usage", func(r *claudeLedgerRecord) {
			r.Status, r.Failure, r.ObservedModel = claudeLedgerStatusFailed, claudeLedgerFailLaunch, ""
			r.Usage = &claudeLedgerUsage{Input: claudeLedgerIntPtr(3), PromptTotal: 3}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := claudeLedgerDirWith(t, claudeLedgerEditRecord(t, valid, tt.edit))
			l, err := claudeLedgerOpen(dir)
			if !errors.Is(err, errClaudeLedgerCorrupt) || l != nil {
				t.Errorf("open = %v, err %v; want errClaudeLedgerCorrupt", l, err)
			}
		})
	}
}

// The size limit applies to the compact form that reserve writes. The same request
// written indented can exceed the limit on disk, so resume must measure it compactly
// and agree with reserve.
func TestClaudeTokenLedgerRequestLimitUsesCompactForm(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	const haiku = "claude-haiku-5-5"
	valid := claudeLedgerValidFile(t, set)
	tests := []struct {
		name     string
		messages int
		ok       bool
	}{
		// About 600 KB compact. Indented, the same request is over the limit.
		{"compact under limit, indented over", 20000, true},
		// About 1.2 MB compact.
		{"compact over limit", 40000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := ChatRequest{Model: haiku, Messages: make([]Message, tt.messages)}
			for i := range req.Messages {
				req.Messages[i] = Message{Role: MessageRoleUser, Content: "x"}
			}
			compact, err := json.Marshal(req)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			indented, err := json.MarshalIndent(req, "", "  ")
			if err != nil {
				t.Fatalf("indent request: %v", err)
			}
			if (len(compact) <= claudeLedgerMaxRequestBytes) != tt.ok {
				t.Fatalf("compact size %d does not match the case", len(compact))
			}
			if tt.ok && len(indented) <= claudeLedgerMaxRequestBytes {
				t.Fatalf("indented size %d does not exceed the limit, so the case proves nothing", len(indented))
			}
			in := claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline)
			in.Request = req
			_, l := claudeLedgerCreateForTest(t)
			if _, err := l.Reserve(in); (err == nil) != tt.ok || (!tt.ok && !errors.Is(err, errClaudeLedgerInput)) {
				t.Errorf("reserve err = %v, want ok %t", err, tt.ok)
			}
			data := claudeLedgerEditRecord(t, valid, func(r *claudeLedgerRecord) { r.Request = indented })
			resumed, err := claudeLedgerOpen(claudeLedgerDirWith(t, data))
			if (err == nil) != tt.ok || (!tt.ok && !errors.Is(err, errClaudeLedgerCorrupt)) {
				t.Fatalf("open indented err = %v, want ok %t", err, tt.ok)
			}
			if err == nil {
				t.Cleanup(func() { _ = resumed.Close() })
			}
		})
	}
}

func TestClaudeTokenLedgerOpenFailsClosed(t *testing.T) {
	t.Parallel()
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
		{"duplicate top-level key", 0o600, swap(`"schema_version": 1`, `"schema_version": 2, "schema_version": 1`)},
		{"attempt id gap", 0o600, swap(`"attempt_id": 1`, `"attempt_id": 2`)},
		{"duplicate attempt key", 0o600, swap(`"attempt_id": 1`, `"attempt_id": 9, "attempt_id": 1`)},
		{"duplicate request key", 0o600, swap(`"model": "claude-haiku-5-5"`, `"model": "claude-opus-5-5", "model": "claude-haiku-5-5"`)},
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
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
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
	if _, err := l.Reserve(claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline)); !errors.Is(err, errClaudeLedgerClosed) {
		t.Errorf("reserve after close err = %v, want errClaudeLedgerClosed", err)
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
	t.Parallel()
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
	t.Parallel()
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

// A failed directory fsync after a write poisons the session. The slot stays
// counted, no launch runs after the failure, and Close still works.
func TestClaudeTokenLedgerDirSyncFailurePoisons(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	const haiku = "claude-haiku-5-5"
	errSync := errors.New("fake directory fsync failure")
	tests := []struct {
		name       string
		failCall   int // 1-based directory sync that fails
		wantStatus claudeLedgerStatus
	}{
		{"reservation sync", 1, claudeLedgerStatusReserved},
		{"outcome sync", 2, claudeLedgerStatusSucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, l := claudeLedgerCreateForTest(t)
			calls := 0
			l.syncDir = func(string) error {
				calls++
				if calls == tt.failCall {
					return errSync
				}
				return nil
			}
			launches := 0
			launch := func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
				launches++
				return claudeLedgerFakeObservation(rec.RequestedModel), nil
			}
			_, err := l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotBaseline), launch)
			if !errors.Is(err, errClaudeLedgerDurability) || !errors.Is(err, errSync) {
				t.Fatalf("first attempt err = %v, want durability error wrapping the fsync failure", err)
			}
			wantLaunches := 1
			if tt.failCall == 1 {
				wantLaunches = 0
			}
			if launches != wantLaunches || l.Total() != 1 {
				t.Errorf("launches = %d total = %d, want %d and 1", launches, l.Total(), wantLaunches)
			}
			_, err = l.RunAttempt(claudeLedgerTestInput(set, haiku, claudeLedgerSlotMarkdown), launch)
			if !errors.Is(err, errClaudeLedgerDurability) || launches != wantLaunches {
				t.Errorf("attempt after poison: err = %v launches = %d, want durability refusal and %d launches", err, launches, wantLaunches)
			}
			if _, err := l.Reserve(claudeLedgerTestInput(set, haiku, claudeLedgerSlotMarkdown)); !errors.Is(err, errClaudeLedgerDurability) {
				t.Errorf("reserve after poison err = %v, want errClaudeLedgerDurability", err)
			}
			if l.Total() != 1 {
				t.Errorf("total after poison = %d, want 1", l.Total())
			}
			if got := claudeLedgerOnDisk(t, dir); len(got) != 1 || got[0].Status != tt.wantStatus {
				t.Errorf("on disk = %+v, want one %q attempt", got, tt.wantStatus)
			}
			if err := l.Close(); err != nil {
				t.Errorf("close after poison: %v", err)
			}
		})
	}
}

// Concurrent callers are serialized in process. Same-pair duplicates admit one
// attempt, and distinct pairs each count once. The goroutine counts are fixed by
// the protocol, so the test starts no unbounded work.
func TestClaudeTokenLedgerConcurrentAttemptsSerialize(t *testing.T) {
	t.Parallel()
	set := loadClaudeFixtureForTest(t)
	t.Run("same pair admits one attempt", func(t *testing.T) {
		_, l := claudeLedgerCreateForTest(t)
		in := claudeLedgerTestInput(set, "claude-haiku-5-5", claudeLedgerSlotBaseline)
		const workers = 8
		errs := make([]error, workers)
		launches := 0
		launch := func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
			launches++
			return claudeLedgerFakeObservation(rec.RequestedModel), nil
		}
		var wg sync.WaitGroup
		for i := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = l.RunAttempt(in, launch)
			}()
		}
		wg.Wait()
		ok, dup := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, errClaudeLedgerDuplicate):
				dup++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}
		if ok != 1 || dup != workers-1 || launches != 1 || l.Total() != 1 {
			t.Errorf("ok = %d dup = %d launches = %d total = %d, want 1, %d, 1, 1", ok, dup, launches, l.Total(), workers-1)
		}
	})
	t.Run("every pair counts once", func(t *testing.T) {
		_, l := claudeLedgerCreateForTest(t)
		errs := make([]error, claudeLedgerMaxAttempts)
		var wg sync.WaitGroup
		for i := range claudeLedgerMaxAttempts {
			model := claudeLedgerModels[i/len(claudeLedgerSlots)]
			slot := claudeLedgerSlots[i%len(claudeLedgerSlots)]
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = l.RunAttempt(claudeLedgerTestInput(set, model, slot), func(rec claudeLedgerRecord) (*claudeLedgerObservation, error) {
					return claudeLedgerFakeObservation(rec.RequestedModel), nil
				})
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("attempt %d: %v", i, err)
			}
		}
		if l.Total() != claudeLedgerMaxAttempts {
			t.Errorf("total = %d, want %d", l.Total(), claudeLedgerMaxAttempts)
		}
		if _, err := l.Reserve(claudeLedgerTestInput(set, claudeLedgerModels[0], claudeLedgerSlots[0])); !errors.Is(err, errClaudeLedgerCap) {
			t.Errorf("reserve at cap err = %v, want errClaudeLedgerCap", err)
		}
	})
}
