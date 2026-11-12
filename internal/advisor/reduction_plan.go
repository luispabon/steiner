package advisor

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

const (
	// reductionClipBytes bounds the head and tail kept from one clipped
	// ordinary tool result. It is a fixed planning constant, not derived from
	// the request, so repeated calls plan the same replacement.
	reductionClipBytes = 2048

	reductionClipHeadBytes = reductionClipBytes * 3 / 4
	reductionClipTailBytes = reductionClipBytes - reductionClipHeadBytes
)

// protectedToolNames lists tool results the advisor must always see in full:
// child-agent work, prior advisor notes, and mutate receipts. internal/delegation
// imports this package, so the delegation names are restated here rather than
// imported; they are already part of the provider-facing tool contract and are
// documented. Mutate receipts prove what actually changed on disk, so reducing
// them would hide the evidence the advisor reasons about.
var protectedToolNames = map[string]bool{
	"sub_agent": true,
	"follow_up": true,
	"mutate":    true,
}

func isProtectedToolName(name string) bool {
	return name == ToolName || protectedToolNames[name]
}

type toolClassification struct {
	// names holds the resolved tool name per transcript index.
	names []string
	// ordinary is true only for tool results whose content may be reduced.
	ordinary []bool
}

// classifyToolResults resolves tool result names before flattening. A tool
// call ID maps to its assistant tool name; the message Name is the fallback.
// Either source naming a delegation tool or the advisor protects the result,
// and an unknown result with neither source is protected as well.
func classifyToolResults(transcript []provider.Message) toolClassification {
	byID := make(map[string]string)
	for _, message := range transcript {
		if message.Role != provider.MessageRoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID != "" && call.Name != "" {
				byID[call.ID] = call.Name
			}
		}
	}
	classification := toolClassification{
		names:    make([]string, len(transcript)),
		ordinary: make([]bool, len(transcript)),
	}
	for i, message := range transcript {
		if message.Role != provider.MessageRoleTool {
			continue
		}
		byIDName := ""
		if message.ToolCallID != "" {
			byIDName = byID[message.ToolCallID]
		}
		byMessageName := message.Name
		resolved := byIDName
		if resolved == "" {
			resolved = byMessageName
		}
		classification.names[i] = resolved
		if resolved == "" || isProtectedToolName(byIDName) || isProtectedToolName(byMessageName) {
			continue
		}
		classification.ordinary[i] = true
	}
	return classification
}

// protectedTail marks ordinary tool results that fall inside the most recent
// tailByte bytes of tool output. Those results stay untouched so the advisor
// keeps the freshest evidence.
func protectedTail(transcript []provider.Message, ordinary []bool, tailBytes int) map[int]bool {
	protected := make(map[int]bool)
	remaining := tailBytes
	for i := len(transcript) - 1; i >= 0 && remaining > 0; i-- {
		if !ordinary[i] {
			continue
		}
		protected[i] = true
		remaining -= len(transcript[i].Content)
	}
	return protected
}

func tokensForBytes(bytes int, ratio float64) int {
	if bytes <= 0 || ratio <= 0 {
		return 0
	}
	return int(math.Ceil(float64(bytes) * ratio))
}

type reductionPlanInput struct {
	transcript    []provider.Message
	stages        []reductionStage
	references    []int
	tuning        reductionTuning
	ratio         float64
	initialTokens int
}

type reductionPlan struct {
	transcript    []provider.Message
	stages        []reductionStage
	references    []int
	savedTokens   int
	appliedStages []string
}

// reductionPlanner carries the mutable planning state shared by the stage
// helpers. Only one planner is active per call.
type reductionPlanner struct {
	in             reductionPlanInput
	plan           reductionPlan
	classification toolClassification
	protected      map[int]bool
	ratio          float64
	appliedTokens  int
	contentSavings int
}

func newReductionPlanner(in reductionPlanInput) *reductionPlanner {
	plan := reductionPlan{
		transcript: append([]provider.Message(nil), in.transcript...),
		stages:     append([]reductionStage(nil), in.stages...),
		references: make([]int, len(in.transcript)),
	}
	for i := range plan.references {
		plan.references[i] = -1
		if i < len(in.references) {
			plan.references[i] = in.references[i]
		}
	}
	ratio := in.ratio
	if ratio <= 0 {
		ratio = 1
	}
	classification := classifyToolResults(in.transcript)
	tailBytes := int(math.Ceil(float64(in.tuning.tail) / ratio))
	return &reductionPlanner{
		in:             in,
		plan:           plan,
		classification: classification,
		protected:      protectedTail(in.transcript, classification.ordinary, tailBytes),
		ratio:          ratio,
	}
}

// planReduction applies the three reduction stages oldest-first and stops as
// soon as the planned request reaches the target. It converts byte savings to
// tokens with the ratio measured from the initial request, so no candidate is
// tokenized.
func planReduction(in reductionPlanInput) reductionPlan {
	planner := newReductionPlanner(in)
	if len(in.transcript) == 0 || in.tuning.trigger <= 0 {
		return planner.plan
	}
	planner.applyReadDedupe()
	if !planner.reachedTarget() {
		planner.applyClips()
	}
	if !planner.reachedTarget() {
		planner.applyStubs()
	}
	planner.reconcileReferences()
	return planner.plan
}

func (p *reductionPlanner) reachedTarget() bool {
	return p.in.initialTokens-p.appliedTokens <= p.in.tuning.target
}

func (p *reductionPlanner) recordStage(stage reductionStage) {
	name := stage.String()
	if len(p.plan.appliedStages) == 0 || p.plan.appliedStages[len(p.plan.appliedStages)-1] != name {
		p.plan.appliedStages = append(p.plan.appliedStages, name)
	}
}

func (p *reductionPlanner) tryApply(index int, stage reductionStage, replacement string) bool {
	if replacement == "" || replacement == p.plan.transcript[index].Content {
		return false
	}
	oldContent := p.plan.transcript[index].Content
	oldStage := p.plan.stages[index]
	oldReference := p.plan.references[index]
	p.plan.transcript[index].Content = replacement
	p.plan.stages[index] = stage
	p.plan.references[index] = -1
	netBytes := p.contentSavings + len(oldContent) - len(replacement)
	netTokens := tokensForBytes(netBytes, p.ratio)
	if netTokens <= p.appliedTokens {
		p.plan.transcript[index].Content = oldContent
		p.plan.stages[index] = oldStage
		p.plan.references[index] = oldReference
		return false
	}
	p.contentSavings += len(oldContent) - len(replacement)
	p.appliedTokens = netTokens
	p.plan.savedTokens = netTokens
	p.recordStage(stage)
	return true
}

// applyReadDedupe replaces later eligible copies of an exact full read with a
// note. The earlier copy is recorded so reconciliation can stub the note if
// that source is reduced in the same or a later batch.
func (p *reductionPlanner) applyReadDedupe() {
	type seenRead struct {
		id    string
		index int
	}
	seen := make(map[fullReadKey]seenRead)
	for i := range p.in.transcript {
		if p.reachedTarget() {
			return
		}
		if p.plan.stages[i] != reductionStageNone || !p.classification.ordinary[i] {
			continue
		}
		key, ok := fullReadIdentity(p.in.transcript[i], p.classification.names[i])
		if !ok {
			continue
		}
		earlier, exists := seen[key]
		if !exists {
			seen[key] = seenRead{id: p.in.transcript[i].ToolCallID, index: i}
			continue
		}
		if p.protected[i] {
			continue
		}
		note, ok := outputReplacement(p.classification.names[i], p.plan.transcript[i].Content, readDedupeNote(earlier.id))
		if ok && p.tryApply(i, reductionStageReadDedupe, note) {
			p.plan.references[i] = earlier.index
		}
	}
}

func (p *reductionPlanner) applyClips() {
	for i := range p.in.transcript {
		if p.reachedTarget() {
			return
		}
		if p.plan.stages[i] != reductionStageNone || !p.classification.ordinary[i] || p.protected[i] {
			continue
		}
		replacement, ok := clipOrdinaryToolContent(p.classification.names[i], p.plan.transcript[i].Content)
		if ok {
			p.tryApply(i, reductionStageClip, replacement)
		}
	}
}

func (p *reductionPlanner) applyStubs() {
	for i := range p.in.transcript {
		if p.reachedTarget() {
			return
		}
		if !p.classification.ordinary[i] || p.protected[i] {
			continue
		}
		if p.plan.stages[i] != reductionStageNone && p.plan.stages[i] != reductionStageClip {
			continue
		}
		stub, ok := outputReplacement(p.classification.names[i], p.plan.transcript[i].Content, reductionStub(p.classification.names[i], p.in.transcript[i].ToolCallID))
		if ok {
			p.tryApply(i, reductionStageStub, stub)
		}
	}
}

// reconcileReferences turns a duplicate note into a self-contained omission
// stub when the earlier source it points at is also reduced.
func (p *reductionPlanner) reconcileReferences() {
	for i := range p.plan.transcript {
		if p.plan.stages[i] != reductionStageReadDedupe {
			continue
		}
		reference := p.plan.references[i]
		if reference < 0 || reference >= len(p.plan.stages) || p.plan.stages[reference] == reductionStageNone {
			continue
		}
		stub, ok := outputReplacement(p.classification.names[i], p.plan.transcript[i].Content, unavailableDuplicateStub)
		if !ok || stub == p.plan.transcript[i].Content {
			continue
		}
		oldContent := p.plan.transcript[i].Content
		p.plan.transcript[i].Content = stub
		p.plan.stages[i] = reductionStageStub
		p.plan.references[i] = -1
		p.contentSavings += len(oldContent) - len(stub)
		p.appliedTokens = tokensForBytes(p.contentSavings, p.ratio)
		p.plan.savedTokens = p.appliedTokens
		p.recordStage(reductionStageStub)
	}
}

// unavailableDuplicateStub replaces a duplicate note whose earlier source was
// also reduced. It is self-contained and points at no unavailable evidence.
const unavailableDuplicateStub = "[duplicate read output omitted by advisor context reduction; the earlier copy was also reduced. Re-read the file if you need the content.]"

// rewriteToolOutput decodes a structured read or bash result, transforms only
// its Output, and re-marshals it with every other field intact. Other tools
// transform their raw content directly. Malformed structured results are left
// unchanged.
func rewriteToolOutput(name, content string, transform func(string) (string, bool)) (string, bool) {
	switch name {
	case "read":
		var result builtin.ReadResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return "", false
		}
		output, ok := transform(result.Output)
		if !ok {
			return "", false
		}
		result.Output = output
		data, err := json.Marshal(result)
		if err != nil {
			return "", false
		}
		return string(data), true
	case "bash":
		var result builtin.BashResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return "", false
		}
		output, ok := transform(result.Output)
		if !ok {
			return "", false
		}
		result.Output = output
		data, err := json.Marshal(result)
		if err != nil {
			return "", false
		}
		return string(data), true
	default:
		return transform(content)
	}
}

// outputReplacement replaces the Output of a structured read or bash result,
// or the raw content of an unknown tool.
func outputReplacement(name, content, output string) (string, bool) {
	return rewriteToolOutput(name, content, func(string) (string, bool) {
		return output, true
	})
}

// clipOrdinaryToolContent reduces a tool result. Structured read and bash
// results keep every field except Output; other tool output is clipped as raw
// text because the advisor flattens it anyway.
func clipOrdinaryToolContent(name, content string) (string, bool) {
	return rewriteToolOutput(name, content, func(text string) (string, bool) {
		return clipHeadTail(text, reductionClipBytes)
	})
}

// clipHeadTail keeps a UTF-8-safe head and tail of text with an omitted-byte
// marker. It returns false when clipping would not shorten the text.
func clipHeadTail(text string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text, false
	}
	head := text[:safePrefixLen(text, reductionClipHeadBytes)]
	tailStart := safeTailStart(text, max(0, len(text)-reductionClipTailBytes))
	tail := text[tailStart:]
	marker := fmt.Sprintf("\n…[%d bytes omitted]…\n", len(text)-len(head)-len(tail))
	clipped := head + marker + tail
	if len(clipped) >= len(text) {
		return text, false
	}
	return clipped, true
}

func safePrefixLen(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

func safeTailStart(s string, n int) int {
	if n <= 0 {
		return 0
	}
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return min(n, len(s))
}

func readDedupeNote(earlierToolCallID string) string {
	call := strings.TrimSpace(earlierToolCallID)
	if call == "" {
		call = "an earlier call"
	} else {
		call = "call " + call
	}
	return fmt.Sprintf("Same content as read %s earlier in this conversation; the content is not repeated here. Re-read the file if you need it.", call)
}

func reductionStub(name, toolCallID string) string {
	tool := strings.TrimSpace(name)
	if tool == "" {
		tool = "unknown tool"
	}
	call := strings.TrimSpace(toolCallID)
	if call == "" {
		call = "unknown call"
	}
	return fmt.Sprintf("[output omitted by advisor context reduction; tool result %s (call %s) is no longer included. Re-read or re-run it if these details matter.]", tool, call)
}
