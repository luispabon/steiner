package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// recallNamePattern is the only shape RecallName may produce: a single path
// element of ASCII letters, digits, '_' and '-'.
var recallNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// recallNameMaxLength is the longest name RecallName can produce.
const recallNameMaxLength = recallNameKindMax + 1 + recallNameCallIDMax + 1 + sha256.Size*2

// independentRecallDigest re-derives the digest from the documented encoding
// (decimal byte length, colon, exact bytes for each field) without calling
// recallEntryDigest, so the encoding itself is pinned.
func independentRecallDigest(entry RecallEntry) string {
	var buf bytes.Buffer
	for _, field := range []string{entry.Kind, entry.Tool, entry.CallID, entry.Body} {
		fmt.Fprintf(&buf, "%d:%s", len(field), field)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func TestRecallNameMatchesDigestEncoding(t *testing.T) {
	tests := []struct {
		name  string
		entry RecallEntry
		want  string
	}{
		{
			name:  "tool result",
			entry: RecallEntry{Kind: "tool", Tool: "bash", CallID: "call_1", Body: "ok\n"},
			want:  "tool-call_1-eb96b74d0e0fc419bd59385525d8bd82daa6e45f68c057c395e9a3a25e458868",
		},
		{
			name:  "agent envelope",
			entry: RecallEntry{Kind: "agent", Tool: "code", CallID: "call_1", Body: "done"},
			want:  "agent-call_1-99dce8d77a30dbf485c3db5758ed38354ad052a49b7a22560e7b49703ee65422",
		},
		{
			name:  "zero entry uses fallbacks",
			entry: RecallEntry{},
			want:  "result-call-8538098a725acce2a400276987f134753b5125003e70c7d36420d4e1e50a036c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecallName(tt.entry)
			if got != tt.want {
				t.Errorf("RecallName(%+v) = %q, want %q", tt.entry, got, tt.want)
			}
			if want := independentRecallDigest(tt.entry); !strings.HasSuffix(got, "-"+want) {
				t.Errorf("RecallName(%+v) = %q, want digest suffix %q", tt.entry, got, want)
			}
			if again := RecallName(tt.entry); again != got {
				t.Errorf("RecallName(%+v) is not stable: %q then %q", tt.entry, got, again)
			}
		})
	}
}

func TestRecallNameDistinguishesEntries(t *testing.T) {
	base := RecallEntry{Kind: "tool", Tool: "bash", CallID: "call_1", Body: "ok"}
	tests := []struct {
		name  string
		other RecallEntry
	}{
		{
			name:  "kind distinguishes tool from agent with the same call id",
			other: RecallEntry{Kind: "agent", Tool: "bash", CallID: "call_1", Body: "ok"},
		},
		{
			name:  "tool",
			other: RecallEntry{Kind: "tool", Tool: "read", CallID: "call_1", Body: "ok"},
		},
		{
			name:  "call id",
			other: RecallEntry{Kind: "tool", Tool: "bash", CallID: "call_2", Body: "ok"},
		},
		{
			name:  "body after a repeated call id",
			other: RecallEntry{Kind: "tool", Tool: "bash", CallID: "call_1", Body: "ok "},
		},
		{
			name:  "field boundary that a plain concatenation would collide",
			other: RecallEntry{Kind: "tool", Tool: "bas", CallID: "hcall_1", Body: "ok"},
		},
		{
			name:  "tool and call id boundary that a plain concatenation would collide",
			other: RecallEntry{Kind: "tool", Tool: "bashcall", CallID: "_1", Body: "ok"},
		},
	}
	baseName := RecallName(base)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecallName(tt.other); got == baseName {
				t.Errorf("RecallName(%+v) = RecallName(%+v) = %q, want distinct names", tt.other, base, got)
			}
		})
	}
}

func TestRecallNameKindPrefix(t *testing.T) {
	toolEntry := RecallEntry{Kind: "tool", Tool: "bash", CallID: "call_1", Body: "ok"}
	agentEntry := RecallEntry{Kind: "agent", Tool: "bash", CallID: "call_1", Body: "ok"}
	if got := RecallName(toolEntry); !strings.HasPrefix(got, "tool-") {
		t.Errorf("RecallName(%+v) = %q, want tool- prefix", toolEntry, got)
	}
	agentName := RecallName(agentEntry)
	if !strings.HasPrefix(agentName, "agent-") {
		t.Errorf("RecallName(%+v) = %q, want agent- prefix", agentEntry, agentName)
	}
	if agentName == RecallName(toolEntry) {
		t.Errorf("tool and agent kinds produced the same name %q", agentName)
	}
}

func TestRecallNameSanitizesCallID(t *testing.T) {
	tests := []struct {
		name      string
		callID    string
		wantParts string
	}{
		{name: "safe id", callID: "call_1", wantParts: "call_1"},
		{name: "slashes and dots", callID: "../a/b.c", wantParts: "___a_b_c"},
		{name: "path traversal", callID: "..", wantParts: "__"},
		{name: "unicode", callID: "caf\u00e9-\u5de5\u5177", wantParts: "caf_-__"},
		{name: "control and spaces", callID: "a b\tc", wantParts: "a_b_c"},
		{name: "empty", callID: "", wantParts: "call"},
		{name: "long", callID: strings.Repeat("x", 80), wantParts: strings.Repeat("x", recallNameCallIDMax)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := RecallEntry{Kind: "tool", Tool: "bash", CallID: tt.callID, Body: "ok"}
			got := RecallName(entry)
			wantPrefix := "tool-" + tt.wantParts + "-"
			if !strings.HasPrefix(got, wantPrefix) {
				t.Errorf("RecallName(call id %q) = %q, want prefix %q", tt.callID, got, wantPrefix)
			}
			if want := independentRecallDigest(entry); !strings.HasSuffix(got, "-"+want) {
				t.Errorf("RecallName(call id %q) = %q, want digest suffix %q", tt.callID, got, want)
			}
		})
	}
}

func TestRecallNameTruncatedCallIDsStayDistinct(t *testing.T) {
	prefix := strings.Repeat("x", recallNameCallIDMax)
	a := RecallEntry{Kind: "tool", Tool: "bash", CallID: prefix + "a", Body: "ok"}
	b := RecallEntry{Kind: "tool", Tool: "bash", CallID: prefix + "b", Body: "ok"}
	if RecallName(a) == RecallName(b) {
		t.Errorf("call ids differing only after truncation share a name: %q", RecallName(a))
	}
}

func TestRecallNameIsSafePathElement(t *testing.T) {
	callIDs := []string{
		"",
		"..",
		"../../etc/passwd",
		`..\..\windows`,
		"a/b",
		"\u00fc\u00f1\u00ef",
		strings.Repeat("z", 300),
		".hidden",
		"bad\x00byte",
		"line\nbreak",
	}
	for _, kind := range []string{"tool", "agent", "/" + "kind", "\u5de5\u5177"} {
		for _, callID := range callIDs {
			name := RecallName(RecallEntry{Kind: kind, Tool: "bash", CallID: callID, Body: "ok"})
			if !recallNamePattern.MatchString(name) {
				t.Errorf("RecallName(kind %q, call id %q) = %q, want only [A-Za-z0-9_-]", kind, callID, name)
			}
			if filepath.Base(name) != name {
				t.Errorf("RecallName(kind %q, call id %q) = %q, want a single path element", kind, callID, name)
			}
			if strings.Contains(name, "..") {
				t.Errorf("RecallName(kind %q, call id %q) = %q, want no dot segments", kind, callID, name)
			}
			if len(name) > recallNameMaxLength {
				t.Errorf("RecallName(kind %q, call id %q) is %d bytes, want at most %d", kind, callID, len(name), recallNameMaxLength)
			}
		}
	}
}
