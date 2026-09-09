// Smoke tests for diagnostics.mjs: assert a non-zero row count and at least
// one hand-computed aggregate value against testdata/diagnostics/, per mode.
// A parser that silently reports zero rows after a format change looks like
// a healthy result -- these tests exist to catch exactly that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(__dirname, "diagnostics.mjs");
const FIXTURES = join(__dirname, "..", "testdata", "diagnostics");

function run(args) {
	const out = execFileSync("node", [SCRIPT, ...args], { encoding: "utf8" });
	return JSON.parse(out);
}

test("cache mode: by-model row count and hand-computed hit rate", () => {
	const out = run(["cache", "--dir", FIXTURES, "--json"]);
	const byModel = out["by model"];
	assert.equal(byModel.length, 1);
	const row = byModel[0];
	assert.equal(row.key, "claude-sonnet-5");
	assert.equal(row.n, 5);
	// prompt totals: 1000+1200+800+1000+1300 = 5300
	// cache_read totals: 0+1000+0+100+1200 = 2300
	// cache_create totals: 1000+200+800+900+100 = 3000
	// nonCached = max(prompt - read - create, 0) per record, summed = 0
	// hit_rate = 2300 / (0 + 2300 + 3000) = 0.4339...
	assert.ok(Math.abs(row.metrics.hitRate - 2300 / 5300) < 1e-9);
});

test("cache mode: by-source groups parent and sub_agent separately", () => {
	const out = run(["cache", "--dir", FIXTURES, "--json"]);
	const bySource = out["by source"];
	const parent = bySource.find((r) => r.key === "parent");
	const subAgent = bySource.find((r) => r.key === "sub_agent");
	assert.equal(parent.n, 4);
	assert.equal(subAgent.n, 1);
	// Only one record per run_id may carry cold_start: true (see
	// internal/agent/cache_diagnostics.go's coldStartRecorded latch); run-a's
	// parent turn 1 claims it, so the sub_agent record in the same run must not.
	assert.equal(subAgent.metrics.coldStarts, 0);
});

test("provider mode: outcome mix and retry rate", () => {
	const out = run(["provider", "--dir", FIXTURES, "--json"]);
	assert.equal(out.outcome_mix.length, 4);
	const total = out.outcome_mix.reduce((a, r) => a + r.n, 0);
	assert.equal(total, 5);
	const byProviderModel = out.by_provider_model.find((r) => r.key === "anthropic/claude-sonnet-5");
	assert.equal(byProviderModel.n, 3);
	// one of the three anthropic records is retried (attempts=2), one exhausted (attempts=3)
	assert.ok(Math.abs(byProviderModel.metrics.retryRate - 2 / 3) < 1e-9);
});

test("provider mode: error_class histogram excludes ok records", () => {
	const out = run(["provider", "--dir", FIXTURES, "--json"]);
	const total = out.error_class.reduce((a, r) => a + r.n, 0);
	assert.equal(total, 3);
	assert.ok(out.error_class.some((r) => r.key === "http_5xx"));
});

test("tools mode: failure rate by tool and mutate op breakdown", () => {
	const out = run(["tools", "--dir", FIXTURES, "--json"]);
	const mutate = out.by_tool.find((r) => r.key === "mutate");
	assert.equal(mutate.n, 3);
	// mutate.jsonl fixtures: 1 error (1 failed op), 1 ok, 1 error (2 failed ops) => 2/3 failed calls
	assert.ok(Math.abs(mutate.metrics.failRate - 2 / 3) < 1e-9);
	const opsByCount = Object.fromEntries(out.mutate_by_op.map((r) => [r.key, r.n]));
	assert.equal(opsByCount.replace, 1);
	assert.equal(opsByCount.create, 1);
	assert.equal(opsByCount.move, 1);
});

test("tools mode: bash denial surfaces as a failure with no op", () => {
	const out = run(["tools", "--dir", FIXTURES, "--json"]);
	const bash = out.by_tool.find((r) => r.key === "bash");
	assert.equal(bash.n, 1);
	assert.equal(bash.metrics.failRate, 1);
});

test("tools mode: reason histogram is keyed by tool, not reason alone", () => {
	const out = run(["tools", "--dir", FIXTURES, "--json"]);
	const keys = out.reason_histogram.map((r) => r.key);
	assert.ok(keys.includes("mutate/no_match"));
	assert.ok(keys.includes("bash/approval_denied"));
	assert.ok(keys.includes("mutate/target_exists"));
	assert.ok(keys.includes("mutate/path_policy"));
	// A flat "no_match"/"approval_denied" key (no tool prefix) would wrongly
	// merge failures from different tools into the same bucket.
	assert.ok(!keys.includes("no_match"));
});

test("prefix mode: append-only and break-at-N verdicts", () => {
	const logfile = join(FIXTURES, "session.jsonl");
	const rows = run(["prefix", logfile, "--json"]);
	assert.equal(rows.length, 4);

	const topLevel = rows.filter((r) => r.agentID === "(top-level)");
	assert.equal(topLevel.length, 2);
	assert.equal(topLevel[0].verdict, "-");
	assert.equal(topLevel[1].verdict, "APPEND-ONLY");
	assert.equal(topLevel[1].cached, 3);
	assert.equal(topLevel[1].msgs, 4);
	assert.equal(topLevel[1].raw, 650);

	const child = rows.filter((r) => r.agentID === "child-1");
	assert.equal(child.length, 2);
	assert.equal(child[0].verdict, "-");
	assert.equal(child[1].verdict, "BREAK-AT-1");
	assert.equal(child[1].cached, 1);
});

test("cache mode --compare prints both build_sha columns with a delta", () => {
	const out = execFileSync("node", [SCRIPT, "cache", "--dir", FIXTURES, "--compare", "aaaa1111", "bbbb2222"], { encoding: "utf8" });
	assert.match(out, /aaaa1111 vs bbbb2222/);
	assert.match(out, /hit_rate\(a\)/);
	assert.match(out, /hit_rate\(delta\)/);
});

test("cache mode --compare --json prints one aggregated document with a/b/delta, never raw records", () => {
	const out = run(["cache", "--dir", FIXTURES, "--compare", "aaaa1111", "bbbb2222", "--json"]);
	assert.equal(out.sha_a, "aaaa1111");
	assert.equal(out.sha_b, "bbbb2222");
	const bySource = out.sections["cache by source"];
	const parent = bySource.find((r) => r.key === "parent");
	assert.ok(parent);
	assert.equal(typeof parent["hit_rate"].a, "number");
	assert.equal(typeof parent["hit_rate"].b, "number");
	assert.ok(Math.abs(parent["hit_rate"].delta - (parent["hit_rate"].b - parent["hit_rate"].a)) < 1e-9);
});

test("--top bounds unbounded listings", () => {
	const out = run(["tools", "--dir", FIXTURES, "--top", "1", "--json"]);
	assert.ok(out.by_tool.length <= 1);
});
