// Verdicts and the Markdown report for the live compaction bench.

import { isNum } from "./compaction-bench-score.mjs";

export const SANDBOX_NOTE = "not verified (steiner exposes no read-only check)";

function numText(value) {
	if (!isNum(value)) return "not reported";
	return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

function durationText(value) {
	if (!isNum(value)) return "not reported";
	if (value < 1000) return `${Math.round(value)} ms`;
	return `${(value / 1000).toFixed(1)} s`;
}

function cacheNote(off, on, family, probeOk) {
	if (!isNum(off.costPerTurn) || !isNum(on.costPerTurn)) return "cost proxy not judged: no cache reporting on one side";
	if (family === "anthropic" && probeOk !== true) return "cost proxy not judged: anthropic probe did not confirm cache control";
	return "cost proxy not judged";
}

// Null when both sides are judgeable, else the reasons that make the pair INVALID.
function invalidReasons(off, on) {
	if (off.valid && on.valid && !off.recallInvalid && !on.recallInvalid && off.exact !== 0) return null;
	const reasons = [];
	if (!off.valid) reasons.push(`off: ${off.invalidReason ?? "run invalid"}`);
	if (!on.valid) reasons.push(`on: ${on.invalidReason ?? "run invalid"}`);
	if (off.recallInvalid) reasons.push("off: a fixture file was read again after a compaction");
	if (on.recallInvalid) reasons.push("on: a fixture file was read again after a compaction");
	// Continuity is relative, so 0/N on both sides would otherwise pass.
	if (off.exact === 0) reasons.push("off mode recalled nothing");
	return reasons;
}

// Null when the compaction counts are close enough to compare, else the reason.
function comparabilityReason(off, on) {
	if (Math.abs(off.compactions - on.compactions) <= 1) return null;
	return `compaction counts differ: off ${off.compactions} vs on ${on.compactions}`;
}

function continuityReason(off, on, tolerance) {
	if (on.exact >= off.exact * (1 - tolerance / 100)) return null;
	return `continuity: on exact ${on.exact} below off ${off.exact} by more than ${tolerance}%`;
}

function costReason(off, on, tolerance, cacheChecked) {
	if (!cacheChecked) return null;
	if (on.costPerTurn <= off.costPerTurn * (1 + tolerance / 100)) return null;
	return `cost proxy: on ${numText(on.costPerTurn)} above off ${numText(off.costPerTurn)} by more than ${tolerance}%`;
}

/** Compare the two mode aggregates into a verdict label, cache state and reasons. */
export function verdict({ off, on, tolerance, informational, probeOk, family }) {
	const cacheChecked = isNum(off.costPerTurn) && isNum(on.costPerTurn) && !(family === "anthropic" && probeOk !== true);
	const cache = cacheChecked ? "checked" : "no cache verdict";

	const invalid = invalidReasons(off, on);
	if (invalid) return { label: "INVALID", cache, reasons: invalid };

	const incomparable = comparabilityReason(off, on);
	if (incomparable) return { label: "INCOMPARABLE", cache, reasons: [incomparable] };

	if (informational) {
		return { label: "INFORMATIONAL", cache, reasons: ["informational alias: no pass or fail verdict"] };
	}

	const reasons = [];
	if (!cacheChecked) reasons.push(cacheNote(off, on, family, probeOk));

	// A max-turn stop stays scored, so name it here rather than invalidating the run.
	const maxTurnsRuns = (off.maxTurnsHitRuns ?? 0) + (on.maxTurnsHitRuns ?? 0);
	if (maxTurnsRuns > 0) reasons.push(`max turns hit in ${maxTurnsRuns} run(s)`);

	const continuity = continuityReason(off, on, tolerance);
	if (continuity) reasons.push(continuity);
	const cost = costReason(off, on, tolerance, cacheChecked);
	if (cost) reasons.push(cost);
	if (continuity || cost) return { label: "FAIL", cache, reasons };

	if (!cacheChecked) return { label: "CONTINUITY_ONLY", cache, reasons };
	reasons.push("continuity and cost proxy within tolerance");
	return { label: "PASS", cache, reasons };
}

const COST_LABELS = {
	anthropic: "uncached + 1.25*write + 0.1*read",
	openai: "uncached + 0.1*read",
};

function continuityCell(agg) {
	if (agg.recallInvalid) return "invalid";
	const base = `${agg.exact}/${agg.total}`;
	const lostAt = Array.isArray(agg.lostAt) ? agg.lostAt : [];
	return lostAt.length > 0 ? `${base} (lost: ${lostAt.join(", ")})` : base;
}

function cell(agg, format) {
	return agg ? format(agg) : "not run";
}

function probeLine(result) {
	if (result.family !== "anthropic") return null;
	if (!result.probe) return "Probe: not run";
	if (result.probe.ok) return "Probe: ok";
	return `Probe: failed (${result.probe.reason})`;
}

// Table rows in render order; the cost row names the alias's weight formula.
const METRIC_ROWS = [
	{ label: () => "turns", value: (agg) => numText(agg.turns) },
	{ label: () => "max turns hit", value: (agg) => numText(agg.maxTurnsHitRuns) },
	{ label: () => "compactions", value: (agg) => numText(agg.compactions) },
	{ label: () => "emergency compactions (applied)", value: (agg) => numText(agg.emergencyCompactions) },
	{ label: () => "compaction failures", value: (agg) => numText(agg.compactionFailures) },
	{ label: () => "emergency failures (recorded stages, lower bound)", value: (agg) => numText(agg.emergencyFailures) },
	{ label: () => "rebases", value: (agg) => numText(agg.rebases) },
	{ label: () => "plain fallbacks", value: (agg) => numText(agg.plainFallbacks) },
	{ label: () => "max segments", value: (agg) => numText(agg.maxSegments) },
	{ label: () => "retained tail median", value: (agg) => numText(agg.retainedTailMedian) },
	{ label: () => "retained tail max", value: (agg) => numText(agg.retainedTailMax) },
	{ label: () => "cached tokens", value: (agg) => numText(agg.cachedTokens) },
	{ label: () => "uncached input tokens", value: (agg) => numText(agg.uncachedTokens) },
	{ label: (result) => `cost proxy per turn (${COST_LABELS[result.family]})`, value: (agg) => numText(agg.costPerTurn) },
	{ label: () => "wall clock", value: (agg) => durationText(agg.wallClockMs) },
	{ label: () => "summed call duration", value: (agg) => durationText(agg.durationMs) },
	{ label: () => "ttft p50", value: (agg) => durationText(agg.ttftP50Ms) },
	{ label: () => "ttft p95", value: (agg) => durationText(agg.ttftP95Ms) },
	{ label: () => "call duration p50", value: (agg) => durationText(agg.durationP50Ms) },
	{ label: () => "call duration p95", value: (agg) => durationText(agg.durationP95Ms) },
	{ label: () => "provider retries", value: (agg) => numText(agg.providerRetries) },
	{ label: () => "rate limited failures (429)", value: (agg) => numText(agg.providerRateLimited) },
	{ label: () => "provider error outcomes", value: (agg) => numText(agg.providerErrors) },
	{ label: () => "continuity", value: continuityCell },
];

function renderAlias(result) {
	const off = result.off;
	const on = result.on;
	const lines = [
		`### ${result.alias} (${result.family})`,
		"",
		"| metric | off | on |",
		"| --- | --- | --- |",
		...METRIC_ROWS.map((row) => `| ${row.label(result)} | ${cell(off, row.value)} | ${cell(on, row.value)} |`),
		"",
		`Verdict: ${result.verdict.label} (cache: ${result.verdict.cache})`,
		...result.verdict.reasons.map((reason) => `- ${reason}`),
	];
	const probe = probeLine(result);
	if (probe) lines.push(probe);
	return lines.join("\n");
}

/** Render one Markdown table per alias with metric rows, verdict and probe lines. */
export function renderTable(results) {
	return results.map(renderAlias).join("\n\n");
}

/** Render the full results.md text for one bench run. */
export function renderResults({ opts, experiment, results, sandbox, timestamp }) {
	return [
		"# Compaction bench",
		"",
		`- generated: ${timestamp}`,
		`- steiner: ${opts.steiner}`,
		`- threshold: ${opts.threshold}`,
		`- context window: ${opts.contextWindow ?? "alias default"}`,
		`- seed: ${opts.seed === null ? "repeat number" : `${opts.seed} + repeat - 1`}`,
		`- files: ${opts.files}`,
		`- padTokens: ${opts.padTokens}`,
		`- maxTurns: ${opts.maxTurns}`,
		`- repeat: ${opts.repeat}`,
		`- tolerance: ${opts.tolerance}%`,
		`- modes: ${opts.modes.join(", ")}`,
		`- env: ${Object.entries(experiment).map(([key, value]) => `${key}=${value}`).join(", ")} (on mode)`,
		`- sandbox: ${sandbox}`,
		"",
		renderTable(results),
		"",
	].join("\n");
}
