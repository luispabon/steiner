#!/usr/bin/env node
// Live compaction bench: CLI entry and orchestration.
//
// The bench runs one `steiner --exec` per model alias, mode (off/on) and
// repeat against a generated notes chain, with a low compaction threshold so
// every run compacts many times. main() orchestrates the live runs and writes
// results.md plus raw.json. The sibling modules own the rest:
// compaction-bench-args.mjs (flags, guard, experiment env),
// compaction-bench-fixtures.mjs (notes chain, prompts, YAML configs),
// compaction-bench-parse.mjs (artefact parsing and recall scoring),
// compaction-bench-score.mjs (cache economics, validity, medians),
// compaction-bench-report.mjs (verdicts and Markdown), and
// compaction-bench-run.mjs (spawning, cleanup, probes and runs). This entry
// re-exports them so tests and callers keep one import path.
//
// The only environment values ever surfaced are
// STEINER_EXPERIMENT_SEGMENTED_COMPACTION and its three
// STEINER_EXPERIMENT_SEGMENT_* limit passthroughs.
//
// Usage: node scripts/compaction-bench.mjs --model <a,b> [flags]

import { randomUUID } from "node:crypto";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import { checkGuard, experimentEnv, parseArgs, USAGE } from "./compaction-bench-args.mjs";
import { generateFixtures } from "./compaction-bench-fixtures.mjs";
import { renderResults, SANDBOX_NOTE, verdict } from "./compaction-bench-report.mjs";
import { aggregateMode } from "./compaction-bench-score.mjs";
import { defaultSpawnRun, detectSandbox, installSignalHandlers, resolveScratchParent, runOnce, runProbe } from "./compaction-bench-run.mjs";

export * from "./compaction-bench-args.mjs";
export * from "./compaction-bench-fixtures.mjs";
export * from "./compaction-bench-parse.mjs";
export * from "./compaction-bench-report.mjs";
export * from "./compaction-bench-run.mjs";
export * from "./compaction-bench-score.mjs";

/** Run the live bench: probe, per-mode exec runs, results.md and raw.json. Returns the exit code. */
export async function main(argv = process.argv.slice(2), env = process.env, deps = {}) {
	const log = deps.log ?? ((line) => process.stderr.write(`${line}\n`));
	const writeOut = deps.stdout ?? ((text) => process.stdout.write(text));
	const spawnRun = deps.spawnRun ?? defaultSpawnRun;
	const now = deps.now ?? (() => new Date());
	const newId = deps.newId ?? (() => randomUUID());
	const workTree = deps.cwd ?? (() => process.cwd());
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
	const scratch = await resolveScratchParent(workTree());
	const results = [];
	for (const entry of opts.modelEntries) {
		const { alias, ref, provider, id } = entry;
		const family = opts.anthropicAliases.includes(ref) ? "anthropic" : "openai";
		const informational = opts.informationalAliases.includes(ref);
		const probe = family === "anthropic" ? await runProbe({ opts, entry, env, spawnRun, newId, scratchParent: scratch.parent }) : null;
		if (probe) log(`probe ${alias}: ${probe.ok ? "ok" : `failed (${probe.reason})`}`);
		const runs = { off: [], on: [] };
		for (let repeat = 1; repeat <= opts.repeat; repeat++) {
			// One seed across thresholds makes the fixtures identical for a paired
			// comparison; without --seed the repeat number stays the seed.
			const seed = opts.seed === null ? repeat : opts.seed + repeat - 1;
			const fixtures = generateFixtures(seed, opts.files, opts.padTokens);
			// Alternate the mode order per repeat so the second mode never starts from
			// a cache entry the first mode just warmed.
			const modes = repeat % 2 === 1 ? opts.modes : [...opts.modes].reverse();
			for (const mode of modes) {
				const run = await runOnce({ opts, entry, family, mode, repeat, runId: newId(), fixtures, out, env, spawnRun, scratchParent: scratch.parent });
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
		results.push({ alias, ref, provider, id, family, informational, probe, off, on, verdict: aliasVerdict, runs });
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
		contextWindow: opts.contextWindow,
		seed: opts.seed,
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
		scratchBase: scratch.scratchBase,
		aliases: results.map(({ alias, ref, provider, id, family, informational, probe, runs }) => ({ alias, ref, provider, id, family, informational, probe, runs })),
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
