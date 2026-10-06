import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const scripts = dirname(fileURLToPath(import.meta.url));
const script = join(scripts, "parallel-tool-stats.mjs");
const fixtures = join(scripts, "..", "testdata", "parallel-tool-stats");

function run(...args) {
	return JSON.parse(execFileSync("node", [script, "--dir", fixtures, "--json", ...args], { encoding: "utf8" }));
}

test("totals count single, multi and no-tool turns", () => {
	const { total } = run();
	assert.equal(total.sessions_scanned, 4);
	assert.equal(total.sessions, 3);
	assert.equal(total.turns_with_tools, 9);
	assert.equal(total.turns_parallel, 4);
	assert.equal(total.tool_calls, 20);
	assert.equal(total.parallel_rate, 4 / 9);
	assert.equal(total.mean_calls_per_turn, 20 / 9);
	assert.deepEqual(total.histogram, { 1: 5, 2: 1, 3: 1, 4: 1, "5+": 1 });
});

test("results are grouped per model", () => {
	const { per_model } = run();
	const claude = per_model["claude-sonnet-x"];
	assert.equal(claude.sessions, 2);
	assert.equal(claude.turns_with_tools, 7);
	assert.equal(claude.turns_parallel, 3);
	assert.equal(claude.tool_calls, 15);
	assert.equal(claude.mean_calls_per_turn, 15 / 7);
	assert.equal(per_model["gpt-test"].sessions, 1);
	assert.equal(per_model["gpt-test"].turns_parallel, 1);
});

test("--since drops earlier sessions", () => {
	const { total, per_model } = run("--since", "2026-10-01");
	assert.equal(total.sessions_scanned, 3);
	assert.equal(total.turns_with_tools, 6);
	assert.equal(per_model["claude-sonnet-x"].turns_with_tools, 4);
});

test("--model filters case-insensitively by substring", () => {
	const { total, per_model } = run("--model", "CLAUDE");
	assert.deepEqual(Object.keys(per_model), ["claude-sonnet-x"]);
	assert.equal(total.sessions_scanned, 2);
});

test("a window with only no-tool turns has null rates", () => {
	const { total } = run("--model", "gpt", "--since", "2026-10-03T11:00:00Z");
	assert.equal(total.sessions_scanned, 1);
	assert.equal(total.turns_with_tools, 0);
	assert.equal(total.parallel_rate, null);
	assert.equal(total.mean_calls_per_turn, null);
});

test("missing directory yields an empty report", () => {
	const out = JSON.parse(execFileSync("node", [script, "--dir", join(fixtures, "nope"), "--json"], { encoding: "utf8" }));
	assert.equal(out.total.sessions_scanned, 0);
	assert.deepEqual(out.per_model, {});
});

test("human output lists models and a TOTAL row", () => {
	const out = execFileSync("node", [script, "--dir", fixtures], { encoding: "utf8" });
	assert.match(out, /claude-sonnet-x/);
	assert.match(out, /TOTAL/);
});
