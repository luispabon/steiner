//go:build claudesub_probe

package provider

import (
	"fmt"
	"strings"
)

// claudeSubProbeFindings derives the per-process facts the report states next to
// the timeline.
func claudeSubProbeFindings(rep *claudeSubProbeProcessReport) []string {
	var out []string
	if rep.InitPath != "" {
		out = append(out, "init path: "+rep.InitPath)
	}
	if len(rep.WarmupRejectedCalls) > 0 {
		out = append(out, fmt.Sprintf("warmup MCP calls rejected with an error result: %v", rep.WarmupRejectedCalls))
	}
	if rep.Init != nil {
		out = append(out, fmt.Sprintf("init: model=%q tools=%v cli=%q capabilities=%s", rep.Init.Model, rep.Init.Tools, rep.Init.CLIVersion, claudeSubProbeExcerpt(string(rep.Init.Capabilities))))
		for _, key := range sortedNames(rep.Init.Extras) {
			out = append(out, fmt.Sprintf("init %s: %v", key, rep.Init.Extras[key].Names))
		}
	}
	if len(rep.Flags) > 0 {
		out = append(out, "argv switches: "+strings.Join(rep.Flags, " "))
	}
	if rep.TimelineTruncated {
		out = append(out, fmt.Sprintf("timeline truncated at %d entries: the recorded exchange is incomplete", claudeSubProbeTimelineMax))
	}
	for i, entry := range rep.Timeline {
		if entry.Dir == "out" && entry.Type == "control_request" && entry.ControlSubtype == "interrupt" {
			out = append(out, claudeSubProbeInterruptFinding(rep.Timeline, i))
		}
	}
	for i, entry := range rep.Timeline {
		if entry.Dir == "in" && entry.Type == "result" {
			out = append(out, claudeSubProbeResultFinding(rep.Timeline, i))
		}
	}
	var cache []string
	for _, turn := range rep.Turns {
		out = append(out, claudeSubProbeTurnFinding(turn))
		cache = append(cache, fmt.Sprintf("turn%d=%d", turn.Index, claudeSubProbeCacheRead(turn)))
	}
	if len(cache) > 0 {
		baseline := "turn0"
		if rep.InitPath == claudeSubProbeInitPathWarmup {
			baseline = "turn1 (turn0 is the warmup turn and its cache counters are not a baseline)"
		}
		out = append(out, "cache_read_input_tokens by turn (baseline "+baseline+"): "+strings.Join(cache, " "))
	}
	for _, call := range rep.MCPCalls {
		var states []string
		for _, state := range call.Calls {
			states = append(states, fmt.Sprintf("%s:%s", state.ID, state.Outcome))
		}
		out = append(out, fmt.Sprintf("MCP at %dms %s: %s", call.AtMS, call.Label, strings.Join(states, " ")))
	}
	if rep.Exit != nil {
		out = append(out, fmt.Sprintf("exit at %dms: status=%d wait_error=%q exited_before_teardown=%v proc_err=%q conn_err=%q pool_close_err=%q unobserved_events=%d",
			rep.Exit.AtMS, rep.Exit.Status, rep.Exit.WaitErr, rep.Exit.ExitedBeforeTeardown, rep.Exit.ProcErr, rep.Exit.ConnErr, rep.Exit.PoolCloseErr, rep.Exit.Unobserved))
	}
	return out
}

// sortedNames lists the init extras in the probe's fixed field order, then any
// extra hook field names.
func sortedNames(extras map[string]claudeSubProbeNames) []string {
	ordered := make([]string, 0, len(extras))
	seen := make(map[string]struct{}, len(extras))
	for _, key := range claudeSubProbeInitExtras {
		if _, ok := extras[key]; ok {
			ordered = append(ordered, key)
			seen[key] = struct{}{}
		}
	}
	var rest []string
	for key := range extras {
		if _, ok := seen[key]; !ok {
			rest = append(rest, key)
		}
	}
	for i := 0; i < len(rest); i++ {
		for j := i + 1; j < len(rest); j++ {
			if rest[j] < rest[i] {
				rest[i], rest[j] = rest[j], rest[i]
			}
		}
	}
	return append(ordered, rest...)
}

// claudeSubProbeCacheRead reports a turn's cache read tokens, preferring the
// assistant message's own usage over the query-cumulative result usage.
func claudeSubProbeCacheRead(turn *claudeSubProbeTurn) int {
	if turn.AssistantUsage != nil {
		return turn.AssistantUsage.CacheReadInputTokens
	}
	if turn.StreamUsage != nil {
		return turn.StreamUsage.CacheReadInputTokens
	}
	if turn.Result != nil && turn.Result.Usage != nil {
		return turn.Result.Usage.CacheReadInputTokens
	}
	return 0
}

func claudeSubProbeInterruptFinding(timeline []claudeSubProbeEntry, at int) string {
	sent := timeline[at]
	responseIdx, resultIdx := -1, -1
	for i := at + 1; i < len(timeline); i++ {
		entry := timeline[i]
		if responseIdx < 0 && entry.Dir == "in" && entry.Type == "control_response" && entry.RequestID == sent.RequestID {
			responseIdx = i
		}
		if resultIdx < 0 && entry.Dir == "in" && entry.Type == "result" {
			resultIdx = i
		}
	}
	describe := func(idx int) string {
		if idx < 0 {
			return "none"
		}
		return fmt.Sprintf("%dms", timeline[idx].AtMS)
	}
	order := "unknown"
	switch {
	case responseIdx < 0 && resultIdx < 0:
	case responseIdx < 0:
		order = "result first, no control_response"
	case resultIdx < 0:
		order = "control_response only, no result"
	case responseIdx < resultIdx:
		order = "control_response before result"
	default:
		order = "result before control_response"
	}
	responseSubtype := "none"
	responseBody := ""
	if responseIdx >= 0 {
		responseSubtype = timeline[responseIdx].ResponseSubtype
		responseBody = timeline[responseIdx].ResponseBody
	}
	return fmt.Sprintf("interrupt %s sent at %dms: control_response=%s subtype=%q body=%q, first result=%s, order=%s",
		sent.RequestID, sent.AtMS, describe(responseIdx), responseSubtype, responseBody, describe(resultIdx), order)
}

func claudeSubProbeResultFinding(timeline []claudeSubProbeEntry, at int) string {
	entry := timeline[at]
	isError := entry.IsError != nil && *entry.IsError
	var after []string
	for _, next := range timeline[at+1:] {
		if next.Dir != "in" {
			continue
		}
		kind := next.Type
		if next.Subtype != "" {
			kind += "/" + next.Subtype
		}
		after = append(after, fmt.Sprintf("%s@%dms", kind, next.AtMS))
		if len(after) == 10 {
			break
		}
	}
	return fmt.Sprintf("result at %dms: subtype=%q terminal_reason=%q is_error=%v stop_reason=%q usage=%s later_inbound=%v",
		entry.AtMS, entry.Subtype, entry.TerminalReason, isError, entry.StopReason, claudeSubProbeUsageString(entry.Usage), after)
}

func claudeSubProbeTurnFinding(turn *claudeSubProbeTurn) string {
	result := "none"
	if turn.Result != nil {
		result = fmt.Sprintf("subtype=%q terminal_reason=%q is_error=%v usage=%s", turn.Result.Subtype, turn.Result.TerminalReason, turn.Result.IsError, claudeSubProbeUsageString(turn.Result.Usage))
	}
	answer := claudeSubProbeExcerpt(turn.AnswerText)
	return fmt.Sprintf("turn %d interrupted=%v events=%d result=%s assistant_usage=%s answer=%q",
		turn.Index, turn.Interrupted, turn.Events, result, claudeSubProbeUsageString(turn.AssistantUsage), answer)
}

// claudeSubProbeResultsIn returns the terminal result entries at or after index.
func claudeSubProbeResultsIn(timeline []claudeSubProbeEntry, index int) []claudeSubProbeEntry {
	if index < 0 || index > len(timeline) {
		return nil
	}
	var out []claudeSubProbeEntry
	for _, entry := range timeline[index:] {
		if entry.Dir == "in" && entry.Type == "result" {
			out = append(out, entry)
		}
	}
	return out
}

// claudeSubProbeResultsWithTextIn counts results at or after index whose text
// contains want, case-insensitively. It is how a specific turn's result is
// identified in the stream.
func claudeSubProbeResultsWithTextIn(timeline []claudeSubProbeEntry, index int, want string) []claudeSubProbeEntry {
	results := claudeSubProbeResultsIn(timeline, index)
	if want == "" {
		return results
	}
	want = strings.ToLower(want)
	var out []claudeSubProbeEntry
	for _, entry := range results {
		if strings.Contains(strings.ToLower(entry.Text), want) {
			out = append(out, entry)
		}
	}
	return out
}

// claudeSubProbeInboundTextIn reports whether inbound assistant or result text at
// or after index contains want, case-insensitively. Outbound lines never match,
// so a probe prompt cannot satisfy its own check.
func claudeSubProbeInboundTextIn(timeline []claudeSubProbeEntry, index int, want string) bool {
	if index < 0 || index > len(timeline) || want == "" {
		return false
	}
	want = strings.ToLower(want)
	for _, entry := range timeline[index:] {
		if entry.Dir != "in" {
			continue
		}
		if entry.Type != "assistant" && entry.Type != "result" && entry.DeltaType != "text_delta" {
			continue
		}
		if strings.Contains(strings.ToLower(entry.Text), want) {
			return true
		}
	}
	return false
}

// claudeSubProbeOutboundInterruptIn reports the index of the first outbound
// interrupt control request at or after index, or -1.
func claudeSubProbeOutboundInterruptIn(timeline []claudeSubProbeEntry, index int) int {
	if index < 0 || index > len(timeline) {
		return -1
	}
	for i := index; i < len(timeline); i++ {
		entry := timeline[i]
		if entry.Dir == "out" && entry.Type == "control_request" && entry.ControlSubtype == "interrupt" {
			return i
		}
	}
	return -1
}
