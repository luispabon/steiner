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

	// claudeSubProbeCrashTrials is E7's trial count in a full run. Two, not three:
	// each trial starts two persisted processes, so it spends one init gate, its
	// resumed process's gate and four waits, and E1 to E6 already claim 1210s of the
	// probe's 1500s ceiling. An E7-only run has the ceiling to itself and derives
	// one more trial (see claudeSubProbeE7TrialCount).
	claudeSubProbeCrashTrials = 2

	// claudeSubProbeCrashSampleTail and claudeSubProbeCrashSampleHead bound the
	// partial and continuation excerpts the findings quote.
	claudeSubProbeCrashSampleTail = 80
	claudeSubProbeCrashSampleHead = 200

	// claudeSubProbeContinuationNoteBefore and claudeSubProbeContinuationNoteAfter
	// bracket the quoted partial in E7's continuation note.
	claudeSubProbeContinuationNoteBefore = "Your previous reply to the message above was cut off by a connection failure before it finished. This is the text it had produced so far, quoted exactly between the markers:\n<<<PARTIAL\n"
	claudeSubProbeContinuationNoteAfter  = "\nPARTIAL>>>\nContinue the reply from exactly where that text ends. Do not repeat any of it and do not mention the interruption."
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

// claudeSubProbeE7Steps is E7's step plan: every trial's four waits and its
// resumed process's init gate, repeated once per trial. The experiment's bound is
// this plan, so the two cannot drift, and the plan carries the trial count the body
// runs.
func claudeSubProbeE7Steps(trials int) *claudeSubProbeSteps {
	steps := &claudeSubProbeSteps{gate: claudeSubProbeInitWorstCase, trials: trials}
	for trial := 0; trial < trials; trial++ {
		steps.waits = append(steps.waits,
			claudeSubProbeCrashReply, claudeSubProbeCrashStream, claudeSubProbeCrashExit, claudeSubProbeCrashContinuation)
		steps.extra = append(steps.extra, claudeSubProbeInitWorstCase)
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
	counts := map[string]int{}
	for trial := 1; trial <= trials; trial++ {
		verdict, err := claudeSubProbeE7Trial(ctx, cfg, exp, steps, trial)
		if err != nil {
			return fmt.Errorf("E7 trial %d: %w", trial, err)
		}
		counts[verdict]++
	}

	verdicts := make([]string, 0, len(counts))
	for verdict := range counts {
		verdicts = append(verdicts, verdict)
	}
	sort.Strings(verdicts)
	parts := make([]string, 0, len(verdicts))
	for _, verdict := range verdicts {
		parts = append(parts, fmt.Sprintf("%s=%d", verdict, counts[verdict]))
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("E7 verdicts over %d trials: %s", trials, strings.Join(parts, " ")))
	return nil
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
	partial, partialTruncated := parent.turnText(turn)
	if partialTruncated {
		return "", fmt.Errorf("the killed turn passed the %d rune answer cap, so the note cannot quote it exactly", claudeSubProbeAnswerMaxRunes)
	}
	if err := parent.killChild(); err != nil {
		return "", err
	}
	if err := parent.awaitExit(ctx, steps.next()); err != nil {
		return "", err
	}
	claudeSubProbeFinish(parent)
	continuationBudget := steps.next()
	exitFinding, exitErr := claudeSubProbeCrashExitFinding(parent.rep.Exit)
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s killed with SIGKILL after %d streamed newlines (%d runes): %s",
		label, strings.Count(partial, "\n"), utf8.RuneCountInString(partial), exitFinding))
	if exitErr != nil {
		return "", exitErr
	}
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript(label+" after the killed turn and exit", configDir, cfg.WorkDir, sessionID))

	// One resumed fork at the first turn's assistant uuid. Its gating turn is the
	// continuation line itself, so no extra turn enters the forked transcript.
	line := claudeSubProbeContinuationBlocks(claudeSubProbePromptE7T2, partial)
	resumeSpec := spec
	resumeSpec.Resume = claudeSubResume{SessionID: sessionID, AtUUID: resumeUUID, Fork: true}
	resumed, err := claudeSubProbeStartGated(ctx, cfg, exp, label+" resume fork", fmt.Sprintf("probe-e7-%d-fork", trial), resumeSpec, claudeSubProbePromptE7T2, line)
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
	continuation, continuationTruncated := resumed.turnText(continuationTurn)
	forkSessionID := resumed.sessionID()
	claudeSubProbeFinish(resumed)
	exp.Transcripts = append(exp.Transcripts, claudeSubProbeReadTranscript(label+" after the forked process exit", configDir, cfg.WorkDir, forkSessionID))

	seam := claudeSubProbeAnalyseSeam(partial, continuation)
	mentioned := claudeSubProbeMentionsInterruption(continuation)
	verdict := claudeSubProbeCrashVerdict(seam, mentioned)
	usage := "none"
	if continuationTurn.Result != nil {
		usage = claudeSubProbeUsageString(continuationTurn.Result.Usage)
	}
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s partial tail (last %d runes): %q",
		label, claudeSubProbeCrashSampleTail, claudeSubProbeTailSample(partial, claudeSubProbeCrashSampleTail)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s continuation head (first %d runes, truncated=%v): %q",
		label, claudeSubProbeCrashSampleHead, continuationTruncated, claudeSubProbeHeadSample(continuation, claudeSubProbeCrashSampleHead)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s seam: partial last complete item=%d, partial max item=%d, continuation first item=%d, duplicates=%v, missing=%v, cut digits=%q, fragment completes=%v, max item=%d",
		label, seam.PartialLastComplete, seam.PartialMax, seam.ContinuationFirst, seam.Duplicate, seam.Missing, seam.CutDigits, seam.FragmentCompletes, seam.MaxItem))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s mentioned the interruption: %v", label, mentioned))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s continuation usage=%s assistant usage=%s",
		label, usage, claudeSubProbeUsageString(continuationTurn.AssistantUsage)))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s sessions: original %s, fork %s, fork differs=%v",
		label, sessionID, forkSessionID, forkSessionID != sessionID))
	exp.Findings = append(exp.Findings, fmt.Sprintf("%s verdict: %s", label, verdict))
	if forkSessionID == "" || forkSessionID == sessionID {
		return "", fmt.Errorf("the resumed process reported session %q, so the fork created no new session", forkSessionID)
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

// claudeSubProbeCrashExitFinding states the killed process's exit evidence and
// fails when the CLI did not die of a signal, which is what the trial needs to
// measure.
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
	switch {
	case !exit.StatusKnown:
		return message, errors.New("the killed CLI's exit status is unknown, so the crash path is unproven")
	case exit.WaitErr == "":
		return message, errors.New("the killed CLI exited without a wait error, so the crash path is unproven")
	case exit.Status == 0:
		return message, errors.New("the killed CLI exited 0 rather than by signal")
	}
	return message, nil
}

// claudeSubProbeSeam is how a continuation joins the text it continues: the item
// numbers either side of the seam, what they duplicate, what the merged numbering
// skips, and whether the continuation finishes an item number the partial cut.
// An item is complete when its number sits on a line the partial finished.
type claudeSubProbeSeam struct {
	PartialLastComplete int
	PartialMax          int
	ContinuationFirst   int
	Duplicate           []int
	Missing             []int
	CutDigits           string
	FragmentCompletes   bool
	MaxItem             int
}

// claudeSubProbeItemPattern matches the list prompt's item numbers.
var claudeSubProbeItemPattern = regexp.MustCompile(`item-([0-9]+)`)

// claudeSubProbeAnalyseSeam compares the item numbers either side of the seam.
// When the partial was cut inside an item number, the digits the continuation
// begins with finish that number, so the continuation is scanned with the cut
// token restored in front of it.
func claudeSubProbeAnalyseSeam(partial, continuation string) claudeSubProbeSeam {
	seam := claudeSubProbeSeam{}
	cutDigits, cut := claudeSubProbeCutItemDigits(partial)
	if cut {
		seam.CutDigits = cutDigits
		seam.FragmentCompletes = claudeSubProbeStartsWithDigit(continuation)
	}
	scan := continuation
	if seam.FragmentCompletes {
		scan = "item-" + cutDigits + continuation
	}

	partialAll := claudeSubProbeItemNumbers(partial)
	if complete := claudeSubProbeItemNumbers(claudeSubProbeCompleteLines(partial)); len(complete) > 0 {
		seam.PartialLastComplete = complete[len(complete)-1]
	}
	if len(partialAll) > 0 {
		seam.PartialMax = partialAll[len(partialAll)-1]
	}
	continuationItems := claudeSubProbeItemNumbers(scan)
	if len(continuationItems) > 0 {
		seam.ContinuationFirst = continuationItems[0]
	}

	partialSet := make(map[int]struct{}, len(partialAll))
	for _, number := range partialAll {
		partialSet[number] = struct{}{}
	}
	continuationSet := make(map[int]struct{}, len(continuationItems))
	for _, number := range continuationItems {
		if _, seen := continuationSet[number]; seen {
			continue
		}
		continuationSet[number] = struct{}{}
		if _, ok := partialSet[number]; ok {
			seam.Duplicate = append(seam.Duplicate, number)
		}
	}

	seam.MaxItem = seam.PartialMax
	for number := range continuationSet {
		if number > seam.MaxItem {
			seam.MaxItem = number
		}
	}
	for number := 1; number <= seam.MaxItem; number++ {
		if _, ok := partialSet[number]; ok {
			continue
		}
		if _, ok := continuationSet[number]; ok {
			continue
		}
		seam.Missing = append(seam.Missing, number)
	}
	return seam
}

// claudeSubProbeItemNumbers returns the item numbers of text, in order.
func claudeSubProbeItemNumbers(text string) []int {
	matches := claudeSubProbeItemPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]int, 0, len(matches))
	for _, match := range matches {
		number, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		out = append(out, number)
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

// claudeSubProbeCutItemDigits reports whether the partial's final, unterminated
// line was cut inside an item number, and the digits it had written. The digits
// are empty when the cut fell inside "item-" itself.
func claudeSubProbeCutItemDigits(partial string) (string, bool) {
	if partial == "" || strings.HasSuffix(partial, "\n") {
		return "", false
	}
	line := partial[strings.LastIndex(partial, "\n")+1:]
	index := strings.LastIndex(line, "item-")
	if index < 0 {
		return "", false
	}
	digits := line[index+len("item-"):]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return digits, true
}

// claudeSubProbeStartsWithDigit reports whether text begins with an ASCII digit,
// which is how a continuation finishes a cut item number.
func claudeSubProbeStartsWithDigit(text string) bool {
	if text == "" {
		return false
	}
	return text[0] >= '0' && text[0] <= '9'
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
// order is deliberate: a restart is a restart even when it also duplicates, and
// commentary is reported only when the numbering itself is clean.
func claudeSubProbeCrashVerdict(seam claudeSubProbeSeam, mentioned bool) string {
	switch {
	case seam.ContinuationFirst == 1:
		return "restart"
	case len(seam.Duplicate) > 0:
		return "duplicate"
	case len(seam.Missing) > 0:
		return "gap"
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
