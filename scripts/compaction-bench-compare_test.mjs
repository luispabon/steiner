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
		effectiveContextWindow: 1000000,
		effectiveThresholdTokens: 560000,
		...over,
	};
}

function rawFile(over = {}) {
	return {
		generatedAt: "2026-11-26T12:00:00.000Z",
		threshold: 560000,
		contextWindow: 1000000,
		files: 96,
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
	assert.equal(rows[0].exact, 24);
	assert.equal(rows[1].mode, "on");
	assert.equal(rows[1].exact, 23);
	const fallback = compareRows(
		rawFile({
			contextWindow: 200000,
			threshold: 140000,
			aliases: [{ alias: "hy4-flash", ref: "hy4-flash", runs: { off: [run({ effectiveContextWindow: null, effectiveThresholdTokens: null })] } }],
		}),
	);
	assert.equal(fallback[0].window, 200000);
	assert.equal(fallback[0].threshold, 140000);
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

test("renderCompare groups by window and model and prints every run's values", () => {
	const rows = sortRows([
		...compareRows(rawFile(), "/a/raw.json"),
		...compareRows(
			rawFile({
				contextWindow: 200000,
				aliases: [{ alias: "hy4-flash", ref: "hy4-flash", runs: { off: [run({ effectiveContextWindow: 200000, effectiveThresholdTokens: 140000 })] } }],
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
	assert.match(md, /\| threshold \| mode \| r \| continuity \| max turns \|/);
	assert.match(md, /\| 560000 \| off \| 1 \| 24\/24 \| no \| 12 \| 1 \| 900 \| 1200 \| 60\.0 s \| 59\.0 s \| 2 \| 1 \| 0 \| 500000 \| 20000 \| 70000 \|/);
	assert.match(md, /\| 560000 \| on \| 1 \| 23\/24 \|/);
	assert.match(md, /Retries sum attempts beyond the first/);
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
