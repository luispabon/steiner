#!/usr/bin/env node
// Live compaction bench: pure parsing, scoring and reporting helpers.
//
// The bench runs one `steiner --exec` per model alias, mode (off/on) and
// repeat against a generated notes chain, with a low compaction threshold so
// every run compacts many times. This module owns the functions that turn a
// run's artefacts (session log JSONL, compaction log text, exec stdout) into
// continuity and cache economics, and main() orchestrates the live runs and
// writes results.md plus raw.json.
//
// The only environment values ever surfaced are
// STEINER_EXPERIMENT_SEGMENTED_COMPACTION and its three
// STEINER_EXPERIMENT_SEGMENT_* limit passthroughs.
//
// Usage: node scripts/compaction-bench.mjs --model <a,b> [flags]

import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { closeSync, openSync, rmSync } from "node:fs";
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const MAX_PAD_TOKENS = 16000;

export const USAGE = `usage: node scripts/compaction-bench.mjs --model <a,b> [flags]

flags:
  --model <a,b>                model aliases to bench (required, comma-separated)
  --anthropic-alias <a,b>      aliases priced with Anthropic cache weights
  --informational-alias <a,b>  aliases reported without a pass or fail verdict
  --modes <a,b>                modes to run, subset of off,on (default: off,on)
  --threshold <n>              compaction_threshold_tokens for every alias (default: 15000)
  --files <n>                  notes chain length, 4 to 99 (default: 24)
  --pad-tokens <n>             filler tokens per notes file, 0 to ${MAX_PAD_TOKENS} (default: 3000)
  --max-turns <n>              limits.max_turns for every run (default: 80)
  --repeat <n>                 runs per alias and mode (default: 1)
  --tolerance <n>              allowed percent change for continuity and cost (default: 5)
  --run-timeout-minutes <n>    kill a run that exceeds this many minutes (default: 30)
  --steiner <path>             steiner binary (default: steiner)
  --out <dir>                  write results.md and raw.json here (default: a temp dir)
  --help                       print this usage and exit

The bench makes real provider calls and spends real tokens. It refuses to run
unless STEINER_LIVE_BENCH=1 is set.
`;

const LIST_FLAGS = {
	"--model": "models",
	"--anthropic-alias": "anthropicAliases",
	"--informational-alias": "informationalAliases",
	"--modes": "modes",
};

const INT_FLAGS = {
	"--threshold": "threshold",
	"--files": "files",
	"--pad-tokens": "padTokens",
	"--max-turns": "maxTurns",
	"--repeat": "repeat",
	"--run-timeout-minutes": "runTimeoutMinutes",
};

// --pad-tokens is the only count where zero is meaningful: the files keep their
// CODE and NEXT lines and lose the filler.
const ZERO_ALLOWED_INT_FLAGS = new Set(["--pad-tokens"]);

const STRING_FLAGS = {
	"--steiner": "steiner",
	"--out": "out",
};

const SEGMENT_PASSTHROUGH = [
	"STEINER_EXPERIMENT_SEGMENT_MAX_COUNT",
	"STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT",
	"STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT",
];

function usageError(message) {
	return new Error(`${message}\n\n${USAGE}`);
}

function parseIntFlag(flag, value, min) {
	const expected = min === 0 ? "a non-negative integer" : "a positive integer";
	const invalid = () => usageError(`invalid value for ${flag}: ${JSON.stringify(value)} (expected ${expected})`);
	if (!/^\d+$/.test(value)) throw invalid();
	const n = Number(value);
	if (!Number.isSafeInteger(n) || n < min) throw invalid();
	return n;
}

function parseTolerance(flag, value) {
	if (!/^\d+(\.\d+)?$/.test(value)) {
		throw usageError(`invalid value for ${flag}: ${JSON.stringify(value)} (expected a non-negative number)`);
	}
	return Number(value);
}

/** Parse bench flags into run options; throws an Error carrying USAGE on bad input. */
export function parseArgs(argv) {
	const args = {
		help: false,
		models: [],
		anthropicAliases: [],
		informationalAliases: [],
		modes: ["off", "on"],
		threshold: 15000,
		files: 24,
		padTokens: 3000,
		maxTurns: 80,
		repeat: 1,
		tolerance: 5,
		runTimeoutMinutes: 30,
		steiner: "steiner",
		out: null,
	};
	const seen = new Set();

	for (let i = 0; i < argv.length; i++) {
		const raw = argv[i];
		let name = raw;
		let value;
		const eq = raw.startsWith("--") ? raw.indexOf("=") : -1;
		if (eq > 2) {
			name = raw.slice(0, eq);
			value = raw.slice(eq + 1);
		}
		if (name === "--help") {
			if (value !== undefined) throw usageError("--help takes no value");
			args.help = true;
			continue;
		}
		const listKey = LIST_FLAGS[name];
		const intKey = INT_FLAGS[name];
		const stringKey = STRING_FLAGS[name];
		const isTolerance = name === "--tolerance";
		if (!listKey && !intKey && !stringKey && !isTolerance) {
			throw usageError(raw.startsWith("-") ? `unknown flag: ${name}` : `unexpected argument: ${raw}`);
		}
		if (value === undefined) {
			const next = argv[i + 1];
			if (next === undefined || (next.length > 2 && next.startsWith("--"))) {
				throw usageError(`missing value for ${name}`);
			}
			value = next;
			i++;
		}
		if (listKey) {
			const values = value
				.split(",")
				.map((v) => v.trim())
				.filter((v) => v !== "");
			if (values.length === 0) throw usageError(`${name} needs at least one value`);
			if (!seen.has(listKey)) {
				args[listKey] = [];
				seen.add(listKey);
			}
			args[listKey].push(...values);
			continue;
		}
		if (intKey) {
			args[intKey] = parseIntFlag(name, value, ZERO_ALLOWED_INT_FLAGS.has(name) ? 0 : 1);
			continue;
		}
		if (isTolerance) {
			args.tolerance = parseTolerance(name, value);
			continue;
		}
		args[stringKey] = value;
	}

	if (args.help) return args;
	if (args.models.length === 0) {
		throw usageError("--model is required (comma-separated model aliases)");
	}
	args.modes = [...new Set(args.modes)];
	for (const mode of args.modes) {
		if (mode !== "off" && mode !== "on") {
			throw usageError(`invalid --modes value: ${JSON.stringify(mode)} (allowed: off,on)`);
		}
	}
	for (const [flag, aliases] of [
		["--anthropic-alias", args.anthropicAliases],
		["--informational-alias", args.informationalAliases],
	]) {
		for (const alias of aliases) {
			if (!args.models.includes(alias)) {
				throw usageError(`${flag} value ${JSON.stringify(alias)} is not in --model`);
			}
		}
	}
	if (args.padTokens > MAX_PAD_TOKENS) {
		throw usageError(
			`--pad-tokens ${args.padTokens} exceeds ${MAX_PAD_TOKENS}: the read tool caps a single output at 65536 runes, so more padding is silently truncated`,
		);
	}
	return args;
}

/** Return null when the live-bench opt-in is set, else the refusal message. */
export function checkGuard(env) {
	if (env && env.STEINER_LIVE_BENCH === "1") return null;
	return "refusing to run: the compaction bench makes real provider calls and spends real tokens; set STEINER_LIVE_BENCH=1 to confirm";
}

/** Build the child environment: the experiment flag plus only the three segment limit passthroughs. */
export function experimentEnv(env, mode) {
	const flag = mode === "on" ? "1" : mode === "off" ? "0" : null;
	if (flag === null) throw new Error(`unknown mode: ${JSON.stringify(mode)} (allowed: off,on)`);
	const out = { STEINER_EXPERIMENT_SEGMENTED_COMPACTION: flag };
	for (const key of SEGMENT_PASSTHROUGH) {
		if (env && env[key] !== undefined) out[key] = env[key];
	}
	return out;
}

const FILLER_WORDS = [
	"haystack", "meadow", "lantern", "harbour", "tram", "copper", "saffron", "brisk",
	"delta", "wobble", "pantry", "hollow", "gantry", "moss", "plume", "quartz",
];

const CODE_LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
const CODE_DIGITS = "0123456789";

function mulberry32(seed) {
	let state = seed >>> 0;
	return function next() {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

function pick(rand, alphabet) {
	return alphabet[Math.floor(rand() * alphabet.length)];
}

function nextCode(rand, used) {
	for (;;) {
		const code = `${pick(rand, CODE_LETTERS)}${pick(rand, CODE_DIGITS)}-${pick(rand, CODE_LETTERS)}${pick(rand, CODE_LETTERS)}${pick(rand, CODE_DIGITS)}${pick(rand, CODE_DIGITS)}`;
		if (!used.has(code)) {
			used.add(code);
			return code;
		}
	}
}

// Filler lines stay just under the read tool's 2000-rune per-line clip
// (internal/tool/builtin/render_bounds.go), so a file's padding arrives in a
// few lines and a default 200-line read returns it in one page.
const FILLER_LINE_TARGET_CHARS = 1900;

function fillerLines(rand, targetChars) {
	const lines = [];
	let total = 0;
	while (total < targetChars) {
		const limit = Math.min(FILLER_LINE_TARGET_CHARS, targetChars - total);
		const words = [];
		let length = 0;
		while (length < limit) {
			const word = FILLER_WORDS[Math.floor(rand() * FILLER_WORDS.length)];
			const extra = words.length === 0 ? word.length : word.length + 1;
			if (words.length > 0 && length + extra > limit) break;
			words.push(word);
			length += extra;
		}
		const line = words.join(" ");
		lines.push(line);
		total += line.length + 1;
	}
	return lines;
}

/** Generate the deterministic notes chain, its codes and the seeded correction. */
export function generateFixtures(seed, files, padTokens) {
	if (!Number.isInteger(files) || files < 4 || files > 99) {
		throw new Error(`files must be an integer from 4 to 99, got ${JSON.stringify(files)}`);
	}
	if (!Number.isInteger(padTokens) || padTokens < 0) {
		throw new Error(`padTokens must be a non-negative integer, got ${JSON.stringify(padTokens)}`);
	}
	const rand = mulberry32(seed);
	const names = Array.from({ length: files }, (_, i) => `f${String(i + 1).padStart(2, "0")}`);
	const used = new Set();
	const codes = {};
	for (const name of names) codes[name] = nextCode(rand, used);

	// The correction must land strictly after its target f03 and in the second
	// half, so a small chain (files=4) still carries it in f04 and never f01-f03.
	const firstCandidate = Math.max(3, Math.floor(files / 2));
	const inFile = names[firstCandidate + Math.floor(rand() * (files - firstCandidate))];
	const newCode = nextCode(rand, used);
	const correction = { inFile, target: "f03", oldCode: codes.f03, newCode };

	const outFiles = names.map((name, index) => {
		const next = index + 1 < names.length ? `${names[index + 1]}.txt` : "END";
		const lines = [`CODE: ${codes[name]}`, `NEXT: ${next}`];
		if (name === inFile) {
			lines.push(`CORRECTION: the code in ${correction.target} is now ${newCode}`);
		}
		lines.push(...fillerLines(rand, padTokens * 4));
		return { name: `notes/${name}.txt`, content: `${lines.join("\n")}\n` };
	});

	return { files: outFiles, codes, correction };
}

/** Render the task prompt that walks the generated notes chain one file at a time. */
export function taskPrompt(files, runId = null) {
	const lines = [];
	// A per-run first line keeps every run's first request byte-distinct, so no
	// run starts from a cache entry warmed by another run.
	if (runId !== null) lines.push(`Run id: ${runId}`);
	lines.push(
		`Read the notes in notes/ one file at a time. The chain has ${files.length} files.`,
		"Start at notes/f01.txt. Each file starts with a CODE line and a NEXT line naming the next file.",
		"Open only the file named by NEXT. Never read a file twice. Never list or search the notes directory.",
		"Use only the read tool on each file. Do not use bash, grep, cat, sed or any other command to inspect files.",
		"Apply any CORRECTION line you find to the code it names.",
		'When you reach NEXT: END, reply with one line per file in the form "fNN: CODE", using the corrected code where a correction applies.',
		"Reply with those lines only.",
	);
	return lines.join("\n");
}

const ALIAS_RE = /^[A-Za-z0-9._-]+$/;

function diagnosticsLines(diagnosticsDir) {
	if (typeof diagnosticsDir !== "string" || diagnosticsDir === "") {
		throw new Error("diagnosticsDir must be a non-empty string");
	}
	return [
		"diagnostics:",
		"  enabled: true",
		`  dir: ${JSON.stringify(diagnosticsDir)}`,
		"  streams:",
		"    cache: true",
	];
}

/** Render the per-alias YAML config with a low compaction threshold and the cache diagnostics stream. */
export function buildBenchConfig({ alias, threshold, diagnosticsDir }) {
	if (typeof alias !== "string" || !ALIAS_RE.test(alias)) {
		throw new Error(`invalid model alias: ${JSON.stringify(alias)} (allowed: letters, digits, dot, underscore, dash)`);
	}
	if (!Number.isInteger(threshold) || threshold <= 0) {
		throw new Error(`threshold must be a positive integer, got ${JSON.stringify(threshold)}`);
	}
	return [
		"models:",
		"  definitions:",
		`    ${alias}:`,
		"      advanced:",
		"        limits:",
		`          compaction_threshold_tokens: ${threshold}`,
		...diagnosticsLines(diagnosticsDir),
		"",
	].join("\n");
}

/** Render the probe's YAML config: cache diagnostics only, so no threshold can exceed the alias context window. */
export function buildProbeConfig({ diagnosticsDir }) {
	return [...diagnosticsLines(diagnosticsDir), ""].join("\n");
}

const SEGMENT_DECISION_RE = /^segment_decision action=(append|rebase|plain) reason=([a-z][a-z-]*) sequence=(\d+) chain_tokens=(\d+) projected_post_tokens=(\d+) budget=(\d+)$/;

/** Parse whole-line segment_decision records from a compaction log, ignoring embedded text. */
export function parseSegmentDecisions(text) {
	const decisions = [];
	for (const raw of String(text).split(/\r?\n/)) {
		const match = SEGMENT_DECISION_RE.exec(raw.trim());
		if (!match) continue;
		decisions.push({
			action: match[1],
			reason: match[2],
			sequence: Number(match[3]),
			chainTokens: Number(match[4]),
			projectedPostTokens: Number(match[5]),
			budget: Number(match[6]),
		});
	}
	return decisions;
}

/** Count decision kinds; plain counts as a rebase fallback and separately as a plain fallback. */
export function summarizeDecisions(decisions) {
	let appends = 0;
	let rebases = 0;
	let plainFallbacks = 0;
	let chainStarts = 0;
	let maxSegments = 0;
	for (const decision of decisions) {
		if (decision.action === "append") {
			appends++;
			maxSegments = Math.max(maxSegments, decision.sequence);
		} else if (decision.action === "rebase") {
			if (decision.reason !== "chain-start") rebases++;
			maxSegments = Math.max(maxSegments, decision.sequence);
		} else {
			plainFallbacks++;
			rebases++;
		}
		if (decision.reason === "chain-start") chainStarts++;
	}
	return { decisions: decisions.length, appends, rebases, plainFallbacks, chainStarts, maxSegments };
}

const READ_NOTES_RE = /(?:^|\/)notes\/(f\d+)\.txt$/;
const READ_BASENAME_RE = /(?:^|\/)(f\d+)\.txt$/;
// A fixture path starts its value or follows a shell-ish boundary, so a value
// that merely contains "notes" (release-notes/, footnotes/, docs/notes/) does
// not count. A fixture file name is inside that same directory.
const FIXTURE_PATH_RE = /(?:^|[\s"'=:(])(?:\.\/)?notes(?:\/|$|[\s"')])/;
const COMPACTION_START_SEVERITIES = new Set(["compacting", "skipped"]);

function readFileName(path) {
	const match = READ_NOTES_RE.exec(path) ?? READ_BASENAME_RE.exec(path);
	return match ? match[1] : null;
}

function stringValues(value, out = []) {
	if (typeof value === "string") out.push(value);
	else if (Array.isArray(value)) for (const item of value) stringValues(item, out);
	else if (value !== null && typeof value === "object") for (const item of Object.values(value)) stringValues(item, out);
	return out;
}

function referencesFixture(argumentValue) {
	return stringValues(argumentValue).some((text) => FIXTURE_PATH_RE.test(text));
}

function scopedToSubAgent(record) {
	const scope = record.scope;
	return Boolean(scope && typeof scope === "object" && scope.agent_type);
}

function parseModelCall(payload) {
	const hasRead = payload.cache_read_tokens !== undefined;
	const hasCreate = payload.cache_create_tokens !== undefined;
	const cacheReported = payload.cache_reported_input_tokens !== undefined || hasRead || hasCreate;
	return {
		turn: payload.turn ?? 0,
		promptTokens: payload.prompt_tokens ?? 0,
		cacheReported,
		cacheReadTokens: payload.cache_read_tokens ?? 0,
		cacheCreateTokens: payload.cache_create_tokens ?? 0,
	};
}

function parseCompactionCall(payload) {
	const hasRead = payload.cache_read_tokens !== undefined;
	const hasCreate = payload.cache_create_tokens !== undefined;
	const cacheReported = payload.cache_reported_input_tokens !== undefined || hasRead || hasCreate;
	// The applied compaction payload's prompt_tokens is the post-compaction prompt
	// estimate, not the summarizer's input (internal/agent/events.go sets it from
	// afterFit). The summary call total is uncached input plus both cache counters.
	const promptTokens = (payload.input_tokens ?? 0) + (payload.cache_read_tokens ?? 0) + (payload.cache_create_tokens ?? 0);
	return {
		promptTokens,
		cacheReported,
		cacheReadTokens: payload.cache_read_tokens ?? 0,
		cacheCreateTokens: payload.cache_create_tokens ?? 0,
	};
}

/** Parse a session log JSONL into ordered reads, applied compactions and model-call cache usage. */
export function parseRunLog(text) {
	const events = [];
	const modelCalls = [];
	const compactionCalls = [];
	let compactions = 0;
	let malformed = 0;
	let nonReadFileAccess = 0;

	for (const raw of String(text).split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "") continue;
		let record;
		try {
			record = JSON.parse(line);
		} catch {
			malformed++;
			continue;
		}
		if (record === null || typeof record !== "object" || Array.isArray(record)) {
			malformed++;
			continue;
		}
		const payload = record.payload && typeof record.payload === "object" ? record.payload : {};
		if (record.type === "tool_call_started") {
			if (scopedToSubAgent(record)) {
				// A sub-agent cannot carry the main conversation's context, so fixture
				// access there scores nothing, not even through the read tool.
				if (referencesFixture(payload.arguments)) nonReadFileAccess++;
				continue;
			}
			if (payload.tool !== "read") {
				// The task allows only the read tool; any other tool that reaches the
				// fixtures wins the score outside the scored access path.
				if (referencesFixture(payload.arguments)) nonReadFileAccess++;
				continue;
			}
			const path = payload.arguments && payload.arguments.path;
			if (typeof path !== "string") continue;
			const file = readFileName(path);
			if (!file) continue;
			events.push({ kind: "read", file, turn: payload.turn ?? 0 });
			continue;
		}
		if (record.type === "model_call_finished") {
			// Failed calls carry no usable usage and sub-agent calls are not part of the
			// main conversation's cache economics; both stay out, as they do for reads
			// and compactions.
			if (payload.success !== true) continue;
			if (scopedToSubAgent(record)) continue;
			modelCalls.push(parseModelCall(payload));
			continue;
		}
		if (record.type === "context_diagnostics") {
			if (payload.kind !== "compaction") continue;
			if (COMPACTION_START_SEVERITIES.has(payload.severity)) continue;
			if (scopedToSubAgent(record)) continue;
			compactions++;
			events.push({ kind: "compaction", index: compactions });
			compactionCalls.push(parseCompactionCall(payload));
		}
	}

	return { events, modelCalls, compactionCalls, compactions, malformed, nonReadFileAccess };
}

/** Return only the ordered read events from a session log. */
export const parseToolReads = (text) => parseRunLog(text).events.filter((event) => event.kind === "read");

const EVENT_LABEL_RE = /^[a-z_]+: /;
const MAX_TURNS_RE = /^status: stopped after \d+ turns?: reached the max turn limit/;
const ASSISTANT_ANSWER_RE = /^turn=\d+ role=\S+ content=([\s\S]*)$/;

/** Parse exec stdout into the last assistant answer and whether the max-turn limit stopped the run. */
export function parseStdout(text) {
	const lines = String(text).split(/\r?\n/);
	let maxTurnsHit = false;
	for (const line of lines) {
		if (MAX_TURNS_RE.test(line)) maxTurnsHit = true;
	}

	let finalAnswer = null;
	for (let i = 0; i < lines.length; i++) {
		const line = lines[i];
		if (!line.startsWith("assistant: ")) continue;
		const match = ASSISTANT_ANSWER_RE.exec(line.slice("assistant: ".length));
		if (!match) continue;
		const parts = [match[1]];
		let j = i + 1;
		for (; j < lines.length; j++) {
			const next = lines[j];
			if (EVENT_LABEL_RE.test(next) || next.startsWith("assistant> ")) break;
			parts.push(next);
		}
		finalAnswer = parts.join("\n").trimEnd();
		i = j - 1;
	}

	return { finalAnswer, maxTurnsHit };
}

function lossPoint(events, file) {
	const firstRead = events.findIndex((event) => event.kind === "read" && event.file === file);
	if (firstRead === -1) return "unattributed";
	const compaction = events.slice(firstRead + 1).find((event) => event.kind === "compaction");
	return compaction ? compaction.index : "unattributed";
}

/** Score the final answer against the fixtures, attributing each miss and flagging re-reads. */
export function scoreRecall({ answer, fixtures, events }) {
	const answerLines = new Map();
	const text = typeof answer === "string" ? answer : "";
	const lineRe = /^\s*(f\d{2})\s*:\s*(\S+)\s*$/gim;
	let match;
	while ((match = lineRe.exec(text)) !== null) {
		answerLines.set(match[1].toLowerCase(), match[2]);
	}

	const names = fixtures.files.map((file) => file.name.replace(/^notes\//, "").replace(/\.txt$/, ""));
	const perFile = [];
	let exact = 0;
	for (const name of names) {
		const isTarget = name === fixtures.correction.target;
		const expected = isTarget ? fixtures.correction.newCode : fixtures.codes[name];
		const got = answerLines.get(name.toLowerCase());
		let ok = got !== undefined && got === expected;
		if (isTarget) ok = ok && !text.includes(fixtures.correction.oldCode);
		if (ok) {
			exact++;
			perFile.push({ file: name, status: "exact", lostAt: null });
		} else {
			perFile.push({ file: name, status: "lost", lostAt: lossPoint(events, name) });
		}
	}

	const invalidReasons = [];
	for (const name of names) {
		const reads = [];
		events.forEach((event, position) => {
			if (event.kind === "read" && event.file === name) reads.push(position);
		});
		if (reads.length < 2) continue;
		// Any later read of the file counts, not just the second one: a compaction
		// between the first read and a later one still drops the file from context.
		const firstCompaction = events.findIndex((event, position) => position > reads[0] && event.kind === "compaction");
		if (firstCompaction === -1) continue;
		if (!reads.some((position) => position > firstCompaction)) continue;
		invalidReasons.push(`${name} read again after compaction ${events[firstCompaction].index}`);
	}

	return {
		exact,
		lost: perFile.length - exact,
		total: perFile.length,
		invalid: invalidReasons.length > 0,
		invalidReason: invalidReasons.length > 0 ? invalidReasons.join("; ") : null,
		perFile,
	};
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
	return runs.map((run) => run[key]).filter((value) => typeof value === "number" && Number.isFinite(value));
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
		rebases: median(numbers(sample, "rebases")),
		plainFallbacks: median(numbers(sample, "plainFallbacks")),
		maxSegments: median(numbers(sample, "maxSegments")),
		cachedTokens: median(numbers(sample, "cachedTokens")),
		costPerTurn: median(numbers(sample, "costPerTurn")),
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

function isNum(value) {
	return typeof value === "number" && Number.isFinite(value);
}

function numText(value) {
	if (!isNum(value)) return "not reported";
	return Number.isInteger(value) ? String(value) : value.toFixed(2);
}

function cacheNote(off, on, family, probeOk) {
	if (!isNum(off.costPerTurn) || !isNum(on.costPerTurn)) return "cost proxy not judged: no cache reporting on one side";
	if (family === "anthropic" && probeOk !== true) return "cost proxy not judged: anthropic probe did not confirm cache control";
	return "cost proxy not judged";
}

/** Compare the two mode aggregates into a verdict label, cache state and reasons. */
export function verdict({ off, on, tolerance, informational, probeOk, family }) {
	const cacheChecked = isNum(off.costPerTurn) && isNum(on.costPerTurn) && !(family === "anthropic" && probeOk !== true);
	const cache = cacheChecked ? "checked" : "no cache verdict";
	const reasons = [];

	if (!off.valid || !on.valid || off.recallInvalid || on.recallInvalid || off.exact === 0) {
		if (!off.valid) reasons.push(`off: ${off.invalidReason ?? "run invalid"}`);
		if (!on.valid) reasons.push(`on: ${on.invalidReason ?? "run invalid"}`);
		if (off.recallInvalid) reasons.push("off: a fixture file was read again after a compaction");
		if (on.recallInvalid) reasons.push("on: a fixture file was read again after a compaction");
		// Continuity is relative, so 0/N on both sides would otherwise pass.
		if (off.exact === 0) reasons.push("off mode recalled nothing");
		return { label: "INVALID", cache, reasons };
	}

	if (Math.abs(off.compactions - on.compactions) > 1) {
		reasons.push(`compaction counts differ: off ${off.compactions} vs on ${on.compactions}`);
		return { label: "INCOMPARABLE", cache, reasons };
	}

	if (informational) {
		reasons.push("informational alias: no pass or fail verdict");
		return { label: "INFORMATIONAL", cache, reasons };
	}

	if (!cacheChecked) reasons.push(cacheNote(off, on, family, probeOk));

	// A max-turn stop stays scored, so name it here rather than invalidating the run.
	const maxTurnsRuns = (off.maxTurnsHitRuns ?? 0) + (on.maxTurnsHitRuns ?? 0);
	if (maxTurnsRuns > 0) reasons.push(`max turns hit in ${maxTurnsRuns} run(s)`);

	let failed = false;
	if (on.exact < off.exact * (1 - tolerance / 100)) {
		reasons.push(`continuity: on exact ${on.exact} below off ${off.exact} by more than ${tolerance}%`);
		failed = true;
	}
	if (cacheChecked && on.costPerTurn > off.costPerTurn * (1 + tolerance / 100)) {
		reasons.push(`cost proxy: on ${numText(on.costPerTurn)} above off ${numText(off.costPerTurn)} by more than ${tolerance}%`);
		failed = true;
	}
	if (failed) return { label: "FAIL", cache, reasons };

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

function renderAlias(result) {
	const off = result.off;
	const on = result.on;
	const lines = [
		`### ${result.alias} (${result.family})`,
		"",
		"| metric | off | on |",
		"| --- | --- | --- |",
		`| turns | ${cell(off, (agg) => numText(agg.turns))} | ${cell(on, (agg) => numText(agg.turns))} |`,
		`| max turns hit | ${cell(off, (agg) => numText(agg.maxTurnsHitRuns))} | ${cell(on, (agg) => numText(agg.maxTurnsHitRuns))} |`,
		`| compactions | ${cell(off, (agg) => numText(agg.compactions))} | ${cell(on, (agg) => numText(agg.compactions))} |`,
		`| rebases | ${cell(off, (agg) => numText(agg.rebases))} | ${cell(on, (agg) => numText(agg.rebases))} |`,
		`| plain fallbacks | ${cell(off, (agg) => numText(agg.plainFallbacks))} | ${cell(on, (agg) => numText(agg.plainFallbacks))} |`,
		`| max segments | ${cell(off, (agg) => numText(agg.maxSegments))} | ${cell(on, (agg) => numText(agg.maxSegments))} |`,
		`| cached tokens | ${cell(off, (agg) => numText(agg.cachedTokens))} | ${cell(on, (agg) => numText(agg.cachedTokens))} |`,
		`| cost proxy per turn (${COST_LABELS[result.family]}) | ${cell(off, (agg) => numText(agg.costPerTurn))} | ${cell(on, (agg) => numText(agg.costPerTurn))} |`,
		`| continuity | ${cell(off, continuityCell)} | ${cell(on, continuityCell)} |`,
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

const SANDBOX_NOTE = "not verified (steiner exposes no read-only check)";

// Live resources for signal cleanup: the CLI installs SIGINT and SIGTERM
// handlers, so an interrupted bench must not leave steiner process groups or
// scratch directories behind.
const liveProcessGroups = new Set();
const liveTempDirs = new Set();

/** Register a detached child's process group so a signal can kill it. */
export function registerProcessGroup(pid) {
	liveProcessGroups.add(pid);
}

/** Register a scratch directory so a signal can remove it. */
export function registerTempDir(dir) {
	liveTempDirs.add(dir);
	return dir;
}

function killProcessGroup(pid) {
	// A missing or invalid pid would make the negated value signal this process
	// group instead of the child's.
	if (!Number.isInteger(pid) || pid <= 0) return;
	try {
		process.kill(-pid, "SIGKILL");
	} catch {
		// ESRCH means the group is already gone; cleanup must continue either way.
	}
}

/** Kill every registered process group and remove every registered directory. */
export function cleanupLiveResources() {
	const processes = liveProcessGroups.size;
	const dirs = liveTempDirs.size;
	for (const pid of liveProcessGroups) {
		killProcessGroup(pid);
		liveProcessGroups.delete(pid);
	}
	for (const dir of liveTempDirs) {
		try {
			rmSync(dir, { recursive: true, force: true });
		} catch {
			// A directory that cannot be removed must not stop the remaining cleanup.
		}
		liveTempDirs.delete(dir);
	}
	return { processes, dirs };
}

async function removeTempDir(dir) {
	liveTempDirs.delete(dir);
	await rm(dir, { recursive: true, force: true });
}

function installSignalHandlers() {
	for (const [signal, code] of [["SIGINT", 130], ["SIGTERM", 143]]) {
		process.once(signal, () => {
			cleanupLiveResources();
			process.exit(code);
		});
	}
}

/**
 * Spawn the run detached so a timeout can kill the whole process group, and turn
 * a spawn failure into a failed result instead of an uncaught exception.
 */
export function defaultSpawnRun({ command, args, cwd, env, stdoutPath, stderrPath, timeoutMs }) {
	return new Promise((resolve) => {
		let stdoutFd = null;
		let stderrFd = null;
		let child = null;
		let timer = null;
		let timedOut = false;
		let settled = false;
		const finish = (result) => {
			if (settled) return;
			settled = true;
			if (timer !== null) clearTimeout(timer);
			if (stdoutFd !== null) closeSync(stdoutFd);
			if (stderrFd !== null) closeSync(stderrFd);
			if (child?.pid !== undefined) liveProcessGroups.delete(child.pid);
			resolve({ timedOut, ...result });
		};
		try {
			stdoutFd = openSync(stdoutPath, "w", 0o600);
			stderrFd = openSync(stderrPath, "w", 0o600);
			child = spawn(command, args, { cwd, env, detached: true, stdio: ["ignore", stdoutFd, stderrFd] });
			// A spawn that fails to start reports no pid, and the error event settles it.
			if (child.pid !== undefined) registerProcessGroup(child.pid);
		} catch (error) {
			finish({ code: null, error: error.message });
			return;
		}
		// The timer is armed only after a live child exists, so a failed spawn never
		// leaves a kill timer pointed at a missing pid.
		timer = setTimeout(() => {
			timedOut = true;
			killProcessGroup(child.pid);
		}, timeoutMs);
		child.on("error", (error) => finish({ code: null, error: error.message }));
		child.on("close", (code) => {
			// The run exited but its group can still hold background descendants.
			if (child.pid !== undefined) killProcessGroup(child.pid);
			finish({ code });
		});
	});
}

function parseSandboxEnabled(text) {
	let inSandbox = false;
	for (const raw of String(text).split(/\r?\n/)) {
		if (/^sandbox:\s*$/.test(raw)) {
			inSandbox = true;
			continue;
		}
		if (!inSandbox) continue;
		if (/^\S/.test(raw)) return null;
		const match = /^\s+enabled:\s*(true|false)\s*$/.exec(raw);
		if (match) return match[1] === "true";
	}
	return null;
}

async function detectSandbox({ opts, env, spawnRun }) {
	const dir = registerTempDir(await mkdtemp(join(tmpdir(), "compaction-bench-config-")));
	try {
		const stdoutPath = join(dir, "config.stdout.txt");
		const stderrPath = join(dir, "config.stderr.txt");
		const child = await spawnRun({
			command: opts.steiner,
			args: ["config"],
			cwd: dir,
			env,
			stdoutPath,
			stderrPath,
			timeoutMs: 30_000,
		});
		if (child.timedOut || child.code !== 0) return null;
		return parseSandboxEnabled(await readTextOrEmpty(stdoutPath));
	} catch {
		// Sandbox status is optional: an unreadable config is reported as not verified.
		return null;
	} finally {
		await removeTempDir(dir);
	}
}

async function readTextOrEmpty(path) {
	try {
		return await readFile(path, "utf8");
	} catch (error) {
		if (error.code === "ENOENT") return "";
		throw error;
	}
}

async function copyIfExists(from, to) {
	try {
		await copyFile(from, to);
	} catch (error) {
		if (error.code !== "ENOENT") throw error;
	}
}

function lostEntries(recall) {
	const entries = [];
	for (const file of recall.perFile) {
		if (file.status !== "lost") continue;
		entries.push(`${file.file}@${file.lostAt === "unattributed" ? "unattributed" : `c${file.lostAt}`}`);
	}
	return entries;
}

function probePrompt(nonce) {
	// The nonce makes each probe's first call a cache write even when an earlier
	// run or a second alias already warmed the identical probe text.
	const lines = [`Probe id: ${nonce}`];
	let chars = 0;
	for (let i = 0; chars < 9000; i++) {
		const line = `Probe line ${i}: the quick brown fox jumps over the lazy dog while the prompt cache warms up.`;
		lines.push(line);
		chars += line.length + 1;
	}
	lines.push("Reply with OK only.");
	return lines.join("\n");
}

const PROBE_FAILURE_PATTERNS = [
	[/\b401\b|unauthori[sz]ed/i, "auth error (401)"],
	[/\b403\b|forbidden/i, "forbidden (403)"],
	[/\b404\b|not found/i, "not found (404)"],
	[/\b400\b/, "bad request (400)"],
	[/timeout/i, "timeout"],
];

/** Map probe stderr to a fixed label; raw stderr text never reaches the report. */
export function classifyProbeFailure(text) {
	const body = String(text);
	for (const [pattern, label] of PROBE_FAILURE_PATTERNS) {
		if (pattern.test(body)) return label;
	}
	return null;
}

async function gitInit(spawnRun, dir, env, timeoutMs) {
	const init = await spawnRun({
		command: "git",
		args: ["init", "-q"],
		cwd: dir,
		env,
		stdoutPath: join(dir, "git-init.stdout.txt"),
		stderrPath: join(dir, "git-init.stderr.txt"),
		timeoutMs,
	});
	if (init.timedOut) throw new Error("git init timed out");
	if (init.code !== 0) throw new Error(`git init failed: ${init.error ?? `exit ${init.code}`}`);
}

async function createScratchRepo(spawnRun, env, timeoutMs) {
	// Register before git init can fail, so a scratch repo that never becomes
	// usable is still removed by signal cleanup.
	const dir = registerTempDir(await mkdtemp(join(tmpdir(), "compaction-bench-repo-")));
	await gitInit(spawnRun, dir, env, timeoutMs);
	return dir;
}

function analyzeRun({ stdoutText, logText, compactionText, fixtures, mode, family }) {
	const stdout = parseStdout(stdoutText);
	const runLog = parseRunLog(logText);
	const decisionSummary = summarizeDecisions(parseSegmentDecisions(compactionText));
	const recall = scoreRecall({ answer: stdout.finalAnswer, fixtures, events: runLog.events });
	// Compaction summary calls cost real tokens, so the cost and cache totals fold
	// them in; turns stays the main-call count that costPerTurn divides by.
	const agg = aggregateCache([...runLog.modelCalls, ...runLog.compactionCalls]);
	const cost = costProxy(agg, family);
	const turns = runLog.modelCalls.length;
	const validity = runValidity({
		mode,
		compactions: runLog.compactions,
		decisionSummary,
		finalAnswer: stdout.finalAnswer,
		nonReadFileAccess: runLog.nonReadFileAccess,
	});
	return {
		turns,
		compactions: runLog.compactions,
		rebases: decisionSummary.rebases,
		plainFallbacks: decisionSummary.plainFallbacks,
		maxSegments: decisionSummary.maxSegments,
		cachedTokens: agg.reported ? agg.cacheReadTokens : null,
		costPerTurn: cost !== null && turns > 0 ? cost / turns : null,
		exact: recall.exact,
		lost: recall.lost,
		total: recall.total,
		recallInvalid: recall.invalid,
		recallInvalidReason: recall.invalidReason,
		lostAt: lostEntries(recall),
		valid: validity.valid,
		invalidReason: validity.reason,
		maxTurnsHit: stdout.maxTurnsHit,
		malformed: runLog.malformed,
	};
}

function failedRun({ mode, repeat, reason, exitCode = null, timedOut = false }) {
	return {
		mode,
		repeat,
		failed: true,
		timedOut,
		exitCode,
		reason,
		turns: null,
		compactions: null,
		rebases: null,
		plainFallbacks: null,
		maxSegments: null,
		cachedTokens: null,
		costPerTurn: null,
		exact: null,
		lost: null,
		total: null,
		recallInvalid: false,
		recallInvalidReason: null,
		lostAt: [],
		valid: false,
		invalidReason: `run failed: ${reason}`,
		maxTurnsHit: false,
		malformed: 0,
	};
}

async function runProbe({ opts, alias, env, spawnRun, newId }) {
	const timeoutMs = opts.runTimeoutMinutes * 60_000;
	let repoDir = null;
	let privDir = null;
	try {
		const probeText = probePrompt(newId());
		repoDir = await createScratchRepo(spawnRun, env, timeoutMs);
		privDir = registerTempDir(await mkdtemp(join(tmpdir(), "compaction-bench-priv-")));
		const diagDir = join(privDir, "diag");
		await mkdir(diagDir, { recursive: true });
		const configPath = join(privDir, "probe.yaml");
		await writeFile(configPath, buildProbeConfig({ diagnosticsDir: diagDir }));
		const calls = [];
		const exits = [];
		for (const attempt of [1, 2]) {
			const logPath = join(privDir, `probe-${attempt}.log.jsonl`);
			const stderrPath = join(privDir, `probe-${attempt}.stderr.txt`);
			const child = await spawnRun({
				command: opts.steiner,
				args: [
					"--exec",
					"--model",
					alias,
					"--max-turns",
					"1",
					"--compaction-log-file",
					join(privDir, `probe-${attempt}-compaction-log.txt`),
					"--log-file",
					logPath,
					"--config",
					configPath,
					"--trust-project-config",
					probeText,
				],
				cwd: repoDir,
				env,
				stdoutPath: join(privDir, `probe-${attempt}.stdout.txt`),
				stderrPath,
				timeoutMs,
			});
			exits.push(child.timedOut ? "timeout" : child.code);
			if (child.timedOut || child.code !== 0) {
				const status = child.timedOut ? "timeout" : child.code === null ? "spawn error" : `exit ${child.code}`;
				const kind = child.timedOut ? null : classifyProbeFailure(await readTextOrEmpty(stderrPath));
				return { ok: false, reason: kind ? `probe calls failed: ${status}: ${kind}` : `probe calls failed: ${status}`, exits };
			}
			calls.push(parseRunLog(await readTextOrEmpty(logPath)).modelCalls);
		}
		return { ...evaluateProbe(calls[0], calls[1]), exits };
	} catch (error) {
		return { ok: false, reason: `probe calls failed: ${error.message}`, exits: [] };
	} finally {
		if (repoDir) await removeTempDir(repoDir);
		if (privDir) await removeTempDir(privDir);
	}
}

async function runOnce({ opts, alias, family, mode, repeat, runId, fixtures, out, env, spawnRun }) {
	const timeoutMs = opts.runTimeoutMinutes * 60_000;
	const base = `${mode}-r${repeat}`;
	const runOutDir = join(out, alias);
	await mkdir(runOutDir, { recursive: true });
	let repoDir = null;
	let privDir = null;
	try {
		repoDir = await createScratchRepo(spawnRun, env, timeoutMs);
		await mkdir(join(repoDir, "notes"));
		for (const file of fixtures.files) {
			await writeFile(join(repoDir, file.name), file.content);
		}
		privDir = registerTempDir(await mkdtemp(join(tmpdir(), "compaction-bench-priv-")));
		const diagDir = join(privDir, "diag");
		await mkdir(diagDir, { recursive: true });
		const configPath = join(privDir, "bench.yaml");
		await writeFile(configPath, buildBenchConfig({ alias, threshold: opts.threshold, diagnosticsDir: diagDir }));
		const logPath = join(privDir, "run.log.jsonl");
		const compactionPath = join(privDir, "compaction-log.txt");
		const stdoutPath = join(runOutDir, `${base}.stdout.txt`);
		const stderrPath = join(runOutDir, `${base}.stderr.txt`);
		const child = await spawnRun({
			command: opts.steiner,
			args: [
				"--exec",
				"--model",
				alias,
				"--max-turns",
				String(opts.maxTurns),
				"--compaction-log-file",
				compactionPath,
				"--log-file",
				logPath,
				"--config",
				configPath,
				"--trust-project-config",
				taskPrompt(fixtures.files, runId),
			],
			cwd: repoDir,
			env: { ...env, ...experimentEnv(env, mode) },
			stdoutPath,
			stderrPath,
			timeoutMs,
		});
		const metrics = analyzeRun({
			stdoutText: await readTextOrEmpty(stdoutPath),
			logText: await readTextOrEmpty(logPath),
			compactionText: await readTextOrEmpty(compactionPath),
			fixtures,
			mode,
			family,
		});
		await copyIfExists(logPath, join(runOutDir, `${base}.run.log.jsonl`));
		await copyIfExists(compactionPath, join(runOutDir, `${base}.compaction-log.txt`));
		let reason = null;
		if (child.timedOut) reason = "timeout";
		else if (child.code !== 0) reason = child.error ?? `exit ${child.code ?? "unknown"}`;
		return {
			mode,
			repeat,
			failed: reason !== null,
			timedOut: child.timedOut,
			exitCode: child.code,
			reason,
			...metrics,
			valid: reason === null && metrics.valid,
			invalidReason: reason === null ? metrics.invalidReason : `run failed: ${reason}`,
		};
	} catch (error) {
		return failedRun({ mode, repeat, reason: `setup failed: ${error.message}` });
	} finally {
		if (repoDir) await removeTempDir(repoDir);
		if (privDir) await removeTempDir(privDir);
	}
}

function renderResults({ opts, experiment, results, sandbox, timestamp }) {
	return [
		"# Compaction bench",
		"",
		`- generated: ${timestamp}`,
		`- steiner: ${opts.steiner}`,
		`- threshold: ${opts.threshold}`,
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

/** Run the live bench: probe, per-mode exec runs, results.md and raw.json. Returns the exit code. */
export async function main(argv = process.argv.slice(2), env = process.env, deps = {}) {
	const log = deps.log ?? ((line) => process.stderr.write(`${line}\n`));
	const writeOut = deps.stdout ?? ((text) => process.stdout.write(text));
	const spawnRun = deps.spawnRun ?? defaultSpawnRun;
	const now = deps.now ?? (() => new Date());
	const newId = deps.newId ?? (() => randomUUID());
	let opts;
	try {
		opts = parseArgs(argv);
	} catch (error) {
		log(error.message);
		return 2;
	}
	if (opts.help) {
		writeOut(USAGE);
		return 0;
	}
	const blocked = checkGuard(env);
	if (blocked) {
		log(blocked);
		return 1;
	}

	const out = opts.out ?? (await mkdtemp(join(tmpdir(), "compaction-bench-")));
	await mkdir(out, { recursive: true });
	const results = [];
	for (const alias of opts.models) {
		const family = opts.anthropicAliases.includes(alias) ? "anthropic" : "openai";
		const informational = opts.informationalAliases.includes(alias);
		const probe = family === "anthropic" ? await runProbe({ opts, alias, env, spawnRun, newId }) : null;
		if (probe) log(`probe ${alias}: ${probe.ok ? "ok" : `failed (${probe.reason})`}`);
		const runs = { off: [], on: [] };
		for (let repeat = 1; repeat <= opts.repeat; repeat++) {
			const fixtures = generateFixtures(repeat, opts.files, opts.padTokens);
			// Alternate the mode order per repeat so the second mode never starts from
			// a cache entry the first mode just warmed.
			const modes = repeat % 2 === 1 ? opts.modes : [...opts.modes].reverse();
			for (const mode of modes) {
				const run = await runOnce({ opts, alias, family, mode, repeat, runId: newId(), fixtures, out, env, spawnRun });
				runs[mode].push(run);
				const exit = run.timedOut ? "timeout" : run.exitCode === null ? "error" : run.exitCode;
				log(`${alias} mode ${mode} r${repeat} exit ${exit} compactions ${run.compactions ?? "n/a"} exact ${run.exact ?? "n/a"}/${run.total ?? "n/a"}`);
			}
		}
		const summaries = {};
		for (const mode of opts.modes) summaries[mode] = aggregateMode(runs[mode]);
		const off = summaries.off ?? null;
		const on = summaries.on ?? null;
		const aliasVerdict = off && on
			? verdict({ off, on, tolerance: opts.tolerance, informational, probeOk: probe ? probe.ok : undefined, family })
			: { label: "INCOMPARABLE", cache: "no cache verdict", reasons: ["single mode"] };
		results.push({ alias, family, informational, probe, off, on, verdict: aliasVerdict, runs });
	}

	const timestamp = now().toISOString();
	const experiment = experimentEnv(env, "on");
	const sandboxEnabled = await detectSandbox({ opts, env, spawnRun });
	const sandbox = sandboxEnabled === null ? SANDBOX_NOTE : sandboxEnabled ? "enabled" : "disabled";
	const markdown = renderResults({ opts, experiment, results, sandbox, timestamp });
	await writeFile(join(out, "results.md"), markdown);
	const raw = {
		generatedAt: timestamp,
		steiner: opts.steiner,
		out,
		threshold: opts.threshold,
		files: opts.files,
		padTokens: opts.padTokens,
		maxTurns: opts.maxTurns,
		repeat: opts.repeat,
		tolerance: opts.tolerance,
		runTimeoutMinutes: opts.runTimeoutMinutes,
		modes: opts.modes,
		anthropicAliases: opts.anthropicAliases,
		informationalAliases: opts.informationalAliases,
		experimentEnv: experiment,
		sandbox,
		aliases: results.map(({ alias, family, informational, probe, runs }) => ({ alias, family, informational, probe, runs })),
	};
	await writeFile(join(out, "raw.json"), `${JSON.stringify(raw, null, 2)}\n`);
	writeOut(`${markdown}\n`);
	const anyFailed = results.some((result) => [...result.runs.off, ...result.runs.on].some((run) => run.failed));
	return anyFailed ? 3 : 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
	installSignalHandlers();
	process.exitCode = await main();
}
