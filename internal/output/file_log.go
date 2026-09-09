package output

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// maxFileLogBytes caps a session log generation's size before rotation. One
// prior session, written with the old text format, reached 25MB; this keeps
// a single generation bounded and cheap to tail.
const maxFileLogBytes = 20 * 1024 * 1024 // 20MB

// maxFileLogGenerations bounds how many rotated generations
// (<path>.1 .. <path>.N) are kept alongside the active file.
const maxFileLogGenerations = 3

// LogStartedPayload is the payload of the first record written to a session
// log by each process, so concurrent processes appending to the same path
// remain separable and a reader can recover build identity per run.
type LogStartedPayload struct {
	RunID     string    `json:"run_id"`
	BuildSHA  string    `json:"build_sha,omitempty"`
	Dirty     bool      `json:"dirty"`
	Version   string    `json:"version,omitempty"`
	StartTime time.Time `json:"start_time"`
}

// EventTypeLogStarted marks the first record of each process's run in a
// session log.
const EventTypeLogStarted = "log_started"

// FileLogOptions configures a FileLogSink.
type FileLogOptions struct {
	// ThinkingChunk, when false, drops ThinkingChunkEvent records.
	ThinkingChunk bool
	// AssistantChunk, when false, drops AssistantChunkEvent records; they
	// duplicate the AssistantMessageEvent that follows them.
	AssistantChunk bool
	// BuildSHA and Dirty identify the running binary; recorded on the
	// log_started line. Sourced from -X main.commit/-X main.dirty via
	// runtime_build.go.
	BuildSHA string
	Dirty    bool
	Version  string
	// CaptureAPIRequestBodies, when true, writes full Messages/Tools/Blocks
	// content for APIRequestEvent instead of the bounded scalar fields. Set
	// from diagnostics.capture_bodies by the composition root; internal/output
	// must not import internal/config.
	CaptureAPIRequestBodies bool
}

// FileLogSink writes events as one JSON object per line (JSONL) to an
// append-only log file, rotating when the active file exceeds
// maxFileLogBytes.
type FileLogSink struct {
	mu             sync.Mutex
	file           *os.File
	path           string
	written        int64
	thinkingChunk  bool
	assistantChunk bool
	captureBodies  bool
	runID          string
	buildSHA       string
	dirty          bool
	version        string
}

// NewFileLogSink creates a new file-based event sink at the given path,
// appending to any existing content. Writes a log_started record as the
// first line of the run.
func NewFileLogSink(path string, opts FileLogOptions) (*FileLogSink, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("log file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log file directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}
	var written int64
	if info, statErr := file.Stat(); statErr == nil {
		written = info.Size()
	}
	sink := &FileLogSink{
		file:           file,
		path:           path,
		written:        written,
		thinkingChunk:  opts.ThinkingChunk,
		assistantChunk: opts.AssistantChunk,
		captureBodies:  opts.CaptureAPIRequestBodies,
		runID:          newRunID(),
		buildSHA:       opts.BuildSHA,
		dirty:          opts.Dirty,
		version:        opts.Version,
	}
	if err := sink.writeRunIdentity(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return sink, nil
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// writeRunIdentity writes the log_started record for this sink's run. The
// caller must hold mu, except during construction where no other goroutine
// can yet observe the sink.
func (s *FileLogSink) writeRunIdentity() error {
	now := time.Now().UTC()
	event := Event{
		Type:      EventTypeLogStarted,
		Timestamp: now,
		Payload: LogStartedPayload{
			RunID:     s.runID,
			BuildSHA:  s.buildSHA,
			Dirty:     s.dirty,
			Version:   s.version,
			StartTime: now,
		},
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal log_started record: %w", err)
	}
	data = append(data, '\n')
	n, err := s.file.Write(data)
	if err != nil {
		return fmt.Errorf("write log_started record: %w", err)
	}
	s.written += int64(n)
	return nil
}

// Emit appends event to the log file as a single JSON line, unless it is
// filtered out.
func (s *FileLogSink) Emit(event Event) {
	if s == nil {
		return
	}
	switch event.Type {
	case EventTypeThinkingChunk:
		if !s.thinkingChunk {
			return
		}
	case EventTypeAssistantChunk:
		if !s.assistantChunk {
			return
		}
	}

	if s.captureBodies {
		if payload, ok := event.Payload.(APIRequestEvent); ok {
			event.Payload = apiRequestFull(payload)
		}
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return
	}

	if s.written+int64(len(data)) > maxFileLogBytes {
		if err := s.rotate(); err != nil {
			return
		}
	}

	n, err := s.file.Write(data)
	if err == nil {
		s.written += int64(n)
	}
}

// rotate closes the active file, shifts existing generations
// (<path>.1 -> <path>.2, ..., dropping anything beyond
// maxFileLogGenerations), renames the active file to <path>.1, and opens a
// fresh file at path with a new log_started record. Caller must hold mu.
//
// Concurrent processes appending to the same path each track written from
// their own Stat() at open, so a rotation by one process leaves any other
// process writing into the renamed generation; this is accepted for
// simplicity per the stage 0 plan.
func (s *FileLogSink) rotate() error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close log file for rotation: %w", err)
	}

	for i := maxFileLogGenerations; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", s.path, i)
		if i == maxFileLogGenerations {
			_ = os.Remove(src)
			continue
		}
		dst := fmt.Sprintf("%s.%d", s.path, i+1)
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, dst)
		}
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate log file: %w", err)
	}

	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open rotated log file: %w", err)
	}
	s.file = file
	s.written = 0
	return s.writeRunIdentity()
}

// Close releases the underlying log file handle.
func (s *FileLogSink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.file.Close()
	s.file = nil
	return err
}

// MultiSink fans each event out to multiple sinks.
type MultiSink struct {
	sinks []EventSink
}

// NewMultiSink returns a sink that emits to every non-nil sink in sinks.
func NewMultiSink(sinks ...EventSink) EventSink {
	filtered := make([]EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink != nil {
			filtered = append(filtered, sink)
		}
	}
	if len(filtered) == 0 {
		return NoopSink{}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return MultiSink{sinks: filtered}
}

// Emit forwards event to each configured child sink.
func (s MultiSink) Emit(event Event) {
	for _, sink := range s.sinks {
		sink.Emit(event)
	}
}
