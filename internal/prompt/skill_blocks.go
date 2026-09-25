package prompt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SkillContentCapBytes is the maximum SKILL.md body size delivered in a
// conversation skill block; larger bodies are truncated.
const SkillContentCapBytes = defaultSkillBudgetBytes

// SkillBlockState is the state carried by a skill block envelope.
type SkillBlockState string

const (
	// SkillBlockActive marks a skill activation envelope.
	SkillBlockActive SkillBlockState = "active"
	// SkillBlockInactive marks a skill deactivation envelope.
	SkillBlockInactive SkillBlockState = "inactive"
)

// SkillBlock is one parsed skill block envelope.
type SkillBlock struct {
	Name  string
	State SkillBlockState
	Text  string // full envelope, open tag through close tag, byte-identical to the rendered text
}

// skillEnvelopeOpenRE matches a skill envelope open tag anchored at the start of
// the input. The name group is a strconv.Quote-style quoted string so any
// directory name round-trips.
var skillEnvelopeOpenRE = regexp.MustCompile(`\A<steiner-skill name=("(?:[^"\\]|\\.)*") state="(active|inactive)" bytes="(\d+)">\n`)

// renderSkillEnvelope builds a skill block envelope with a byte-length header,
// using the single shared envelope format.
func renderSkillEnvelope(name string, state SkillBlockState, inner string) string {
	open := fmt.Sprintf("<steiner-skill name=%s state=%q bytes=\"%d\">\n", strconv.Quote(name), state, len(inner))
	return open + inner + "\n</steiner-skill>"
}

// RenderSkillActivation renders an activation envelope for name, truncating body
// to SkillContentCapBytes when it is larger. The returned bool reports whether
// truncation happened.
func RenderSkillActivation(name, body string) (string, bool) {
	truncated := len(body) > SkillContentCapBytes
	if truncated {
		body = truncateText(body, SkillContentCapBytes) +
			"\n\n[truncated: skill exceeded the " + strconv.Itoa(SkillContentCapBytes) + "-byte cap]"
	}
	inner := "## Active Skill: " + name + "\n\n" + skillFramingText + "\n\n" + body
	return renderSkillEnvelope(name, SkillBlockActive, inner), truncated
}

// RenderSkillDeactivation renders a deactivation envelope for name.
func RenderSkillDeactivation(name string) string {
	inner := fmt.Sprintf("Skill %q is no longer active. Stop following its workflow; its earlier instructions no longer apply.", name)
	return renderSkillEnvelope(name, SkillBlockInactive, inner)
}

// PrependSkillBlocks joins rendered skill blocks ahead of content, separated by
// blank lines. With no blocks it returns content unchanged.
func PrependSkillBlocks(blocks []string, content string) string {
	if len(blocks) == 0 {
		return content
	}
	return strings.Join(blocks, "\n\n") + "\n\n" + content
}

// SplitSkillBlocks splits content into an optional leading execution-mode
// notice, any leading skill block envelopes, and the remaining content. Parsing
// stops at the first malformed envelope; rest then starts there and preserves
// the remaining bytes.
func SplitSkillBlocks(content string) (modePrefix string, blocks []SkillBlock, rest string) {
	modePrefix = modeNoticePrefix(content)
	pos := len(modePrefix)
	for pos < len(content) {
		block, next, ok := parseSkillBlockAt(content, pos)
		if !ok {
			break
		}
		blocks = append(blocks, block)
		pos = next
		if strings.HasPrefix(content[pos:], "\n\n") {
			pos += 2
		}
	}
	return modePrefix, blocks, content[pos:]
}

// parseSkillBlockAt parses one envelope starting at pos. It returns the block,
// the offset just past its close tag, and whether parsing succeeded.
func parseSkillBlockAt(content string, pos int) (SkillBlock, int, bool) {
	m := skillEnvelopeOpenRE.FindStringSubmatchIndex(content[pos:])
	if m == nil {
		return SkillBlock{}, pos, false
	}
	name, err := strconv.Unquote(content[pos+m[2] : pos+m[3]])
	if err != nil {
		return SkillBlock{}, pos, false
	}
	size, err := strconv.Atoi(content[pos+m[6] : pos+m[7]])
	if err != nil || size < 0 {
		return SkillBlock{}, pos, false
	}
	innerStart := pos + m[1]
	// Guard before the addition: a size that fits int can still overflow
	// innerStart+size and wrap negative, panicking the slice below.
	if size > len(content)-innerStart {
		return SkillBlock{}, pos, false
	}
	innerEnd := innerStart + size
	const closeTag = "\n</steiner-skill>"
	if !strings.HasPrefix(content[innerEnd:], closeTag) {
		return SkillBlock{}, pos, false
	}
	end := innerEnd + len(closeTag)
	return SkillBlock{
		Name:  name,
		State: SkillBlockState(content[pos+m[4] : pos+m[5]]),
		Text:  content[pos:end],
	}, end, true
}

// StripSkillBlocks removes leading skill block envelopes from content, keeping
// any execution-mode notice prefix and the trailing content byte-for-byte.
func StripSkillBlocks(content string) string {
	modePrefix, _, rest := SplitSkillBlocks(content)
	return modePrefix + rest
}

// EffectiveSkills folds skill blocks across the given user-role message contents
// in order and returns the skills still active. A later activation of a name
// replaces the earlier block and moves it to the end of the order; a
// deactivation removes it.
func EffectiveSkills(contents []string) []SkillBlock {
	var active []SkillBlock
	for _, content := range contents {
		_, blocks, _ := SplitSkillBlocks(content)
		for _, block := range blocks {
			active = withoutSkill(active, block.Name)
			if block.State == SkillBlockActive {
				active = append(active, block)
			}
		}
	}
	return active
}

// withoutSkill returns blocks with any entry named name removed, preserving
// order.
func withoutSkill(blocks []SkillBlock, name string) []SkillBlock {
	out := blocks[:0]
	for _, b := range blocks {
		if b.Name != name {
			out = append(out, b)
		}
	}
	return out
}
