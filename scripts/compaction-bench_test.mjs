import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
	USAGE,
	aggregateCache,
	aggregateMode,
	buildBenchConfig,
	checkGuard,
	costProxy,
	evaluateProbe,
	experimentEnv,
	generateFixtures,
	main,
	median,
	parseArgs,
	parseRunLog,
	parseSegmentDecisions,
	parseStdout,
	parseToolReads,
	renderTable,
	runValidity,
	scoreRecall,
	summarizeDecisions,
	taskPrompt,
	verdict,
} from "./compaction-bench.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixtureDir = join(here, "..", "testdata", "compaction-bench");
const fixture = (name) => readFileSync(join(fixtureDir, name), "utf8");

function recallFixtures() {
	return {
		files: [1, 2, 3, 4].map((n) => ({ name: `notes/f0${n}.txt`, content: "" })),
		codes: { f01: "A1-BC23", f02: "C2-DE45", f03: "E3-FG67", f04: "G4-HI89" },
		correction: { inFile: "f04", target: "f03", oldCode: "E3-FG67", newCode: "Z9-YX87" },
	};
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

test("taskPrompt walks the chain, forbids re-reads and listing, and asks for fNN lines", () => {
	const files = generateFixtures(5, 6, 10).files;
	const prompt = taskPrompt(files);
	assert.equal(prompt, taskPrompt(files));
	assert.match(prompt, /notes\/f01\.txt/);
	assert.match(prompt, /NEXT/);
	assert.match(prompt, /Never read a file twice/);
	assert.match(prompt, /Never list or search the notes directory/);
	assert.match(prompt, /CORRECTION/);
	assert.match(prompt, /"fNN: CODE"/);
	assert.match(prompt, /6 files/);
});

test("buildBenchConfig emits the threshold under the alias and the cache diagnostics stream", () => {
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
			"",
		].join("\n"),
	);
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
		{ promptTokens: 2000, cacheReported: true, cacheReadTokens: 500, cacheCreateTokens: 300 },
		{ promptTokens: 0, cacheReported: false, cacheReadTokens: 0, cacheCreateTokens: 0 },
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
		"assistant: turn 1 role assistant content draft: wrong",
		"status: model call model=x turn=1",
		"assistant: turn 2 role assistant content f01: A1-BC23",
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

test("median handles odd, even and empty input", () => {
	assert.equal(median([3, 1, 2]), 2);
	assert.equal(median([4, 1, 3, 2]), 2.5);
	assert.equal(median([]), null);
});

test("runValidity gates flag-on runs on compaction count and append decisions", () => {
	assert.deepEqual(runValidity({ mode: "on", compactions: 3, decisionSummary: { appends: 2 } }), {
		valid: false,
		reason: "fewer than 4 compactions",
	});
	assert.deepEqual(runValidity({ mode: "on", compactions: 4, decisionSummary: { appends: 0 } }), {
		valid: false,
		reason: "no append decision",
	});
	assert.deepEqual(runValidity({ mode: "on", compactions: 4, decisionSummary: { appends: 1 } }), { valid: true, reason: null });
	assert.deepEqual(runValidity({ mode: "off", compactions: 0, decisionSummary: { appends: 0 } }), { valid: true, reason: null });
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
			verdict: { label: "PASS", cache: "no cache verdict", reasons: ["cost proxy not judged: no cache reporting on one side"] },
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
	assert.match(md, /Verdict: PASS \(cache: no cache verdict\)/);
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
	return [`assistant: turn 8 role assistant content ${lines[0]}`, ...lines.slice(1), "status: run finished", ""].join("\n");
}

function makeFakeRunner({ failOn = null, failProbe = false, secret = "", sandbox = true } = {}) {
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
		if (call.args[call.args.length - 1].includes("Reply with OK only.")) {
			const alias = flag("--model");
			const attempt = (probeAttempts.get(alias) ?? 0) + 1;
			probeAttempts.set(alias, attempt);
			if (failProbe) {
				await writeFile(call.stdoutPath, "");
				await writeFile(call.stderrPath, `error: auth ${secret} rejected\nprobe route unavailable\n`);
				return { code: 2, timedOut: false };
			}
			const payload = attempt === 1
				? { turn: 1, prompt_tokens: 3000, cache_create_tokens: 2500, success: true }
				: { turn: 1, prompt_tokens: 3000, cache_read_tokens: 2500, success: true };
			await writeFile(flag("--log-file"), `${JSON.stringify({ type: "model_call_finished", payload })}\n`);
			await writeFile(call.stdoutPath, "assistant: turn 1 role assistant content OK\nstatus: run finished\n");
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
		await writeFile(call.stdoutPath, fakeStdout());
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
		}
		assert.equal(fake.normalRuns(), 4);

		for (const text of [markdown, JSON.stringify(raw), stderrLines.join("\n"), stdoutChunks.join("\n")]) {
			assert.ok(!text.includes(secret), "the secret env value must not appear in bench output");
			assert.ok(!text.includes("sk-should-not-leak"), "the api key env value must not appear in bench output");
		}
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

test("main reports a failed probe with a stderr line that carries no env value", async () => {
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
		assert.equal(raw.aliases[0].probe.reason, "probe calls failed: exit 2: probe route unavailable");
		assert.match(markdown, /Probe: failed \(probe calls failed: exit 2: probe route unavailable\)/);
		assert.ok(!markdown.includes(secret));
		assert.ok(!JSON.stringify(raw).includes(secret));
		assert.ok(!stderrLines.join("\n").includes(secret));
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


