package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// renderErrorLogger is a package-level hook for logging render errors.
// The TUI model sets this to emit events through the event bridge.
var renderErrorLogger func(error)

func emitRenderError(err error) {
	if renderErrorLogger != nil {
		renderErrorLogger(err)
	}
}

type contentSegmentKind int

const (
	segmentPlain contentSegmentKind = iota
	segmentAssistantProse
	segmentAssistantMarkdown
	segmentApproval
	segmentTool
	segmentThinking
	segmentUser
	segmentUserMarkdown
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
	bodyKind                 string // "bash", "diff", "file", "glob", "grep", "ls", "plain"
	body                     string // raw result text
	callID                   string // for matching started→finished
	collapsed                bool   // default true
	hasError                 bool   // set when ToolCallFinished carries an error
	rawArgs                  map[string]any
	writeTargetExistedBefore *bool
	preview                  output.ToolPreview
	displayPreview           *output.PreviewDocument
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
	lastRenderErr     error        // captures the last render error for logging
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeThinkingChunk:
		b.appendThinkingChunkEvent(event)
	case output.EventTypeAssistantChunk:
		b.appendAssistantChunkEvent(event)
	case output.EventTypeApprovalRequested:
		b.appendApprovalRequestedEvent(event)
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
		b.appendApprovalDecisionEvent(event)
	case output.EventTypeDelegationStarted, output.EventTypeDelegationComplete, output.EventTypeDelegationFailed:
		b.appendDelegationEvent(event)
	case output.EventTypeToolCallStarted:
		b.appendToolCallStartedEvent(event)
	case output.EventTypeToolCallFinished:
		b.appendToolCallFinishedEvent(event)
	case output.EventTypeDisplayFile:
		b.appendDisplayFileEvent(event)
	case output.EventTypeStopReason:
		b.appendStopReasonEvent(event)
	case output.EventTypeAssistantMessage:
		b.appendAssistantMessageEvent(event)
	case output.EventTypeContextReport:
		b.appendContextReportEvent(event)
	case output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeContextDiagnostics:
		b.appendModelCallDiagnosticsEvent(event)
	case output.EventTypeUserInput:
		b.appendUserInputEvent(event)
	case output.EventTypeRunStarted, output.EventTypeRunFinished,
		output.EventTypeTurnStarted, output.EventTypeTurnFinished,
		output.EventTypeAPIRequest, output.EventTypeAPIResponse:
		return
	default:
		b.finishStreaming()
		line := strings.TrimSpace(output.FormatEvent(event))
		if shouldSuppressLine(line) {
			return
		}
		b.appendLine(line)
	}
}

func (b *contentBuffer) appendThinkingChunkEvent(event output.Event) {
	if b.inCompaction {
		return
	}
	if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
		b.appendThinkingChunk(payload.Content)
	}
}

func (b *contentBuffer) appendAssistantChunkEvent(event output.Event) {
	if b.inCompaction {
		return
	}
	if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
		b.finalizeThinkingBlock()
		b.appendAssistantChunk(payload.Content)
	}
}

func (b *contentBuffer) appendApprovalRequestedEvent(event output.Event) {
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
}

func (b *contentBuffer) appendApprovalDecisionEvent(event output.Event) {
	b.finishStreaming()
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
	b.appendStyled(formatApprovalEvent(event), segmentApproval)
}

func (b *contentBuffer) appendDelegationEvent(event output.Event) {
	b.finishStreaming()
	b.appendStyled(formatDelegationEvent(event), segmentPlain)
}

func (b *contentBuffer) appendToolCallStartedEvent(event output.Event) {
	b.finishStreaming()
	b.streamingPhase = "tool"
	if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
		if strings.EqualFold(payload.Tool, "display_file") {
			return
		}
		rawArgs := cloneToolArguments(payload.Arguments)
		tc := &toolCallSegment{
			tool:                     strings.ToLower(payload.Tool),
			args:                     summarizeArgs(payload.Tool, payload.Arguments),
			callID:                   payload.CallID,
			collapsed:                true,
			rawArgs:                  rawArgs,
			writeTargetExistedBefore: payload.WriteTargetExistedBefore,
		}
		tc.preview = output.BuildToolPreview(tc.tool, rawArgs, "", tc.writeTargetExistedBefore)
		if tc.preview.Kind != output.ToolPreviewKindPlain {
			tc.bodyKind = previewBodyKind(tc.tool, tc.preview)
		}
		b.segments = append(b.segments, contentSegment{
			kind:     segmentToolCall,
			toolData: tc,
		})
	} else {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
	}
}

func (b *contentBuffer) appendToolCallFinishedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ToolCallFinishedEvent); ok {
		if strings.EqualFold(payload.Tool, "display_file") {
			return
		}
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
					if payload.Preview.Kind != "" && payload.Preview.Kind != output.ToolPreviewKindPlain {
						td.preview = payload.Preview
					} else {
						td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result, td.writeTargetExistedBefore)
					}
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
}

func (b *contentBuffer) appendDisplayFileEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.DisplayFilePayload); ok {
		preview := payload.Preview
		idx := len(b.segments)
		b.segments = append(b.segments, contentSegment{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:           "display_file",
				args:           payload.Path,
				bodyKind:       "file",
				collapsed:      false,
				displayPreview: &preview,
			},
		})
		b.collapseState[idx] = false
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

func (b *contentBuffer) appendStopReasonEvent(event output.Event) {
	b.finishStreaming()
	b.appendLine(formatStopReasonEvent(event))
}

func (b *contentBuffer) appendAssistantMessageEvent(event output.Event) {
	if b.inCompaction {
		return
	}
	if payload, ok := event.Payload.(output.AssistantMessageEvent); ok && payload.Content != "" && !b.hadChunks {
		b.finishStreaming()
		b.appendMarkdownBlock(payload.Content)
	}
	b.hadChunks = false
}

func (b *contentBuffer) appendContextReportEvent(event output.Event) {
	if payload, ok := event.Payload.(output.ContextReportEvent); ok && strings.TrimSpace(payload.Content) != "" {
		b.finishStreaming()
		b.appendMarkdownBlock(payload.Content)
	}
}

func (b *contentBuffer) appendModelCallDiagnosticsEvent(event output.Event) {
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
}

func (b *contentBuffer) appendUserInputEvent(event output.Event) {
	if payload, ok := event.Payload.(output.UserInputEvent); ok && strings.TrimSpace(payload.Content) != "" {
		kind := segmentUser
		if isMarkdownLikeUserContent(payload.Content) {
			kind = segmentUserMarkdown
		}
		b.segments = append(b.segments, contentSegment{kind: kind, text: payload.Content})
		if len(b.segments)-1 >= 0 {
			b.collapseState[len(b.segments)-1] = false
		}
	}
}

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	b.appendLine(line)
}

// AppendUser appends a submitted user prompt. Markdown-like content is
// rendered with glamour; plain text uses the simple block style.
func (b *contentBuffer) AppendUser(text string) {
	b.finishStreaming()
	idx := len(b.segments)
	kind := segmentUser
	if isMarkdownLikeUserContent(text) {
		kind = segmentUserMarkdown
	}
	b.segments = append(b.segments, contentSegment{kind: kind, text: text})
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
