package advisor

import (
	"encoding/json"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// fullReadKey identifies a full-file read without relying on file_hash, which
// normalizes trailing whitespace and cannot prove byte equality.
type fullReadKey struct {
	path   string
	output string
}

// strictFullRead reports whether a read result is a complete, untruncated,
// byte-faithful copy of the whole file: no image, no pagination, at least one
// line, the whole line range, and no per-line truncation marker.
func strictFullRead(result builtin.ReadResult) (fullReadKey, bool) {
	if result.Image != nil || result.NextOffset != 0 || result.TotalLines == 0 {
		return fullReadKey{}, false
	}
	if result.StartLine != 1 || result.EndLine != result.TotalLines {
		return fullReadKey{}, false
	}
	if strings.Contains(result.Output, builtin.LineTruncationMarker) {
		return fullReadKey{}, false
	}
	return fullReadKey{path: result.Path, output: result.Output}, true
}

// fullReadIdentity resolves a tool result as an exact whole-file read. It also
// requires non-empty Output so the planner never treats two empty reads as
// interchangeable evidence.
func fullReadIdentity(message provider.Message, name string) (fullReadKey, bool) {
	if name != "read" {
		return fullReadKey{}, false
	}
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
		return fullReadKey{}, false
	}
	key, ok := strictFullRead(result)
	if !ok || key.output == "" {
		return fullReadKey{}, false
	}
	return key, true
}

// dedupeAttachments drops requested file content only when an untouched full
// read of that file remains in the reduced transcript and its Output matches
// the file bytes exactly. It deliberately avoids file_hash, which normalizes
// trailing whitespace. Any uncertainty leaves the attachment in place.
// The transcript is decoded in a single pass, so each read result can satisfy
// every matching duplicate path, not only the most recent one.
func dedupeAttachments(files []advisorFile, transcript []provider.Message, stages []reductionStage, workDir string) []advisorFile {
	if len(files) == 0 {
		return nil
	}
	cloned := append([]advisorFile(nil), files...)
	byPath, pending, shortest := attachmentCandidates(cloned)
	if pending == 0 {
		return cloned
	}
	for i := len(transcript) - 1; i >= 0 && pending > 0; i-- {
		key, ok := untouchedFullRead(transcript, stages, i, shortest)
		if !ok {
			continue
		}
		pending -= dedupeMatchingFiles(cloned, byPath[relDisplayPath(workDir, key.path)], key.output)
	}
	return cloned
}

// attachmentCandidates groups eligible file indices by display path and
// returns how many are still pending along with the shortest content length.
// Only files read in full are eligible: a capped read is a prefix of the file,
// so it cannot be proven byte-identical.
func attachmentCandidates(files []advisorFile) (map[string][]int, int, int) {
	byPath := make(map[string][]int)
	pending := 0
	shortest := -1
	for i := range files {
		file := files[i]
		if file.TotalBytes > maxAdvisorFileBytes || len(file.Content) != file.TotalBytes {
			continue
		}
		byPath[file.DisplayPath] = append(byPath[file.DisplayPath], i)
		pending++
		if shortest < 0 || len(file.Content) < shortest {
			shortest = len(file.Content)
		}
	}
	return byPath, pending, shortest
}

// untouchedFullRead decodes one transcript message as an untouched full read.
// Output is embedded verbatim in the JSON result, so a message shorter than
// the shortest pending attachment cannot match any of them.
func untouchedFullRead(transcript []provider.Message, stages []reductionStage, index, shortest int) (fullReadKey, bool) {
	if index < len(stages) && stages[index] != reductionStageNone {
		return fullReadKey{}, false
	}
	message := transcript[index]
	if message.Role != provider.MessageRoleTool || message.Name != "read" {
		return fullReadKey{}, false
	}
	if len(message.Content) < shortest {
		return fullReadKey{}, false
	}
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
		return fullReadKey{}, false
	}
	return strictFullRead(result)
}

// dedupeMatchingFiles clears every pending file whose bytes equal output and
// returns how many it deduped.
func dedupeMatchingFiles(files []advisorFile, indices []int, output string) int {
	deduped := 0
	for _, index := range indices {
		file := &files[index]
		if file.Deduped || file.Content != output {
			continue
		}
		file.Content = ""
		file.Deduped = true
		deduped++
	}
	return deduped
}
