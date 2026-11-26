// Turn parsed calls and runs into cache economics, validity verdicts and
// per-mode medians.

import { parseProviderLog, parseRunLog, parseSegmentDecisions, parseStdout, scoreRecall, summarizeDecisions } from "./compaction-bench-parse.mjs";

/** True for a reported number; null and NaN both mean "not reported". */
export function isNum(value) {
	return typeof value === "number" && Number.isFinite(value);
}

/** Sum model-call token usage and report only when every call reported cache counters. */
export function aggregateCache(modelCalls) {
	let reportingCalls = 0;
	let promptTokens = 0;
	let cacheReadTokens = 0;
	let cacheCreateTokens = 0;
	for (const call of modelCalls) {
		if (call.cacheReported) reportingCalls++;
		promptTokens += call.promptTokens ?? 0;
		cacheReadTokens += call.cacheReadTokens ?? 0;
		cacheCreateTokens += call.cacheCreateTokens ?? 0;
	}
	return {
		calls: modelCalls.length,
		reportingCalls,
		reported: reportingCalls > 0 && reportingCalls === modelCalls.length,
		promptTokens,
		cacheReadTokens,
		cacheCreateTokens,
	};
}

/** Convert aggregate token usage into a weighted cost proxy, or null when cache was not reported. */
export function costProxy(agg, family) {
	if (!agg.reported) return null;
	if (family === "anthropic") {
		const uncached = Math.max(0, agg.promptTokens - agg.cacheReadTokens - agg.cacheCreateTokens);
		return uncached + 1.25 * agg.cacheCreateTokens + 0.1 * agg.cacheReadTokens;
	}
	if (family === "openai") {
		const uncached = Math.max(0, agg.promptTokens - agg.cacheReadTokens);
		return uncached + 0.1 * agg.cacheReadTokens;
	}
	throw new Error(`unknown cost family: ${JSON.stringify(family)} (allowed: anthropic,openai)`);
}

/** Check that the first probe call wrote cache and the second read it back. */
export function evaluateProbe(call1Calls, call2Calls) {
	if (call1Calls.length === 0 || call2Calls.length === 0) {
		return { ok: false, reason: "probe calls failed" };
	}
	const wrote = call1Calls.some((call) => call.cacheReported && call.cacheCreateTokens > 0);
	const read = call2Calls.some((call) => call.cacheReported && call.cacheReadTokens > 0);
	if (wrote && read) return { ok: true, reason: null };
	return { ok: false, reason: "cache_control not honoured on this route" };
}

/** Return the nearest-rank percentile of the numbers, or null for an empty list. */
export function percentile(nums, p) {
	if (nums.length === 0) return null;
	const sorted = [...nums].sort((a, b) => a - b);
	const rank = Math.ceil(p * sorted.length);
	return sorted[Math.min(sorted.length - 1, Math.max(0, rank - 1))];
}

/** Return the median of the numbers, or null for an empty list. */
export function median(nums) {
	if (nums.length === 0) return null;
	const sorted = [...nums].sort((a, b) => a - b);
	const mid = Math.floor(sorted.length / 2);
	return sorted.length % 2 === 1 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

/** Judge a single run: it must answer, stay inside the read tool, and, when on, compact enough. */
export function runValidity({ mode, compactions, decisionSummary, finalAnswer, nonReadFileAccess = 0 }) {
	if (finalAnswer === null) return { valid: false, reason: "no final answer parsed" };
	if (nonReadFileAccess > 0) return { valid: false, reason: "fixture accessed outside main-agent read tool" };
	if (mode !== "on") return { valid: true, reason: null };
	if (compactions < 4) return { valid: false, reason: "fewer than 4 compactions" };
	if ((decisionSummary?.appends ?? 0) === 0) return { valid: false, reason: "no append decision" };
	return { valid: true, reason: null };
}

function numbers(runs, key) {
	return runs.map((run) => run[key]).filter(isNum);
}

/** Fold one mode's run records into median metrics, a union of loss points and the first failure reason. */
export function aggregateMode(runs) {
	const lostAt = [];
	for (const run of runs) {
		for (const entry of run.lostAt ?? []) {
			if (!lostAt.includes(entry)) lostAt.push(entry);
		}
	}
	const failed = runs.find((run) => run.failed);
	const invalid = runs.find((run) => run.valid === false);
	// A failed run can still carry partial metrics from a dead run, so only the
	// completed runs feed the medians; with none completed, all runs are used.
	const completed = runs.filter((run) => !run.failed);
	const sample = completed.length > 0 ? completed : runs;
	return {
		turns: median(numbers(sample, "turns")),
		compactions: median(numbers(sample, "compactions")),
		emergencyCompactions: median(numbers(sample, "emergencyCompactions")),
		compactionFailures: median(numbers(sample, "compactionFailures")),
		emergencyFailures: median(numbers(sample, "emergencyFailures")),
		rebases: median(numbers(sample, "rebases")),
		plainFallbacks: median(numbers(sample, "plainFallbacks")),
		maxSegments: median(numbers(sample, "maxSegments")),
		retainedTailMedian: median(numbers(sample, "retainedTailMedian")),
		retainedTailMax: median(numbers(sample, "retainedTailMax")),
		cachedTokens: median(numbers(sample, "cachedTokens")),
		uncachedTokens: median(numbers(sample, "uncachedTokens")),
		promptTokens: median(numbers(sample, "promptTokens")),
		costProxy: median(numbers(sample, "costProxy")),
		costPerTurn: median(numbers(sample, "costPerTurn")),
		wallClockMs: median(numbers(sample, "wallClockMs")),
		durationMs: median(numbers(sample, "durationMs")),
		ttftP50Ms: median(numbers(sample, "ttftP50Ms")),
		ttftP95Ms: median(numbers(sample, "ttftP95Ms")),
		durationP50Ms: median(numbers(sample, "durationP50Ms")),
		durationP95Ms: median(numbers(sample, "durationP95Ms")),
		providerCalls: median(numbers(sample, "providerCalls")),
		providerRetries: median(numbers(sample, "providerRetries")),
		providerRateLimited: median(numbers(sample, "providerRateLimited")),
		providerErrors: median(numbers(sample, "providerErrors")),
		exact: median(numbers(sample, "exact")),
		lost: median(numbers(sample, "lost")),
		total: median(numbers(sample, "total")),
		recallInvalid: runs.some((run) => run.recallInvalid === true),
		maxTurnsHitRuns: runs.filter((run) => run.maxTurnsHit === true).length,
		valid: !failed && !invalid,
		invalidReason: failed ? `run failed: ${failed.reason ?? "unknown"}` : (invalid?.invalidReason ?? null),
		lostAt,
	};
}

function lostEntries(recall) {
	const entries = [];
	for (const file of recall.perFile) {
		if (file.status !== "lost") continue;
		entries.push(`${file.file}@${file.lostAt === "unattributed" ? "unattributed" : `c${file.lostAt}`}`);
	}
	return entries;
}

// One run record's metric shape and key order; a failed run reports no metrics.
function emptyMetrics() {
	return {
		turns: null,
		compactions: null,
		emergencyCompactions: null,
		compactionFailures: null,
		emergencyFailures: null,
		rebases: null,
		plainFallbacks: null,
		maxSegments: null,
		retainedTailMedian: null,
		retainedTailMax: null,
		cachedTokens: null,
		uncachedTokens: null,
		promptTokens: null,
		costProxy: null,
		costPerTurn: null,
		wallClockMs: null,
		durationMs: null,
		ttftP50Ms: null,
		ttftP95Ms: null,
		durationP50Ms: null,
		durationP95Ms: null,
		providerCalls: null,
		providerRetries: null,
		providerRateLimited: null,
		providerErrors: null,
		exact: null,
		lost: null,
		total: null,
		recallInvalid: false,
		recallInvalidReason: null,
		lostAt: [],
		configuredContextWindow: null,
		configuredThresholdTokens: null,
		observedContextWindow: null,
		observedThresholdTokens: null,
		valid: false,
		invalidReason: null,
		maxTurnsHit: false,
		malformed: 0,
	};
}

/** Turn one run's artefacts into metrics, recall scoring and a validity verdict. */
export function analyzeRun({
	stdoutText,
	logText,
	compactionText,
	providerText = "",
	fixtures,
	mode,
	family,
	notesDir = null,
	wallClockMs = null,
	contextWindow = null,
	threshold = null,
}) {
	const stdout = parseStdout(stdoutText);
	const runLog = parseRunLog(logText, { notesDir });
	const decisionSummary = summarizeDecisions(parseSegmentDecisions(compactionText));
	const provider = parseProviderLog(providerText);
	const recall = scoreRecall({ answer: stdout.finalAnswer, fixtures, events: runLog.events });
	// Compaction summary calls cost real tokens, so the cost and cache totals fold
	// them in; turns stays the main-call count that costPerTurn divides by.
	const calls = [...runLog.modelCalls, ...runLog.compactionCalls];
	const agg = aggregateCache(calls);
	const cost = costProxy(agg, family);
	const turns = runLog.modelCalls.length;
	// The provider stream measures every call, compaction summaries included, and
	// its duration spans retry waits. Fall back to the session log's main and
	// compaction calls only when that stream reported no measurement; a zero is
	// unreported (both fields are omitempty) and never fills a metric.
	const sessionTimings = (key) => calls.map((call) => call[key]).filter((value) => isNum(value) && value > 0);
	const durations = provider.durations.length > 0 ? provider.durations : sessionTimings("durationMs");
	const ttfts = provider.ttfts.length > 0 ? provider.ttfts : sessionTimings("ttftMs");
	const validity = runValidity({
		mode,
		compactions: runLog.compactions,
		decisionSummary,
		finalAnswer: stdout.finalAnswer,
		nonReadFileAccess: runLog.nonReadFileAccess,
	});
	return {
		...emptyMetrics(),
		turns,
		compactions: runLog.compactions,
		emergencyCompactions: runLog.emergencyCompactions,
		compactionFailures: runLog.compactionFailures,
		emergencyFailures: runLog.emergencyFailures,
		rebases: decisionSummary.rebases,
		plainFallbacks: decisionSummary.plainFallbacks,
		maxSegments: decisionSummary.maxSegments,
		retainedTailMedian: median(runLog.retainedTail),
		retainedTailMax: runLog.retainedTail.length > 0 ? Math.max(...runLog.retainedTail) : null,
		cachedTokens: agg.reported ? agg.cacheReadTokens : null,
		uncachedTokens: agg.reported ? Math.max(0, agg.promptTokens - agg.cacheReadTokens - agg.cacheCreateTokens) : null,
		promptTokens: agg.promptTokens,
		costProxy: cost,
		costPerTurn: cost !== null && turns > 0 ? cost / turns : null,
		wallClockMs,
		durationMs: durations.length > 0 ? durations.reduce((sum, value) => sum + value, 0) : null,
		ttftP50Ms: percentile(ttfts, 0.5),
		ttftP95Ms: percentile(ttfts, 0.95),
		durationP50Ms: percentile(durations, 0.5),
		durationP95Ms: percentile(durations, 0.95),
		providerCalls: provider.calls,
		providerRetries: provider.retries,
		providerRateLimited: provider.rateLimited,
		providerErrors: provider.errors,
		exact: recall.exact,
		lost: recall.lost,
		total: recall.total,
		recallInvalid: recall.invalid,
		recallInvalidReason: recall.invalidReason,
		lostAt: lostEntries(recall),
		configuredContextWindow: contextWindow,
		configuredThresholdTokens: threshold,
		observedContextWindow: runLog.contextWindow,
		observedThresholdTokens: runLog.thresholdTokens,
		valid: validity.valid,
		invalidReason: validity.reason,
		maxTurnsHit: stdout.maxTurnsHit,
		malformed: runLog.malformed,
	};
}

/** A run record for a run that failed before it could be analysed. */
export function failedRun({ mode, repeat, reason, exitCode = null, timedOut = false }) {
	return {
		mode,
		repeat,
		failed: true,
		timedOut,
		exitCode,
		reason,
		...emptyMetrics(),
		invalidReason: `run failed: ${reason}`,
	};
}
