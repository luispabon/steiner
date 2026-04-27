package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type contentSegmentKind int

const (
	segmentPlain contentSegmentKind = iota
	segmentAssistantProse
	segmentAssistantMarkdown
	segmentApproval
	segmentTool
	segmentThinking
	segmentUser
	segmentThinkingBlock
	segmentToolCall
	segmentApprovalPill
	segmentCompactionBanner
	segmentInterrupted
)

type thinkingBlockData struct {
	preview   string // first 80 chars
	collapsed bool   // default true
	body      string // full content
	streaming bool   // true while chunks are still arriving
}

type toolCallSegment struct {
	tool                     string // "bash", "read", "write", "edit", "glob", "grep", "todo", etc.
	args                     string // summarized args, ~60 chars max
	meta                     string // "✅" or "❌" for finished calls
	bodyKind                 string // "bash", "diff", "file", "plain"
	body                     string // raw result text
	callID                   string // for matching started→finished
	collapsed                bool   // default true
	hasError                 bool   // set when ToolCallFinished carries an error
	rawArgs                  map[string]any
	writeTargetExistedBefore *bool
	preview                  output.ToolPreview
}

type approvalPillData struct {
	tool     string
	mode     string
	preview  string
	resolved bool
	accepted bool
}

type compactionBannerData struct {
	label    string
	subtitle string
	finished bool
	summary  string
	progress float64 // 0.0-1.0 fill ratio for in-progress bar (if known)
	pct      int     // percentage label for in-progress (if known)
	msgCount int     // number of messages compacted (for finished summary)
}

type contentSegment struct {
	kind           contentSegmentKind
	text           string
	thinkData      *thinkingBlockData    // non-nil only for segmentThinkingBlock
	toolData       *toolCallSegment      // non-nil only for segmentToolCall
	approvalData   *approvalPillData     // non-nil only for segmentApprovalPill
	compactionData *compactionBannerData // non-nil only for segmentCompactionBanner
}

type contentBuffer struct {
	segments          []contentSegment
	streaming         bool
	hadChunks         bool
	streamBuffer      string
	renderer          *glamour.TermRenderer
	renderWidth       int
	styles            theme.Styles
	glamourStyleSheet glamour.TermRendererOption
	collapseState     map[int]bool // segment index → collapsed (for tool calls and thinking)
	segmentHeights    []int        // rendered line count per segment (recomputed in String())
	showThinking      bool         // from prefs; when false skip thinking segments
	inCompaction      bool         // when true skip thinking chunks from compaction
	streamingPhase    string       // "thinking" | "tool" | "answer" | ""
	tickCount         int          // incremented by 500ms tick, used for cursor blink
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeThinkingChunk:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
			b.appendThinkingChunk(payload.Content)
			return
		}
	case output.EventTypeAssistantChunk:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
			b.finalizeThinkingBlock()
			b.appendAssistantChunk(payload.Content)
			return
		}
	case output.EventTypeApprovalRequested:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ApprovalEvent); ok {
			seg := contentSegment{
				kind: segmentApprovalPill,
				approvalData: &approvalPillData{
					tool:    payload.Tool,
					mode:    payload.Mode,
					preview: payload.Preview,
				},
			}
			b.segments = append(b.segments, seg)
		} else {
			b.appendStyled(formatApprovalEvent(event), segmentApproval)
		}
		return
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
		b.finishStreaming()
		// Find the last unresolved approval pill and mark it resolved
		accepted := event.Type == output.EventTypeApprovalAccepted
		for i := len(b.segments) - 1; i >= 0; i-- {
			if b.segments[i].kind == segmentApprovalPill && b.segments[i].approvalData != nil {
				if !b.segments[i].approvalData.resolved {
					b.segments[i].approvalData.resolved = true
					b.segments[i].approvalData.accepted = accepted
					return
				}
			}
		}
		// Fallback
		b.appendStyled(formatApprovalEvent(event), segmentApproval)
		return
	case output.EventTypeDelegationStarted, output.EventTypeDelegationComplete, output.EventTypeDelegationFailed:
		b.finishStreaming()
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	case output.EventTypeToolCallStarted:
		b.finishStreaming()
		b.streamingPhase = "tool"
		if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
			tc := &toolCallSegment{
				tool:                     strings.ToLower(payload.Tool),
				args:                     summarizeArgs(payload.Tool, payload.Arguments),
				callID:                   payload.CallID,
				collapsed:                true,
				rawArgs:                  cloneToolArguments(payload.Arguments),
				writeTargetExistedBefore: payload.WriteTargetExistedBefore,
			}
			b.segments = append(b.segments, contentSegment{
				kind:     segmentToolCall,
				toolData: tc,
			})
		} else {
			b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
		}
		return

	case output.EventTypeToolCallFinished:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ToolCallFinishedEvent); ok {
			// Find the last segmentToolCall with matching callID (or just last tool call)
			for i := len(b.segments) - 1; i >= 0; i-- {
				if b.segments[i].kind == segmentToolCall && b.segments[i].toolData != nil {
					td := b.segments[i].toolData
					if td.callID == "" || td.callID == payload.CallID {
						td.body = payload.Result
						td.hasError = payload.Error != ""

						td.meta = "✅"
						if td.hasError {
							td.meta = "❌"
						}

						td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result, td.writeTargetExistedBefore)
						if td.preview.Kind != output.ToolPreviewKindPlain {
							td.bodyKind = previewBodyKind(td.tool, td.preview)
						} else {
							td.bodyKind = inferBodyKind(td.tool, payload.Result)
						}
						break
					}
				}
			}
		} else {
			b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
		}
		return
	case output.EventTypeStopReason:
		b.finishStreaming()
		b.appendLine(formatStopReasonEvent(event))
		return
	case output.EventTypeAssistantMessage:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.AssistantMessageEvent); ok && payload.Content != "" && !b.hadChunks {
			b.finishStreaming()
			b.appendMarkdownBlock(payload.Content)
		}
		b.hadChunks = false
		return
	case output.EventTypeContextReport:
		if payload, ok := event.Payload.(output.ContextReportEvent); ok && strings.TrimSpace(payload.Content) != "" {
			b.finishStreaming()
			b.appendMarkdownBlock(payload.Content)
		}
		return
	case output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeContextDiagnostics:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ContextDiagnosticsEvent); ok {
			switch payload.Kind {
			case "compaction":
				if payload.Severity == "compacting" {
					b.segments = append(b.segments, contentSegment{
						kind: segmentCompactionBanner,
						compactionData: &compactionBannerData{
							label:    "Compacting",
							subtitle: "summarizing context",
							finished: false,
						},
					})
				} else {
					msgCount := payload.CompactedMessages
					var summary string
					switch {
					case payload.SummaryTitle != "":
						summary = payload.SummaryTitle
					case msgCount > 0:
						summary = fmt.Sprintf("%d messages summarized into 1", msgCount)
					case payload.CompactedTurns > 0:
						summary = fmt.Sprintf("%d turns summarized", payload.CompactedTurns)
					default:
						summary = "context compacted"
					}
					b.segments = append(b.segments, contentSegment{
						kind: segmentCompactionBanner,
						compactionData: &compactionBannerData{
							label:    "Context compacted",
							subtitle: summary,
							finished: true,
							summary:  summary,
							msgCount: msgCount,
						},
					})
				}
			case "session_health":
				if b.inCompaction {
					return
				}
				b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentThinking)
			}
		}
		return
	case output.EventTypeUserInput:
		if payload, ok := event.Payload.(output.UserInputEvent); ok && strings.TrimSpace(payload.Content) != "" {
			b.segments = append(b.segments, contentSegment{kind: segmentUser, text: payload.Content})
			if len(b.segments)-1 >= 0 {
				b.collapseState[len(b.segments)-1] = false // user segments never collapsed
			}
		}
		return
	case output.EventTypeRunStarted, output.EventTypeRunFinished,
		output.EventTypeTurnStarted, output.EventTypeTurnFinished,
		output.EventTypeAPIRequest, output.EventTypeAPIResponse:
		return
	}

	b.finishStreaming()
	line := strings.TrimSpace(output.FormatEvent(event))
	if shouldSuppressLine(line) {
		return
	}
	b.appendLine(line)
}

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	b.appendLine(line)
}

func (b *contentBuffer) AppendUser(text string) {
	b.finishStreaming()
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{kind: segmentUser, text: text})
	b.collapseState[idx] = false
}

func (b *contentBuffer) Clear() {
	b.segments = nil
	b.segmentHeights = nil
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
	b.collapseState = make(map[int]bool)
}

func (b *contentBuffer) AppendInterrupted() {
	b.finishStreaming()
	b.segments = append(b.segments, contentSegment{kind: segmentInterrupted})
}

func (b *contentBuffer) finishStreaming() {
	if !b.streaming {
		return
	}
	b.finalizeThinkingBlock()
	if strings.TrimSpace(b.streamBuffer) != "" {
		b.appendMarkdownBlock(b.streamBuffer)
	}
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
}

// liveThinkingSegment returns the thinkingBlockData of the last segment if it is
// a thinking block that is still receiving streamed chunks, or nil otherwise.
func (b *contentBuffer) liveThinkingSegment() *thinkingBlockData {
	if len(b.segments) == 0 {
		return nil
	}
	last := &b.segments[len(b.segments)-1]
	if last.kind == segmentThinkingBlock && last.thinkData != nil && last.thinkData.streaming {
		return last.thinkData
	}
	return nil
}

func (b *contentBuffer) appendThinkingChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamingPhase = "thinking"

	if td := b.liveThinkingSegment(); td != nil {
		td.body += text
		runes := []rune(td.body)
		if len(runes) > 80 {
			td.preview = string(runes[:80])
		} else {
			td.preview = td.body
		}
	} else {
		runes := []rune(text)
		preview := string(runes)
		if len(runes) > 80 {
			preview = string(runes[:80])
		}
		idx := len(b.segments)
		b.segments = append(b.segments, contentSegment{
			kind: segmentThinkingBlock,
			thinkData: &thinkingBlockData{
				preview:   preview,
				collapsed: true,
				streaming: true,
				body:      text,
			},
		})
		b.collapseState[idx] = true
	}
}

func (b *contentBuffer) finalizeThinkingBlock() {
	if td := b.liveThinkingSegment(); td != nil {
		td.streaming = false
	}
}

func (b *contentBuffer) appendAssistantChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamBuffer += text
	if b.streamingPhase == "" || b.streamingPhase == "thinking" {
		b.streamingPhase = "answer"
	}

	// Disabled for now because it mangles Glamour rendering
	// @todo investigate and fix, don't delete the commented out code!
	// b.flushCompletedBlocks()
}
