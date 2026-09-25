package prompt

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
)

func TestRenderSkillActivationRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		name  string
		body  string
	}{
		{label: "plain", name: "plan", body: "Follow the steps."},
		{label: "quotes and backslashes", name: `odd "name\here`, body: "body"},
		{label: "unicode name", name: "\u6280\u80fd", body: "body"},
		{label: "empty name", name: "", body: "body"},
		{label: "newline name", name: "multi\nline", body: "body"},
		{label: "body contains close tag", name: "plan", body: "literal </steiner-skill> inside"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			text, truncated := RenderSkillActivation(tc.name, tc.body)
			if truncated {
				t.Fatalf("truncated = true, want false for small body")
			}

			modePrefix, blocks, rest := SplitSkillBlocks(text)
			if modePrefix != "" {
				t.Fatalf("modePrefix = %q, want empty", modePrefix)
			}
			if len(blocks) != 1 {
				t.Fatalf("len(blocks) = %d, want 1", len(blocks))
			}
			if got := blocks[0].Name; got != tc.name {
				t.Fatalf("name = %q, want %q", got, tc.name)
			}
			if got := blocks[0].State; got != SkillBlockActive {
				t.Fatalf("state = %q, want %q", got, SkillBlockActive)
			}
			if got := blocks[0].Text; got != text {
				t.Fatalf("block text = %q, want full envelope %q", got, text)
			}
			if rest != "" {
				t.Fatalf("rest = %q, want empty", rest)
			}
		})
	}
}

func TestRenderSkillDeactivationRoundTrip(t *testing.T) {
	t.Parallel()

	name := `odd "name\here`
	text := RenderSkillDeactivation(name)

	modePrefix, blocks, rest := SplitSkillBlocks(text)
	if modePrefix != "" {
		t.Fatalf("modePrefix = %q, want empty", modePrefix)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if got := blocks[0].Name; got != name {
		t.Fatalf("name = %q, want %q", got, name)
	}
	if got := blocks[0].State; got != SkillBlockInactive {
		t.Fatalf("state = %q, want %q", got, SkillBlockInactive)
	}
	if got := blocks[0].Text; got != text {
		t.Fatalf("block text = %q, want full envelope %q", got, text)
	}
	if rest != "" {
		t.Fatalf("rest = %q, want empty", rest)
	}
}

func TestSplitSkillBlocksPrefixBlocksRest(t *testing.T) {
	t.Parallel()

	a, _ := RenderSkillActivation("alpha", "alpha body")
	b, _ := RenderSkillActivation("beta", "beta body")
	user := "now do the thing"
	modePrefix := ModeNotice(config.ExecutionModePlan) + "\n\n"

	full := modePrefix + PrependSkillBlocks([]string{a, b}, user)

	gotPrefix, blocks, rest := SplitSkillBlocks(full)
	if gotPrefix != modePrefix {
		t.Fatalf("modePrefix = %q, want %q", gotPrefix, modePrefix)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	if blocks[0].Name != "alpha" || blocks[1].Name != "beta" {
		t.Fatalf("block names = %q, %q, want alpha, beta", blocks[0].Name, blocks[1].Name)
	}
	if blocks[0].Text != a || blocks[1].Text != b {
		t.Fatalf("block texts do not match rendered envelopes")
	}
	if rest != user {
		t.Fatalf("rest = %q, want %q", rest, user)
	}
}

func TestPrependSkillBlocks(t *testing.T) {
	t.Parallel()

	if got := PrependSkillBlocks(nil, "body"); got != "body" {
		t.Fatalf("PrependSkillBlocks(nil) = %q, want unchanged", got)
	}
	if got := PrependSkillBlocks([]string{}, "body"); got != "body" {
		t.Fatalf("PrependSkillBlocks(empty) = %q, want unchanged", got)
	}

	a, _ := RenderSkillActivation("alpha", "a")
	b, _ := RenderSkillActivation("beta", "b")
	got := PrependSkillBlocks([]string{a, b}, "body")
	want := a + "\n\n" + b + "\n\nbody"
	if got != want {
		t.Fatalf("PrependSkillBlocks() = %q, want %q", got, want)
	}
}

func TestSplitSkillBlocksMalformed(t *testing.T) {
	t.Parallel()

	valid := RenderSkillDeactivation("good")
	malformed := "<steiner-skill name=\"y\" state=\"bogus\" bytes=\"3\">\nabc\n</steiner-skill>"

	cases := []struct {
		label       string
		input       string
		wantBlocks  int
		wantRest    string
		restIsInput bool
	}{
		{label: "empty", input: "", wantBlocks: 0, restIsInput: true},
		{label: "plain text", input: "just text", wantBlocks: 0, restIsInput: true},
		{label: "open tag only", input: "<steiner-skill name=\"x\" state=\"active\" bytes=\"5\">\n", wantBlocks: 0, restIsInput: true},
		{label: "bad state", input: malformed, wantBlocks: 0, restIsInput: true},
		{label: "size beyond content", input: "<steiner-skill name=\"x\" state=\"active\" bytes=\"99\">\nabc\n</steiner-skill>", wantBlocks: 0, restIsInput: true},
		{label: "size numeric overflow", input: "<steiner-skill name=\"x\" state=\"active\" bytes=\"99999999999999999999\">\nabc\n</steiner-skill>", wantBlocks: 0, restIsInput: true},
		{label: "size max int64", input: "<steiner-skill name=\"x\" state=\"active\" bytes=\"9223372036854775807\">\nabc\n</steiner-skill>", wantBlocks: 0, restIsInput: true},
		{label: "missing close tag", input: "<steiner-skill name=\"x\" state=\"active\" bytes=\"3\">\nabc", wantBlocks: 0, restIsInput: true},
		{label: "unterminated name", input: "<steiner-skill name=\"x state=\"active\" bytes=\"3\">\nabc\n</steiner-skill>", wantBlocks: 0, restIsInput: true},
		{label: "valid then malformed", input: valid + "\n\n" + malformed, wantBlocks: 1, wantRest: malformed},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			wantRest := tc.wantRest
			if tc.restIsInput {
				wantRest = tc.input
			}

			modePrefix, blocks, rest := SplitSkillBlocks(tc.input)
			if modePrefix != "" {
				t.Fatalf("modePrefix = %q, want empty", modePrefix)
			}
			if len(blocks) != tc.wantBlocks {
				t.Fatalf("len(blocks) = %d, want %d", len(blocks), tc.wantBlocks)
			}
			if rest != wantRest {
				t.Fatalf("rest = %q, want %q", rest, wantRest)
			}
		})
	}
}

func TestStripSkillBlocks(t *testing.T) {
	t.Parallel()

	a, _ := RenderSkillActivation("alpha", "a")
	b, _ := RenderSkillActivation("beta", "b")
	user := "keep this text"
	modePrefix := ModeNotice(config.ExecutionModeBuild) + "\n\n"
	full := modePrefix + PrependSkillBlocks([]string{a, b}, user)

	if got, want := StripSkillBlocks(full), modePrefix+user; got != want {
		t.Fatalf("StripSkillBlocks() = %q, want %q", got, want)
	}
	if got := StripSkillBlocks(user); got != user {
		t.Fatalf("StripSkillBlocks() = %q, want unchanged", got)
	}
	malformed := "<steiner-skill name=\"x\" state=\"bogus\" bytes=\"3\">\nabc\n</steiner-skill>"
	if got := StripSkillBlocks(malformed); got != malformed {
		t.Fatalf("StripSkillBlocks() mangled malformed input: %q", got)
	}
}

func TestRenderSkillActivationCap(t *testing.T) {
	t.Parallel()

	capBody := strings.Repeat("a", SkillContentCapBytes)
	if _, truncated := RenderSkillActivation("plan", capBody); truncated {
		t.Fatalf("truncated = true at exactly the cap")
	}

	bigBody := capBody + "bbbbbbbbbb"
	text, truncated := RenderSkillActivation("plan", bigBody)
	if !truncated {
		t.Fatalf("truncated = false for over-cap body")
	}
	marker := "[truncated: skill exceeded the " + strconv.Itoa(SkillContentCapBytes) + "-byte cap]"
	if !strings.Contains(text, marker) {
		t.Fatalf("truncated envelope missing marker %q", marker)
	}
	if strings.Contains(text, bigBody) {
		t.Fatalf("over-cap body was not truncated")
	}
	if !utf8.ValidString(text) {
		t.Fatalf("truncated envelope is not valid UTF-8")
	}

	_, blocks, rest := SplitSkillBlocks(text)
	if len(blocks) != 1 || rest != "" {
		t.Fatalf("truncated envelope did not round-trip: blocks=%d rest=%q", len(blocks), rest)
	}
	if blocks[0].Text != text {
		t.Fatalf("round-tripped text differs from rendered text")
	}

	multi := strings.Repeat("\u00e9", SkillContentCapBytes) // 2 bytes per rune
	mtext, mtrunc := RenderSkillActivation("plan", multi)
	if !mtrunc {
		t.Fatalf("truncated = false for multibyte over-cap body")
	}
	if !utf8.ValidString(mtext) {
		t.Fatalf("multibyte truncation produced invalid UTF-8")
	}
}

func TestEffectiveSkills(t *testing.T) {
	t.Parallel()

	a1, _ := RenderSkillActivation("alpha", "A one")
	a2, _ := RenderSkillActivation("alpha", "A two")
	b1, _ := RenderSkillActivation("beta", "B")
	offA := RenderSkillDeactivation("alpha")
	offUnknown := RenderSkillDeactivation("ghost")

	cases := []struct {
		label string
		input []string
		want  []SkillBlock
	}{
		{label: "no blocks", input: []string{"just text"}, want: nil},
		{label: "single activation", input: []string{a1}, want: []SkillBlock{{Name: "alpha", State: SkillBlockActive, Text: a1}}},
		{label: "switch alpha to beta", input: []string{a1, b1}, want: []SkillBlock{{Name: "alpha", State: SkillBlockActive, Text: a1}, {Name: "beta", State: SkillBlockActive, Text: b1}}},
		{label: "deactivate removes", input: []string{a1, offA}, want: nil},
		{label: "deactivate unknown is no-op", input: []string{a1, offUnknown}, want: []SkillBlock{{Name: "alpha", State: SkillBlockActive, Text: a1}}},
		{label: "reactivation moves to end and latest wins", input: []string{a1, b1, a2}, want: []SkillBlock{{Name: "beta", State: SkillBlockActive, Text: b1}, {Name: "alpha", State: SkillBlockActive, Text: a2}}},
		{label: "deactivate then reactivate", input: []string{a1, offA, a2}, want: []SkillBlock{{Name: "alpha", State: SkillBlockActive, Text: a2}}},
		{label: "blocks in one message", input: []string{PrependSkillBlocks([]string{a1, b1}, "user text")}, want: []SkillBlock{{Name: "alpha", State: SkillBlockActive, Text: a1}, {Name: "beta", State: SkillBlockActive, Text: b1}}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			got := EffectiveSkills(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("len(EffectiveSkills()) = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("block %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
