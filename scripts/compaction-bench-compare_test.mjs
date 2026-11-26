import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { USAGE, compareRows, findRawFiles, loadRaws, main, renderCompare, sortRows } from "./compaction-bench-compare.mjs";

function run(over = {}) {
	return {
		mode: "off",
		repeat: 1,
		failed: false,
		valid: true,
		invalidReason: null,
		exact: 24,
		lost: 0,
		total: 24,
		maxTurnsHit: false,
		compactions: 12,
		emergencyCompactions: 1,
		compactionFailures: 0,
		emergencyFailures: 0,
		timingSource: "provider",
		retainedTailMedian: 900,
		retainedTailMax: 1200,
		wallClockMs: 60000,
		durationMs: 59000,
		providerRetries: 2,
		providerRateLimited: 1,
		providerErrors: 0,
		cachedTokens: 500000,
		uncachedTokens: 20000,
		costProxy: 70000,
		configuredContextWindow: 1000000,
		configuredThresholdTokens: 560000,
		observedContextWindow: 1000000,
		observedThresholdTokens: 560000,
		...over,
	};
}

function rawFile(over = {}) {
	return {
		generatedAt: "2026-11-26T12:00:00.000Z",
		threshold: 560000,
		contextWindow: 1000000,
		seed: null,
		files: 96,
		padTokens: 10,
		maxTurns: 80,
		aliases: [
			{
				alias: "bench--opencode-go--beam-free",
				ref: "opencode-go/beam-free",
				family: "openai",
				runs: { off: [run({ mode: "off" })], on: [run({ mode: "on", threshold: 560000, exact: 23 })] },
			},
		],
		...over,
	};
}

test("compareRows reads one row per run, preferring the run's effective window and threshold", () => {
	const rows = compareRows(rawFile(), "/bench/raw.json");
	assert.equal(rows.length, 2);
	assert.equal(rows[0].model, "bench--opencode-go--beam-free");
	assert.equal(rows[0].ref, "opencode-go/beam-free");
	assert.equal(rows[0].window, 1000000);
	assert.equal(rows[0].threshold, 560000);
	assert.equal(rows[0].observedWindow, 1000000);
	assert.equal(rows[0].configuredWindow, 1000000);
	assert.equal(rows[0].compactionFailures, 0);
	assert.equal(rows[0].emergencyFailures, 0);
	assert.equal(rows[0].timingSource, "provider");
	assert.equal(rows[0].exact, 24);
	assert.equal(rows[1].mode, "on");
	assert.equal(rows[1].exact, 23);
	const fallback = compareRows(
		rawFile({
			contextWindow: 200000,
			threshold: 140000,
			aliases: [
				{
					alias: "hy4-flash",
					ref: "hy4-flash",
					runs: { off: [run({ observedContextWindow: null, observedThresholdTokens: null, configuredContextWindow: null, configuredThresholdTokens: null })] },
				},
			],
		}),
	);
	assert.equal(fallback[0].window, 200000);
	assert.equal(fallback[0].threshold, 140000);
});

test("compareRows carries the effective seed, source and fixture settings", () => {
	const rows = compareRows(rawFile(), "/phase1/w1000000-t560000/raw.json");
	assert.equal(rows[0].seed, 1, "without --seed the repeat number is the seed");
	assert.equal(rows[0].source, "/phase1/w1000000-t560000/raw.json");
	assert.equal(rows[0].files, 96);
	assert.equal(rows[0].padTokens, 10);
	assert.equal(rows[0].maxTurns, 80);
	const seeded = compareRows(
		rawFile({
			seed: 11,
			aliases: [{ alias: "hy4-flash", ref: "hy4-flash", runs: { off: [run({ repeat: 2 }), run({ repeat: 3 })], on: [] } }],
		}),
		"run-b/raw.json",
	);
	assert.deepEqual(seeded.map((row) => row.seed), [12, 13], "--seed N with repeat r uses N + r - 1");
	const missing = compareRows(rawFile({ files: null, padTokens: undefined, maxTurns: null, seed: 4, aliases: [{ alias: "m", ref: "m", runs: { off: [run({ repeat: null })] } }] }));
	assert.equal(missing[0].files, null);
	assert.equal(missing[0].padTokens, null);
	assert.equal(missing[0].maxTurns, null);
	assert.equal(missing[0].seed, null, "a run without a repeat cannot resolve a base seed");
});

test("compareRows flags an observed limit that differs from the configured one", () => {
	const rows = compareRows(
		rawFile({
			contextWindow: 200000,
			threshold: 140000,
			aliases: [
				{
					alias: "hy4-flash",
					ref: "hy4-flash",
					runs: {
						off: [
							run({
								configuredContextWindow: 200000,
								configuredThresholdTokens: 140000,
								observedContextWindow: 1000000,
								observedThresholdTokens: 560000,
							}),
						],
					},
				},
			],
		}),
		"/bench/raw.json",
	);
	assert.equal(rows[0].windowMismatch, true);
	assert.equal(rows[0].thresholdMismatch, true);
	assert.equal(rows[0].window, 1000000, "grouping keeps the observed window");
	const md = renderCompare(sortRows(rows), ["/bench/raw.json"]);
	assert.match(md, /## window 1000000/);
	assert.match(md, /\| 560000 \| window\+threshold \|/);
	assert.match(md, /1 run\(s\) reported an observed limit that differs from the configured one/);
	const thresholdOnly = compareRows(
		rawFile({ aliases: [{ alias: "m", ref: "m", runs: { off: [run({ observedThresholdTokens: 600000 })] } }] }),
		"raw.json",
	);
	assert.equal(thresholdOnly[0].thresholdMismatch, true);
	assert.equal(thresholdOnly[0].windowMismatch, false);
	assert.match(renderCompare(sortRows(thresholdOnly), ["raw.json"]), /\| 600000 \| threshold \|/);
	const matching = compareRows(rawFile({ aliases: [{ alias: "m", ref: "m", runs: { off: [run()] } }] }), "raw.json");
	assert.equal(matching[0].windowMismatch, false);
	assert.equal(matching[0].thresholdMismatch, false);
	assert.match(renderCompare(sortRows(matching), ["raw.json"]), /0 run\(s\) reported an observed limit/);
});

test("compareRows marks failed and invalid runs instead of scoring them", () => {
	const rows = compareRows(
		rawFile({
			aliases: [
				{
					alias: "m",
					ref: "m",
					runs: {
						off: [run({ failed: true, valid: false, invalidReason: "run failed: exit 3" })],
						on: [run({ mode: "on", valid: false, invalidReason: "fewer than 4 compactions" })],
					},
				},
			],
		}),
	);
	const md = renderCompare(sortRows(rows), ["raw.json"]);
	assert.match(md, /failed: run failed: exit 3/);
	assert.match(md, /invalid: fewer than 4 compactions/);
});

test("sortRows orders by window, model, threshold, mode and repeat", () => {
	const rows = [
		{ window: 1000000, model: "b", threshold: 700000, mode: "on", repeat: 1 },
		{ window: 200000, model: "a", threshold: 140000, mode: "on", repeat: 2 },
		{ window: 1000000, model: "b", threshold: 560000, mode: "on", repeat: 1 },
		{ window: 1000000, model: "b", threshold: 560000, mode: "off", repeat: 1 },
		{ window: 1000000, model: "b", threshold: 560000, mode: "off", repeat: 2 },
	];
	assert.deepEqual(
		sortRows(rows).map((row) => `${row.window}/${row.model}/${row.threshold}/${row.mode}/${row.repeat}`),
		[
			"200000/a/140000/on/2",
			"1000000/b/560000/off/1",
			"1000000/b/560000/off/2",
			"1000000/b/560000/on/1",
			"1000000/b/700000/on/1",
		],
	);
});

test("sortRows orders equal runs by their effective seed", () => {
	const rows = [
		{ window: 1, model: "m", threshold: 1, mode: "off", repeat: 1, seed: 12, source: "/a/raw.json" },
		{ window: 1, model: "m", threshold: 1, mode: "off", repeat: 1, seed: 11, source: "/z/raw.json" },
	];
	assert.deepEqual(sortRows(rows).map((row) => row.seed), [11, 12], "the seed orders phase1 rows ahead of the source path");
});

test("renderCompare groups by window and model and prints every run's values", () => {
	const rows = sortRows([
		...compareRows(rawFile(), "/a/raw.json"),
		...compareRows(
			rawFile({
				contextWindow: 200000,
				seed: 11,
				aliases: [{ alias: "hy4-flash", ref: "hy4-flash", runs: { off: [run({ observedContextWindow: 200000, observedThresholdTokens: 140000, configuredContextWindow: 200000, configuredThresholdTokens: 140000 })] } }],
			}),
			"/b/raw.json",
		),
	]);
	const md = renderCompare(rows, ["/a/raw.json", "/b/raw.json"]);
	assert.match(md, /^# Compaction bench comparison/);
	assert.match(md, /- sources: 2 raw\.json file\(s\)/);
	assert.match(md, /- runs: 3/);
	assert.match(md, /## window 200000/);
	assert.match(md, /## window 1000000/);
	assert.ok(md.indexOf("## window 200000") < md.indexOf("## window 1000000"), "windows sort ascending");
	assert.match(md, /### hy4-flash/);
	assert.match(md, /### bench--opencode-go--beam-free \(opencode-go\/beam-free\)/);
	assert.match(md, /\| threshold \| limits \| mode \| r \| seed \| continuity \| max turns \|/);
	assert.match(md, /\| 560000 \| - \| off \| 1 \| 1 \| 24\/24 \| no \| 12 \| 1 \| 0 \| 0 \| 900 \| 1200 \| 60\.0 s \| 59\.0 s \| 2 \| 1 \| 0 \| 500000 \| 20000 \| 70000 \| \/a\/raw\.json \|/);
	assert.match(md, /\| 560000 \| - \| on \| 1 \| 1 \| 23\/24 \|/);
	assert.match(md, /\| 140000 \| - \| off \| 1 \| 11 \| 24\/24 \| .* \| \/b\/raw\.json \|/, "a seeded invocation's rows carry their own seed and source");
	assert.match(md, /Retries sum attempts beyond the first/);
	assert.match(md, /429 fail counts final rate-limited failures only/);
	assert.match(md, /emergency fail is a lower bound over failures that recorded an emergency stage/);
	assert.match(md, /a failed compaction reports no usage for any of its summary calls/);
	assert.match(md, /JSON rows carry timingSource: provider, session/);
	assert.match(md, /may include delegated or advisor calls/);
});

test("findRawFiles scans directories recursively, dedupes and rejects missing paths", async () => {
	const root = await mkdtemp(join(tmpdir(), "bench-compare-"));
	try {
		await mkdir(join(root, "a", "nested"), { recursive: true });
		await writeFile(join(root, "a", "raw.json"), "{}\n");
		await writeFile(join(root, "a", "nested", "raw.json"), "{}\n");
		await writeFile(join(root, "a", "notes.txt"), "x\n");
		assert.deepEqual(findRawFiles([root, join(root, "a", "raw.json")]), [join(root, "a", "nested", "raw.json"), join(root, "a", "raw.json")].sort());
		assert.throws(() => findRawFiles([join(root, "missing")]), /no such path/);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("loadRaws names the file that fails to parse", async () => {
	const root = await mkdtemp(join(tmpdir(), "bench-compare-"));
	try {
		await writeFile(join(root, "raw.json"), "not json\n");
		assert.throws(() => loadRaws([join(root, "raw.json")]), /read .*raw\.json/);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("main prints Markdown, JSON and usage errors", async () => {
	const root = await mkdtemp(join(tmpdir(), "bench-compare-"));
	const runDir = join(root, "run");
	try {
		await mkdir(runDir, { recursive: true });
		await writeFile(join(runDir, "raw.json"), JSON.stringify(rawFile()));
		const out = [];
		const err = [];
		const deps = { stdout: (text) => out.push(text), log: (line) => err.push(line) };
		assert.equal(main([root], deps), 0);
		assert.match(out.join(""), /# Compaction bench comparison/);
		assert.match(out.join(""), /## window 1000000/);
		out.length = 0;
		assert.equal(main([runDir, "--json"], deps), 0);
		const payload = JSON.parse(out.join(""));
		assert.equal(payload.sources.length, 1);
		assert.equal(payload.rows.length, 2);
		assert.equal(payload.rows[0].model, "bench--opencode-go--beam-free");
		assert.equal(payload.rows[0].timingSource, "provider");
		assert.equal(payload.rows[0].source, join(runDir, "raw.json"));
		assert.equal(payload.rows[0].seed, 1);
		assert.equal(payload.rows[0].files, 96);
		assert.equal(payload.rows[0].padTokens, 10);
		assert.equal(payload.rows[0].maxTurns, 80);
		assert.equal(payload.rows[0].windowMismatch, false);
		assert.equal(payload.rows[0].thresholdMismatch, false);
		out.length = 0;
		assert.equal(main(["--help"], deps), 0);
		assert.equal(out.join(""), USAGE);
		assert.equal(main([], deps), 2);
		assert.match(err.join("\n"), /at least one out dir or raw\.json is required/);
		err.length = 0;
		assert.equal(main(["--bogus"], deps), 2);
		assert.match(err.join("\n"), /unknown flag: --bogus/);
		err.length = 0;
		assert.equal(main([join(root, "missing")], deps), 2);
		assert.match(err.join("\n"), /no such path/);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});

test("main reports an empty scan instead of printing an empty table", async () => {
	const root = await mkdtemp(join(tmpdir(), "bench-compare-"));
	const err = [];
	try {
		assert.equal(main([root], { stdout: () => {}, log: (line) => err.push(line) }), 2);
		assert.match(err.join("\n"), /no raw\.json found/);
	} finally {
		await rm(root, { recursive: true, force: true });
	}
});
