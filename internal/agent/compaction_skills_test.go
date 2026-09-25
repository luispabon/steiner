package agent

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

func mustSkillActivation(t *testing.T, name, body string) string {
	t.Helper()
	text, _ := prompt.RenderSkillActivation(name, body)
	return text
}

func skillEnvelopeCount(messages []Message) int {
	count := 0
	for _, msg := range messages {
		_, blocks, _ := prompt.SplitSkillBlocks(msg.Content)
		count += len(blocks)
	}
	return count
}

func stateFromMessages(messages []Message) RunState {
	return RunState{Conversation: cloneMessages(messages), Lineage: newConversationLineage(messages)}
}

func TestBuildSummarizedCompactionStateNoSkillBlocks(t *testing.T) {
	t.Parallel()

	all := []Message{
		{Role: MessageRoleUser, Content: "turn one"},
		{Role: MessageRoleAssistant, Content: "reply one"},
		{Role: MessageRoleUser, Content: "turn two"},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", cloneMessages(all[2:]))

	if got, want := len(out.Conversation), 2; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	if got, want := out.Conversation[0].Content, "summary"; got != want {
		t.Fatalf("conversation[0].content = %q, want %q", got, want)
	}
	if got, want := out.Conversation[1].Content, "turn two"; got != want {
		t.Fatalf("retained content = %q, want %q", got, want)
	}
	if got := skillEnvelopeCount(out.Conversation); got != 0 {
		t.Fatalf("skill envelopes = %d, want 0", got)
	}
}

func TestBuildSummarizedCompactionStateReinjectsActiveSkill(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "alpha body")
	all := []Message{
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{a1}, "first request")},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: "tail request"},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary text", cloneMessages(all[2:]))

	if got, want := len(out.Conversation), 3; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("conversation[0].role = %q, want %q", got, want)
	}
	if got, want := out.Conversation[0].Content, "summary text"; got != want {
		t.Fatalf("conversation[0].content = %q, want %q", got, want)
	}
	if got, want := out.Conversation[1].Content, a1; got != want {
		t.Fatalf("replayed skill content = %q, want verbatim %q", got, want)
	}
	if got, want := out.Conversation[2].Content, "tail request"; got != want {
		t.Fatalf("retained content = %q, want %q", got, want)
	}
	if got := skillEnvelopeCount(out.Conversation); got != 1 {
		t.Fatalf("skill envelopes = %d, want 1", got)
	}
}

func TestBuildSummarizedCompactionStateLateSwitch(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	b1 := mustSkillActivation(t, "beta", "two")
	offA := prompt.RenderSkillDeactivation("alpha")
	all := []Message{
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{a1}, "start")},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{offA, b1}, "switch")},
		{Role: MessageRoleAssistant, Content: "ok"},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", cloneMessages(all[2:]))

	if got, want := len(out.Conversation), 4; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[1].Content, b1; got != want {
		t.Fatalf("replayed skill content = %q, want latest %q", got, want)
	}
	if got, want := out.Conversation[2].Content, "switch"; got != want {
		t.Fatalf("stripped retained content = %q, want %q", got, want)
	}
	if strings.Contains(out.Conversation[2].Content, "steiner-skill") {
		t.Fatalf("retained message kept skill envelope: %q", out.Conversation[2].Content)
	}
	if got := skillEnvelopeCount(out.Conversation); got != 1 {
		t.Fatalf("skill envelopes = %d, want 1", got)
	}
}

func TestBuildSummarizedCompactionStateDeactivationLeavesNoSkill(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	offA := prompt.RenderSkillDeactivation("alpha")
	all := []Message{
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{a1}, "start")},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{offA}, "stop")},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", cloneMessages(all[2:]))

	if got, want := len(out.Conversation), 2; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[1].Content, "stop"; got != want {
		t.Fatalf("retained content = %q, want %q", got, want)
	}
	if got := skillEnvelopeCount(out.Conversation); got != 0 {
		t.Fatalf("skill envelopes = %d, want 0", got)
	}
}

func TestBuildSummarizedCompactionStateEmergencyReinjectsOnce(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	all := []Message{
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{a1}, "start")},
		{Role: MessageRoleAssistant, Content: "ack"},
		{Role: MessageRoleUser, Content: "tail"},
	}
	normal := buildSummarizedCompactionState(stateFromMessages(all), "normal summary", cloneMessages(all[1:]))
	if got := skillEnvelopeCount(normal.Conversation); got != 1 {
		t.Fatalf("normal skill envelopes = %d, want 1", got)
	}

	emergency := buildSummarizedCompactionState(normal, "emergency summary", cloneMessages(normal.Conversation[1:]))

	if got, want := len(emergency.Conversation), 4; got != want {
		t.Fatalf("emergency conversation len = %d, want %d", got, want)
	}
	if got, want := emergency.Conversation[1].Content, a1; got != want {
		t.Fatalf("emergency replayed skill = %q, want %q", got, want)
	}
	if got := skillEnvelopeCount(emergency.Conversation); got != 1 {
		t.Fatalf("emergency skill envelopes = %d, want exactly 1", got)
	}
}

func TestBuildSummarizedCompactionStateDropsSkillOnlyRetained(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	skillOnly := prompt.PrependSkillBlocks([]string{a1}, "")
	all := []Message{
		{Role: MessageRoleUser, Content: skillOnly},
		{Role: MessageRoleAssistant, Content: "ack"},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", cloneMessages(all[:1]))

	if got, want := len(out.Conversation), 2; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[1].Content, a1; got != want {
		t.Fatalf("replayed skill content = %q, want %q", got, want)
	}
	if got := skillEnvelopeCount(out.Conversation); got != 1 {
		t.Fatalf("skill envelopes = %d, want 1", got)
	}
}

func TestBuildSummarizedCompactionStateKeepsModeNoticeWhenSkillStripped(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	notice := prompt.ModeNotice(config.ExecutionModePlan)
	all := []Message{
		{Role: MessageRoleUser, Content: notice + "\n\n" + prompt.PrependSkillBlocks([]string{a1}, "")},
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", cloneMessages(all))

	if got, want := len(out.Conversation), 3; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[1].Content, a1; got != want {
		t.Fatalf("replayed skill content = %q, want %q", got, want)
	}
	if got, want := out.Conversation[2].Content, notice+"\n\n"; got != want {
		t.Fatalf("retained content = %q, want mode notice prefix %q", got, want)
	}
}

func TestBuildSummarizedCompactionStatePreservesNonContentFields(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	retained := Message{
		Role:    MessageRoleUser,
		Content: prompt.PrependSkillBlocks([]string{a1}, "body"),
		Name:    "user-name",
		Source:  "interactive",
		Images:  []ImageBlock{{ID: "img-1"}},
		Turn:    7,
	}
	all := []Message{
		{Role: MessageRoleUser, Content: a1},
		retained,
	}
	out := buildSummarizedCompactionState(stateFromMessages(all), "summary", []Message{retained})

	var got Message
	found := false
	for _, msg := range out.Conversation {
		if msg.Name == "user-name" {
			got = msg
			found = true
		}
	}
	if !found {
		t.Fatalf("stripped retained message missing from conversation: %#v", out.Conversation)
	}
	if got.Content != "body" {
		t.Fatalf("content = %q, want %q", got.Content, "body")
	}
	if got.Role != MessageRoleUser || got.Source != "interactive" || got.Turn != 7 {
		t.Fatalf("non-content fields not preserved: %+v", got)
	}
	if len(got.Images) != 1 || got.Images[0].ID != "img-1" {
		t.Fatalf("images not preserved: %+v", got.Images)
	}
}

func TestBuildSummarizedCompactionStateScansConversationWhenLineageEmpty(t *testing.T) {
	t.Parallel()

	a1 := mustSkillActivation(t, "alpha", "one")
	state := RunState{Conversation: []Message{
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{a1}, "hi")},
	}}
	out := buildSummarizedCompactionState(state, "summary", nil)

	if got, want := len(out.Conversation), 2; got != want {
		t.Fatalf("conversation len = %d, want %d", got, want)
	}
	if got, want := out.Conversation[1].Content, a1; got != want {
		t.Fatalf("replayed skill content = %q, want %q", got, want)
	}
}
