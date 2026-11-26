import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
	USAGE,
	aggregateCache,
	aggregateMode,
	analyzeRun,
	buildBenchConfig,
	buildProbeConfig,
	checkGuard,
	classifyProbeFailure,
	cleanupLiveResources,
	copyLogGenerations,
	costProxy,
	defaultSpawnRun,
	evaluateProbe,
	experimentEnv,
	generateFixtures,
	main,
	median,
	parseArgs,
	parseProviderLog,
	parseRunLog,
	parseSegmentDecisions,
	parseStdout,
	parseToolReads,
	percentile,
	registerProcessGroup,
	registerTempDir,
	readLogGenerations,
	renderTable,
	runValidity,
	scoreRecall,
	SEGMENT_PASSTHROUGH,
	summarizeDecisions,
	taskPrompt,
	verdict,
} from "./compaction-bench.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureDir = join(here, "..", "testdata", "compaction-bench");
const fixture = (name) => readFileSync(join(fixtureDir, name), "utf8");

// A killed orphan can linger as a zombie until a new parent reaps it, which
// still answers signal 0 on Linux, so check the state when /proc exists.
function processIsGone(pid) {
	try {
		process.kill(pid, 0);
	} catch (error) {
		if (error.code === "ESRCH") return true;
		throw error;
	}
	try {
		const stat = readFileSync(`/proc/${pid}/stat`, "utf8");
		return stat.slice(stat.lastIndexOf(")") + 2).startsWith("Z");
	} catch {
		// Without /proc a live signal-0 answer is the only evidence, so stay with it.
		return false;
	}
}

async function waitUntilGone(pid, timeoutMs = 5000) {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		if (processIsGone(pid)) return true;
		if (Date.now() > deadline) return false;
		await new Promise((resolve) => setTimeout(resolve, 20));
	}
}

// Wait for a CLI run child to announce its pid, failing early if the bench
// process exits first instead of burning the whole timeout.
async function waitForPidFile(pidPath, exited, timeoutMs = 30_000) {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		try {
			const text = readFileSync(pidPath, "utf8").trim();
			if (/^\d+$/.test(text)) return Number(text);
		} catch (error) {
			if (error.code !== "ENOENT") throw error;
		}
		const exit = exited();
		if (exit !== null) throw new Error(`the bench exited before its run child started: ${JSON.stringify(exit)}`);
		if (Date.now() > deadline) throw new Error(`timed out waiting for ${pidPath}`);
		await new Promise((resolve) => setTimeout(resolve, 20));
	}
}

function killQuietly(signal, pid) {
	try {
		process.kill(signal, pid);
	} catch {
		// Best-effort cleanup for a stand-in process that may already be dead.
	}
}

function recallFixtures() {
	return {
		files: [1, 2, 3, 4].map((n) => ({ name: `notes/f0${n}.txt`, content: "" })),
		codes: { f01: "A1-BC23", f02: "C2-DE45", f03: "E3-FG67", f04: "G4-HI89" },
		correction: { inFile: "f04", target: "f03", oldCode: "E3-FG67", newCode: "Z9-YX87" },
	};
}

// Text after the CODE/NEXT/CORRECTION header block, which always comes first.
function fillerText(content) {
	const lines = content.replace(/\n$/, "").split("\n");
	return lines.slice(2 + (lines.some((line) => line.startsWith("CORRECTION:")) ? 1 : 0)).join("\n");
}

// Model one default read page: at most 200 lines, each clipped to 2000 runes,
// total output capped at 65536 runes (internal/tool/builtin/render_bounds.go).
function defaultReadPrefix(content) {
	let total = 0;
	const kept = [];
	for (const [index, line] of content.split("\n").slice(0, 200).entries()) {
		const clipped = line.length > 2000 ? line.slice(0, 2000) : line;
		const sep = index > 0 ? 1 : 0;
		if (total + sep + clipped.length > 65536) break;
		total += sep + clipped.length;
		kept.push(clipped);
	}
	return kept.join("\n");
}

function modeAgg(over = {}) {
	return {
		turns: 10,
		compactions: 5,
		rebases: 1,
		plainFallbacks: 0,
		maxSegments: 3,
		cachedTokens: 5000,
		costPerTurn: 100,
		exact: 24,
		lost: 0,
		total: 24,
		recallInvalid: false,
		maxTurnsHitRuns: 0,
		lostAt: [],
		...over,
	};
}

function verdictSide(over = {}) {
	return {
		valid: true,
		invalidReason: null,
		compactions: 5,
		costPerTurn: 100,
		exact: 24,
		recallInvalid: false,
		maxTurnsHitRuns: 0,
		...over,
	};
}

function verdictFor(over = {}) {
	return verdict({
		off: verdictSide(),
		on: verdictSide(),
		tolerance: 5,
		informational: false,
		probeOk: true,
		family: "anthropic",
		...over,
	});
}

test("parseArgs defaults", () => {
	assert.deepEqual(parseArgs(["--model", "a"]), {
		help: false,
		models: ["a"],
		modelEntries: [{ ref: "a", alias: "a", provider: null, id: null }],
		anthropicAliases: [],
		informationalAliases: [],
		modes: ["off", "on"],
		threshold: 15000,
		contextWindow: null,
		seed: null,
		files: 24,
		padTokens: 3000,
		maxTurns: 80,
		repeat: 1,
		tolerance: 5,
		runTimeoutMinutes: 30,
		steiner: "steiner",
		out: null,
	});
});

test("parseArgs reads every flag, comma lists and the --flag=value form", () => {
	const args = parseArgs([
		"--model=a,b,c",
		"--anthropic-alias", "a",
		"--anthropic-alias", "c",
		"--informational-alias", "b",
		"--modes", "on",
		"--threshold", "8000",
		"--context-window", "1000000",
		"--seed", "7",
		"--files", "6",
		"--pad-tokens", "50",
		"--max-turns", "12",
		"--repeat", "2",
		"--tolerance", "2.5",
		"--run-timeout-minutes", "45",
		"--steiner", "/opt/bin/steiner",
		"--out", "bench.md",
	]);
	assert.deepEqual(args.models, ["a", "b", "c"]);
	assert.deepEqual(args.anthropicAliases, ["a", "c"]);
	assert.deepEqual(args.informationalAliases, ["b"]);
	assert.deepEqual(args.modes, ["on"]);
	assert.equal(args.threshold, 8000);
	assert.equal(args.contextWindow, 1000000);
	assert.equal(args.seed, 7);
	assert.equal(args.files, 6);
	assert.equal(args.padTokens, 50);
	assert.equal(args.maxTurns, 12);
	assert.equal(args.repeat, 2);
	assert.equal(args.tolerance, 2.5);
	assert.equal(args.runTimeoutMinutes, 45);
	assert.equal(args.steiner, "/opt/bin/steiner");
	assert.equal(args.out, "bench.md");
});

test("parseArgs requires --model and carries USAGE in the error", () => {
	assert.throws(
		() => parseArgs([]),
		(err) => {
			assert.match(err.message, /--model is required/);
			assert.ok(err.message.includes(USAGE));
			return true;
		},
	);
});

test("parseArgs rejects invalid integers, tolerance, modes and unknown flags", () => {
	for (const argv of [
		["--model", "a", "--threshold", "0"],
		["--model", "a", "--threshold", "1.5"],
		["--model", "a", "--threshold", "-3"],
		["--model", "a", "--files", "x"],
		["--model", "a", "--repeat", "0"],
		["--model", "a", "--run-timeout-minutes", "0"],
		["--model", "a", "--max-turns", ""],
		["--model", "a", "--pad-tokens", "1e3"],
		["--model", "a", "--tolerance", "-1"],
		["--model", "a", "--tolerance", "abc"],
		["--model", "a", "--modes", "off,on,maybe"],
		["--model", "a", "--modes", ","],
		["--model", "a", "--nope", "1"],
		["--model", "a", "positional"],
		["--model"],
	]) {
		assert.throws(() => parseArgs(argv), /usage:/, `argv ${JSON.stringify(argv)} should throw with usage`);
	}
});

test("parseArgs rejects alias flags whose value is not in --model", () => {
	assert.throws(
		() => parseArgs(["--model", "a", "--anthropic-alias", "b"]),
		/--anthropic-alias value "b" is not in --model/,
	);
	assert.throws(
		() => parseArgs(["--model", "a", "--informational-alias", "b"]),
		/--informational-alias value "b" is not in --model/,
	);
	assert.deepEqual(parseArgs(["--model", "a,b", "--anthropic-alias", "b"]).anthropicAliases, ["b"]);
});

test("parseArgs caps --pad-tokens at the read tool's output size", () => {
	assert.equal(parseArgs(["--model", "a", "--pad-tokens", "0"]).padTokens, 0, "no filler is a valid chain");
	assert.equal(parseArgs(["--model", "a", "--pad-tokens", "16000"]).padTokens, 16000);
	assert.throws(() => parseArgs(["--model", "a", "--pad-tokens", "-1"]), /expected a non-negative integer/);
	assert.throws(
		() => parseArgs(["--model", "a", "--pad-tokens", "16001"]),
		(err) => {
			assert.match(err.message, /--pad-tokens 16001 exceeds 16000/);
			assert.match(err.message, /65536 runes/);
			assert.ok(err.message.includes(USAGE));
			return true;
		},
	);
	assert.throws(() => parseArgs(["--model", "a", "--files", "0"]), /expected a positive integer/);
});

test("parseArgs --help needs no model and prints usage", () => {
	const args = parseArgs(["--help"]);
	assert.equal(args.help, true);
	assert.deepEqual(args.models, []);
	assert.match(USAGE, /usage: node scripts\/compaction-bench\.mjs/);
	assert.match(USAGE, /STEINER_LIVE_BENCH=1/);
});

test("checkGuard only passes on STEINER_LIVE_BENCH=1", () => {
	assert.equal(checkGuard({ STEINER_LIVE_BENCH: "1" }), null);
	const blocked = checkGuard({ STEINER_LIVE_BENCH: "true" });
	assert.equal(typeof blocked, "string");
	assert.match(blocked, /STEINER_LIVE_BENCH=1/);
	assert.match(checkGuard({}), /refusing to run/);
});

test("experimentEnv sets the flag and passes through only the three limit vars", () => {
	assert.deepEqual(experimentEnv({}, "off"), { STEINER_EXPERIMENT_SEGMENTED_COMPACTION: "0" });
	assert.deepEqual(experimentEnv({}, "on"), { STEINER_EXPERIMENT_SEGMENTED_COMPACTION: "1" });
	const env = {
		STEINER_EXPERIMENT_SEGMENT_MAX_COUNT: "7",
		STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT: "40",
		STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT: "55",
		OPENAI_API_KEY: "secret",
		STEINER_EXPERIMENT_OTHER: "leak",
	};
	assert.deepEqual(experimentEnv(env, "on"), {
		STEINER_EXPERIMENT_SEGMENTED_COMPACTION: "1",
		STEINER_EXPERIMENT_SEGMENT_MAX_COUNT: "7",
		STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT: "40",
		STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT: "55",
	});
	assert.throws(() => experimentEnv({}, "sideways"), /unknown mode/);
});

test(".env.dist segment limit variables match the passthrough list", () => {
	const envDist = readFileSync(join(here, "..", ".env.dist"), "utf8");
	const names = envDist
		.split(/\r?\n/)
		.map((line) => line.trim())
		.filter((line) => line !== "" && !line.startsWith("#"))
		.map((line) => line.split("=")[0].trim());
	const limits = names.filter((name) => name.startsWith("STEINER_EXPERIMENT_SEGMENT_"));
	assert.ok(limits.length > 0, ".env.dist documents the segment limits");
	assert.deepEqual([...SEGMENT_PASSTHROUGH].sort(), [...limits].sort());
	// The bench sets the enable flag per mode itself, so .env.dist documents it
	// but it is not one of the passthroughs.
	assert.ok(names.includes("STEINER_EXPERIMENT_SEGMENTED_COMPACTION"));
	assert.ok(!SEGMENT_PASSTHROUGH.includes("STEINER_EXPERIMENT_SEGMENTED_COMPACTION"));
});

test("generateFixtures is deterministic per seed and changes with the seed", () => {
	assert.deepEqual(generateFixtures(7, 6, 50), generateFixtures(7, 6, 50));
	assert.notDeepEqual(generateFixtures(7, 6, 50), generateFixtures(8, 6, 50));
});

test("generateFixtures chains NEXT links, pads names and pads content", () => {
	const { files } = generateFixtures(3, 12, 40);
	assert.equal(files.length, 12);
	for (const [index, file] of files.entries()) {
		const label = `f${String(index + 1).padStart(2, "0")}`;
		assert.equal(file.name, `notes/${label}.txt`);
		assert.match(file.content, /^CODE: [A-Z][0-9]-[A-Z]{2}[0-9]{2}$/m);
		const next = index + 1 < files.length ? `f${String(index + 2).padStart(2, "0")}.txt` : "END";
		assert.match(file.content, new RegExp(`^NEXT: ${next.replace(".", "\\.")}$`, "m"));
		assert.ok(file.content.length >= 40 * 4, `${label} content is long enough`);
	}
});

test("generateFixtures codes are unique and never appear in filler", () => {
	const { files, codes, correction } = generateFixtures(11, 24, 20);
	const values = Object.values(codes);
	assert.equal(new Set(values).size, values.length);
	assert.equal(values.length, 24);
	assert.ok(!values.includes(correction.newCode));
	for (const code of [...values, correction.newCode]) {
		assert.match(code, /^[A-Z][0-9]-[A-Z]{2}[0-9]{2}$/);
	}
	for (const file of files) {
		const label = file.name.replace(/^notes\//, "").replace(/\.txt$/, "");
		const found = file.content.match(/[A-Z][0-9]-[A-Z]{2}[0-9]{2}/g) ?? [];
		const expected = label === correction.inFile ? [codes[label], correction.newCode] : [codes[label]];
		assert.deepEqual(found, expected, `${label} carries only its own code in filler`);
	}
});

test("generateFixtures places the single correction in the second half", () => {
	for (const seed of [1, 2, 3, 99]) {
		const { files, codes, correction } = generateFixtures(seed, 24, 12);
		assert.equal(correction.target, "f03");
		assert.equal(correction.oldCode, codes.f03);
		assert.ok(Number(correction.inFile.slice(1)) > 12, `${correction.inFile} is in the second half`);
		const carriers = files.filter((file) => file.content.includes("CORRECTION:"));
		assert.equal(carriers.length, 1);
		assert.equal(carriers[0].name, `notes/${correction.inFile}.txt`);
		assert.ok(carriers[0].content.includes(`CORRECTION: the code in f03 is now ${correction.newCode}`));
	}
});

test("generateFixtures keeps the correction out of f01..f03 on small chains", () => {
	for (const seed of [1, 2, 3, 99]) {
		const four = generateFixtures(seed, 4, 10);
		assert.equal(four.correction.inFile, "f04");
		const five = generateFixtures(seed, 5, 10);
		assert.ok(["f04", "f05"].includes(five.correction.inFile), `${five.correction.inFile} comes after f03 and sits in the second half`);
		for (const fixtures of [four, five]) {
			const carriers = fixtures.files.filter((file) => file.content.includes("CORRECTION:"));
			assert.equal(carriers.length, 1);
			assert.equal(carriers[0].name, `notes/${fixtures.correction.inFile}.txt`);
		}
	}
});

test("generateFixtures enforces the 4 to 99 file range", () => {
	assert.throws(() => generateFixtures(1, 3, 10), /4 to 99/);
	assert.throws(() => generateFixtures(1, 100, 10), /4 to 99/);
});

test("generateFixtures filler uses long lines and keeps the requested volume", () => {
	for (const padTokens of [500, 4000, 12000]) {
		const { files } = generateFixtures(9, 8, padTokens);
		const target = padTokens * 4;
		const longLines = [];
		for (const file of files) {
			const filler = fillerText(file.content);
			assert.ok(filler.length >= target - 1 && filler.length <= target + 1901, `${file.name} filler is close to ${target} chars`);
			for (const line of filler.split("\n")) {
				assert.ok(line.length > 0 && line.length <= 2000, `${file.name} filler line stays under the 2000-char read clip`);
				if (line.length > 1000) longLines.push(line);
			}
		}
		assert.ok(longLines.length > 0, `padTokens=${padTokens} emits at least one line over 1000 chars`);
	}
});

test("generateFixtures keeps CODE, NEXT and CORRECTION at the top and the seeded correction file fixed", () => {
	const { files, codes, correction } = generateFixtures(7, 24, 12000);
	assert.equal(correction.inFile, "f15");
	for (const file of files) {
		const label = file.name.replace(/^notes\//, "").replace(/\.txt$/, "");
		const lines = file.content.split("\n");
		assert.equal(lines[0], `CODE: ${codes[label]}`);
		assert.match(lines[1], /^NEXT: /);
		if (label === correction.inFile) {
			assert.equal(lines[2], `CORRECTION: the code in f03 is now ${correction.newCode}`);
		} else {
			assert.ok(!file.content.includes("CORRECTION:"), `${label} carries no correction`);
		}
	}
});

test("a default 200-line read of a padded file returns nearly all of its bytes", () => {
	for (const padTokens of [4000, 12000]) {
		for (const file of generateFixtures(2, 6, padTokens).files) {
			const covered = defaultReadPrefix(file.content).length;
			assert.ok(covered >= file.content.length * 0.9, `${file.name} padTokens=${padTokens} read covers ${covered}/${file.content.length} bytes`);
		}
	}
});

test("taskPrompt walks the chain, forbids re-reads and listing, and asks for fNN lines", () => {
	const files = generateFixtures(5, 6, 10).files;
	const prompt = taskPrompt(files);
	assert.equal(prompt, taskPrompt(files));
	assert.ok(!prompt.startsWith("Run id:"));
	assert.match(prompt, /notes\/f01\.txt/);
	assert.match(prompt, /NEXT/);
	assert.match(prompt, /Never read a file twice/);
	assert.match(prompt, /Never list or search the notes directory/);
	assert.match(prompt, /Use only the read tool on each file/);
	assert.match(prompt, /Do not use bash, grep, cat, sed or any other command to inspect files/);
	assert.match(prompt, /CORRECTION/);
	assert.match(prompt, /"fNN: CODE"/);
	assert.match(prompt, /6 files/);
});

test("taskPrompt puts a run id on the first line when one is supplied", () => {
	const files = generateFixtures(5, 6, 10).files;
	const base = taskPrompt(files);
	const withId = taskPrompt(files, "run-abc");
	assert.equal(withId, `Run id: run-abc\n${base}`);
	assert.notEqual(taskPrompt(files, "run-def"), withId);
});

test("buildBenchConfig emits the threshold under the alias and the cache and provider diagnostics streams", () => {
	const yaml = buildBenchConfig({ alias: "claude-test", threshold: 15000, diagnosticsDir: "/tmp/bench dir/\"x\"" });
	assert.equal(
		yaml,
		[
			"models:",
			"  definitions:",
			"    claude-test:",
			"      advanced:",
			"        limits:",
			"          compaction_threshold_tokens: 15000",
			"diagnostics:",
			"  enabled: true",
			'  dir: "/tmp/bench dir/\\\"x\\\""',
			"  streams:",
			"    cache: true",
			"    provider: true",
			"",
		].join("\n"),
	);
});

test("buildBenchConfig pins the provider and id of a synthesized alias and writes context_window", () => {
	const yaml = buildBenchConfig({
		alias: "bench--opencode-go--beam-free",
		provider: "opencode-go",
		id: "beam-free",
		threshold: 560000,
		contextWindow: 1000000,
		diagnosticsDir: "/tmp/diag",
	});
	assert.equal(
		yaml,
		[
			"models:",
			"  definitions:",
			"    bench--opencode-go--beam-free:",
			"      provider: opencode-go",
			"      id: beam-free",
			"      advanced:",
			"        limits:",
			"          compaction_threshold_tokens: 560000",
			"          context_window: 1000000",
			"diagnostics:",
			"  enabled: true",
			'  dir: "/tmp/diag"',
			"  streams:",
			"    cache: true",
			"    provider: true",
			"",
		].join("\n"),
	);
	assert.match(buildBenchConfig({ alias: "a", threshold: 10, diagnosticsDir: "/tmp/d" }), /compaction_threshold_tokens: 10\n/);
	assert.throws(() => buildBenchConfig({ alias: "a", threshold: 10, contextWindow: 0, diagnosticsDir: "/tmp/d" }), /contextWindow/);
});

test("buildProbeConfig emits the diagnostics streams without a threshold", () => {
	const yaml = buildProbeConfig({ diagnosticsDir: "/tmp/probe dir" });
	assert.equal(
		yaml,
		[
			"diagnostics:",
			"  enabled: true",
			'  dir: "/tmp/probe dir"',
			"  streams:",
			"    cache: true",
			"    provider: true",
			"",
		].join("\n"),
	);
	assert.ok(!yaml.includes("compaction_threshold_tokens"), "a probe config must not override the threshold");
	assert.throws(() => buildProbeConfig({ diagnosticsDir: "" }), /diagnosticsDir/);
});

test("buildProbeConfig pins a synthesized alias's provider and id without limits", () => {
	const yaml = buildProbeConfig({ diagnosticsDir: "/tmp/diag", alias: "bench--p--m", provider: "p", id: "m" });
	assert.match(yaml, /^models:\n  definitions:\n    bench--p--m:\n      provider: p\n      id: m\ndiagnostics:/);
	assert.ok(!yaml.includes("limits:"), "a probe config must not set limits");
});

test("buildBenchConfig rejects unsafe aliases, bad thresholds and empty dirs", () => {
	assert.throws(() => buildBenchConfig({ alias: "a: b", threshold: 100, diagnosticsDir: "/tmp/x" }), /invalid model alias/);
	assert.throws(() => buildBenchConfig({ alias: "", threshold: 100, diagnosticsDir: "/tmp/x" }), /invalid model alias/);
	assert.throws(() => buildBenchConfig({ alias: "a", threshold: 0, diagnosticsDir: "/tmp/x" }), /positive integer/);
	assert.throws(() => buildBenchConfig({ alias: "a", threshold: 100, diagnosticsDir: "" }), /diagnosticsDir/);
});

test("parseSegmentDecisions reads whole lines from the compaction log fixture", () => {
	assert.deepEqual(parseSegmentDecisions(fixture("compaction-log.txt")), [
		{ action: "append", reason: "append", sequence: 1, chainTokens: 700, projectedPostTokens: 2900, budget: 8000 },
		{ action: "append", reason: "append", sequence: 2, chainTokens: 900, projectedPostTokens: 3000, budget: 8000 },
		{ action: "rebase", reason: "chain-start", sequence: 1, chainTokens: 1200, projectedPostTokens: 9500, budget: 8000 },
		{ action: "rebase", reason: "chain-tokens", sequence: 2, chainTokens: 1001, projectedPostTokens: 9000, budget: 8000 },
		{ action: "plain", reason: "empty-source", sequence: 0, chainTokens: 700, projectedPostTokens: 2000, budget: 8000 },
	]);
});

test("parseSegmentDecisions ignores embedded or malformed lines", () => {
	const text = [
		"prefix segment_decision action=append reason=append sequence=9 chain_tokens=1 projected_post_tokens=2 budget=3",
		"segment_decision action=append reason=append sequence=9 chain_tokens=1 projected_post_tokens=2 budget=3 trailing",
		"segment_decision action=bogus reason=append sequence=9 chain_tokens=1 projected_post_tokens=2 budget=3",
		"segment_decision action=append reason=append sequence=x chain_tokens=1 projected_post_tokens=2 budget=3",
		"",
		"segment_decision action=plain reason=no-progress sequence=0 chain_tokens=0 projected_post_tokens=0 budget=8000",
	].join("\n");
	assert.deepEqual(parseSegmentDecisions(text), [
		{ action: "plain", reason: "no-progress", sequence: 0, chainTokens: 0, projectedPostTokens: 0, budget: 8000 },
	]);
	assert.deepEqual(parseSegmentDecisions(""), []);
});

test("summarizeDecisions excludes chain-start rebases and counts plain twice", () => {
	assert.deepEqual(summarizeDecisions(parseSegmentDecisions(fixture("compaction-log.txt"))), {
		decisions: 5,
		appends: 2,
		rebases: 2,
		plainFallbacks: 1,
		chainStarts: 1,
		maxSegments: 2,
	});
});

test("summarizeDecisions keeps rebase sequences in maxSegments without appends", () => {
	const decisions = parseSegmentDecisions(fixture("compaction-log.txt").replace(/^segment_decision action=append.*$/gm, ""));
	const summary = summarizeDecisions(decisions);
	assert.equal(summary.appends, 0);
	assert.equal(summary.maxSegments, 2);
});

test("parseRunLog reads events, applied compactions and malformed lines from the fixture", () => {
	const run = parseRunLog(fixture("run.log.jsonl"));
	assert.deepEqual(run.events, [
		{ kind: "read", file: "f01", turn: 2 },
		{ kind: "read", file: "f02", turn: 3 },
		{ kind: "compaction", index: 1 },
		{ kind: "read", file: "f03", turn: 12 },
		{ kind: "compaction", index: 2 },
		{ kind: "read", file: "f04", turn: 20 },
	]);
	assert.equal(run.compactions, 2);
	assert.equal(run.malformed, 1);
});

test("parseRunLog skips start, skipped, session_health and sub-agent records", () => {
	const run = parseRunLog(fixture("run.log.jsonl"));
	assert.equal(run.compactions, 2, "only the two parent applied compactions count");
	assert.deepEqual(
		run.events.filter((event) => event.kind === "read").map((event) => event.file),
		["f01", "f02", "f03", "f04"],
	);
	assert.equal(parseRunLog("").malformed, 0);
});

test("parseRunLog treats absent cache counters as unreported rather than zero", () => {
	const { modelCalls } = parseRunLog(fixture("run.log.jsonl"));
	assert.equal(modelCalls.length, 3);
	assert.deepEqual(modelCalls[0], {
		turn: 2,
		promptTokens: 61177,
		durationMs: 7269,
		ttftMs: 0,
		cacheReported: true,
		cacheReadTokens: 60928,
		cacheCreateTokens: 0,
	});
	assert.equal(modelCalls[1].cacheReported, false);
	assert.equal(modelCalls[1].cacheReadTokens, 0);
	assert.equal(modelCalls[1].promptTokens, 800);
	assert.equal(modelCalls[2].cacheReported, true);
	assert.equal(modelCalls[2].cacheReadTokens, 0);
});

test("parseRunLog collects summarizer usage from applied compactions", () => {
	const run = parseRunLog(fixture("run.log.jsonl"));
	// The payload's prompt_tokens (4500) is the post-compaction estimate, not the
	// summary call input, so the total is input plus both cache counters.
	assert.deepEqual(run.compactionCalls, [
		{ promptTokens: 2000, durationMs: 0, ttftMs: 0, cacheReported: true, cacheReadTokens: 500, cacheCreateTokens: 300 },
		{ promptTokens: 0, durationMs: 0, ttftMs: 0, cacheReported: false, cacheReadTokens: 0, cacheCreateTokens: 0 },
	]);
	assert.equal(parseRunLog("").compactionCalls.length, 0);
});

test("parseRunLog skips failed and sub-agent model calls", () => {
	const lines = [
		{ type: "model_call_finished", payload: { turn: 1, prompt_tokens: 1000, cache_read_tokens: 900, success: true } },
		{ type: "model_call_finished", payload: { turn: 2, prompt_tokens: 1000, error: "rate limited" } },
		{ type: "model_call_finished", payload: { turn: 3, prompt_tokens: 1000, cache_read_tokens: 900, success: false } },
		{
			type: "model_call_finished",
			scope: { agent_id: "child-1", agent_type: "code" },
			payload: { turn: 4, prompt_tokens: 1000, cache_read_tokens: 900, success: true },
		},
	];
	const run = parseRunLog(lines.map((line) => JSON.stringify(line)).join("\n"));
	assert.equal(run.modelCalls.length, 1);
	assert.equal(run.modelCalls[0].turn, 1);
});

test("parseToolReads keeps only ordered read events", () => {
	const reads = parseToolReads(fixture("run.log.jsonl"));
	assert.deepEqual(
		reads.map((event) => event.file),
		["f01", "f02", "f03", "f04"],
	);
	assert.deepEqual(reads[2], { kind: "read", file: "f03", turn: 12 });
});

test("parseStdout returns the final answer and detects the max-turn stop", () => {
	assert.deepEqual(parseStdout(fixture("stdout.txt")), {
		finalAnswer: "f01: K7-QX42\nf02: A1-BC23\nf03: E3-FG67",
		maxTurnsHit: true,
	});
});

test("parseStdout prefers the last answer and stops at the next label", () => {
	const text = [
		"assistant: turn=1 role=assistant content=draft: wrong",
		"status: model call model=x turn=1",
		"assistant: turn=2 role=assistant content=f01: A1-BC23",
		"f02: C2-DE45",
		"assistant> read notes/f04.txt",
		"status: run finished",
	].join("\n");
	assert.deepEqual(parseStdout(text), {
		finalAnswer: "f01: A1-BC23\nf02: C2-DE45",
		maxTurnsHit: false,
	});
	assert.deepEqual(parseStdout(""), { finalAnswer: null, maxTurnsHit: false });
});

test("parseStdout reads the exec renderer format through the final status line", () => {
	const text = [
		"assistant: turn=25 role=assistant content=f01: Q0-NZ92",
		"f02: P7-LZ44",
		"f03: U3-GD83",
		"status: run complete after 25 turns",
	].join("\n");
	assert.deepEqual(parseStdout(text), {
		finalAnswer: "f01: Q0-NZ92\nf02: P7-LZ44\nf03: U3-GD83",
		maxTurnsHit: false,
	});
});

test("scoreRecall marks every answer line exact, including the correction", () => {
	const run = parseRunLog(fixture("run.log.jsonl"));
	const answer = "f01: A1-BC23\nf02: C2-DE45\nf03: Z9-YX87\nf04: G4-HI89";
	const score = scoreRecall({ answer, fixtures: recallFixtures(), events: run.events });
	assert.equal(score.exact, 4);
	assert.equal(score.lost, 0);
	assert.equal(score.total, 4);
	assert.equal(score.invalid, false);
	assert.equal(score.invalidReason, null);
	assert.deepEqual(
		score.perFile.map((file) => [file.file, file.status, file.lostAt]),
		[
			["f01", "exact", null],
			["f02", "exact", null],
			["f03", "exact", null],
			["f04", "exact", null],
		],
	);
});

test("scoreRecall attributes losses to the first compaction after the read", () => {
	const run = parseRunLog(fixture("run.log.jsonl"));
	const score = scoreRecall({ answer: "f01: A1-BC23\nf02: WRONG", fixtures: recallFixtures(), events: run.events });
	assert.equal(score.exact, 1);
	assert.equal(score.lost, 3);
	assert.deepEqual(score.perFile, [
		{ file: "f01", status: "exact", lostAt: null },
		{ file: "f02", status: "lost", lostAt: 1 },
		{ file: "f03", status: "lost", lostAt: 2 },
		{ file: "f04", status: "lost", lostAt: "unattributed" },
	]);
});

test("scoreRecall treats a null answer as all lost", () => {
	const score = scoreRecall({ answer: null, fixtures: recallFixtures(), events: [] });
	assert.equal(score.exact, 0);
	assert.equal(score.lost, 4);
	assert.equal(score.total, 4);
	assert.deepEqual(score.perFile[0], { file: "f01", status: "lost", lostAt: "unattributed" });
});

test("scoreRecall loses the correction target when the old code appears anywhere", () => {
	const answer = "f01: A1-BC23\nf02: C2-DE45\nf03: Z9-YX87\nf04: G4-HI89\nnote: E3-FG67 was the old code";
	const score = scoreRecall({ answer, fixtures: recallFixtures(), events: [] });
	assert.equal(score.exact, 3);
	assert.deepEqual(score.perFile[2], { file: "f03", status: "lost", lostAt: "unattributed" });
});

test("scoreRecall matches file keys case-insensitively", () => {
	const answer = "F01: A1-BC23\nf02: C2-DE45\nf03: Z9-YX87\nf04: G4-HI89";
	assert.equal(scoreRecall({ answer, fixtures: recallFixtures(), events: [] }).exact, 4);
});

test("scoreRecall invalidates a run that re-reads a file after a compaction", () => {
	const events = [
		{ kind: "read", file: "f02", turn: 1 },
		{ kind: "compaction", index: 1 },
		{ kind: "read", file: "f02", turn: 4 },
	];
	const answer = "f01: A1-BC23\nf02: C2-DE45\nf03: Z9-YX87\nf04: G4-HI89";
	const score = scoreRecall({ answer, fixtures: recallFixtures(), events });
	assert.equal(score.invalid, true);
	assert.match(score.invalidReason, /f02 read again after compaction 1/);
});

test("scoreRecall invalidates any later read after a compaction past the first read", () => {
	const answer = "f01: A1-BC23\nf02: C2-DE45\nf03: Z9-YX87\nf04: G4-HI89";
	const reread = [
		{ kind: "read", file: "f02", turn: 1 },
		{ kind: "read", file: "f02", turn: 2 },
		{ kind: "compaction", index: 1 },
		{ kind: "read", file: "f02", turn: 4 },
	];
	const score = scoreRecall({ answer, fixtures: recallFixtures(), events: reread });
	assert.equal(score.invalid, true, "the third read follows a compaction that followed the first read");
	assert.match(score.invalidReason, /f02 read again after compaction 1/);

	const noCompactionAfter = [
		{ kind: "read", file: "f02", turn: 1 },
		{ kind: "read", file: "f02", turn: 2 },
		{ kind: "compaction", index: 1 },
	];
	assert.equal(scoreRecall({ answer, fixtures: recallFixtures(), events: noCompactionAfter }).invalid, false);
});

test("parseRunLog counts non-read tool access to the fixtures", () => {
	const lines = [
		{ type: "tool_call_started", payload: { turn: 1, tool: "read", arguments: { path: "notes/f01.txt" } } },
		{ type: "tool_call_started", payload: { turn: 2, tool: "grep", arguments: { pattern: "CODE", path: "notes/" } } },
		{ type: "tool_call_started", payload: { turn: 3, tool: "bash", arguments: { command: "cat notes/f02.txt" } } },
		{ type: "tool_call_started", payload: { turn: 4, tool: "grep", arguments: { pattern: "notes" } } },
		{ type: "tool_call_started", payload: { turn: 5, tool: "glob", arguments: { pattern: "src/**" } } },
		{
			type: "tool_call_started",
			scope: { agent_id: "child-1", agent_type: "code" },
			payload: { turn: 6, tool: "bash", arguments: { command: "cat notes/f03.txt" } },
		},
	];
	const run = parseRunLog(lines.map((line) => JSON.stringify(line)).join("\n"));
	assert.equal(run.nonReadFileAccess, 4, "every tool call that reaches the fixtures counts, unrelated ones do not");
	assert.deepEqual(
		run.events.filter((event) => event.kind === "read").map((event) => event.file),
		["f01"],
	);
	assert.equal(
		parseRunLog(fixture("run.log.jsonl")).nonReadFileAccess,
		1,
		"the fixture's sub-agent read of notes/f03.txt is access outside the main-agent read tool",
	);
	assert.equal(parseRunLog("").nonReadFileAccess, 0);
});

test("parseRunLog only counts fixture paths, not paths that merely contain notes", () => {
	const access = (tool, args) =>
		parseRunLog(JSON.stringify({ type: "tool_call_started", payload: { turn: 1, tool, arguments: args } })).nonReadFileAccess;
	for (const [tool, args] of [
		["bash", { command: "ls docs/release-notes/" }],
		["grep", { pattern: "footnotes/", path: "docs" }],
		["glob", { pattern: "docs/notes/*.md" }],
		["bash", { command: "cat ~/work/notes/todo.txt" }],
		["grep", { pattern: "TODO", path: "docs/notes/" }],
	]) {
		assert.equal(access(tool, args), 0, `${tool} ${JSON.stringify(args)} must not count as fixture access`);
	}
	for (const [tool, args] of [
		["bash", { command: "cat notes/f05.txt" }],
		["grep", { command: "grep -r CODE notes" }],
		["bash", { command: "cat ./notes/f01.txt" }],
		["glob", { pattern: "notes/*.txt" }],
		["grep", { pattern: "CODE", path: "notes" }],
		["bash", { command: "cat /abs/repo/notes/f05.txt" }],
		["glob", { pattern: "/tmp/x/notes/f01.txt" }],
	]) {
		assert.equal(access(tool, args), 1, `${tool} ${JSON.stringify(args)} must count as fixture access`);
	}
});

test("parseRunLog judges an absolute notes dir only against the run's own repo", () => {
	const access = (args, options) =>
		parseRunLog(JSON.stringify({ type: "tool_call_started", payload: { turn: 1, tool: "bash", arguments: args } }), options)
			.nonReadFileAccess;
	const notesDir = "/tmp/compaction-bench-repo-abc/notes";
	assert.equal(access({ path: `${notesDir}/` }, { notesDir }), 1);
	assert.equal(access({ command: `ls ${notesDir}` }, { notesDir }), 1);
	assert.equal(access({ path: `${notesDir}/` }), 0, "without the repo path a bare absolute notes dir is not judged");
	assert.equal(access({ path: "docs/notes/" }, { notesDir }), 0);
	assert.equal(
		access({ path: "/tmp/compaction-bench-repo-xyz/notes/" }, { notesDir }),
		0,
		"another run's notes dir is not this run's fixture",
	);
	assert.deepEqual(parseRunLog("", { notesDir }), {
		events: [],
		modelCalls: [],
		compactionCalls: [],
		compactions: 0,
		emergencyCompactions: 0,
		compactionFailures: 0,
		emergencyFailures: 0,
		retainedTail: [],
		contextWindow: null,
		thresholdTokens: null,
		malformed: 0,
		nonReadFileAccess: 0,
	});
});

test("parseRunLog counts sub-agent fixture access, read tool included", () => {
	const scope = { agent_id: "child-1", agent_type: "code" };
	const lines = [
		{ type: "tool_call_started", payload: { turn: 1, tool: "read", arguments: { path: "notes/f01.txt" } } },
		{ type: "tool_call_started", scope, payload: { turn: 2, tool: "bash", arguments: { command: "cat notes/f03.txt" } } },
		{ type: "tool_call_started", scope, payload: { turn: 3, tool: "read", arguments: { path: "notes/f02.txt" } } },
		{ type: "tool_call_started", scope, payload: { turn: 4, tool: "grep", arguments: { pattern: "TODO", path: "docs" } } },
	];
	const run = parseRunLog(lines.map((line) => JSON.stringify(line)).join("\n"));
	assert.equal(run.nonReadFileAccess, 2, "both sub-agent calls that reach the fixtures count");
	assert.deepEqual(
		run.events.filter((event) => event.kind === "read").map((event) => event.file),
		["f01"],
		"only the main agent's read is scored",
	);
});

function call(over = {}) {
	return { turn: 1, promptTokens: 0, cacheReported: true, cacheReadTokens: 0, cacheCreateTokens: 0, ...over };
}

test("aggregateCache reports only when every call reported cache", () => {
	assert.deepEqual(
		aggregateCache([call({ promptTokens: 100, cacheReadTokens: 60 }), call({ promptTokens: 50, cacheCreateTokens: 40 })]),
		{ calls: 2, reportingCalls: 2, reported: true, promptTokens: 150, cacheReadTokens: 60, cacheCreateTokens: 40 },
	);
	const mixed = aggregateCache([call({ promptTokens: 100, cacheReadTokens: 60 }), call({ cacheReported: false, promptTokens: 20 })]);
	assert.equal(mixed.reported, false);
	assert.equal(mixed.reportingCalls, 1);
	assert.equal(aggregateCache([]).reported, false);
});

test("costProxy applies per-family weights and returns null without reporting", () => {
	const agg = { reported: true, promptTokens: 1000, cacheReadTokens: 600, cacheCreateTokens: 200 };
	assert.equal(costProxy(agg, "anthropic"), 510);
	assert.equal(costProxy(agg, "openai"), 460);
	assert.equal(costProxy({ ...agg, promptTokens: 100, cacheCreateTokens: 0 }, "anthropic"), 60);
	assert.equal(costProxy({ ...agg, reported: false }, "anthropic"), null);
	assert.throws(() => costProxy(agg, "google"), /unknown cost family/);
});

test("evaluateProbe distinguishes a cache miss from failed probe calls", () => {
	assert.deepEqual(evaluateProbe([call({ cacheCreateTokens: 900 })], [call({ cacheReadTokens: 800 })]), { ok: true, reason: null });
	assert.deepEqual(evaluateProbe([call({ cacheCreateTokens: 0 })], [call({ cacheReadTokens: 800 })]), {
		ok: false,
		reason: "cache_control not honoured on this route",
	});
	assert.deepEqual(evaluateProbe([], [call({ cacheReadTokens: 800 })]), { ok: false, reason: "probe calls failed" });
	assert.deepEqual(evaluateProbe([call({ cacheCreateTokens: 900 })], []), { ok: false, reason: "probe calls failed" });
});

test("classifyProbeFailure maps stderr to fixed labels and never echoes it", () => {
	assert.equal(classifyProbeFailure("error: 401 Unauthorized: token rejected"), "auth error (401)");
	assert.equal(classifyProbeFailure("request rejected: unauthorized"), "auth error (401)");
	assert.equal(classifyProbeFailure("HTTP 403 forbidden"), "forbidden (403)");
	assert.equal(classifyProbeFailure("model not found"), "not found (404)");
	assert.equal(classifyProbeFailure("HTTP 404: missing"), "not found (404)");
	assert.equal(classifyProbeFailure("HTTP 400 bad input"), "bad request (400)");
	assert.equal(classifyProbeFailure("context deadline: timeout"), "timeout");
	assert.equal(classifyProbeFailure("probe route unavailable"), null);
	assert.equal(classifyProbeFailure(""), null);
});

test("median handles odd, even and empty input", () => {
	assert.equal(median([3, 1, 2]), 2);
	assert.equal(median([4, 1, 3, 2]), 2.5);
	assert.equal(median([]), null);
});

test("runValidity gates flag-on runs on compaction count and append decisions", () => {
	const ok = { finalAnswer: "f01: A1-BC23" };
	assert.deepEqual(runValidity({ mode: "on", compactions: 3, decisionSummary: { appends: 2 }, ...ok }), {
		valid: false,
		reason: "fewer than 4 compactions",
	});
	assert.deepEqual(runValidity({ mode: "on", compactions: 4, decisionSummary: { appends: 0 }, ...ok }), {
		valid: false,
		reason: "no append decision",
	});
	assert.deepEqual(runValidity({ mode: "on", compactions: 4, decisionSummary: { appends: 1 }, ...ok }), { valid: true, reason: null });
	assert.deepEqual(runValidity({ mode: "off", compactions: 0, decisionSummary: { appends: 0 }, ...ok }), { valid: true, reason: null });
});

test("runValidity invalidates an off or on run without a parsed final answer", () => {
	assert.deepEqual(
		runValidity({ mode: "off", compactions: 0, decisionSummary: { appends: 0 }, finalAnswer: null }),
		{ valid: false, reason: "no final answer parsed" },
	);
	assert.deepEqual(
		runValidity({ mode: "on", compactions: 8, decisionSummary: { appends: 3 }, finalAnswer: null }),
		{ valid: false, reason: "no final answer parsed" },
	);
});

test("runValidity invalidates a run that reached the fixtures outside the read tool", () => {
	assert.deepEqual(
		runValidity({
			mode: "off",
			compactions: 4,
			decisionSummary: { appends: 0 },
			finalAnswer: "f01: A1-BC23",
			nonReadFileAccess: 2,
		}),
		{ valid: false, reason: "fixture accessed outside main-agent read tool" },
	);
	assert.deepEqual(
		runValidity({ mode: "on", compactions: 4, decisionSummary: { appends: 1 }, finalAnswer: "f01: A1-BC23", nonReadFileAccess: 0 }),
		{ valid: true, reason: null },
	);
});

test("verdict prefers INVALID over every other label", () => {
	const invalid = verdictFor({
		on: verdictSide({ valid: false, invalidReason: "fewer than 4 compactions", compactions: 9 }),
	});
	assert.equal(invalid.label, "INVALID");
	assert.match(invalid.reasons[0], /fewer than 4 compactions/);
	const recallInvalid = verdictFor({ off: verdictSide({ recallInvalid: true }) });
	assert.equal(recallInvalid.label, "INVALID");
	assert.ok(recallInvalid.reasons.some((reason) => /off: a fixture file was read again after a compaction/.test(reason)));
});

test("verdict is INVALID when the off side recalled nothing", () => {
	const both = verdictFor({ off: verdictSide({ exact: 0 }), on: verdictSide({ exact: 0 }) });
	assert.equal(both.label, "INVALID", "0/N on both sides must not pass on a relative comparison");
	assert.ok(both.reasons.includes("off mode recalled nothing"));
	const oneSide = verdictFor({ off: verdictSide({ exact: 0 }), on: verdictSide({ exact: 24 }) });
	assert.equal(oneSide.label, "INVALID");
	assert.ok(oneSide.reasons.includes("off mode recalled nothing"));
	assert.equal(verdictFor({ off: verdictSide({ exact: 1 }), on: verdictSide({ exact: 1 }) }).label, "PASS");
});

test("verdict reports CONTINUITY_ONLY when cost was not judged", () => {
	const result = verdictFor({ family: "openai", on: verdictSide({ costPerTurn: null }) });
	assert.equal(result.label, "CONTINUITY_ONLY");
	assert.equal(result.cache, "no cache verdict");
	assert.ok(result.reasons.some((reason) => /cost proxy not judged/.test(reason)));
	assert.ok(!result.reasons.some((reason) => /within tolerance/.test(reason)));
	assert.equal(verdictFor().label, "PASS");
});

test("verdict treats an invalid off run as INVALID, not INCOMPARABLE", () => {
	const oneOffFailure = verdictFor({ off: verdictSide({ valid: false, invalidReason: "run failed: exit 2" }) });
	assert.equal(oneOffFailure.label, "INVALID");
	assert.ok(oneOffFailure.reasons.includes("off: run failed: exit 2"));

	const allOffFailed = verdictFor({
		off: verdictSide({ valid: false, invalidReason: "run failed: timeout", compactions: null, costPerTurn: null }),
		on: verdictSide({ compactions: 5 }),
	});
	assert.equal(allOffFailed.label, "INVALID");
	assert.notEqual(allOffFailed.label, "INCOMPARABLE");
	assert.ok(allOffFailed.reasons.includes("off: run failed: timeout"));
});

test("verdict calls a compaction-count gap of two INCOMPARABLE but not one", () => {
	assert.equal(verdictFor({ on: verdictSide({ compactions: 7 }) }).label, "INCOMPARABLE");
	assert.equal(verdictFor({ on: verdictSide({ compactions: 6 }) }).label, "PASS");
});

test("verdict returns INFORMATIONAL before judging continuity or cost", () => {
	const result = verdictFor({ informational: true, on: verdictSide({ exact: 1, costPerTurn: 500 }) });
	assert.equal(result.label, "INFORMATIONAL");
	assert.equal(result.cache, "checked");
	assert.ok(result.reasons.some((reason) => /informational alias/.test(reason)));
});

test("verdict fails on continuity and on the cost proxy with named reasons", () => {
	const continuity = verdictFor({ on: verdictSide({ exact: 22 }) });
	assert.equal(continuity.label, "FAIL");
	assert.ok(continuity.reasons.some((reason) => /continuity/.test(reason)));
	const cost = verdictFor({ on: verdictSide({ costPerTurn: 106 }), family: "openai" });
	assert.equal(cost.label, "FAIL");
	assert.ok(cost.reasons.some((reason) => /cost proxy/.test(reason)));
});

test("verdict names max-turn stops without invalidating the run", () => {
	const result = verdictFor({ off: verdictSide({ maxTurnsHitRuns: 1 }), on: verdictSide({ maxTurnsHitRuns: 1 }) });
	assert.equal(result.label, "PASS");
	assert.ok(result.reasons.includes("max turns hit in 2 run(s)"));
	assert.ok(!verdictFor().reasons.some((reason) => /max turns/.test(reason)));
});

test("verdict passes at the tolerance boundary", () => {
	const boundary = verdictFor({ off: verdictSide({ exact: 20, costPerTurn: 100 }), on: verdictSide({ exact: 19, costPerTurn: 105 }) });
	assert.equal(boundary.label, "PASS");
	assert.equal(boundary.cache, "checked");
});

test("verdict drops the cache verdict when cost is missing or the anthropic probe failed", () => {
	const missing = verdictFor({ family: "openai", on: verdictSide({ costPerTurn: null }) });
	assert.equal(missing.cache, "no cache verdict");
	assert.ok(missing.reasons.some((reason) => /cost proxy not judged/.test(reason)));
	const probe = verdictFor({ probeOk: false });
	assert.equal(probe.cache, "no cache verdict");
	assert.ok(probe.reasons.some((reason) => /probe/.test(reason)));
	assert.equal(verdictFor({ family: "openai", probeOk: false }).cache, "checked", "openai needs no probe");
});

test("renderTable prints metric rows, not reported cells, verdict and probe lines", () => {
	const results = [
		{
			alias: "opus",
			family: "anthropic",
			informational: false,
			probe: { ok: true, reason: null },
			off: modeAgg(),
			on: modeAgg({ costPerTurn: null, exact: 22, lost: 2, lostAt: ["f05@c2", "f09@unattributed"], maxTurnsHitRuns: 1 }),
			verdict: { label: "CONTINUITY_ONLY", cache: "no cache verdict", reasons: ["cost proxy not judged: no cache reporting on one side"] },
		},
		{
			alias: "gpt",
			family: "openai",
			informational: true,
			probe: null,
			off: modeAgg({ cachedTokens: null }),
			on: modeAgg({ recallInvalid: true }),
			verdict: { label: "INFORMATIONAL", cache: "checked", reasons: ["informational alias: no pass or fail verdict"] },
		},
	];
	const md = renderTable(results);
	assert.match(md, /### opus \(anthropic\)/);
	assert.match(md, /\| metric \| off \| on \|/);
	assert.match(md, /\| --- \| --- \| --- \|/);
	assert.match(md, /\| turns \| 10 \| 10 \|/);
	assert.match(md, /\| max turns hit \| 0 \| 1 \|/);
	assert.match(md, /\| plain fallbacks \| 0 \| 0 \|/);
	assert.match(md, /\| cost proxy per turn \(uncached \+ 1\.25\*write \+ 0\.1\*read\) \| 100 \| not reported \|/);
	assert.match(md, /cost proxy per turn \(uncached \+ 0\.1\*read\)/);
	assert.match(md, /\| continuity \| 24\/24 \| 22\/24 \(lost: f05@c2, f09@unattributed\) \|/);
	assert.match(md, /\| continuity \| 24\/24 \| invalid \|/);
	assert.match(md, /\| cached tokens \| not reported \| 5000 \|/);
	assert.match(md, /Verdict: CONTINUITY_ONLY \(cache: no cache verdict\)/);
	assert.match(md, /Verdict: INFORMATIONAL \(cache: checked\)/);
	assert.match(md, /- cost proxy not judged: no cache reporting on one side/);
	assert.equal((md.match(/Probe:/g) ?? []).length, 1);
	assert.match(md, /Probe: ok/);
});

function runRecord(over = {}) {
	return {
		turns: 10,
		compactions: 4,
		rebases: 1,
		plainFallbacks: 0,
		maxSegments: 2,
		cachedTokens: 100,
		costPerTurn: 10,
		exact: 24,
		lost: 0,
		total: 24,
		recallInvalid: false,
		lostAt: [],
		failed: false,
		valid: true,
		invalidReason: null,
		maxTurnsHit: false,
		...over,
	};
}

test("aggregateMode takes medians, unions loss points and ignores unreported values", () => {
	const agg = aggregateMode([
		runRecord(),
		runRecord({
			turns: 12,
			compactions: 6,
			rebases: 2,
			plainFallbacks: 1,
			maxSegments: 3,
			cachedTokens: null,
			costPerTurn: null,
			exact: 22,
			lost: 2,
			lostAt: ["f05@c2", "f09@unattributed"],
		}),
		runRecord({
			turns: 14,
			compactions: 5,
			rebases: 0,
			maxSegments: 4,
			cachedTokens: 300,
			costPerTurn: 20,
			exact: 20,
			lost: 4,
			recallInvalid: true,
			lostAt: ["f05@c2", "f11@c1"],
		}),
	]);
	assert.equal(agg.turns, 12);
	assert.equal(agg.compactions, 5);
	assert.equal(agg.rebases, 1);
	assert.equal(agg.plainFallbacks, 0);
	assert.equal(agg.maxSegments, 3);
	assert.equal(agg.cachedTokens, 200);
	assert.equal(agg.costPerTurn, 15);
	assert.equal(agg.exact, 22);
	assert.equal(agg.lost, 2);
	assert.equal(agg.total, 24);
	assert.equal(agg.recallInvalid, true);
	assert.equal(agg.valid, true);
	assert.equal(agg.invalidReason, null);
	assert.deepEqual(agg.lostAt, ["f05@c2", "f09@unattributed", "f11@c1"]);
});

test("aggregateMode reports a failed run and a validity failure as invalid", () => {
	const failed = aggregateMode([runRecord(), runRecord({ failed: true, reason: "timeout", turns: null })]);
	assert.equal(failed.valid, false);
	assert.equal(failed.invalidReason, "run failed: timeout");
	assert.equal(failed.turns, 10);

	const invalid = aggregateMode([
		runRecord(),
		runRecord({ valid: false, invalidReason: "fewer than 4 compactions" }),
	]);
	assert.equal(invalid.valid, false);
	assert.equal(invalid.invalidReason, "fewer than 4 compactions");
	assert.equal(aggregateMode([]).turns, null);
});

test("aggregateMode takes metric medians over completed runs only", () => {
	const agg = aggregateMode([
		runRecord({ turns: 10, exact: 24 }),
		runRecord({ turns: 12, exact: 22 }),
		runRecord({ failed: true, reason: "timeout", turns: 90, exact: 0, lost: 24, total: 24, costPerTurn: 900 }),
	]);
	assert.equal(agg.turns, 11, "a failed run's partial metrics must not move the median");
	assert.equal(agg.exact, 23);
	assert.equal(agg.costPerTurn, 10);
	assert.equal(agg.valid, false);
	assert.equal(agg.invalidReason, "run failed: timeout");
	assert.equal(aggregateMode([runRecord({ failed: true, turns: null })]).turns, null);
});

test("aggregateMode counts runs that hit the max-turn limit", () => {
	assert.equal(aggregateMode([runRecord(), runRecord({ maxTurnsHit: true }), runRecord({ maxTurnsHit: true })]).maxTurnsHitRuns, 2);
	assert.equal(aggregateMode([runRecord()]).maxTurnsHitRuns, 0);
	assert.equal(aggregateMode([]).maxTurnsHitRuns, 0);
});

function fakeRunLog() {
	const lines = [];
	for (let n = 1; n <= 4; n++) {
		lines.push(JSON.stringify({ type: "tool_call_started", payload: { turn: n * 2, tool: "read", call_id: `call_${n}`, arguments: { path: `notes/f0${n}.txt` } } }));
		lines.push(JSON.stringify({ type: "model_call_finished", payload: { turn: n * 2, prompt_tokens: 1000, cache_read_tokens: 900, success: true } }));
		lines.push(
			JSON.stringify({
				type: "context_diagnostics",
				payload: {
					kind: "compaction",
					severity: "done",
					compaction_count: n,
					scope: "conversation",
					input_tokens: 100,
					cache_read_tokens: 50,
					cache_create_tokens: 50,
					cache_reported_input_tokens: 200,
				},
			}),
		);
	}
	return lines.join("\n");
}

function fakeStdout() {
	const fixtures = generateFixtures(1, 4, 10);
	const codes = { ...fixtures.codes, [fixtures.correction.target]: fixtures.correction.newCode };
	const lines = fixtures.files.map((file) => {
		const name = file.name.replace(/^notes\//, "").replace(/\.txt$/, "");
		return `${name}: ${codes[name]}`;
	});
	return [`assistant: turn=8 role=assistant content=${lines[0]}`, ...lines.slice(1), "status: run finished", ""].join("\n");
}

function makeFakeRunner({ failOn = null, failProbe = false, probeStderr = null, secret = "", sandbox = true, noAnswer = false } = {}) {
	const invocations = [];
	const probeAttempts = new Map();
	let normalRuns = 0;
	async function runner(call) {
		invocations.push({ command: call.command, args: call.args, env: call.env });
		await mkdir(dirname(call.stdoutPath), { recursive: true });
		await mkdir(dirname(call.stderrPath), { recursive: true });
		if (call.command === "git") {
			await writeFile(call.stdoutPath, "");
			await writeFile(call.stderrPath, "");
			return { code: 0, timedOut: false };
		}
		if (call.args[0] === "config") {
			await writeFile(call.stdoutPath, sandbox === "fail" ? "" : `sandbox:\n  enabled: ${sandbox}\n`);
			await writeFile(call.stderrPath, "");
			return { code: sandbox === "fail" ? 1 : 0, timedOut: false };
		}
		const flag = (name) => call.args[call.args.indexOf(name) + 1];
		const configPath = flag("--config");
		if (configPath) invocations.at(-1).configText = await readFile(configPath, "utf8");
		// Write the provider and cache streams where the generated config points,
		// so the run exercises the diagnostics copy and provider metrics.
		const dirLine = configPath ? /^  dir: "((?:[^"\\]|\\.)*)"$/m.exec(invocations.at(-1).configText) : null;
		if (dirLine) {
			const diagDir = JSON.parse(`"${dirLine[1]}"`);
			await mkdir(diagDir, { recursive: true });
			await writeFile(
				join(diagDir, "provider.jsonl"),
				`${JSON.stringify({ kind: "provider", payload: { outcome: "ok", attempts: 1, duration_ms: 10, ttft_ms: 4 } })}\n${JSON.stringify({ kind: "provider", payload: { outcome: "exhausted", attempts: 3, error_class: "rate_limited", duration_ms: 30, ttft_ms: 9 } })}\n`,
			);
			await writeFile(join(diagDir, "cache.jsonl"), "{}\n");
		}
		if (call.args[call.args.length - 1].includes("Reply with OK only.")) {
			const alias = flag("--model");
			const attempt = (probeAttempts.get(alias) ?? 0) + 1;
			probeAttempts.set(alias, attempt);
			if (failProbe) {
				await writeFile(call.stdoutPath, "");
				await writeFile(
					call.stderrPath,
					probeStderr ?? `error: 401 Unauthorized: ${secret} rejected\nprobe route unavailable\n`,
				);
				return { code: 2, timedOut: false };
			}
			const payload = attempt === 1
				? { turn: 1, prompt_tokens: 3000, cache_create_tokens: 2500, success: true }
				: { turn: 1, prompt_tokens: 3000, cache_read_tokens: 2500, success: true };
			await writeFile(flag("--log-file"), `${JSON.stringify({ type: "model_call_finished", payload })}\n`);
			await writeFile(call.stdoutPath, "assistant: turn=1 role=assistant content=OK\nstatus: run finished\n");
			await writeFile(call.stderrPath, "");
			return { code: 0, timedOut: false };
		}
		normalRuns++;
		if (failOn && failOn(call.env.STEINER_EXPERIMENT_SEGMENTED_COMPACTION)) {
			await writeFile(call.stdoutPath, "");
			await writeFile(call.stderrPath, "");
			return { code: 2, timedOut: false };
		}
		await writeFile(flag("--log-file"), `${fakeRunLog()}\n`);
		await writeFile(flag("--compaction-log-file"), fixture("compaction-log.txt"));
		await writeFile(call.stdoutPath, noAnswer ? "" : fakeStdout());
		await writeFile(call.stderrPath, "");
		return { code: 0, timedOut: false };
	}
	return { runner, invocations, normalRuns: () => normalRuns };
}

test("main writes results.md and raw.json through a fake runner without leaking env values", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const secret = "hunter2-secret-value";
	const env = { STEINER_LIVE_BENCH: "1", SECRET_TOKEN: secret, OPENAI_API_KEY: "sk-should-not-leak" };
	const fake = makeFakeRunner();
	const stderrLines = [];
	const stdoutChunks = [];
	try {
		const code = await main(
			[
				"--model", "claude-test,gpt-test",
				"--anthropic-alias", "claude-test",
				"--files", "4",
				"--pad-tokens", "10",
				"--out", outDir,
			],
			env,
			{
				spawnRun: fake.runner,
				log: (line) => stderrLines.push(line),
				stdout: (text) => stdoutChunks.push(text),
				now: () => new Date("2026-11-26T12:00:00Z"),
			},
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));

		assert.equal(code, 0);
		assert.match(markdown, /^# Compaction bench/);
		assert.match(markdown, /generated: 2026-11-26T12:00:00\.000Z/);
		assert.match(markdown, /- steiner: steiner/);
		assert.match(markdown, /- sandbox: enabled/);
		assert.match(markdown, /STEINER_EXPERIMENT_SEGMENTED_COMPACTION=1/);
		assert.match(markdown, /### claude-test \(anthropic\)/);
		assert.match(markdown, /### gpt-test \(openai\)/);
		assert.match(markdown, /Verdict: PASS/);
		assert.equal((markdown.match(/Probe:/g) ?? []).length, 1);
		assert.match(markdown, /Probe: ok/);

		assert.equal(raw.aliases.length, 2);
		assert.equal(raw.aliases[0].probe.ok, true);
		assert.equal(raw.aliases[1].probe, null);
		assert.equal(raw.aliases[0].runs.off[0].compactions, 4);
		// Main calls: 4 x (1000 prompt, 900 cache read). Compaction summary calls:
		// 4 x (100 uncached + 50 read + 50 write), so the cache and cost totals fold
		// both in while turns stays the main-call count.
		assert.equal(raw.aliases[0].runs.off[0].turns, 4);
		assert.equal(raw.aliases[0].runs.off[0].cachedTokens, 3800);
		assert.equal(raw.aliases[0].runs.off[0].costPerTurn, 357.5);
		assert.equal(raw.aliases[1].runs.on[0].costPerTurn, 345);
		assert.equal(raw.aliases[0].runs.on[0].exitCode, 0);
		assert.equal(raw.aliases[0].runs.on[0].exact, 4);
		assert.deepEqual(raw.experimentEnv, { STEINER_EXPERIMENT_SEGMENTED_COMPACTION: "1" });
		assert.equal(raw.sandbox, "enabled");

		const probeCalls = fake.invocations.filter((call) => call.args[call.args.length - 1].includes("Reply with OK only."));
		assert.equal(probeCalls.length, 2, "the probe runs twice for one anthropic alias");
		for (const call of probeCalls) {
			assert.equal(call.args[call.args.indexOf("--model") + 1], "claude-test");
			assert.ok(!call.configText.includes("compaction_threshold_tokens"), "a probe config must not set a threshold");
			assert.match(call.configText, /    cache: true/);
		}
		assert.equal(probeCalls[0].args.at(-1), probeCalls[1].args.at(-1), "both calls of one probe share the prompt");
		assert.match(probeCalls[0].args.at(-1), /^Probe id: \S+\n/);
		assert.equal(fake.normalRuns(), 4);

		for (const text of [markdown, JSON.stringify(raw), stderrLines.join("\n"), stdoutChunks.join("\n")]) {
			assert.ok(!text.includes(secret), "the secret env value must not appear in bench output");
			assert.ok(!text.includes("sk-should-not-leak"), "the api key env value must not appear in bench output");
		}
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main invalidates runs that never produce a final answer instead of passing 0/N", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ noAnswer: true, sandbox: false });
	try {
		const code = await main(
			["--model", "gpt-test", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {} },
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 0);
		assert.equal(raw.aliases[0].runs.off[0].valid, false);
		assert.equal(raw.aliases[0].runs.off[0].invalidReason, "no final answer parsed");
		assert.match(markdown, /Verdict: INVALID/);
		assert.match(markdown, /- off: no final answer parsed/);
		assert.match(markdown, /- on: no final answer parsed/);
		assert.ok(!markdown.includes("Verdict: PASS"));
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main returns 3 when a run fails", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ failOn: (flag) => flag === "1", sandbox: "fail" });
	try {
		const code = await main(
			["--model", "claude-test", "--anthropic-alias", "claude-test", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {} },
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 3);
		assert.match(markdown, /- sandbox: not verified \(steiner exposes no read-only check\)/);
		assert.match(markdown, /on: run failed: exit 2/);
		assert.equal(raw.aliases[0].runs.on[0].failed, true);
		assert.equal(raw.aliases[0].runs.on[0].reason, "exit 2");
		assert.equal(raw.aliases[0].runs.off[0].failed, false);
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main reports a failed probe reason classified from stderr, never the raw line", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const secret = "probe-secret-token";
	const fake = makeFakeRunner({ failProbe: true, secret });
	const stderrLines = [];
	try {
		const code = await main(
			["--model", "claude-test", "--anthropic-alias", "claude-test", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1", SECRET_TOKEN: secret },
			{ spawnRun: fake.runner, log: (line) => stderrLines.push(line), stdout: () => {} },
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 0);
		assert.equal(raw.aliases[0].probe.ok, false);
		assert.equal(raw.aliases[0].probe.reason, "probe calls failed: exit 2: auth error (401)");
		assert.match(markdown, /Probe: failed \(probe calls failed: exit 2: auth error \(401\)\)/);
		assert.match(markdown, /Verdict: CONTINUITY_ONLY \(cache: no cache verdict\)/);
		for (const text of [markdown, JSON.stringify(raw), stderrLines.join("\n")]) {
			assert.ok(!text.includes(secret), "the secret value must not appear");
			assert.ok(!text.includes("Unauthorized"), "raw stderr must not appear");
			assert.ok(!text.includes("probe route unavailable"), "raw stderr must not appear");
		}
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main drops an unrecognized probe stderr and reports only the exit code", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ failProbe: true, probeStderr: "probe route unavailable\n" });
	try {
		await main(
			["--model", "claude-test", "--anthropic-alias", "claude-test", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {} },
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(raw.aliases[0].probe.reason, "probe calls failed: exit 2");
		assert.ok(!markdown.includes("probe route unavailable"));
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main skips the verdict for a single mode", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ sandbox: false });
	try {
		const code = await main(
			["--model", "gpt-test", "--modes", "on", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {} },
		);
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 0);
		assert.equal(raw.aliases[0].runs.off.length, 0);
		assert.equal(raw.sandbox, "disabled");
		assert.match(markdown, /- sandbox: disabled/);
		assert.match(markdown, /Verdict: INCOMPARABLE \(cache: no cache verdict\)/);
		assert.match(markdown, /- single mode/);
		assert.match(markdown, /\| turns \| not run \| 4 \|/);
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main gives each probe its own nonce and reuses it for that probe's two calls", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ sandbox: false });
	let next = 0;
	try {
		const code = await main(
			[
				"--model", "claude-one,claude-two",
				"--anthropic-alias", "claude-one,claude-two",
				"--files", "4",
				"--pad-tokens", "10",
				"--out", outDir,
			],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {}, newId: () => `id-${++next}` },
		);
		assert.equal(code, 0);
		const prompts = fake.invocations
			.filter((call) => call.args.at(-1).includes("Reply with OK only."))
			.map((call) => call.args.at(-1));
		assert.equal(prompts.length, 4, "two calls per probe and one probe per anthropic alias");
		assert.equal(prompts[0], prompts[1]);
		assert.equal(prompts[2], prompts[3]);
		assert.notEqual(prompts[0], prompts[2], "a second probe must not reuse the first probe's cached prompt");
		assert.match(prompts[0], /^Probe id: id-1\n/);
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("main alternates mode order per repeat and gives every run its own first request", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ sandbox: false });
	let next = 0;
	try {
		const code = await main(
			["--model", "gpt-test", "--repeat", "2", "--files", "4", "--pad-tokens", "10", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {}, newId: () => `run-${++next}` },
		);
		assert.equal(code, 0);
		const runs = fake.invocations.filter((call) => call.env.STEINER_EXPERIMENT_SEGMENTED_COMPACTION !== undefined);
		assert.deepEqual(
			runs.map((call) => call.env.STEINER_EXPERIMENT_SEGMENTED_COMPACTION),
			["0", "1", "1", "0"],
			"repeat 2 reverses the mode order so on does not inherit off's warm cache",
		);
		const prompts = runs.map((call) => call.args.at(-1));
		assert.deepEqual(
			prompts.map((prompt) => prompt.split("\n")[0]),
			["Run id: run-1", "Run id: run-2", "Run id: run-3", "Run id: run-4"],
		);
		assert.equal(new Set(prompts).size, 4, "no two runs share a byte-identical request");
		for (const prompt of prompts) {
			assert.match(prompt.split("\n")[1], /^Read the notes in notes\//);
		}
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});

test("a failed git init removes its scratch repo instead of leaving it for signal cleanup", async () => {
	const root = await mkdtemp(join(tmpdir(), "compaction-bench-gitinit-"));
	const benchTmp = join(root, "tmp");
	const outDir = join(root, "out");
	const savedTmp = process.env.TMPDIR;
	cleanupLiveResources();
	const failingGit = async (call) => {
		await mkdir(dirname(call.stdoutPath), { recursive: true });
		await mkdir(dirname(call.stderrPath), { recursive: true });
		await writeFile(call.stdoutPath, "");
		await writeFile(call.stderrPath, "fatal: not a git repository\n");
		return { code: 1, timedOut: false };
	};
	try {
		await mkdir(benchTmp);
		process.env.TMPDIR = benchTmp;
		const code = await main(
			["--model", "gpt-test", "--files", "4", "--pad-tokens", "1", "--out", outDir],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: failingGit, log: () => {}, stdout: () => {} },
		);
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 3);
		assert.match(raw.aliases[0].runs.off[0].invalidReason, /setup failed: git init failed: exit 1/);
		assert.deepEqual(readdirSync(benchTmp), [], "a scratch repo whose git init failed is removed right away");
		assert.equal(cleanupLiveResources().dirs, 0, "no scratch dir stays registered after a failed git init");
	} finally {
		if (savedTmp === undefined) delete process.env.TMPDIR;
		else process.env.TMPDIR = savedTmp;
		await rm(root, { recursive: true, force: true });
	}
});

for (const [signal, exitCode] of [
	["SIGINT", 130],
	["SIGTERM", 143],
]) {
	test(`the CLI exits ${exitCode} on ${signal} after killing the run group and removing scratch dirs`, { timeout: 60_000 }, async () => {
		const root = await mkdtemp(join(tmpdir(), "compaction-bench-signal-"));
		const benchTmp = join(root, "tmp");
		const outDir = join(root, "out");
		const pidPath = join(root, "run-child.pid");
		const steinerPath = join(root, "fake-steiner");
		let bench = null;
		let runPid = null;
		try {
			await mkdir(benchTmp);
			await writeFile(
				steinerPath,
				[
					"#!/bin/sh",
					'if [ "$1" = "config" ]; then',
					"  printf 'sandbox:\\n  enabled: true\\n'",
					"  exit 0",
					"fi",
					`printf '%s' "$$" > ${JSON.stringify(pidPath)}`,
					"exec sleep 300",
					"",
				].join("\n"),
				{ mode: 0o755 },
			);
			let exited = null;
			let resolveExit = null;
			const exitPromise = new Promise((resolve) => {
				resolveExit = resolve;
			});
			bench = spawn(
				process.execPath,
				[
					join(here, "compaction-bench.mjs"),
					"--model", "fake-model",
					"--modes", "off",
					"--files", "4",
					"--pad-tokens", "1",
					"--steiner", steinerPath,
					"--out", outDir,
				],
				{ env: { ...process.env, TMPDIR: benchTmp, STEINER_LIVE_BENCH: "1" }, stdio: "ignore" },
			);
			bench.on("exit", (code, killedBy) => {
				exited = { code, signal: killedBy };
				resolveExit(exited);
			});
			runPid = await waitForPidFile(pidPath, () => exited);
			bench.kill(signal);
			assert.deepEqual(await exitPromise, { code: exitCode, signal: null });
			assert.ok(await waitUntilGone(runPid), "the interrupted run's process group is killed");
			assert.deepEqual(readdirSync(benchTmp), [], "no scratch dir from the interrupted run survives");
		} finally {
			if (bench !== null) killQuietly("SIGKILL", bench.pid);
			if (runPid !== null) killQuietly("SIGKILL", -runPid);
			if (runPid !== null) killQuietly("SIGKILL", runPid);
			await rm(root, { recursive: true, force: true });
		}
	});
}

test("defaultSpawnRun kills a timed-out child and returns quickly", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-spawn-"));
	try {
		const started = Date.now();
		const result = await defaultSpawnRun({
			command: process.execPath,
			args: ["-e", "setTimeout(() => {}, 60000)"],
			cwd: dir,
			env: process.env,
			stdoutPath: join(dir, "out.txt"),
			stderrPath: join(dir, "err.txt"),
			timeoutMs: 300,
		});
		assert.equal(result.timedOut, true);
		assert.ok(Date.now() - started < 10_000, "the timeout kills the run instead of waiting it out");
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});

test("defaultSpawnRun kills the whole process group on timeout", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-group-"));
	const marker = join(dir, "grandchild-heartbeat.txt");
	try {
		const grandchild = [
			'const fs = require("node:fs");',
			`const marker = ${JSON.stringify(marker)};`,
			'fs.writeFileSync(marker, "start");',
			'setInterval(() => fs.appendFileSync(marker, "."), 100);',
		].join("\n");
		const parent = [
			'const { spawn } = require("node:child_process");',
			`spawn(process.execPath, ["-e", ${JSON.stringify(grandchild)}]);`,
			"setTimeout(() => {}, 60000);",
		].join("\n");
		const result = await defaultSpawnRun({
			command: process.execPath,
			args: ["-e", parent],
			cwd: dir,
			env: process.env,
			stdoutPath: join(dir, "out.txt"),
			stderrPath: join(dir, "err.txt"),
			timeoutMs: 1500,
		});
		assert.equal(result.timedOut, true);
		await new Promise((resolve) => setTimeout(resolve, 300));
		const beat = await readFile(marker, "utf8");
		assert.ok(beat.startsWith("start"), "the grandchild started before the kill");
		await rm(marker, { force: true });
		await new Promise((resolve) => setTimeout(resolve, 500));
		assert.equal(existsSync(marker), false, "the grandchild must die with the group");
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});

test("defaultSpawnRun kills background descendants when the run exits", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-exit-"));
	let pid = null;
	try {
		const result = await defaultSpawnRun({
			command: "sh",
			args: ["-c", "sleep 97 >/dev/null 2>&1 & echo $!"],
			cwd: dir,
			env: process.env,
			stdoutPath: join(dir, "out.txt"),
			stderrPath: join(dir, "err.txt"),
			timeoutMs: 30_000,
		});
		assert.equal(result.timedOut, false);
		assert.equal(result.code, 0);
		pid = Number((await readFile(join(dir, "out.txt"), "utf8")).trim());
		assert.ok(Number.isInteger(pid) && pid > 0, "the background sleep reports its pid");
		assert.ok(await waitUntilGone(pid), "the background sleep must die with the exited run's group");
		assert.deepEqual(cleanupLiveResources(), { processes: 0, dirs: 0 }, "a closed run is unregistered");
	} finally {
		if (pid !== null) killQuietly("SIGKILL", pid);
		await rm(dir, { recursive: true, force: true });
	}
});

test("cleanupLiveResources kills registered process groups and removes registered dirs", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-cleanup-"));
	await writeFile(join(dir, "marker.txt"), "x");
	const child = spawn("sleep", ["97"], { detached: true, stdio: "ignore" });
	child.unref();
	try {
		assert.ok(Number.isInteger(child.pid) && child.pid > 0, "the stand-in sleep started");
		registerTempDir(dir);
		registerProcessGroup(child.pid);
		assert.deepEqual(cleanupLiveResources(), { processes: 1, dirs: 1 });
		assert.equal(existsSync(dir), false, "the registered temp dir is removed");
		assert.ok(await waitUntilGone(child.pid), "the registered process group is killed");
		assert.deepEqual(cleanupLiveResources(), { processes: 0, dirs: 0 }, "a second cleanup finds nothing");
	} finally {
		killQuietly("SIGKILL", -child.pid);
		killQuietly("SIGKILL", child.pid);
		await rm(dir, { recursive: true, force: true });
	}
});

test("defaultSpawnRun turns a missing command into a failed result", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-spawn-"));
	try {
		const result = await defaultSpawnRun({
			command: join(dir, "no-such-steiner-binary"),
			args: ["--help"],
			cwd: dir,
			env: process.env,
			stdoutPath: join(dir, "out.txt"),
			stderrPath: join(dir, "err.txt"),
			timeoutMs: 5000,
		});
		assert.equal(result.timedOut, false);
		assert.equal(result.code, null);
		assert.match(result.error, /ENOENT/);
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});

test("defaultSpawnRun reports an unopenable output file as a failed result", async () => {
	const dir = await mkdtemp(join(tmpdir(), "compaction-bench-spawn-"));
	try {
		const result = await defaultSpawnRun({
			command: process.execPath,
			args: ["-e", "0"],
			cwd: dir,
			env: process.env,
			stdoutPath: join(dir, "missing-dir", "out.txt"),
			stderrPath: join(dir, "err.txt"),
			timeoutMs: 5000,
		});
		assert.equal(result.timedOut, false);
		assert.equal(result.code, null);
		assert.match(result.error, /ENOENT/);
	} finally {
		await rm(dir, { recursive: true, force: true });
	}
});

test("parseArgs accepts slash refs and synthesizes a safe alias", () => {
	const args = parseArgs(["--model", "opencode-go/beam-free,hy4-flash"]);
	assert.deepEqual(args.modelEntries, [
		{ ref: "opencode-go/beam-free", alias: "bench--opencode-go--beam-free", provider: "opencode-go", id: "beam-free" },
		{ ref: "hy4-flash", alias: "hy4-flash", provider: null, id: null },
	]);
	assert.equal(parseArgs(["--model", "a/b", "--anthropic-alias", "a/b"]).anthropicAliases[0], "a/b");
});

test("parseArgs rejects unsafe or colliding model refs", () => {
	for (const argv of [
		["--model", "a b"],
		["--model", "a:b"],
		["--model", "p/m/x"],
		["--model", "/m"],
		["--model", "p/"],
		["--model", "a--b/c,a/b--c"],
	]) {
		assert.throws(() => parseArgs(argv), /usage:/, `argv ${JSON.stringify(argv)} should throw with usage`);
	}
});

test("parseArgs rejects a threshold at or above the context window and bad seed values", () => {
	assert.equal(parseArgs(["--model", "a", "--context-window", "1000000"]).contextWindow, 1000000);
	assert.throws(() => parseArgs(["--model", "a", "--context-window", "15000"]), /--threshold 15000 must be less than --context-window 15000/);
	assert.throws(
		() => parseArgs(["--model", "a", "--threshold", "100", "--context-window", "100"]),
		/--threshold 100 must be less than --context-window 100/,
	);
	assert.equal(parseArgs(["--model", "a", "--threshold", "99", "--context-window", "100"]).contextWindow, 100);
	assert.equal(parseArgs(["--model", "a", "--seed", "0"]).seed, 0);
	for (const argv of [
		["--model", "a", "--context-window", "0"],
		["--model", "a", "--seed", "-1"],
		["--model", "a", "--seed", "1.5"],
	]) {
		assert.throws(() => parseArgs(argv), /usage:/, `argv ${JSON.stringify(argv)} should throw with usage`);
	}
});

test("percentile uses nearest rank and handles empty input", () => {
	assert.equal(percentile([], 0.5), null);
	assert.equal(percentile([5], 0.95), 5);
	assert.equal(percentile([1, 2, 3, 4], 0.5), 2);
	assert.equal(percentile([9, 1, 5], 0.95), 9);
	assert.equal(percentile([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 0.95), 10);
});

test("parseProviderLog counts attempts, final rate-limited failures, outcomes and timings", () => {
	const text = [
		JSON.stringify({ kind: "provider", payload: { outcome: "ok", attempts: 1, duration_ms: 100, ttft_ms: 10 } }),
		JSON.stringify({ kind: "provider", payload: { outcome: "exhausted", attempts: 3, error_class: "rate_limited", duration_ms: 300, ttft_ms: 30 } }),
		JSON.stringify({ outcome: "exhausted", attempts: 2, error_class: "http_5xx", duration_ms: 200 }),
		JSON.stringify({ kind: "cache", payload: { outcome: "ok", attempts: 1 } }),
		"not json",
	].join("\n");
	assert.deepEqual(parseProviderLog(text), {
		calls: 3,
		retries: 3,
		rateLimited: 1,
		errors: 2,
		durations: [100, 300, 200],
		ttfts: [10, 30],
	});
	assert.deepEqual(parseProviderLog(""), { calls: 0, retries: 0, rateLimited: 0, errors: 0, durations: [], ttfts: [] });
});

test("parseRunLog reads stage sizes, applied and failed compactions and observed limits", () => {
	const lines = [
		{
			type: "context_diagnostics",
			payload: {
				kind: "compaction",
				severity: "info",
				stages: [
					{ stage: "normal", retained_tokens: 400 },
					{ stage: "emergency", retained_tokens: 120 },
					{ stage: "emergency", retained_tokens: 60, failed: true },
				],
				context_window: 1000000,
				compaction_threshold_tokens: 560000,
			},
		},
		{
			type: "context_diagnostics",
			payload: { kind: "compaction", severity: "warning", stages: [{ stage: "normal", retained_tokens: 50 }], context_window: 1000000, compaction_threshold_tokens: 560000 },
		},
		{ type: "context_diagnostics", payload: { kind: "compaction", severity: "skipped", stages: [{ stage: "emergency", retained_tokens: 1, failed: true }] } },
		{ type: "context_diagnostics", payload: { kind: "compaction", severity: "skipped", compaction_result: "failed", stages: [{ stage: "emergency", retained_tokens: 5, failed: true }] } },
		{ type: "context_diagnostics", payload: { kind: "compaction", severity: "skipped", notes: ["compaction skipped: no stage applied"] } },
	].map((line) => JSON.stringify(line)).join("\n");
	const run = parseRunLog(lines);
	assert.equal(run.compactions, 2);
	assert.equal(run.emergencyCompactions, 1, "the skipped failure event is not a compaction");
	assert.equal(run.compactionFailures, 1);
	assert.equal(run.emergencyFailures, 1);
	assert.deepEqual(run.retainedTail, [120, 50], "a failed trailing stage never supplies the tail and failed events add none");
	assert.equal(run.contextWindow, 1000000);
	assert.equal(run.thresholdTokens, 560000);
});

test("analyzeRun takes provider timings, provider outcomes, retained tail and configured/observed limits", () => {
	const log = [
		{ type: "model_call_finished", payload: { turn: 1, prompt_tokens: 100, duration_ms: 111, ttft_ms: 11, success: true } },
		{ type: "model_call_finished", payload: { turn: 2, prompt_tokens: 100, duration_ms: 222, ttft_ms: 22, success: true } },
		{
			type: "context_diagnostics",
			payload: { kind: "compaction", severity: "info", stages: [{ stage: "normal", retained_tokens: 100 }, { stage: "emergency" }], context_window: 1000000, compaction_threshold_tokens: 560000 },
		},
	].map((line) => JSON.stringify(line)).join("\n");
	const providerText = [
		JSON.stringify({ kind: "provider", payload: { outcome: "ok", attempts: 1, duration_ms: 1000, ttft_ms: 100 } }),
		JSON.stringify({ kind: "provider", payload: { outcome: "exhausted", attempts: 3, error_class: "rate_limited", duration_ms: 3000, ttft_ms: 300 } }),
	].join("\n");
	const metrics = analyzeRun({
		stdoutText: "",
		logText: log,
		compactionText: "",
		providerText,
		fixtures: recallFixtures(),
		mode: "on",
		family: "openai",
		wallClockMs: 9000,
		contextWindow: 200000,
		threshold: 140000,
	});
	assert.equal(metrics.timingSource, "provider");
	assert.equal(metrics.durationMs, 4000, "provider timings win over the session log");
	assert.equal(metrics.durationP50Ms, 1000);
	assert.equal(metrics.durationP95Ms, 3000);
	assert.equal(metrics.ttftP50Ms, 100);
	assert.equal(metrics.ttftP95Ms, 300);
	assert.equal(metrics.retainedTailMedian, 0, "an applied stage without retained_tokens counts as zero");
	assert.equal(metrics.retainedTailMax, 0);
	assert.equal(metrics.emergencyCompactions, 1);
	assert.equal(metrics.compactionFailures, 0);
	assert.equal(metrics.emergencyFailures, 0);
	assert.equal(metrics.configuredContextWindow, 200000);
	assert.equal(metrics.configuredThresholdTokens, 140000);
	assert.equal(metrics.observedContextWindow, 1000000);
	assert.equal(metrics.observedThresholdTokens, 560000);
	assert.equal(metrics.providerRetries, 2);
	assert.equal(metrics.providerRateLimited, 1);
	assert.equal(metrics.providerErrors, 1);
	assert.equal(metrics.promptTokens, 200);
	assert.equal(metrics.cachedTokens, null);
	assert.equal(metrics.wallClockMs, 9000);
});

test("analyzeRun reports configured limits, null observed limits and null timing when nothing measured them", () => {
	const metrics = analyzeRun({
		stdoutText: "",
		logText: "",
		compactionText: "",
		fixtures: recallFixtures(),
		mode: "off",
		family: "openai",
		contextWindow: 200000,
		threshold: 140000,
	});
	assert.equal(metrics.configuredContextWindow, 200000);
	assert.equal(metrics.configuredThresholdTokens, 140000);
	assert.equal(metrics.observedContextWindow, null);
	assert.equal(metrics.observedThresholdTokens, null);
	assert.equal(metrics.retainedTailMedian, null);
	assert.equal(metrics.timingSource, null);
	assert.equal(metrics.durationMs, null, "missing timing is null, not zero");
	assert.equal(metrics.durationP50Ms, null);
	assert.equal(metrics.ttftP50Ms, null);
});

test("analyzeRun falls back to session-log timings when the provider stream measured none", () => {
	const log = [
		{ type: "model_call_finished", payload: { turn: 1, prompt_tokens: 10, duration_ms: 500, ttft_ms: 50, success: true } },
		{ type: "model_call_finished", payload: { turn: 2, prompt_tokens: 10, duration_ms: 1500, success: true } },
	].map((line) => JSON.stringify(line)).join("\n");
	const metrics = analyzeRun({
		stdoutText: "",
		logText: log,
		compactionText: "",
		providerText: JSON.stringify({ kind: "provider", payload: { outcome: "ok", attempts: 1 } }),
		fixtures: recallFixtures(),
		mode: "off",
		family: "openai",
	});
	assert.equal(metrics.timingSource, "session");
	assert.equal(metrics.durationMs, 2000);
	assert.equal(metrics.durationP50Ms, 500);
	assert.equal(metrics.durationP95Ms, 1500);
	assert.equal(metrics.ttftP50Ms, 50);
});

test("aggregateMode medians the timing, retry and retained-tail metrics", () => {
	const agg = aggregateMode([
		runRecord({
			wallClockMs: 1000,
			durationMs: 500,
			emergencyCompactions: 0,
			compactionFailures: 0,
			emergencyFailures: 0,
			retainedTailMedian: 10,
			retainedTailMax: 20,
			ttftP50Ms: 1,
			ttftP95Ms: 2,
			durationP50Ms: 3,
			durationP95Ms: 4,
			providerCalls: 1,
			providerRetries: 0,
			providerRateLimited: 0,
			providerErrors: 0,
			cachedTokens: 10,
			uncachedTokens: 90,
			promptTokens: 100,
			costProxy: 100,
		}),
		runRecord({
			wallClockMs: 3000,
			durationMs: 1500,
			emergencyCompactions: 2,
			compactionFailures: 2,
			emergencyFailures: 2,
			retainedTailMedian: 30,
			retainedTailMax: 40,
			ttftP50Ms: 3,
			ttftP95Ms: 4,
			durationP50Ms: 5,
			durationP95Ms: 6,
			providerCalls: 3,
			providerRetries: 2,
			providerRateLimited: 1,
			providerErrors: 1,
			cachedTokens: 30,
			uncachedTokens: 70,
			promptTokens: 100,
			costProxy: 300,
		}),
	]);
	assert.equal(agg.wallClockMs, 2000);
	assert.equal(agg.durationMs, 1000);
	assert.equal(agg.emergencyCompactions, 1);
	assert.equal(agg.compactionFailures, 1);
	assert.equal(agg.emergencyFailures, 1);
	assert.equal(agg.retainedTailMedian, 20);
	assert.equal(agg.retainedTailMax, 30);
	assert.equal(agg.providerRetries, 1);
	assert.equal(agg.providerRateLimited, 0.5);
	assert.equal(agg.providerErrors, 0.5);
	assert.equal(agg.uncachedTokens, 80);
	assert.equal(agg.costProxy, 200);
});

test("renderTable shows the timing, retry and retained-tail rows", () => {
	const md = renderTable([
		{
			alias: "m",
			family: "openai",
			informational: false,
			probe: null,
			off: modeAgg({ emergencyCompactions: 1, compactionFailures: 2, emergencyFailures: 1, retainedTailMedian: 120, retainedTailMax: 400, wallClockMs: 61000, durationMs: 59000, ttftP50Ms: 900, durationP50Ms: 2000, providerRetries: 3, providerRateLimited: 1, providerErrors: 0, uncachedTokens: 8000 }),
			on: modeAgg({ emergencyCompactions: 4, compactionFailures: 0, emergencyFailures: 0 }),
			verdict: { label: "PASS", cache: "checked", reasons: [] },
		},
	]);
	assert.match(md, /\| emergency compactions \(applied\) \| 1 \| 4 \|/);
	assert.match(md, /\| compaction failures \| 2 \| 0 \|/);
	assert.match(md, /\| emergency failures \(recorded stages, lower bound\) \| 1 \| 0 \|/);
	assert.match(md, /\| retained tail median \| 120 \| not reported \|/);
	assert.match(md, /\| retained tail max \| 400 \| not reported \|/);
	assert.match(md, /\| wall clock \| 61\.0 s \| not reported \|/);
	assert.match(md, /\| summed call duration \| 59\.0 s \| not reported \|/);
	assert.match(md, /\| ttft p50 \| 900 ms \| not reported \|/);
	assert.match(md, /\| call duration p50 \| 2\.0 s \| not reported \|/);
	assert.match(md, /\| provider retries \| 3 \| not reported \|/);
	assert.match(md, /\| rate limited failures \(429\) \| 1 \| not reported \|/);
	assert.match(md, /\| uncached input tokens \| 8000 \| not reported \|/);
});

test("readLogGenerations reads rotated generations oldest first", async () => {
	const text = await readLogGenerations("/logs/run.log.jsonl", { readText: async (path) => `${path}\n` });
	assert.equal(
		text,
		["/logs/run.log.jsonl.3", "/logs/run.log.jsonl.2", "/logs/run.log.jsonl.1", "/logs/run.log.jsonl"].map((path) => `${path}\n`).join(""),
	);
});

test("copyLogGenerations copies the active file and rotated generations", async () => {
	const src = await mkdtemp(join(tmpdir(), "compaction-bench-gen-src-"));
	const dst = await mkdtemp(join(tmpdir(), "compaction-bench-gen-dst-"));
	try {
		await writeFile(join(src, "run.log.jsonl"), "active\n");
		await writeFile(join(src, "run.log.jsonl.1"), "one\n");
		await writeFile(join(src, "run.log.jsonl.2"), "two\n");
		const copied = await copyLogGenerations(join(src, "run.log.jsonl"), join(dst, "run.log.jsonl"));
		assert.deepEqual(copied, [join(dst, "run.log.jsonl"), join(dst, "run.log.jsonl.1"), join(dst, "run.log.jsonl.2")]);
		assert.equal(await readFile(join(dst, "run.log.jsonl.2"), "utf8"), "two\n");
		assert.ok(!existsSync(join(dst, "run.log.jsonl.3")));
	} finally {
		await rm(src, { recursive: true, force: true });
		await rm(dst, { recursive: true, force: true });
	}
});

test("main benches a provider/id ref under its synthesized alias and keeps rotated evidence", async () => {
	const outDir = await mkdtemp(join(tmpdir(), "compaction-bench-e2e-"));
	const fake = makeFakeRunner({ sandbox: false });
	try {
		const code = await main(
			[
				"--model", "opencode-go/beam-free",
				"--modes", "off",
				"--context-window", "1000000",
				"--threshold", "560000",
				"--seed", "10",
				"--files", "4",
				"--pad-tokens", "10",
				"--out", outDir,
			],
			{ STEINER_LIVE_BENCH: "1" },
			{ spawnRun: fake.runner, log: () => {}, stdout: () => {} },
		);
		const raw = JSON.parse(await readFile(join(outDir, "raw.json"), "utf8"));
		assert.equal(code, 0);
		assert.equal(raw.contextWindow, 1000000);
		assert.equal(raw.seed, 10);
		assert.equal(raw.aliases[0].alias, "bench--opencode-go--beam-free");
		assert.equal(raw.aliases[0].ref, "opencode-go/beam-free");
		assert.equal(raw.aliases[0].provider, "opencode-go");
		assert.equal(raw.aliases[0].id, "beam-free");
		const run = raw.aliases[0].runs.off[0];
		assert.equal(typeof run.wallClockMs, "number");
		assert.equal(run.configuredContextWindow, 1000000);
		assert.equal(run.configuredThresholdTokens, 560000);
		assert.equal(run.observedContextWindow, null, "the fake log reports no limits, so none are claimed as observed");
		assert.equal(run.observedThresholdTokens, null);
		assert.equal(run.durationMs, 40, "provider timings cover both reported calls");
		assert.equal(run.providerRetries, 2);
		assert.equal(run.providerRateLimited, 1);
		assert.equal(run.providerErrors, 1);
		const modelIndex = (call) => call.args.indexOf("--model") + 1;
		const benchCall = fake.invocations.find((call) => call.args[modelIndex(call)] === "bench--opencode-go--beam-free");
		assert.ok(benchCall, "steiner runs under the synthesized alias");
		assert.match(benchCall.configText, /      provider: opencode-go/);
		assert.match(benchCall.configText, /      id: beam-free/);
		assert.match(benchCall.configText, /          context_window: 1000000/);
		assert.match(benchCall.configText, /          compaction_threshold_tokens: 560000/);
		const files = readdirSync(join(outDir, "bench--opencode-go--beam-free"));
		assert.ok(files.includes("off-r1.provider.jsonl"));
		assert.ok(files.includes("off-r1.cache.jsonl"));
		assert.ok(files.includes("off-r1.run.log.jsonl"));
		const markdown = await readFile(join(outDir, "results.md"), "utf8");
		assert.match(markdown, /- context window: 1000000/);
		assert.match(markdown, /- seed: 10 \+ repeat - 1/);
	} finally {
		await rm(outDir, { recursive: true, force: true });
	}
});


