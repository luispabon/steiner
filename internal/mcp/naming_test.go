package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// hashSuffix mirrors the production hash so tests can assert the suffix is
// computed over the ORIGINAL unsanitised inputs.
func hashSuffix(server, tool string) string {
	sum := sha256.Sum256([]byte(server + "\x00" + tool))
	return hex.EncodeToString(sum[:])[:hashLen]
}

func TestToolName(t *testing.T) {
	tests := []struct {
		name   string
		server string
		tool   string
		want   string
	}{
		{
			name:   "clean names pass through unchanged and unhashed",
			server: "server",
			tool:   "tool",
			want:   "mcp__server__tool",
		},
		{
			name:   "tool with dots is sanitised and hashed over originals",
			server: "server",
			tool:   "tool.name",
			want:   "mcp__server__tool_name__" + hashSuffix("server", "tool.name"),
		},
		{
			name:   "server with special chars is sanitised and hashed",
			server: "my.server!one",
			tool:   "tool",
			want:   "mcp__my_server_one__tool__" + hashSuffix("my.server!one", "tool"),
		},
		{
			name:   "both server and tool need sanitisation",
			server: "a.b",
			tool:   "c d",
			want:   "mcp__a_b__c_d__" + hashSuffix("a.b", "c d"),
		},
		{
			name:   "unicode collapses to a single underscore per rune",
			server: "srv",
			tool:   "café",
			want:   "mcp__srv__caf___" + hashSuffix("srv", "café"),
		},
		{
			name:   "multi-byte runes collapse one-for-one, not byte-wise",
			server: "srv",
			tool:   "日本語",
			want:   "mcp__srv_______" + hashSuffix("srv", "日本語"),
		},
		{
			name:   "empty server still produces a valid name",
			server: "",
			tool:   "tool",
			want:   "mcp____tool",
		},
		{
			name:   "empty tool still produces a valid name",
			server: "server",
			tool:   "",
			want:   "mcp__server__",
		},
		{
			name:   "both empty still produces a valid name",
			server: "",
			tool:   "",
			want:   "mcp____",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolName(tt.server, tt.tool)
			if got != tt.want {
				t.Errorf("ToolName(%q, %q) = %q, want %q", tt.server, tt.tool, got, tt.want)
			}
			if !namePattern.MatchString(got) {
				t.Errorf("ToolName(%q, %q) = %q does not match %s", tt.server, tt.tool, got, namePattern)
			}
			// Determinism: repeated calls return the same value.
			if again := ToolName(tt.server, tt.tool); again != got {
				t.Errorf("ToolName(%q, %q) not deterministic: %q then %q", tt.server, tt.tool, got, again)
			}
		})
	}
}

func TestToolNameExactlyMaxLenCleanNoHash(t *testing.T) {
	const toolLen = maxNameLen - len("mcp__a__") // 57
	tool := strings.Repeat("b", toolLen)
	want := "mcp__a__" + tool
	got := ToolName("a", tool)
	if got != want {
		t.Errorf("ToolName(\"a\", %d b's) = %q, want %q", toolLen, got, want)
	}
	if len(got) != maxNameLen {
		t.Errorf("expected exactly %d chars, got %d", maxNameLen, len(got))
	}

	// One char over the limit: clean pair must be truncated and hashed.
	tool = strings.Repeat("b", toolLen+1)
	got = ToolName("a", tool)
	if len(got) > maxNameLen {
		t.Errorf("ToolName result %q is %d chars, want <= %d", got, len(got), maxNameLen)
	}
	if !strings.HasSuffix(got, hashSuffix("a", tool)) {
		t.Errorf("ToolName(\"a\", %d b's) = %q, want hash suffix %q", toolLen+1, got, hashSuffix("a", tool))
	}
	if !namePattern.MatchString(got) {
		t.Errorf("ToolName result %q does not match %s", got, namePattern)
	}
}

func TestToolNameOverLengthCleanPairTruncatedAndHashed(t *testing.T) {
	server := strings.Repeat("a", 40)
	tool := strings.Repeat("b", 40)
	want := "mcp__" + strings.Repeat("a", 23) + "__" + strings.Repeat("b", 24) + "__" + hashSuffix(server, tool)
	got := ToolName(server, tool)
	if got != want {
		t.Errorf("ToolName = %q, want %q", got, want)
	}
	if len(got) != maxNameLen {
		t.Errorf("expected exactly %d chars, got %d", maxNameLen, len(got))
	}
}

func TestToolNameSanitiseCollisionDiffers(t *testing.T) {
	// Two tools that sanitise to the same string must still get different
	// names, because the hash keys off the ORIGINAL inputs.
	a := ToolName("s", "a.b")
	b := ToolName("s", "a!b")
	if a == b {
		t.Errorf("ToolName(\"s\", \"a.b\") and ToolName(\"s\", \"a!b\") both = %q, want different names", a)
	}
	// Both sanitise to the same base; they must differ only via the hash.
	const base = "mcp__s__a_b__"
	for name, got := range map[string]string{"a.b": a, "a!b": b} {
		if !strings.HasPrefix(got, base) {
			t.Errorf("ToolName(\"s\", %q) = %q, want prefix %q", name, got, base)
		}
		if len(got) != len(base)+hashLen {
			t.Errorf("ToolName(\"s\", %q) = %q, want length %d", name, got, len(base)+hashLen)
		}
	}
	// Spec's example pair also differs: "a-b" is clean so it is unhashed.
	if ToolName("s", "a.b") == ToolName("s", "a-b") {
		t.Errorf("ToolName(\"s\", \"a.b\") and ToolName(\"s\", \"a-b\") both = %q, want different names", ToolName("s", "a.b"))
	}
}

func TestToolNameOutputPattern(t *testing.T) {
	inputs := [][2]string{
		{"server", "tool"},
		{"my.server!one", "tool.name"},
		{"", ""},
		{"", "tool"},
		{"日本語", "café"},
		{strings.Repeat("x", 100), strings.Repeat("y", 100)},
		{"with space", "with space"},
		{"under_score", "dash-name"},
	}
	for _, in := range inputs {
		got := ToolName(in[0], in[1])
		if !namePattern.MatchString(got) {
			t.Errorf("ToolName(%q, %q) = %q does not match %s", in[0], in[1], got, namePattern)
		}
	}
}

func TestToolNameNoPackageState(t *testing.T) {
	// The function must depend on (server, tool) only: repeated interleaved
	// calls must agree with the first result regardless of order or count.
	inputs := [][2]string{
		{"server", "tool"},
		{"a.b", "c.d"},
		{strings.Repeat("a", 40), strings.Repeat("b", 40)},
		{"", "x"},
	}
	first := make(map[[2]string]string, len(inputs))
	for _, in := range inputs {
		first[in] = ToolName(in[0], in[1])
	}
	for i := 0; i < 100; i++ {
		for _, in := range inputs {
			if got := ToolName(in[0], in[1]); got != first[in] {
				t.Fatalf("ToolName(%q, %q) changed across calls: %q then %q", in[0], in[1], first[in], got)
			}
		}
	}
}
