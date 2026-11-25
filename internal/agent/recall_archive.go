package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// RecallEntry is one result body archived so the model can read it back after
// compaction removes it from the active context.
type RecallEntry struct {
	// Kind is the entry category: "tool" for a tool result, "agent" for a
	// sub-agent result envelope body.
	Kind string
	// Tool names the tool that produced a tool result, or the sub-agent type
	// for an agent entry.
	Tool string
	// CallID is the provider tool-call id. Providers may repeat ids, so names
	// also cover the body.
	CallID string
	// Body is the exact result text, archived unmodified.
	Body string
}

// RecallArchive stores result bodies compaction drops from the active context.
// RunRequest.RecallArchive carries it; nil disables recall.
type RecallArchive interface {
	// Write stores entry and returns the absolute path of its file.
	// The store is idempotent by name: an existing file is accepted only when
	// it is a regular file, not a symlink, and byte-identical to the complete
	// expected file. Any other existing file fails the write.
	Write(ctx context.Context, entry RecallEntry) (string, error)
	// Path returns the absolute path Write would use for name, without writing.
	Path(name string) string
}

// Bounds and fallbacks for the readable part of a recall name. The digest that
// follows them is always the full 64 hex characters.
const (
	recallNameKindMax     = 16
	recallNameCallIDMax   = 48
	recallNameKindDefault = "result"
	recallNameCallDefault = "call"
)

// RecallName returns the deterministic file name for entry: the sanitized kind
// as prefix, the sanitized call id, and the full SHA-256 digest of the entry's
// length-prefixed kind, tool, original call id and body. The digest keeps names
// distinct when sanitizing or truncating loses information and when providers
// repeat call ids for different results, so identical entries always share a
// name. The result is one path element safe for any input.
func RecallName(entry RecallEntry) string {
	digest := recallEntryDigest(entry)
	return recallNameSegment(entry.Kind, recallNameKindMax, recallNameKindDefault) + "-" +
		recallNameSegment(entry.CallID, recallNameCallIDMax, recallNameCallDefault) + "-" +
		hex.EncodeToString(digest[:])
}

// recallEntryDigest hashes the entry fields with a decimal byte length and a
// colon before each one, so no two field splits share an input.
func recallEntryDigest(entry RecallEntry) [sha256.Size]byte {
	h := sha256.New()
	var prefixBuf [20]byte
	for _, field := range [...]string{entry.Kind, entry.Tool, entry.CallID, entry.Body} {
		prefix := strconv.AppendInt(prefixBuf[:0], int64(len(field)), 10)
		// hash.Hash.Write never returns an error.
		_, _ = h.Write(append(prefix, ':'))
		_, _ = h.Write([]byte(field))
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}

// recallNameSegment keeps ASCII letters, digits, '_' and '-', replaces every
// other rune with '_', truncates to limit characters, and uses fallback when
// nothing remains.
func recallNameSegment(s string, limit int, fallback string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= limit {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return fallback
	}
	return b.String()
}
