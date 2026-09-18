import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";

const scripts = dirname(fileURLToPath(import.meta.url));
const delegationScript = join(scripts, "delegation-session-stats.mjs");
const mutateScript = join(scripts, "mutate-session-stats.mjs");

function run(script, dir) {
	return JSON.parse(execFileSync("node", [script, "--dir", dir, "--json"], { encoding: "utf8" }));
}

function tempDir() {
	return mkdtempSync(join(tmpdir(), "steiner-session-stats-"));
}

function sessionWithMutates(contents) {
	const messages = [];
	for (const [index, content] of contents.entries()) {
		const id = `call-${index}`;
		messages.push({
			role: "assistant",
			tool_calls: [{ id, name: "mutate", arguments: { operations: [{ type: "write", path: "file.txt" }] } }],
		});
		messages.push({ role: "tool", name: "mutate", tool_call_id: id, content });
	}
	return { model: "test-model", lineage: { generations: [{ messages }] } };
}

test("session stats return empty reports for a missing directory", () => {
	const dir = join(tempDir(), "missing");
	try {
		assert.deepEqual(run(delegationScript, dir), { rows: [], batches: [] });
		const mutate = run(mutateScript, dir);
		assert.equal(mutate.sessions_scanned, 0);
		assert.equal(mutate.calls, 0);
		assert.equal(mutate.failed_calls, 0);
		assert.deepEqual(mutate.op_type_usage, {});
		assert.deepEqual(mutate.failure_class, {});
	} finally {
		rmSync(dirname(dir), { recursive: true, force: true });
	}
});

test("mutate stats classify result envelopes, not error text", () => {
	const dir = tempDir();
	try {
		const contents = [
			JSON.stringify({ operations_failed: 0, error: "ordinary output error text" }, null, 2),
			JSON.stringify({ operations_failed: 0, output: "ordinary output contains error" }, null, 2),
			JSON.stringify({ operations_failed: 1, operations_skipped: 1, output: "failed" }, null, 2),
			"mutate: input could not be decoded",
			JSON.stringify({ error: "mutate result envelope error" }, null, 2),
		];
		writeFileSync(join(dir, "session.json"), JSON.stringify(sessionWithMutates(contents)));

		const stats = run(mutateScript, dir);
		assert.equal(stats.calls, 5);
		assert.equal(stats.operations, 5);
		assert.equal(stats.failed_calls, 3);
		assert.equal(stats.ops_never_attempted, 1);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});
