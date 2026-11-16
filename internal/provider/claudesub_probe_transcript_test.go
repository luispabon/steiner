//go:build claudesub_probe

package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// claudeSubProbeSessionIDPattern is the only session id shape the probe accepts
// in a filesystem path. The CLI generates uuid-like ids, so anything else is
// treated as hostile and never joined into a path.
var claudeSubProbeSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// claudeSubProbeSessionIDValid reports whether id is safe to use as a session
// file name.
func claudeSubProbeSessionIDValid(id string) bool {
	return claudeSubProbeSessionIDPattern.MatchString(id)
}

// claudeSubProbeConfigDir is the CLI's config root: CLAUDE_CONFIG_DIR when set,
// otherwise ~/.claude.
func claudeSubProbeConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// claudeSubProbeProjectDir is the CLI's per-cwd session directory. The CLI
// encodes the working directory by replacing every character that is not a
// letter or digit with a dash.
func claudeSubProbeProjectDir(configDir, cwd string) string {
	encoded := make([]rune, 0, len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			encoded = append(encoded, r)
		default:
			encoded = append(encoded, '-')
		}
	}
	return filepath.Join(configDir, "projects", string(encoded))
}

// claudeSubProbeTranscriptPath is the on-disk transcript for one CLI session. An
// id that is not a plain session id yields no path at all.
func claudeSubProbeTranscriptPath(configDir, cwd, sessionID string) string {
	if configDir == "" || !claudeSubProbeSessionIDValid(sessionID) {
		return ""
	}
	return filepath.Join(claudeSubProbeProjectDir(configDir, cwd), sessionID+".jsonl")
}

// claudeSubProbeTranscriptEntry is one recorded transcript line, reduced to its
// type and identifiers. Neither message text nor tool content is recorded.
type claudeSubProbeTranscriptEntry struct {
	Line    int    `json:"line"`
	Type    string `json:"type,omitempty"`
	Subtype string `json:"subtype,omitempty"`
	UUID    string `json:"uuid,omitempty"`
}

// claudeSubProbeTranscript is one snapshot of a CLI session transcript file.
type claudeSubProbeTranscript struct {
	Label   string                          `json:"label"`
	Path    string                          `json:"path"`
	Exists  bool                            `json:"exists"`
	Lines   int                             `json:"lines"`
	Entries []claudeSubProbeTranscriptEntry `json:"entries,omitempty"`
	ReadErr string                          `json:"read_error,omitempty"`
}

// claudeSubProbeReadTranscript records the shape of a session transcript: its
// line count and the type and uuid of every entry, never its content.
func claudeSubProbeReadTranscript(label, configDir, cwd, sessionID string) claudeSubProbeTranscript {
	snap := claudeSubProbeTranscript{Label: label, Path: claudeSubProbeTranscriptPath(configDir, cwd, sessionID)}
	if snap.Path == "" {
		return snap
	}
	data, err := os.ReadFile(snap.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			snap.ReadErr = claudeSubProbeErrorString(err)
		}
		return snap
	}
	snap.Exists = true
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		snap.Lines++
		var payload struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			UUID    string `json:"uuid"`
		}
		entry := claudeSubProbeTranscriptEntry{Line: i + 1}
		if json.Unmarshal([]byte(line), &payload) == nil {
			entry.Type = payload.Type
			entry.Subtype = payload.Subtype
			entry.UUID = payload.UUID
		}
		snap.Entries = append(snap.Entries, entry)
	}
	return snap
}

// claudeSubProbeCleanupSessions removes transcript files of sessions the probe
// itself recorded, when cleanup is enabled. It only removes a regular file whose
// directory is exactly the probe's own project directory, so a recorded id can
// never reach another session, another directory, or the CLI's configuration.
func claudeSubProbeCleanupSessions(cfg claudeSubProbeConfig, configDir string, report *claudeSubProbeReport) claudeSubProbeCleanupReport {
	out := claudeSubProbeCleanupReport{
		Enabled: cfg.Cleanup,
		Note:    "only transcript files of sessions this probe recorded, inside the probe's own project directory, are candidates; nothing else is read or removed",
	}
	if !cfg.Cleanup {
		return out
	}
	projectDir := claudeSubProbeProjectDir(configDir, cfg.WorkDir)
	seen := map[string]struct{}{}
	for _, exp := range report.Experiments {
		for _, process := range exp.Processes {
			id := process.SessionID
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			if !claudeSubProbeSessionIDValid(id) {
				out.Skipped = append(out.Skipped, fmt.Sprintf("%s: not a plain session id", id))
				continue
			}
			path := claudeSubProbeTranscriptPath(configDir, cfg.WorkDir, id)
			if path == "" || filepath.Dir(path) != projectDir {
				out.Skipped = append(out.Skipped, fmt.Sprintf("%s: outside the probe project directory", id))
				continue
			}
			switch err := os.Remove(path); {
			case err == nil:
				out.Removed = append(out.Removed, path)
			case errors.Is(err, os.ErrNotExist):
				out.Absent = append(out.Absent, path)
			default:
				out.Errors = append(out.Errors, claudeSubProbeErrorString(err))
			}
		}
	}
	return out
}
