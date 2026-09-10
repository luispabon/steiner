// Smoke tests for diagnostics.mjs: assert a non-zero row count and at least
// one hand-computed aggregate value against testdata/diagnostics/, per mode.
// A parser that silently reports zero rows after a format change looks like
// a healthy result -- these tests exist to catch exactly that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(__dirname, "diagnostics.mjs");
const FIXTURES = join(__dirname, "..", "testdata", "diagnostics");
// coldturns needs cache and tool records that line up in time, which the
// flat fixtures deliberately do not: they exist to pin per-mode aggregates
// and every existing assertion counts their rows.
const COLDTURN_FIXTURES = join(FIXTURES, "coldturns");

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

// ---------------------------------------------------------------- coldturns

const coldturns = (extra = []) => run(["coldturns", "--dir", COLDTURN_FIXTURES, "--json", ...extra]);

test("coldturns: a model that never reports cache reads is excluded, not counted 100% cold", () => {
	const out = coldturns();
	// silent-upstream has two parent records, both with no cache_read_tokens.
	// Counting it would put a 100% cold row at the top of the table, which is
	// a fabricated finding: the backend simply never sent the field.
	assert.deepEqual(out.excluded_no_cache_reporting.map((r) => r.key), ["silent-upstream"]);
	assert.equal(out.excluded_no_cache_reporting[0].n, 2);
	assert.ok(!out.by_model.some((r) => r.key === "silent-upstream"));
	assert.ok(!out.delegation_ladder.some((r) => r.key.startsWith("silent-upstream")));
});

test("coldturns: groups by backend_model_id, so one provider_type does not merge upstreams", () => {
	const out = coldturns();
	const keys = out.by_model.map((r) => r.key);
	assert.ok(keys.includes("gpt-5.6-terra"));
	assert.ok(keys.includes("glm-5.3"));
	// glm-5.3 is provider_type openai_compat, as several resold upstreams are.
	// A row keyed on provider_type would collapse them into one meaningless
	// average.
	assert.ok(!keys.includes("openai_compat"));
	assert.ok(!keys.includes("codex"));
});

test("coldturns: drops each run's first turn, which is a cold start not a cold turn", () => {
	const out = coldturns();
	// cx-1 has 7 parent records; only 6 have a predecessor. Its turn 1 is
	// cold (cold_start: true) and must not appear as a cold turn.
	const terra = out.by_model.find((r) => r.key === "gpt-5.6-terra");
	assert.equal(terra.n, 6);
	assert.equal(terra.metrics.coldTurns, 5);
});

test("coldturns: a warm turn after a long delegation is counted, not skipped", () => {
	const out = coldturns();
	const terra = out.by_model.find((r) => r.key === "gpt-5.6-terra");
	// Three delegations over LONG_DELEGATION_MS sit in terra's gaps; two of
	// those turns are cold. The third (turn 2, a 596s sub_agent) is warm, and
	// is the observation #569 hinges on -- if long delegations stopped being
	// counted when warm, the denominator would silently vanish.
	assert.equal(terra.metrics.longDelegationN, 3);
	assert.equal(terra.metrics.longDelegationCold, 2);
});

test("coldturns: the join tolerates a delegation starting just before the previous turn", () => {
	const out = coldturns();
	// cx-1 turn 2's sub_agent runs 596s and ends 5s before the turn, so it
	// starts 1s *before* the previous cache record's ts. The two streams are
	// stamped at different points in the turn, so a strict containment test
	// would drop it and undercount long delegations.
	const row = out.delegation_ladder.find((r) => r.key === "gpt-5.6-terra | 300-600s");
	assert.equal(row.n, 3);
});

test("coldturns: a delegation from another run never attributes across runs", () => {
	const out = coldturns();
	// other-run's 590s sub_agent sits squarely inside cx-1's turn-5 gap. If
	// run_id were ignored, that cold turn would classify as delegation
	// instead of idle -- the exact false positive this mode exists to avoid.
	const byClass = Object.fromEntries(out.cold_turn_attribution.map((r) => [r.key, r.n]));
	assert.equal(byClass.idle, 1);
	assert.equal(byClass.delegation, 1);
});

test("coldturns: a rewritten prefix outranks a long delegation in attribution", () => {
	const out = coldturns();
	// cx-1 turn 6 is cold, has shared_prefix_messages 0, AND had a 595s
	// follow_up in its gap. A prefix that diverges at message 0 cannot hit
	// any entry regardless of timing, so blaming the delegation would credit
	// it with a miss it did not cause.
	const byClass = Object.fromEntries(out.cold_turn_attribution.map((r) => [r.key, r.n]));
	assert.equal(byClass.prefix_rewrite, 2);
	assert.equal(byClass.unexplained, 1);
	assert.equal(out.cold_turn_attribution.reduce((a, r) => a + r.n, 0), 5);
});

test("coldturns: attribution carries the uncached token cost, not just a count", () => {
	const out = coldturns();
	const idle = out.cold_turn_attribution.find((r) => r.key === "idle");
	// cx-1 turn 5: prompt 15000, no cache read, no cache create.
	assert.equal(idle.metrics.uncached, 15000);
});

test("coldturns: --min-n suppresses a rate the sample cannot support", () => {
	const thin = coldturns();
	const terraThin = thin.by_model.find((r) => r.key === "gpt-5.6-terra");
	// 3 long-delegation observations against the default minimum of 10.
	assert.equal(terraThin.metrics.adequate, false);
	assert.equal(terraThin.metrics.longDelegationN, 3);

	const relaxed = coldturns(["--min-n", "1"]);
	const terraRelaxed = relaxed.by_model.find((r) => r.key === "gpt-5.6-terra");
	assert.equal(terraRelaxed.metrics.adequate, true);
});

test("coldturns: the text table prints insufficient rather than a bare zero", () => {
	const out = execFileSync("node", [SCRIPT, "coldturns", "--dir", COLDTURN_FIXTURES], { encoding: "utf8" });
	assert.match(out, /insufficient/);
	// glm-5.3 has exactly one long-delegation observation and zero cold. A
	// printed "0" there reads as a result; it is one sample.
	assert.doesNotMatch(out, /glm-5\.3\s+1\s+0\s+0\.0%\s+0\s+1\s+0\s*$/m);
});

test("coldturns: --compare is refused rather than silently ignored", () => {
	assert.throws(
		() => execFileSync("node", [SCRIPT, "coldturns", "--dir", COLDTURN_FIXTURES, "--compare", "a", "b"], { encoding: "utf8", stdio: "pipe" }),
		(err) => err.status === 1,
	);
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

test("--top bounds every unbounded listing", () => {
	const provider = run(["provider", "--dir", FIXTURES, "--top", "1", "--json"]);
	assert.ok(provider.outcome_mix.length <= 1);
	assert.ok(provider.error_class.length <= 1);

	const tools = run(["tools", "--dir", FIXTURES, "--top", "1", "--json"]);
	assert.ok(tools.by_tool.length <= 1);
	assert.ok(tools.reason_histogram.length <= 1);
	assert.ok(tools.mutate_by_op.length <= 1);
});

test("loads rotated generations oldest first and active last", () => {
	const dir = mkdtempSync(join(tmpdir(), "steiner-diagnostics-"));
	try {
		const record = (outcome) => JSON.stringify({ ts: "2025-01-01T00:00:00Z", payload: { outcome } });
		writeFileSync(join(dir, "provider.jsonl.2"), record("oldest") + String.fromCharCode(10));
		writeFileSync(join(dir, "provider.jsonl.1"), record("middle") + String.fromCharCode(10));
		writeFileSync(join(dir, "provider.jsonl"), record("active") + String.fromCharCode(10));
		const out = run(["provider", "--dir", dir, "--json"]);
		assert.deepEqual(out.outcome_mix.map((row) => row.key), ["oldest", "middle", "active"]);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});
