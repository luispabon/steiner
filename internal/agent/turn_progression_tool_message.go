package agent

import (
	"fmt"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func (p *turnProgressor) buildToolMessage(turn int, call provider.ToolCall, result any, err error, prior []Message) Message {
	return p.buildToolMessageWithEvent(turn, call, result, err, true, prior)
}

func (p *turnProgressor) buildToolMessageWithEvent(turn int, call provider.ToolCall, result any, err error, emitFinished bool, prior []Message) Message {
	var toolContent string
	var preview output.ToolPreview
	normalizedResult := ToolResultEnvelope{}
	if err != nil {
		normalizedResult.DelegationAdmission = admissionFromToolResult(result)
		if normalizedResult.DelegationAdmission == nil {
			normalizedResult.DelegationAdmission = tool.DelegationAdmissionFromError(err)
		}
		if normalizedResult.DelegationAdmission == nil {
			normalizedResult.DelegationAdmission = p.defaultRejectedAdmission(call.Name, err)
		}
		markModelGuidance(normalizedResult.DelegationAdmission, err)
		if projected, ok := projectedToolError(err); ok {
			toolContent = projected
		} else {
			toolContent = formatToolError(err)
		}
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		p.emitToolFinished(turn, call, toolContent, err, preview, normalizedResult.DelegationAdmission, emitFinished)
	} else {
		recordMutationForContextManager(p.request.ContextManager, call.Name, call.Arguments, result)
		normalizedResult = normalizeToolResult(result)
		if normalizedResult.DelegationAdmission == nil && p.isDelegationCall(call.Name) {
			normalizedResult.DelegationAdmission = &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted}
		}
		if normalizedResult.Projected {
			projected, ok := projectedToolResult(resultValue(result))
			if ok {
				toolContent = projected
			} else {
				toolContent = normalizedResult.Content
			}
		} else {
			toolContent = shapeFreshToolResultForContextManager(p.request.ContextManager, turn, call.Name, cloneInput(call.Arguments), normalizedResult.Content, prior)
		}
		preview = output.BuildToolPreview(call.Name, cloneInput(call.Arguments), toolContent)
		p.emitToolFinished(turn, call, toolContent, nil, preview, normalizedResult.DelegationAdmission, emitFinished)
		if emitFinished {
			if event, ok := bashArtifactTruncatedEvent(turn, call, result); ok {
				emitEvent(p.request.Events, event)
			}
		}
	}
	toolMessage := Message{
		Role:       MessageRoleTool,
		Content:    toolContent,
		ToolCallID: call.ID,
		Name:       call.Name,
		Turn:       turn,
	}
	toolMessage.DelegationAdmission = normalizedResult.DelegationAdmission.Clone()
	if err == nil {
		toolMessage.Retention = cloneMessageRetention(normalizedResult.Retention)
		if normalizedResult.Image != nil {
			image := *normalizedResult.Image
			if call.Name == "read" && p.request.ImageStore != nil && image.FilePath != "" {
				ref := p.request.ImageStore.Register(image.FilePath, image.MediaType, image.Width, image.Height, image.SizeBytes)
				image.ID = ref.ID
				image.FilePath = ref.FilePath
			}
			toolMessage.Images = []ImageBlock{image}
		}
	}
	return toolMessage
}

func resultValue(result any) any {
	if execution, ok := result.(tool.ExecutionResult); ok {
		return execution.Value
	}
	return result
}

// bashArtifactTruncatedEvent builds the warning for a bash result whose
// artifact size cap dropped bytes. It reports false for any other result.
func bashArtifactTruncatedEvent(turn int, call provider.ToolCall, result any) (output.Event, bool) {
	var res *builtin.BashResult
	switch v := resultValue(result).(type) {
	case *builtin.BashResult:
		res = v
	case builtin.BashResult:
		res = &v
	}
	if res == nil || !res.ArtifactLimitHit {
		return output.Event{}, false
	}
	message := "warning: bash artifact truncated; saved output is partial"
	if res.FilePath != "" {
		message = fmt.Sprintf("warning: bash artifact truncated: partial output saved to %s", res.FilePath)
	}
	return output.NewBashArtifactTruncatedEvent(turn, call.ID, res.FilePath, message), true
}

func workflowHandoffTransitionFromResult(result any) (*tool.WorkflowHandoffTransition, bool) {
	switch v := result.(type) {
	case tool.WorkflowHandoffAccepted:
		return cloneWorkflowHandoffTransition(&v.Transition), true
	case *tool.WorkflowHandoffAccepted:
		if v == nil {
			return nil, false
		}
		return cloneWorkflowHandoffTransition(&v.Transition), true
	default:
		return nil, false
	}
}
