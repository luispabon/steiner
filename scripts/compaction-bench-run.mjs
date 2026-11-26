// Execute the bench's live runs: scratch dirs, process spawning, cleanup,
// cache probes and one steiner --exec run per mode.

import { spawn } from "node:child_process";
import { closeSync, openSync, rmSync } from "node:fs";
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { experimentEnv } from "./compaction-bench-args.mjs";
import { buildBenchConfig, buildProbeConfig, taskPrompt } from "./compaction-bench-fixtures.mjs";
import { parseRunLog } from "./compaction-bench-parse.mjs";
import { analyzeRun, evaluateProbe, failedRun } from "./compaction-bench-score.mjs";

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

/** Install the SIGINT and SIGTERM handlers that clean up live resources before exit. */
export function installSignalHandlers() {
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

/** Report whether `steiner config` says the sandbox is enabled, or null when it cannot be read. */
export async function detectSandbox({ opts, env, spawnRun }) {
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
	try {
		await gitInit(spawnRun, dir, env, timeoutMs);
	} catch (error) {
		// The caller never sees this dir, so it would otherwise linger until a signal.
		// A failed removal must not mask the git init error the caller reports.
		await removeTempDir(dir).catch(() => {});
		throw error;
	}
	return dir;
}

// Create the scratch repo and the private dir with its diagnostics dir, then
// remove both once `body` settles, so a probe or run that fails part-way leaves
// nothing behind. Both stay registered until removed, so a signal still cleans up.
async function withBenchDirs({ spawnRun, env, timeoutMs }, body) {
	let repoDir = null;
	let privDir = null;
	try {
		repoDir = await createScratchRepo(spawnRun, env, timeoutMs);
		privDir = registerTempDir(await mkdtemp(join(tmpdir(), "compaction-bench-priv-")));
		const diagDir = join(privDir, "diag");
		await mkdir(diagDir, { recursive: true });
		return await body({ repoDir, privDir, diagDir });
	} finally {
		if (repoDir) await removeTempDir(repoDir);
		if (privDir) await removeTempDir(privDir);
	}
}

// The `steiner --exec` argument list both a probe call and a bench run use.
function steinerExecArgs({ alias, maxTurns, compactionPath, logPath, configPath, prompt }) {
	return [
		"--exec",
		"--model",
		alias,
		"--max-turns",
		String(maxTurns),
		"--compaction-log-file",
		compactionPath,
		"--log-file",
		logPath,
		"--config",
		configPath,
		"--trust-project-config",
		prompt,
	];
}

/** Probe cache control for one Anthropic alias: two one-turn calls with one shared prompt. */
export async function runProbe({ opts, alias, env, spawnRun, newId }) {
	const timeoutMs = opts.runTimeoutMinutes * 60_000;
	try {
		return await withBenchDirs({ spawnRun, env, timeoutMs }, async ({ repoDir, privDir, diagDir }) => {
			const probeText = probePrompt(newId());
			const configPath = join(privDir, "probe.yaml");
			await writeFile(configPath, buildProbeConfig({ diagnosticsDir: diagDir }));
			const calls = [];
			const exits = [];
			for (const attempt of [1, 2]) {
				const logPath = join(privDir, `probe-${attempt}.log.jsonl`);
				const stderrPath = join(privDir, `probe-${attempt}.stderr.txt`);
				const child = await spawnRun({
					command: opts.steiner,
					args: steinerExecArgs({
						alias,
						maxTurns: 1,
						compactionPath: join(privDir, `probe-${attempt}-compaction-log.txt`),
						logPath,
						configPath,
						prompt: probeText,
					}),
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
		});
	} catch (error) {
		return { ok: false, reason: `probe calls failed: ${error.message}`, exits: [] };
	}
}

/** Run one mode/repeat: write the notes chain and config, exec steiner, then analyse its artefacts. */
export async function runOnce({ opts, alias, family, mode, repeat, runId, fixtures, out, env, spawnRun }) {
	const timeoutMs = opts.runTimeoutMinutes * 60_000;
	const base = `${mode}-r${repeat}`;
	const runOutDir = join(out, alias);
	await mkdir(runOutDir, { recursive: true });
	try {
		return await withBenchDirs({ spawnRun, env, timeoutMs }, async ({ repoDir, privDir, diagDir }) => {
			await mkdir(join(repoDir, "notes"));
			for (const file of fixtures.files) {
				await writeFile(join(repoDir, file.name), file.content);
			}
			const configPath = join(privDir, "bench.yaml");
			await writeFile(configPath, buildBenchConfig({ alias, threshold: opts.threshold, diagnosticsDir: diagDir }));
			const logPath = join(privDir, "run.log.jsonl");
			const compactionPath = join(privDir, "compaction-log.txt");
			const stdoutPath = join(runOutDir, `${base}.stdout.txt`);
			const stderrPath = join(runOutDir, `${base}.stderr.txt`);
			const child = await spawnRun({
				command: opts.steiner,
				args: steinerExecArgs({
					alias,
					maxTurns: opts.maxTurns,
					compactionPath,
					logPath,
					configPath,
					prompt: taskPrompt(fixtures.files, runId),
				}),
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
				notesDir: join(repoDir, "notes"),
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
		});
	} catch (error) {
		return failedRun({ mode, repeat, reason: `setup failed: ${error.message}` });
	}
}
