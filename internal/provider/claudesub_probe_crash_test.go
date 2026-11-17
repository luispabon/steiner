//go:build claudesub_probe

package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// E7 prompts, kill point, budgets and note. E7 measures the join a crashed
// claude_subscription turn needs: a persisted parent answers a first turn, a
// second turn is killed with SIGKILL while it streams, and a forked process
// resumed at the first turn's assistant uuid receives the second prompt plus the
// quoted partial and a continuation note.
const (
	claudeSubProbePromptE7T1 = "PROBE-T1E7 Reply with exactly: alpha. Do not use tools."
	claudeSubProbePromptE7T2 = "PROBE-T2E7 Write a numbered list from 1 to 200, one item per line, in the form item-<n>. Use at least 200 lines and no tools."

	// claudeSubProbeCrashLines is how many newlines the streamed second turn must
	// carry before the probe kills the CLI, so the quoted partial is a real prefix
	// of the answer rather than an empty or a complete one.
	claudeSubProbeCrashLines = 40

	// claudeSubProbeCrashReply is the budget of the first turn's one-word answer,
	// claudeSubProbeCrashStream the budget of the streamed list reaching its kill
	// point, claudeSubProbeCrashExit the budget of the killed process's exit being
	// observed, and claudeSubProbeCrashContinuation the budget of the resumed
	// continuation's result, which regenerates the rest of the list.
	claudeSubProbeCrashReply        = 15 * time.Second
	claudeSubProbeCrashStream       = 15 * time.Second
	claudeSubProbeCrashExit         = 5 * time.Second
	claudeSubProbeCrashContinuation = 30 * time.Second

	// claudeSubProbeCrashTrials is E7's trial count in a full run. One, not two:
	// each trial starts two persisted processes, so it spends one init gate, its
	// resumed process's gate, four waits and two per-process teardown budgets, and
	// E1 to E6 with their own teardowns already claim most of the probe's 1500s
	// ceiling. An E7-only selection derives claudeSubProbeE7OnlyTrials instead (see
	// claudeSubProbeE7TrialCount).
	claudeSubProbeCrashTrials = 1

	// claudeSubProbeE7OnlyTrials is E7's derived trial count when E7 is the only
	// selected experiment.
	claudeSubProbeE7OnlyTrials = 3

	// claudeSubProbeListEnd is the last item the second prompt asks for: a
	// continuation that stops short of it is a truncated answer.
	claudeSubProbeListEnd = 200

	// claudeSubProbeCrashSampleTail and claudeSubProbeCrashSampleHead bound the
	// partial and continuation excerpts the findings quote.
	claudeSubProbeCrashSampleTail = 80
	claudeSubProbeCrashSampleHead = 200

	// claudeSubProbeContinuationNoteBefore and claudeSubProbeContinuationNoteAfter
	// bracket the quoted partial in E7's continuation note.
	claudeSubProbeContinuationNoteBefore = "Your previous reply to the message above was cut off by an error before it finished. This is the text it had produced so far, quoted exactly between the markers:\n<<<PARTIAL\n"
	claudeSubProbeContinuationNoteAfter  = "\nPARTIAL>>>\nContinue the reply from exactly where that text ends. The quoted text ends exactly at the end marker, so if the next character of your reply should be a line break or a space, start with it. Do not repeat any of it and do not mention the interruption."
)

// claudeSubProbeContinuationNote renders the note that follows the original
// prompt in E7's resumed line, quoting partial exactly.
func claudeSubProbeContinuationNote(partial string) string {
	return claudeSubProbeContinuationNoteBefore + partial + claudeSubProbeContinuationNoteAfter
}

// claudeSubProbeContinuationBlocks is E7's one resumed user line: the original
// prompt text, then the note quoting the partial. Both are text blocks of one
// user message, which is what the production writer sends as a single line.
func claudeSubProbeContinuationBlocks(prompt, partial string) []map[string]any {
	return claudeSubUserBlocks([]Message{
		{Role: MessageRoleUser, Content: prompt},
		{Role: MessageRoleUser, Content: claudeSubProbeContinuationNote(partial)},
	})
}

// claudeSubProbeE7Steps is E7's step plan: every trial starts a parent process and
// its resumed fork, so each trial is charged both init gates, its four waits and
// two teardowns. The experiment's own gate field stays zero because no process
// gates only once for the whole experiment; the per-trial gates are extras. The
// experiment's bound is this plan, so the two cannot drift, and the plan carries the
// trial count the body runs.
func claudeSubProbeE7Steps(trials int) *claudeSubProbeSteps {
	steps := &claudeSubProbeSteps{trials: trials, processes: 2 * trials}
	for trial := 0; trial < trials; trial++ {
		steps.waits = append(steps.waits,
			claudeSubProbeCrashReply, claudeSubProbeCrashStream, claudeSubProbeCrashExit, claudeSubProbeCrashContinuation)
		steps.extra = append(steps.extra, claudeSubProbeInitWorstCase, claudeSubProbeInitWorstCase)
	}
	return steps
}

// claudeSubProbeE7 runs every E7 trial and reports the per-trial verdicts with
// their aggregate counts.
func claudeSubProbeE7(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps) error {
	trials := steps.trials
	if trials < claudeSubProbeE7MinTrials {
		return fmt.Errorf("probe E7 step plan declares %d trials", trials)
	}
	verdicts := make([]string, 0, trials)
	for trial := 1; trial <= trials; trial++ {
		verdict, err := claudeSubProbeE7Trial(ctx, cfg, exp, steps, trial)
		if err != nil {
			return fmt.Errorf("E7 trial %d: %w", trial, err)
		}
		verdicts = append(verdicts, verdict)
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E7 verdicts over %d trials: %s", trials, claudeSubProbeVerdictSummary(verdicts)))
	return nil
}

// claudeSubProbeVerdictSummary counts an experiment's trial verdicts and renders
// them in name order, so the aggregate names every verdict the trials produced.
func claudeSubProbeVerdictSummary(verdicts []string) string {
	counts := map[string]int{}
	for _, verdict := range verdicts {
		counts[verdict]++
	}
	names := make([]string, 0, len(counts))
	for verdict := range counts {
		names = append(names, verdict)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[name]))
	}
	return strings.Join(parts, " ")
}

// claudeSubProbeE7Trial runs one E7 trial: a persisted turn to completion, a
// streamed turn killed at its kill point, production teardown, then a forked
// resume at the first turn's assistant uuid carrying the continuation line. It
// returns the join verdict of that trial.
func claudeSubProbeE7Trial(ctx context.Context, cfg claudeSubProbeConfig, exp *claudeSubProbeExperimentReport, steps *claudeSubProbeSteps, trial int) (string, error) {
	configDir := claudeSubProbeConfigDir()
	label := fmt.Sprintf("E7 trial %d", trial)
	// Every wait is consumed in the declared order whether or not the branch that
	// spends it runs, so a later trial's budget never shifts.
	replyBudget := steps.next()
	spec := claudeSubProbeParentSpec(cfg.Model)
	parent, err := claudeSubProbeStart(ctx, cfg, exp, label+" parent", fmt.Sprintf("probe-e7-%d-parent", trial), spec, claudeSubProbePromptE7T1)
	if err != nil {
		return "", err
	}
	defer claudeSubProbeFinish(parent)

	if !parent.gateConsumed {
		if err := parent.runTurn(ctx, claudeSubProbePromptE7T1, replyBudget); err != nil {
			return "", err
		}
	}
	sessionID := parent.sessionID()
	resumeUUID := parent.lastAssistantUUIDForPrompt("PROBE-T1E7")
	if sessionID == "" || resumeUUID == "" {
		return "", errors.New("probe E7 needs the PROBE-T1E7 assistant uuid and the CLI session id")
	}

	// Kill the CLI mid-reply, as a crash would, once the streamed list is long
	// enough to leave a real partial behind.
	turn, err := parent.beginTurn(claudeSubProbePromptE7T2)
	if err != nil {
		return "", err
	}
	if _, err := parent.pumpUntil(ctx, steps.next(), "the streamed list reaching its kill point", claudeSubProbeLinesAtLeast(parent, turn, claudeSubProbeCrashLines)); err != nil {
		return "", err
	}
	// A turn that already has its result was never crashed mid-reply, so the quoted
	// text would be the whole answer and the trial would measure nothing.
	if result := parent.turnResult(turn); result != nil {
		return "", fmt.Errorf("the turn finished before kill (subtype %q terminal_reason %q), so there was no partial to quote", result.Subtype, result.TerminalReason)
	}
	if err := parent.killChild(); err != nil {
		return "", err
	}
	killedAt := parent.at()
	if err := parent.awaitExit(ctx, steps.next()); err != nil {
		return "", err
	}
	exitedAt := parent.at()
	claudeSubProbeFinish(parent)
	continuationBudget := steps.next()
	// The kill races the turn's own result: if one arrived while the signal was in
	// flight the turn was never crashed mid-reply, so the partial is the whole
	// answer and the trial would measure nothing.
	if result := parent.turnResult(turn); result != nil {
		return "", fmt.Errorf("the turn finished before kill: its result arrived before the process exited (subtype %q terminal_reason %q)", result.Subtype, result.TerminalReason)
	}
	if exitedAt < killedAt {
		return "", fmt.Errorf("the killed CLI's exit was observed at %dms, before the kill at %dms", exitedAt, killedAt)
	}
	// Read the partial only after the process exited and its events were drained, so
	// the quoted text is everything the CLI streamed before the kill.
	partial, partialTruncated := parent.turnText(turn)
	if partialTruncated {
		return "", fmt.Errorf("the killed turn passed the %d rune answer cap, so the note cannot quote it exactly", claudeSubProbeAnswerMaxRunes)
	}
	exitFinding, exitErr := claudeSubProbeCrashExitFinding(parent.rep.Exit)
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s killed with SIGKILL at %dms, exit observed at %dms, after %d streamed newlines (%d runes): %s",
		label, killedAt, exitedAt, strings.Count(partial, "\n"), utf8.RuneCountInString(partial), exitFinding))
	if exitErr != nil {
		return "", exitErr
	}
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript(label+" after the killed turn and exit", configDir, cfg.WorkDir, sessionID))

	// One resumed fork at the first turn's assistant uuid. Its gating turn is the
	// continuation line itself, so no extra turn enters the forked transcript.
	line := claudeSubProbeContinuationBlocks(claudeSubProbePromptE7T2, partial)
	resumeSpec := spec
	resumeSpec.Resume = claudeSubResume{SessionID: sessionID, AtUUID: resumeUUID, Fork: true}
	resumed, err := claudeSubProbeStartGated(ctx, cfg, exp, label+" resume fork", fmt.Sprintf("probe-e7-%d-fork", trial), resumeSpec, claudeSubProbePromptE7T2, line, continuationBudget)
	if err != nil {
		return "", err
	}
	defer claudeSubProbeFinish(resumed)
	if !resumed.gateConsumed {
		if _, err := resumed.beginBlockTurn(claudeSubProbePromptE7T2, line); err != nil {
			return "", err
		}
		if err := resumed.awaitResult(ctx, continuationBudget); err != nil {
			return "", err
		}
	}
	continuationTurn := resumed.turn(0)
	if continuationTurn == nil {
		return "", errors.New("the resumed process recorded no continuation turn")
	}
	result := continuationTurn.Result
	if result == nil {
		return "", errors.New("the resumed turn recorded no result")
	}
	continuation, continuationTruncated := resumed.turnText(continuationTurn)
	forkSessionID := resumed.sessionID()
	claudeSubProbeFinish(resumed)
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript(label+" after the forked process exit", configDir, cfg.WorkDir, forkSessionID))

	seam := claudeSubProbeAnalyseSeam(partial, continuation)
	seam.Errored = result.IsError
	mentioned := claudeSubProbeMentionsInterruption(continuation)
	verdict := claudeSubProbeCrashVerdict(seam, continuation, mentioned)
	usage := claudeSubProbeUsageString(result.Usage)
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s partial tail (last %d runes): %q",
		label, claudeSubProbeCrashSampleTail, claudeSubProbeTailSample(partial, claudeSubProbeCrashSampleTail)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s continuation head (first %d runes, truncated=%v): %q",
		label, claudeSubProbeCrashSampleHead, continuationTruncated, claudeSubProbeHeadSample(continuation, claudeSubProbeCrashSampleHead)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s seam: partial last complete item=%d, partial max item=%d, continuation first item=%d, continuation items=%d, duplicates=%v, missing=%v, missing line break=%v, cut digits=%q, fragment completes=%v, max item=%d, result error=%v",
		label, seam.PartialLastComplete, seam.PartialMax, seam.ContinuationFirst, seam.ContinuationItems, seam.Duplicate, seam.Missing, seam.MissingLineBreak, seam.CutDigits, seam.FragmentCompletes, seam.MaxItem, seam.Errored))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s mentioned the interruption: %v", label, mentioned))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s continuation usage=%s assistant usage=%s",
		label, usage, claudeSubProbeUsageString(continuationTurn.AssistantUsage)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s sessions: original %s, fork %s, fork differs=%v",
		label, sessionID, forkSessionID, forkSessionID != sessionID))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s verdict: %s", label, verdict))
	if forkSessionID == "" || forkSessionID == sessionID {
		return "", fmt.Errorf("the resumed process reported session %q, so the fork created no new session", forkSessionID)
	}
	if result.IsError {
		return "", fmt.Errorf("the resumed turn ended with an error result (subtype %q terminal_reason %q), so the trial measured nothing", result.Subtype, result.TerminalReason)
	}
	return verdict, nil
}

// turnText returns the turn's recorded assistant text so far and whether it hit
// the answer cap.
func (p *claudeSubProbeProcess) turnText(turn *claudeSubProbeTurn) (string, bool) {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return turn.AnswerText, turn.AnswerTruncated
}

// turnResult returns the turn's terminal result, or nil while the turn is still
// open.
func (p *claudeSubProbeProcess) turnResult(turn *claudeSubProbeTurn) *claudeSubProbeResult {
	p.rec.mu.Lock()
	defer p.rec.mu.Unlock()
	return turn.Result
}

// at reports milliseconds since this process's recorder started, which is the
// clock the probe's findings use for the kill and the observed exit.
func (p *claudeSubProbeProcess) at() int64 {
	return p.rec.at()
}

// claudeSubProbeLinesAtLeast matches once the turn's recorded text carries want
// newlines, which is E7's kill point.
func claudeSubProbeLinesAtLeast(p *claudeSubProbeProcess, turn *claudeSubProbeTurn, want int) func(claudeSubProbeEntry) bool {
	return func(claudeSubProbeEntry) bool {
		text, _ := p.turnText(turn)
		return strings.Count(text, "\n") >= want
	}
}

// beginBlockTurn records a turn whose prompt is prompt and writes blocks as its
// user line. It refuses until the process's init event has been validated.
func (p *claudeSubProbeProcess) beginBlockTurn(prompt string, blocks []map[string]any) (*claudeSubProbeTurn, error) {
	if err := p.rec.initGate(); err != nil {
		return nil, fmt.Errorf("send probe prompt: %w", err)
	}
	turn := p.rec.beginTurn(prompt)
	if err := p.sendRawBlocks(blocks); err != nil {
		return nil, err
	}
	return turn, nil
}

// killChild SIGKILLs the CLI process this probe spawned, through the connection
// the spawn seam returned, and marks the report so the crash-derived exit and
// teardown results are read as the measurement rather than as failures.
func (p *claudeSubProbeProcess) killChild() error {
	if p.tee == nil {
		return errors.New("probe process has no spawned connection to kill")
	}
	if err := claudeSubProbeKillProcess(p.tee.claudeSubConn); err != nil {
		return fmt.Errorf("kill probe CLI process: %w", err)
	}
	p.rep.ExpectedCrash = true
	return nil
}

// awaitExit waits for the killed CLI's event stream to end, which the probe's
// tee reports as the CLI exiting.
func (p *claudeSubProbeProcess) awaitExit(ctx context.Context, timeout time.Duration) error {
	_, err := p.pumpUntil(ctx, timeout, "the killed CLI to exit", nil)
	if errors.Is(err, errClaudeSubProbeCLIExited) {
		return nil
	}
	if err == nil {
		return errors.New("probe waited for the killed CLI to exit but the wait reported an event")
	}
	return err
}

// claudeSubProbeKilledBySignal reports the exit E7's crash path produces: the
// reaped child died of SIGKILL, so its wait error names the signal and its status
// is -1.
func claudeSubProbeKilledBySignal(exit *claudeSubProbeExit) bool {
	return exit != nil && exit.StatusKnown && exit.Status == -1 && strings.Contains(exit.WaitErr, "signal: killed")
}

// claudeSubProbeCrashExitFinding states the killed process's exit evidence and
// fails unless the CLI died of the SIGKILL the probe sent, which is what the trial
// needs to measure.
func claudeSubProbeCrashExitFinding(exit *claudeSubProbeExit) (string, error) {
	if exit == nil {
		return "no exit evidence", errors.New("the killed CLI recorded no exit evidence")
	}
	status := "unknown"
	if exit.StatusKnown {
		status = strconv.Itoa(exit.Status)
	}
	message := fmt.Sprintf("exit status=%s wait_error=%q exited_before_teardown=%v proc_err=%q conn_err=%q",
		status, exit.WaitErr, exit.ExitedBeforeTeardown, exit.ProcErr, exit.ConnErr)
	if !claudeSubProbeKilledBySignal(exit) {
		return message, errors.New("the CLI exited before kill: its exit is not the SIGKILL the probe sent")
	}
	return message, nil
}

// claudeSubProbeSeam is how a continuation joins the text it continues, judged on
// the joined text: the item numbers the partial and the continuation each wrote,
// what the join duplicates across the seam, what the merged numbering skips,
// whether the continuation finished a number the partial cut, and whether the join
// lost the line break at the seam. An item is complete when its number sits on a
// line the partial finished. Errored records that the continuation's own turn ended
// with an error result, which the calling trial sets.
type claudeSubProbeSeam struct {
	PartialLastComplete int
	PartialMax          int
	ContinuationFirst   int
	ContinuationItems   int
	Duplicate           []int
	Missing             []int
	MissingLineBreak    bool
	CutDigits           string
	FragmentCompletes   bool
	MaxItem             int
	Errored             bool
}

// claudeSubProbeItemPattern matches the list prompt's item numbers.
var claudeSubProbeItemPattern = regexp.MustCompile(`item-([0-9]+)`)

// claudeSubProbeAnalyseSeam judges the seam on the joined text partial +
// continuation, so every place the crash can cut the reply is read the same way:
// the partial's digits and the continuation's digits add up to the number the join
// carries, whether the cut fell inside the word, before the digits, inside them,
// after them, after the line break or inside a numbered prefix. A token that ends
// inside the partial is the partial's item, a token that starts past the partial is
// the continuation's, and a token bridging the two is a number the continuation
// finished. Judgements that belong to the seam rather than to the whole reply stay
// scoped to it: a duplicate is a continuation number the partial already wrote, and
// the lost line break is judged on the last partial item and the continuation's
// first, not on any two items that share a line. The missing scan stops at
// claudeSubProbeListEnd, so a runaway number cannot make it long.
func claudeSubProbeAnalyseSeam(partial, continuation string) claudeSubProbeSeam {
	seam := claudeSubProbeSeam{}
	seam.CutDigits = claudeSubProbeCutItemDigits(partial)
	if complete := claudeSubProbeItemNumbers(claudeSubProbeCompleteLines(partial)); len(complete) > 0 {
		seam.PartialLastComplete = complete[len(complete)-1]
	}
	joined := partial + continuation
	boundary := len(partial)
	matches := claudeSubProbeItemMatches(joined)
	counts := make(map[int]int, len(matches))
	partialNumbers := make(map[int]struct{}, len(matches))
	duplicated := make(map[int]struct{}, len(matches))
	partialLine, scanned, line := -1, 0, 0
	for _, match := range matches {
		line += strings.Count(joined[scanned:match.Start], "\n")
		scanned = match.End
		counts[match.Number]++
		if match.Number > seam.MaxItem {
			seam.MaxItem = match.Number
		}
		if match.End <= boundary {
			partialNumbers[match.Number] = struct{}{}
			seam.PartialMax = match.Number
			partialLine = line
			continue
		}
		if match.Start < boundary {
			seam.FragmentCompletes = true
		}
		seam.ContinuationItems++
		if seam.ContinuationItems == 1 {
			seam.ContinuationFirst = match.Number
			// The continuation picked up on the partial's unfinished line, so the
			// join dropped the break between the two items.
			seam.MissingLineBreak = partialLine == line
		}
		if _, ok := partialNumbers[match.Number]; ok {
			if _, seen := duplicated[match.Number]; !seen {
				duplicated[match.Number] = struct{}{}
				seam.Duplicate = append(seam.Duplicate, match.Number)
			}
		}
	}
	limit := seam.MaxItem
	if limit > claudeSubProbeListEnd {
		limit = claudeSubProbeListEnd
	}
	for number := 1; number <= limit; number++ {
		if counts[number] == 0 {
			seam.Missing = append(seam.Missing, number)
		}
	}
	return seam
}

// claudeSubProbeItemMatch is one item token of text, with the byte range it
// occupies.
type claudeSubProbeItemMatch struct {
	Number int
	Start  int
	End    int
}

// claudeSubProbeItemMatches returns the item tokens of text, in order, with the
// byte ranges they occupy.
func claudeSubProbeItemMatches(text string) []claudeSubProbeItemMatch {
	indexes := claudeSubProbeItemPattern.FindAllStringSubmatchIndex(text, -1)
	if len(indexes) == 0 {
		return nil
	}
	out := make([]claudeSubProbeItemMatch, 0, len(indexes))
	for _, index := range indexes {
		number, err := strconv.Atoi(text[index[2]:index[3]])
		if err != nil {
			continue
		}
		out = append(out, claudeSubProbeItemMatch{Number: number, Start: index[0], End: index[1]})
	}
	return out
}

// claudeSubProbeItemNumbers returns the item numbers of text, in order.
func claudeSubProbeItemNumbers(text string) []int {
	matches := claudeSubProbeItemMatches(text)
	if len(matches) == 0 {
		return nil
	}
	out := make([]int, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Number)
	}
	return out
}

// claudeSubProbeCompleteLines returns text up to and including its last newline,
// so only lines the partial finished are considered.
func claudeSubProbeCompleteLines(text string) string {
	index := strings.LastIndex(text, "\n")
	if index < 0 {
		return ""
	}
	return text[:index+1]
}

// claudeSubProbeCutItemDigits returns the digits the partial's final, unterminated
// line wrote after its item prefix, and nothing when that line is not a cut item
// token. The digits are empty when the cut fell inside "item-" itself.
func claudeSubProbeCutItemDigits(partial string) string {
	if partial == "" || strings.HasSuffix(partial, "\n") {
		return ""
	}
	line := partial[strings.LastIndex(partial, "\n")+1:]
	index := strings.LastIndex(line, "item-")
	if index < 0 {
		return ""
	}
	digits := line[index+len("item-"):]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return digits
}

// claudeSubProbeMentionsInterruption reports whether text talks about the
// interruption instead of only continuing.
func claudeSubProbeMentionsInterruption(text string) bool {
	lowered := strings.ToLower(text)
	for _, word := range []string{"interrupt", "cut off", "continu"} {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	return false
}

// claudeSubProbeCrashVerdict names how a continuation joined the partial. The
// precedence is total and deliberate: a failed measurement comes before the
// numbering, a restart is a restart even when it also duplicates, a numbering gap
// comes before a lost line break, a list that stops short comes before commentary,
// and commentary is reported only when the numbering is complete and clean.
func claudeSubProbeCrashVerdict(seam claudeSubProbeSeam, continuation string, mentioned bool) string {
	switch {
	case seam.Errored || strings.TrimSpace(continuation) == "":
		return "empty"
	case seam.ContinuationItems == 0:
		return "no-items"
	case seam.ContinuationFirst == 1:
		return "restart"
	case len(seam.Duplicate) > 0:
		return "duplicate"
	case len(seam.Missing) > 0:
		return "gap"
	case seam.MissingLineBreak:
		return "line-break-missing"
	case seam.MaxItem < claudeSubProbeListEnd:
		return "short"
	case mentioned:
		return "commentary"
	default:
		return "clean"
	}
}

// claudeSubProbeTailSample returns the last want runes of text.
func claudeSubProbeTailSample(text string, want int) string {
	runes := []rune(text)
	if len(runes) <= want {
		return text
	}
	return string(runes[len(runes)-want:])
}

// claudeSubProbeHeadSample returns the first want runes of text.
func claudeSubProbeHeadSample(text string, want int) string {
	runes := []rune(text)
	if len(runes) <= want {
		return text
	}
	return string(runes[:want])
}
