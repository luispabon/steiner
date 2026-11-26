#!/usr/bin/env node
// Compare compaction bench runs from one or more raw.json files.
//
// The bench writes raw.json per invocation. A threshold experiment runs many
// invocations, so this script takes the bench --out directories (or a root
// directory holding them) and prints one table per context window and model,
// grouped by threshold, with every run's values rather than mode medians.
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
context window, then model, then threshold, and show per-run values.
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
	const fallbackWindow = isNum(raw.contextWindow) ? raw.contextWindow : null;
	const fallbackThreshold = isNum(raw.threshold) ? raw.threshold : null;
	const rows = [];
	for (const alias of Array.isArray(raw.aliases) ? raw.aliases : []) {
		for (const mode of ["off", "on"]) {
			for (const run of alias.runs?.[mode] ?? []) {
				rows.push({
					source,
					model: alias.alias,
					ref: alias.ref ?? alias.alias,
					window: run.effectiveContextWindow ?? fallbackWindow,
					threshold: run.effectiveThresholdTokens ?? fallbackThreshold,
					mode,
					repeat: run.repeat ?? null,
					failed: run.failed === true,
					valid: run.valid === true,
					invalidReason: run.invalidReason ?? null,
					exact: run.exact ?? null,
					lost: run.lost ?? null,
					total: run.total ?? null,
					maxTurnsHit: run.maxTurnsHit === true,
					compactions: run.compactions ?? null,
					emergencyCompactions: run.emergencyCompactions ?? null,
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

function compareNullable(a, b) {
	if (a === b) return 0;
	if (!isNum(a)) return 1;
	if (!isNum(b)) return -1;
	return a - b;
}

/** Sort rows by window, model, threshold, mode and repeat. */
export function sortRows(rows) {
	return [...rows].sort(
		(a, b) =>
			compareNullable(a.window, b.window) ||
			String(a.model).localeCompare(String(b.model)) ||
			compareNullable(a.threshold, b.threshold) ||
			(MODE_ORDER[a.mode] ?? 9) - (MODE_ORDER[b.mode] ?? 9) ||
			compareNullable(a.repeat, b.repeat) ||
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

const COLUMNS = [
	["threshold", (row) => numText(row.threshold)],
	["mode", (row) => row.mode],
	["r", (row) => numText(row.repeat)],
	["continuity", continuityText],
	["max turns", (row) => (row.maxTurnsHit ? "hit" : "no")],
	["compactions", (row) => numText(row.compactions)],
	["emergency", (row) => numText(row.emergencyCompactions)],
	["retained med", (row) => numText(row.retainedTailMedian)],
	["retained max", (row) => numText(row.retainedTailMax)],
	["wall", (row) => durationText(row.wallClockMs)],
	["summed duration", (row) => durationText(row.durationMs)],
	["retries", (row) => numText(row.providerRetries)],
	["429", (row) => numText(row.providerRateLimited)],
	["errors", (row) => numText(row.providerErrors)],
	["cached in", (row) => numText(row.cachedTokens)],
	["uncached in", (row) => numText(row.uncachedTokens)],
	["cost proxy", (row) => numText(row.costProxy)],
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
	lines.push(
		"Continuity is the exact/total recall score; a failed or invalid run names its reason instead. Times are bench wall clock and summed reported per-call duration. Retries sum attempts beyond the first, 429 counts rate-limited calls, and errors counts exhausted or cancelled calls. Token counts and the cost proxy fold in compaction summary calls, and cost proxy is the weighted token estimate, not billing.",
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
