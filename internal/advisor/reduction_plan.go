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

// delegationToolNames mirrors delegation.SubAgentToolName and
// delegation.FollowUpToolName. internal/delegation imports this package, so a
// direct import would cycle; the names are part of the provider-facing tool
// contract and are already restated in docs. Results from these tools carry
// child work that the advisor must see in full. Prior advisor notes are
// protected the same way because they are distilled guidance.
var delegationToolNames = map[string]bool{
	"sub_agent": true,
	"follow_up": true,
}

func isProtectedToolName(name string) bool {
	return name == ToolName || isDelegationToolName(name)
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

func isDelegationToolName(name string) bool {
	return name != "" && delegationToolNames[name]
}

// fullReadKey identifies a full-file read without relying on file_hash, which
// normalizes trailing whitespace and cannot prove byte equality.
type fullReadKey struct {
	path   string
	output string
}

func fullReadIdentity(message provider.Message, name string) (fullReadKey, bool) {
	if name != "read" {
		return fullReadKey{}, false
	}
	var result builtin.ReadResult
	if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
		return fullReadKey{}, false
	}
	if result.Image != nil || result.NextOffset != 0 || result.TotalLines == 0 {
		return fullReadKey{}, false
	}
	if result.StartLine != 1 || result.EndLine != result.TotalLines {
		return fullReadKey{}, false
	}
	if result.Output == "" || strings.Contains(result.Output, builtin.LineTruncationMarker) {
		return fullReadKey{}, false
	}
	return fullReadKey{path: result.Path, output: result.Output}, true
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

// dedupeAttachments drops requested file content only when an untouched full
// read of that file remains in the reduced transcript and its Output matches
// the file bytes exactly. It deliberately avoids file_hash, which normalizes
// trailing whitespace. Any uncertainty leaves the attachment in place.
func dedupeAttachments(files []advisorFile, transcript []provider.Message, stages []reductionStage, workDir string) []advisorFile {
	if len(files) == 0 {
		return nil
	}
	cloned := append([]advisorFile(nil), files...)
	for i := range cloned {
		file := &cloned[i]
		if file.TotalBytes > maxAdvisorFileBytes || len(file.Content) != file.TotalBytes {
			continue
		}
		if !hasUntouchedFullReadBytes(transcript, stages, file.DisplayPath, file.Content, workDir) {
			continue
		}
		file.Content = ""
		file.Deduped = true
	}
	return cloned
}

func hasUntouchedFullReadBytes(transcript []provider.Message, stages []reductionStage, path, content, workDir string) bool {
	for i := len(transcript) - 1; i >= 0; i-- {
		if i < len(stages) && stages[i] != reductionStageNone {
			continue
		}
		message := transcript[i]
		if message.Role != provider.MessageRoleTool || message.Name != "read" {
			continue
		}
		var result builtin.ReadResult
		if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
			continue
		}
		if result.Image != nil || result.NextOffset != 0 || result.TotalLines == 0 {
			continue
		}
		if result.StartLine != 1 || result.EndLine != result.TotalLines {
			continue
		}
		if strings.Contains(result.Output, builtin.LineTruncationMarker) {
			continue
		}
		if relDisplayPath(workDir, result.Path) != path {
			continue
		}
		if result.Output != content {
			continue
		}
		return true
	}
	return false
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
	savedBytes    int
	savedTokens   int
	appliedStages []string
}

// planReduction applies the three reduction stages oldest-first and stops as
// soon as the planned request reaches the target. It converts byte savings to
// tokens with the ratio measured from the initial request, so no candidate is
// tokenized.
func planReduction(in reductionPlanInput) reductionPlan {
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
	if len(in.transcript) == 0 || in.tuning.trigger <= 0 {
		return plan
	}
	ratio := in.ratio
	if ratio <= 0 {
		ratio = 1
	}
	classification := classifyToolResults(in.transcript)
	tailBytes := int(math.Ceil(float64(in.tuning.tail) / ratio))
	protected := protectedTail(in.transcript, classification.ordinary, tailBytes)
	appliedTokens := 0
	contentSavings := 0

	reachedTarget := func() bool {
		return in.initialTokens-appliedTokens <= in.tuning.target
	}
	recordStage := func(stage reductionStage) {
		name := stage.String()
		if len(plan.appliedStages) == 0 || plan.appliedStages[len(plan.appliedStages)-1] != name {
			plan.appliedStages = append(plan.appliedStages, name)
		}
	}
	tryApply := func(index int, stage reductionStage, replacement string) bool {
		if replacement == "" || replacement == plan.transcript[index].Content {
			return false
		}
		oldContent := plan.transcript[index].Content
		oldStage := plan.stages[index]
		oldReference := plan.references[index]
		plan.transcript[index].Content = replacement
		plan.stages[index] = stage
		plan.references[index] = -1
		netBytes := contentSavings + len(oldContent) - len(replacement)
		netTokens := tokensForBytes(netBytes, ratio)
		if netTokens <= appliedTokens {
			plan.transcript[index].Content = oldContent
			plan.stages[index] = oldStage
			plan.references[index] = oldReference
			return false
		}
		contentSavings += len(oldContent) - len(replacement)
		appliedTokens = netTokens
		plan.savedBytes = netBytes
		plan.savedTokens = netTokens
		recordStage(stage)
		return true
	}
	finish := func() reductionPlan {
		// A duplicate note may point at a source that this batch also reduces.
		// Replace such references with a self-contained omission stub so the
		// advisor never points at evidence it no longer has.
		for i := range plan.transcript {
			if plan.stages[i] != reductionStageReadDedupe {
				continue
			}
			reference := plan.references[i]
			if reference < 0 || reference >= len(plan.stages) || plan.stages[reference] == reductionStageNone {
				continue
			}
			stub, ok := outputReplacement(classification.names[i], plan.transcript[i].Content, unavailableDuplicateStub)
			if !ok || stub == plan.transcript[i].Content {
				continue
			}
			oldContent := plan.transcript[i].Content
			plan.transcript[i].Content = stub
			plan.stages[i] = reductionStageStub
			plan.references[i] = -1
			contentSavings += len(oldContent) - len(stub)
			appliedTokens = tokensForBytes(contentSavings, ratio)
			plan.savedBytes = contentSavings
			plan.savedTokens = appliedTokens
			recordStage(reductionStageStub)
		}
		return plan
	}

	// Stage 1: exact duplicate full reads. Replace later eligible copies with
	// a note; finish turns that note into a self-contained stub when the
	// earlier copy is reduced too. Output bytes must match exactly.
	type seenRead struct {
		id    string
		index int
	}
	seen := make(map[fullReadKey]seenRead)
	for i := range in.transcript {
		if plan.stages[i] != reductionStageNone || !classification.ordinary[i] {
			continue
		}
		key, ok := fullReadIdentity(in.transcript[i], classification.names[i])
		if !ok {
			continue
		}
		if earlier, exists := seen[key]; exists {
			if protected[i] {
				continue
			}
			note, ok := outputReplacement(classification.names[i], plan.transcript[i].Content, readDedupeNote(earlier.id))
			if ok && tryApply(i, reductionStageReadDedupe, note) {
				plan.references[i] = earlier.index
				if reachedTarget() {
					return finish()
				}
			}
			continue
		}
		seen[key] = seenRead{id: in.transcript[i].ToolCallID, index: i}
	}

	// Stage 2: head and tail clips for older ordinary results.
	for i := range in.transcript {
		if reachedTarget() {
			break
		}
		if plan.stages[i] != reductionStageNone || !classification.ordinary[i] || protected[i] {
			continue
		}
		replacement, ok := clipOrdinaryToolContent(classification.names[i], plan.transcript[i].Content)
		if ok && tryApply(i, reductionStageClip, replacement) && reachedTarget() {
			return finish()
		}
	}

	// Stage 3: stubs for older results, including already clipped ones.
	for i := range in.transcript {
		if reachedTarget() {
			break
		}
		if !classification.ordinary[i] || protected[i] {
			continue
		}
		if plan.stages[i] != reductionStageNone && plan.stages[i] != reductionStageClip {
			continue
		}
		stub, ok := outputReplacement(classification.names[i], plan.transcript[i].Content, reductionStub(classification.names[i], in.transcript[i].ToolCallID))
		if ok && tryApply(i, reductionStageStub, stub) && reachedTarget() {
			return finish()
		}
	}
	return finish()
}

// unavailableDuplicateStub replaces a duplicate note whose earlier source was
// also reduced. It is self-contained and points at no unavailable evidence.
const unavailableDuplicateStub = "[duplicate read output omitted by advisor context reduction; the earlier copy was also reduced. Re-read the file if you need the content.]"

// outputReplacement changes only Output for structured read and bash results,
// keeping every other field intact. Unknown tools use the replacement as raw
// content.
func outputReplacement(name, content, output string) (string, bool) {
	switch name {
	case "read":
		var result builtin.ReadResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
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
		result.Output = output
		data, err := json.Marshal(result)
		if err != nil {
			return "", false
		}
		return string(data), true
	default:
		return output, true
	}
}

// clipOrdinaryToolContent reduces a tool result. Structured read and bash
// results keep every field except Output; other tool output is clipped as raw
// text because the advisor flattens it anyway.
func clipOrdinaryToolContent(name, content string) (string, bool) {
	switch name {
	case "read":
		var result builtin.ReadResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return "", false
		}
		clipped, changed := clipHeadTail(result.Output, reductionClipBytes)
		if !changed {
			return "", false
		}
		return outputReplacement(name, content, clipped)
	case "bash":
		var result builtin.BashResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return "", false
		}
		clipped, changed := clipHeadTail(result.Output, reductionClipBytes)
		if !changed {
			return "", false
		}
		return outputReplacement(name, content, clipped)
	}
	return clipHeadTail(content, reductionClipBytes)
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
