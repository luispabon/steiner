package tui

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	gitInitialOID  = "(initial)"
	gitDetachedRef = "(detached)"
)

// gitCommand builds a git invocation that never takes optional locks, so
// background refreshes cannot collide with the agent's own index-writing commands.
func gitCommand(ctx context.Context, repoRoot string, args ...string) *exec.Cmd {
	full := append([]string{"--no-optional-locks", "-C", repoRoot}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func execGit(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, repoRoot, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

type gitStatus struct {
	oid   string
	head  string
	ahead int
	files []gitModifiedFile
}

func (s gitStatus) branch() string {
	if s.head != gitDetachedRef {
		return s.head
	}
	if len(s.oid) < 7 || s.oid == gitInitialOID {
		return "detached"
	}
	return "detached@" + s.oid[:7]
}

func readGitStatus(ctx context.Context, repoRoot string) (gitStatus, error) {
	out, err := execGit(ctx, repoRoot, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=normal")
	if err != nil {
		return gitStatus{}, err
	}
	return parseGitStatusV2(out), nil
}

func parseGitStatusV2(data []byte) gitStatus {
	var st gitStatus
	seen := make(map[string]bool)
	records := strings.Split(string(data), "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}
		if header, ok := strings.CutPrefix(rec, "# "); ok {
			parseGitBranchHeader(&st, header)
			continue
		}

		var xy, path string
		switch rec[0] {
		case '1':
			xy, path = splitGitEntry(rec, 9)
		case '2':
			xy, path = splitGitEntry(rec, 10)
			i++ // the original path follows as its own record
		case 'u':
			xy, path = splitGitEntry(rec, 11)
		case '?':
			if len(rec) > 2 {
				xy, path = "??", rec[2:]
			}
		}
		if path == "" || seen[path] {
			continue
		}
		path = filepath.Clean(path)
		seen[path] = true
		kind := rec[0]
		st.files = append(st.files, gitModifiedFile{Status: gitStatusGlyph(kind, xy), Path: path})
	}
	return st
}

func splitGitEntry(rec string, fields int) (xy, path string) {
	parts := strings.SplitN(rec, " ", fields)
	if len(parts) != fields {
		return "", ""
	}
	return parts[1], parts[fields-1]
}

func parseGitBranchHeader(st *gitStatus, header string) {
	key, value, _ := strings.Cut(header, " ")
	switch key {
	case "branch.oid":
		st.oid = value
	case "branch.head":
		st.head = value
	case "branch.ab":
		if plus, _, ok := strings.Cut(value, " "); ok {
			if n, err := strconv.Atoi(strings.TrimPrefix(plus, "+")); err == nil && n > 0 {
				st.ahead = n
			}
		}
	}
}

func gitStatusGlyph(kind byte, xy string) string {
	switch {
	case kind == '?', kind == 'u', strings.Contains(xy, "U"):
		return "U"
	case strings.Contains(xy, "D"):
		return "D"
	case strings.ContainsAny(xy, "ARC"):
		return "A"
	default:
		return "M"
	}
}

type gitCounts struct{ added, deleted int }

func readGitNumstat(ctx context.Context, repoRoot string) (map[string]gitCounts, error) {
	out, err := execGit(ctx, repoRoot, "diff", "--numstat", "-z", "--no-ext-diff", "HEAD")
	if err != nil {
		return nil, err
	}
	return parseGitNumstatZ(out), nil
}

func parseGitNumstatZ(data []byte) map[string]gitCounts {
	counts := make(map[string]gitCounts)
	records := strings.Split(string(data), "\x00")
	for i := 0; i < len(records); i++ {
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) < 3 {
			continue
		}
		path := fields[2]
		if path == "" {
			if i+2 >= len(records) {
				break
			}
			path = records[i+2] // rename/copy: source then destination
			i += 2
		}
		if path == "" {
			continue
		}
		counts[filepath.Clean(path)] = gitCounts{
			added:   parseGitNumstatCount(fields[0]),
			deleted: parseGitNumstatCount(fields[1]),
		}
	}
	return counts
}

func parseGitNumstatCount(value string) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func applyGitCounts(files []gitModifiedFile, counts map[string]gitCounts) {
	for i := range files {
		c := counts[files[i].Path]
		files[i].Added, files[i].Deleted = c.added, c.deleted
	}
}

func statusPriority(s string) int {
	switch s {
	case "M":
		return 0
	case "A":
		return 1
	case "D":
		return 2
	case "U":
		return 3
	default:
		return 4
	}
}

func sortGitModifiedFiles(files []gitModifiedFile) {
	slices.SortStableFunc(files, func(a, b gitModifiedFile) int {
		if c := cmp.Compare(statusPriority(a.Status), statusPriority(b.Status)); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
}
