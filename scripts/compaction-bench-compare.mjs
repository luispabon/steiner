#!/usr/bin/env node
// Compare compaction bench runs from one or more raw.json files.
//
// The bench writes raw.json per invocation. A threshold experiment runs many
// invocations, so this script takes the bench --out directories (or a root
// directory holding them) and prints one table per context window and model,
// grouped by threshold, with every run's values rather than mode medians. Each
// row names its effective fixture seed and its raw.json, so repeats that a phase
// script encodes only in --seed stay distinguishable.
//
// Usage: node scripts/compaction-bench-compare.mjs <dir-or-raw.json...> [--json]

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export const USAGE = `usage: node scripts/compaction-bench-compare.mjs <dir-or-raw.json...> [flags]

flags:
  --json     print the collected per-run rows as JSON instead of Markdown
  --help     print this usage and exit

Each path is either a bench --out directory, a directory holding bench runs
(scanned recursively for raw.json), or a raw.json file. Tables are grouped by
context window, then model, then threshold, and show per-run values with the
run's effective fixture seed, its raw.json and any configured/observed limit
mismatch.
`;

const MODE_ORDER = { off: 0, on: 1 };

function isNum(value) {
	return typeof value === "number" && Number.isFinite(value);
}

function walkRaw(dir, out) {
	let entries;
	try {
		entries = readdirSync(dir, { withFileTypes: true });
	} catch {
		return;
	}
	for (const entry of entries) {
		const path = join(dir, entry.name);
		if (entry.isDirectory()) walkRaw(path, out);
		else if (entry.isFile() && entry.name === "raw.json") out.push(path);
	}
}

/** Expand paths (directories or raw.json files) into the raw.json files they contain, sorted. */
export function findRawFiles(inputs) {
	const found = new Set();
	for (const input of inputs) {
		const path = resolve(input);
		const info = statSync(path, { throwIfNoEntry: false });
		if (!info) throw new Error(`no such path: ${input}`);
		if (info.isDirectory()) {
			const nested = [];
			walkRaw(path, nested);
			for (const file of nested) found.add(file);
		} else if (info.isFile()) {
			found.add(path);
		} else {
			throw new Error(`not a file or directory: ${input}`);
		}
	}
	return [...found].sort();
}

/** Read and parse every raw.json file, reporting which file failed to parse. */
export function loadRaws(inputs) {
	return findRawFiles(inputs).map((path) => {
		let raw;
		try {
			raw = JSON.parse(readFileSync(path, "utf8"));
		} catch (error) {
			throw new Error(`read ${path}: ${error.message}`);
		}
		return { path, raw };
	});
}

// One row per bench run. A run's own reported window and threshold win over the
// invocation defaults, because the effective values are what the model saw.
export function compareRows(raw, source = null) {
	const configuredWindow = isNum(raw.contextWindow) ? raw.contextWindow : null;
	const configuredThreshold = isNum(raw.threshold) ? raw.threshold : null;
	const files = isNum(raw.files) ? raw.files : null;
	const padTokens = isNum(raw.padTokens) ? raw.padTokens : null;
	const maxTurns = isNum(raw.maxTurns) ? raw.maxTurns : null;
	const rows = [];
	for (const alias of Array.isArray(raw.aliases) ? raw.aliases : []) {
		for (const mode of ["off", "on"]) {
			for (const run of alias.runs?.[mode] ?? []) {
				const observedWindow = run.observedContextWindow ?? null;
				const observedThreshold = run.observedThresholdTokens ?? null;
				const rowConfiguredWindow = run.configuredContextWindow ?? configuredWindow;
				const rowConfiguredThreshold = run.configuredThresholdTokens ?? configuredThreshold;
				rows.push({
					source,
					model: alias.alias,
					ref: alias.ref ?? alias.alias,
					// The observed value is what the run reported; the configured value is
					// what the bench asked for. Grouping uses observed when present.
					observedWindow,
					observedThreshold,
					configuredWindow: rowConfiguredWindow,
					configuredThreshold: rowConfiguredThreshold,
					window: observedWindow ?? rowConfiguredWindow,
					threshold: observedThreshold ?? rowConfiguredThreshold,
					windowMismatch: limitsDiffer(observedWindow, rowConfiguredWindow),
					thresholdMismatch: limitsDiffer(observedThreshold, rowConfiguredThreshold),
					mode,
					repeat: run.repeat ?? null,
					// Phase1 invocations pass --repeat 1 and carry the repeat index in
					// --seed, so the repeat alone cannot identify a run's fixtures.
					seed: effectiveSeed(raw.seed, run.repeat),
					files,
					padTokens,
					maxTurns,
					failed: run.failed === true,
					valid: run.valid === true,
					invalidReason: run.invalidReason ?? null,
					exact: run.exact ?? null,
					lost: run.lost ?? null,
					total: run.total ?? null,
					maxTurnsHit: run.maxTurnsHit === true,
					compactions: run.compactions ?? null,
					emergencyCompactions: run.emergencyCompactions ?? null,
					compactionFailures: run.compactionFailures ?? null,
					emergencyFailures: run.emergencyFailures ?? null,
					timingSource: run.timingSource ?? null,
					retainedTailMedian: run.retainedTailMedian ?? null,
					retainedTailMax: run.retainedTailMax ?? null,
					wallClockMs: run.wallClockMs ?? null,
					durationMs: run.durationMs ?? null,
					providerRetries: run.providerRetries ?? null,
					providerRateLimited: run.providerRateLimited ?? null,
					providerErrors: run.providerErrors ?? null,
					cachedTokens: run.cachedTokens ?? null,
					uncachedTokens: run.uncachedTokens ?? null,
					costProxy: run.costProxy ?? null,
				});
			}
		}
	}
	return rows;
}

// Without --seed the bench seeds each repeat with its repeat number, so the
// effective seed needs both values to recover.
function effectiveSeed(seed, repeat) {
	if (isNum(seed) && isNum(repeat)) return seed + repeat - 1;
	return isNum(repeat) ? repeat : null;
}

function limitsDiffer(observed, configured) {
	return isNum(observed) && isNum(configured) && observed !== configured;
}

function compareNullable(a, b) {
	if (a === b) return 0;
	if (!isNum(a)) return 1;
	if (!isNum(b)) return -1;
	return a - b;
}

/** Sort rows by window, model, threshold, mode, repeat, seed and source. */
export function sortRows(rows) {
	return [...rows].sort(
		(a, b) =>
			compareNullable(a.window, b.window) ||
			String(a.model).localeCompare(String(b.model)) ||
			compareNullable(a.threshold, b.threshold) ||
			(MODE_ORDER[a.mode] ?? 9) - (MODE_ORDER[b.mode] ?? 9) ||
			compareNullable(a.repeat, b.repeat) ||
			// Phase1 encodes the repeat index in the seed, so seed order keeps one
			// table in repeat order even when every invocation uses --repeat 1.
			compareNullable(a.seed, b.seed) ||
			String(a.source).localeCompare(String(b.source)),
	);
}

function numText(value) {
	if (!isNum(value)) return "n/a";
	return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

function durationText(value) {
	if (!isNum(value)) return "n/a";
	return `${(value / 1000).toFixed(1)} s`;
}

function continuityText(row) {
	if (row.failed) return `failed: ${row.invalidReason ?? "unknown"}`;
	if (!row.valid) return `invalid: ${row.invalidReason ?? "unknown"}`;
	if (!isNum(row.exact) || !isNum(row.total)) return "n/a";
	return `${row.exact}/${row.total}`;
}

// The limits column flags a run whose observed limit differs from the configured
// one, so a table grouped by observed values never hides the gap. A run that
// lacks an observed window or threshold falls back to the configured value,
// which "cfg" marks instead of a bare "-" that would read as a verified match.
function limitsText(row) {
	const parts = [];
	if (!isNum(row.observedWindow) || !isNum(row.observedThreshold)) parts.push("cfg");
	if (row.windowMismatch) parts.push("window");
	if (row.thresholdMismatch) parts.push("threshold");
	return parts.length === 0 ? "-" : parts.join("+");
}

// A raw.json path may hold a pipe or backslash, which would split the cell or
// escape the next character, or a line break, which would end the row: escape
// the first two, then fold CR and LF to a space, before a path enters a
// Markdown cell.
function mdCell(value) {
	return String(value)
		.replace(/\\/g, "\\\\")
		.replace(/\|/g, "\\|")
		.replace(/[\r\n]+/g, " ");
}

const COLUMNS = [
	["threshold", (row) => numText(row.threshold)],
	["limits", limitsText],
	["mode", (row) => row.mode],
	["r", (row) => numText(row.repeat)],
	["seed", (row) => numText(row.seed)],
	["continuity", continuityText],
	["max turns", (row) => (row.maxTurnsHit ? "hit" : "no")],
	["compactions", (row) => numText(row.compactions)],
	["emergency done", (row) => numText(row.emergencyCompactions)],
	["failures", (row) => numText(row.compactionFailures)],
	["emergency fail (lower bound)", (row) => numText(row.emergencyFailures)],
	["retained med", (row) => numText(row.retainedTailMedian)],
	["retained max", (row) => numText(row.retainedTailMax)],
	["wall", (row) => durationText(row.wallClockMs)],
	["summed duration", (row) => durationText(row.durationMs)],
	["retries", (row) => numText(row.providerRetries)],
	["429 fail", (row) => numText(row.providerRateLimited)],
	["errors", (row) => numText(row.providerErrors)],
	["cached in", (row) => numText(row.cachedTokens)],
	["uncached in", (row) => numText(row.uncachedTokens)],
	["cost proxy", (row) => numText(row.costProxy)],
	["source", (row) => (row.source === null ? "n/a" : mdCell(row.source))],
];

function tableFor(rows) {
	const header = `| ${COLUMNS.map(([label]) => label).join(" | ")} |`;
	const divider = `| ${COLUMNS.map(() => "---").join(" | ")} |`;
	const body = rows.map((row) => `| ${COLUMNS.map(([, value]) => value(row)).join(" | ")} |`);
	return [header, divider, ...body].join("\n");
}

// Consecutive window/model groups, because sortRows already ordered by both.
function groupRows(rows) {
	const groups = [];
	for (const row of rows) {
		const last = groups.at(-1);
		if (last && last.window === row.window && last.model === row.model) last.rows.push(row);
		else groups.push({ window: row.window, model: row.model, rows: [row] });
	}
	return groups;
}

/** Render the comparison Markdown for already sorted rows. */
export function renderCompare(rows, sources = []) {
	const lines = [
		"# Compaction bench comparison",
		"",
		`- sources: ${sources.length} raw.json file(s)`,
		`- runs: ${rows.length}`,
		"",
	];
	if (rows.length === 0) {
		lines.push("No bench runs found.", "");
		return lines.join("\n");
	}
	let windowShown;
	for (const group of groupRows(rows)) {
		if (group.window !== windowShown) {
			lines.push(`## window ${isNum(group.window) ? group.window : "unknown"}`, "");
			windowShown = group.window;
		}
		const refs = new Set(group.rows.map((row) => row.ref));
		const ref = refs.size === 1 ? group.rows[0].ref : null;
		const label = ref && ref !== group.model ? `${group.model} (${ref})` : group.model;
		lines.push(`### ${label}`, "", tableFor(group.rows), "");
	}
	const mismatched = rows.filter((row) => row.windowMismatch || row.thresholdMismatch).length;
	const configuredOnly = rows.filter((row) => !isNum(row.observedWindow) || !isNum(row.observedThreshold)).length;
	lines.push(
		`Continuity is the exact/total recall score; a failed or invalid run names its reason instead. Wall is the bench clock around the process; summed duration and p50/p95 come from the provider stream, which covers every provider call (compaction summaries and failed calls included), includes retry waits, and may include delegated or advisor calls, so its scope can exceed main-conversation time. JSON rows carry timingSource: provider, session (fallback when the provider stream measured nothing), or null. Retries sum attempts beyond the first. 429 fail counts final rate-limited failures only: a call that retried and succeeded carries no error class. Emergency done counts applied compactions with an emergency stage. Failures counts failed compactions; emergency fail is a lower bound over failures that recorded an emergency stage, because production omits the stage for some emergency failures. Seed is the fixture seed the run used: the bench seed plus the repeat minus one, or the repeat number when the bench ran without --seed. Window and threshold are the run's observed limits, falling back to the configured values; both values are in the JSON. ${mismatched} run(s) reported an observed limit that differs from the configured one; the limits column names the mismatched limit (window, threshold, or window+threshold), combined with cfg when the other limit was not observed. ${configuredOnly} run(s) lack an observed window and/or threshold and fall back to the configured values; the limits column marks them cfg. Token counts and the cost proxy fold in applied compaction summary calls only: a failed compaction reports no usage for any of its summary calls, so they are absent. The cost proxy is a weighted token estimate, not billing.`,
		"",
	);
	return lines.join("\n");
}

/** Run the comparison CLI. Returns the exit code. */
export function main(argv = process.argv.slice(2), deps = {}) {
	const writeOut = deps.stdout ?? ((text) => process.stdout.write(text));
	const log = deps.log ?? ((line) => process.stderr.write(`${line}\n`));
	const inputs = [];
	let json = false;
	for (const arg of argv) {
		if (arg === "--help" || arg === "-h") {
			writeOut(USAGE);
			return 0;
		}
		if (arg === "--json") {
			json = true;
			continue;
		}
		if (arg.startsWith("-")) {
			log(`unknown flag: ${arg}\n\n${USAGE}`);
			return 2;
		}
		inputs.push(arg);
	}
	if (inputs.length === 0) {
		log(`at least one out dir or raw.json is required\n\n${USAGE}`);
		return 2;
	}
	let raws;
	try {
		raws = loadRaws(inputs);
	} catch (error) {
		log(error.message);
		return 2;
	}
	if (raws.length === 0) {
		log("no raw.json found under the given paths");
		return 2;
	}
	const rows = sortRows(raws.flatMap(({ path, raw }) => compareRows(raw, path)));
	if (json) {
		writeOut(`${JSON.stringify({ sources: raws.map(({ path }) => path), rows }, null, 2)}\n`);
		return 0;
	}
	writeOut(`${renderCompare(rows, raws.map(({ path }) => path))}\n`);
	return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	process.exitCode = main();
}
