// Smoke tests for diagnostics.mjs: assert a non-zero row count and at least
// one hand-computed aggregate value against testdata/diagnostics/, per mode.
// A parser that silently reports zero rows after a format change looks like
// a healthy result -- these tests exist to catch exactly that.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { mkdtempSync, writeFileSync, rmSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(__dirname, "diagnostics.mjs");
const FIXTURES = join(__dirname, "..", "testdata", "diagnostics");
// coldturns needs cache and tool records that line up in time, which the
// flat fixtures deliberately do not: they exist to pin per-mode aggregates
// and every existing assertion counts their rows.
const COLDTURN_FIXTURES = join(FIXTURES, "coldturns");
// A second coldturns-shaped set for the seq-ordering and
// prefix_predecessor_known behaviours. Kept apart from coldturns/ so the
// original set's row counts stay pinned by their own assertions.
const COLDTURN_SEQ_FIXTURES = join(FIXTURES, "coldturns_seq");
// peak reads whole files positional-style, so its session logs and telemetry
// lines are a set of their own: one rotated session pair plus a telemetry
// file, standing in for the two formats a single run may leave behind.
const PEAK_FIXTURES = join(FIXTURES, "peak");
const PEAK_SESSION_OLD = join(PEAK_FIXTURES, "session.jsonl.1");
const PEAK_SESSION = join(PEAK_FIXTURES, "session.jsonl");
const PEAK_TELEMETRY = join(PEAK_FIXTURES, "telemetry.jsonl");
const PEAK_FILES = [PEAK_SESSION_OLD, PEAK_SESSION, PEAK_TELEMETRY];

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
	assert.equal(row.n, 6);
	assert.equal(row.metrics.callKinds.compaction, 1);
	assert.equal(row.metrics.callKinds.turn, 5);
	assert.equal(row.metrics.endpoints["eu-west-1"], 1);
	// Compaction records are included in cache mode aggregates.
	// nonCached = max(prompt - read - create, 0) per record, summed = 0.
	// hit_rate = 2320 / (0 + 2320 + 3100) = 0.4280...
	assert.ok(Math.abs(row.metrics.hitRate - 2320 / 5420) < 1e-9);
});

test("cache mode: table reports call kinds and per-endpoint counts", () => {
	const out = execFileSync("node", [SCRIPT, "cache", "--dir", FIXTURES], { encoding: "utf8" });
	assert.match(out, /call_kinds/);
	assert.match(out, /turn:5,compaction:1/);
	assert.match(out, /endpoints/);
	assert.match(out, /eu-west-1:1/);
});

test("cache mode: counts endpoint switches within run, source, and agent", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-cache-"));
	try {
		writeFileSync(join(dir, "cache.jsonl"), [
			{ run_id: "run-1", source: "parent", turn: 1, seq: 1, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "east" } },
			{ run_id: "run-1", source: "parent", turn: 2, seq: 2, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "west" } },
			{ run_id: "run-1", source: "parent", agent_id: "agent-1", turn: 1, seq: 3, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "east" } },
			{ run_id: "run-1", source: "sub_agent", agent_id: "agent-1", turn: 1, seq: 4, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "west" } },
			{ run_id: "run-2", source: "parent", turn: 1, seq: 5, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "east" } },
			{ source: "parent", turn: 1, seq: 6, kind: "cache", payload: { backend_model_id: "model", upstream_endpoint: "west" } },
		].map((record) => JSON.stringify(record)).join("\n") + "\n");
		const out = run(["cache", "--dir", dir, "--json"]);
		assert.equal(out["by model"][0].metrics.endpointSwitches, 1);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("cache mode: by-source groups parent and sub_agent separately", () => {
	const out = run(["cache", "--dir", FIXTURES, "--json"]);
	const bySource = out["by source"];
	const parent = bySource.find((r) => r.key === "parent");
	const subAgent = bySource.find((r) => r.key === "sub_agent");
	assert.equal(parent.n, 5);
	assert.equal(subAgent.n, 1);
	assert.equal(parent.metrics.callKinds.compaction, 1);
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

test("prefix mode: compaction requests do not affect pairing", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-prefix-"));
	try {
		const records = [
			{ type: "api_request", payload: { kind: "turn", turn: 1, message_count: 1, message_hashes: ["one"] } },
			{ type: "api_request", payload: { kind: "compaction", turn: 1, message_count: 1, message_hashes: ["summary"] } },
			{ type: "api_request", payload: { kind: "turn", turn: 2, message_count: 2, message_hashes: ["one", "two"] } },
		];
		const logfile = join(dir, "session.jsonl");
		writeFileSync(logfile, records.map((record) => JSON.stringify(record)).join("\n") + "\n");
		const rows = run(["prefix", logfile, "--json"]);
		assert.equal(rows.length, 2);
		assert.deepEqual(rows.map((row) => row.verdict), ["-", "APPEND-ONLY"]);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("prefix mode: interactive prompts that reset turn numbers stay in log order", () => {
	const logfile = join(FIXTURES, "session_interactive.jsonl");
	const rows = run(["prefix", logfile, "--json"]);

	// Two interactive prompts in the top-level group both start at turn 1, so
	// sorting by turn would interleave their requests and report a false
	// BREAK-AT-2. Append order (the file's order) shows a prefix that only ever
	// grows.
	const topLevel = rows.filter((r) => r.agentID === "(top-level)");
	assert.deepEqual(topLevel.map((r) => r.turn), [1, 2, 1, 2]);
	assert.deepEqual(topLevel.map((r) => r.verdict), ["-", "APPEND-ONLY", "APPEND-ONLY", "APPEND-ONLY"]);
	assert.deepEqual(topLevel.map((r) => r.cached), [0, 1, 2, 3]);

	// The child's events are interleaved in the log with the parent's; grouping
	// must keep each agent's own append order rather than the global file order.
	const child = rows.filter((r) => r.agentID === "child-1");
	assert.deepEqual(child.map((r) => r.verdict), ["-", "APPEND-ONLY"]);
	assert.equal(child[1].cached, 1);
});

// ---------------------------------------------------------------- coldturns

const coldturns = (extra = []) => run(["coldturns", "--dir", COLDTURN_FIXTURES, "--json", ...extra]);

test("coldturns: compaction records do not affect pairing", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-coldturns-"));
	try {
		writeFileSync(join(dir, "cache.jsonl"), [
			{ ts: "2026-09-01T10:00:00Z", run_id: "run-1", source: "parent", seq: 1, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 50 } },
			{ ts: "2026-09-01T10:01:00Z", run_id: "run-1", source: "parent", seq: 2, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 50, call_kind: "compaction" } },
			{ ts: "2026-09-01T10:02:00Z", run_id: "run-1", source: "parent", seq: 3, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 50 } },
		].map((record) => JSON.stringify(record)).join("\n") + "\n");
		writeFileSync(join(dir, "tool.jsonl"), "");
		const out = run(["coldturns", "--dir", dir, "--json", "--min-n", "1"]);
		const row = out.by_model.find((candidate) => candidate.key === "model");
		assert.equal(row.n, 1);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

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

// ------------------------------------------------- coldturns seq ordering

const coldturnsSeq = (extra = []) => run(["coldturns", "--dir", COLDTURN_SEQ_FIXTURES, "--json", ...extra]);

test("coldturns: consecutive turns pair by process seq, not by wall-clock ts", () => {
	const out = coldturnsSeq();
	// seq-1's turn 2 (seq 2) is appended before the next interactive prompt's
	// turn 1 (seq 3), but its ts is a minute later -- a clock step back, which
	// is why the pairing follows seq. In seq order the 420s sub_agent fits the
	// gap between turn 1 and turn 2, so the cold turn attributes to delegation.
	// Ordering by ts instead would pair turn 2 with the later request and report
	// unexplained.
	const byClass = Object.fromEntries(out.cold_turn_attribution.map((r) => [r.key, r.n]));
	assert.equal(byClass.delegation, 1);
	assert.equal(byClass.unexplained, undefined);

	const terra = out.by_model.find((r) => r.key === "gpt-5.6-terra");
	assert.equal(terra.n, 2);
	assert.equal(terra.metrics.coldTurns, 1);
	assert.equal(terra.metrics.longDelegationN, 1);
});

test("coldturns: no_prior_baseline needs prefix_comparison_enabled true, not just predecessor false", () => {
	const out = coldturnsSeq();
	const byClass = Object.fromEntries(out.cold_turn_attribution.map((r) => [r.key, r.n]));
	// prec-1 turn 2 sets prefix_predecessor_known false with
	// prefix_comparison_enabled true: a real missing baseline. Turn 3 omits both
	// (legacy), turn 4 disables comparison, and turn 5 omits the comparison flag
	// with predecessor false. None of those may masquerade as no_prior_baseline;
	// they keep the legacy prefix_rewrite reading.
	assert.equal(byClass.no_prior_baseline, 1);
	assert.equal(byClass.prefix_rewrite, 3);

	const nova = out.by_model.find((r) => r.key === "gpt-5.6-nova");
	assert.equal(nova.metrics.coldTurns, 4);
});

test("coldturns: resume-flagged records are labelled baseline-unavailable, not attributed as a cause", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-resume-"));
	try {
		writeFileSync(
			join(dir, "cache.jsonl"),
			[
				// A warm first turn keeps the model out of the no-reporting
				// exclusion list below; the pair walk drops it.
				{ ts: "2026-09-01T10:00:00Z", run_id: "run-1", source: "parent", seq: 1, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 50 } },
				// In-process TUI resume: the flagged record is a normal pair.
				{ ts: "2026-09-01T10:01:00Z", run_id: "run-1", source: "parent", seq: 2, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 0, prefix_comparison_enabled: true, prefix_predecessor_known: false, resume_baseline_unavailable: true } },
				// A genuine missing baseline that was not a resume keeps its own
				// attribution.
				{ ts: "2026-09-01T10:02:00Z", run_id: "run-1", source: "parent", seq: 3, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 0, prefix_comparison_enabled: true, prefix_predecessor_known: false } },
				// CLI resume: the first record of the run already has no
				// predecessor, so it is never a pair, but it must still be
				// counted.
				{ ts: "2026-09-01T11:00:00Z", run_id: "run-2", source: "parent", seq: 4, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 0, prefix_comparison_enabled: true, prefix_predecessor_known: false, resume_baseline_unavailable: true } },
			].map((record) => JSON.stringify(record)).join("\n") + "\n",
		);
		writeFileSync(join(dir, "tool.jsonl"), "");
		const out = run(["coldturns", "--dir", dir, "--json", "--min-n", "1"]);

		// Both flagged records are counted by model, including the one the pair
		// walk drops as a run's first turn.
		const resumeRow = out.baseline_unavailable_after_resume.find((r) => r.key === "model");
		assert.equal(resumeRow.n, 2);

		// The flagged pair is not attributed, so the resume cannot be read as a
		// miss cause; only the unflagged missing baseline is.
		const byClass = Object.fromEntries(out.cold_turn_attribution.map((r) => [r.key, r.n]));
		assert.equal(byClass.no_prior_baseline, 1);
		assert.equal(byClass.resume_baseline_unavailable, undefined);

		// Model aggregation uses the same attributable population: the flagged
		// pair must not inflate the model's n or cold-turn count while the
		// attribution table excludes it.
		const byModel = out.by_model.find((r) => r.key === "model");
		assert.equal(byModel.n, 1);
		assert.equal(byModel.metrics.coldTurns, 1);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("coldturns: the text table names the resume baseline section", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-resume-text-"));
	try {
		writeFileSync(
			join(dir, "cache.jsonl"),
			JSON.stringify({ ts: "2026-09-01T10:00:00Z", run_id: "run-1", source: "parent", seq: 1, payload: { backend_model_id: "model", prompt_tokens: 100, cache_read_tokens: 10, resume_baseline_unavailable: true } }) + "\n",
		);
		writeFileSync(join(dir, "tool.jsonl"), "");
		const out = execFileSync("node", [SCRIPT, "coldturns", "--dir", dir], { encoding: "utf8" });
		assert.match(out, /baseline unavailable after resume/);
		assert.doesNotMatch(out, /resume.*cause/i);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
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

// tui fixture, build aaaa1111 (hand-computed): "key" has 20 Updates in two
// windows; merged update hist is idx3 (0.5 ms) 11, idx4 (1 ms) 7, idx5 (2 ms)
// 2, so p50 = 0.5 and p95 = 2; view hist idx5 10, idx6 14 cumulative, idx7 20,
// so p50 = 2 and p95 = 8. CPU = 5 + 4 + 20 + 10 = 39 ms over 20 s of windows.
test("tui mode: per-type percentiles, cpu and share from merged histograms", () => {
	const out = run(["tui", "--dir", FIXTURES, "--sha", "aaaa1111", "--json"]);
	assert.equal(out.by_type.length, 2);
	const key = out.by_type.find((r) => r.key === "key");
	assert.equal(key.n, 20);
	assert.equal(key.metrics.updP50, 0.5);
	assert.equal(key.metrics.updP95, 2);
	assert.equal(key.metrics.updMax, 1.5);
	assert.equal(key.metrics.viewN, 20);
	assert.equal(key.metrics.viewP50, 2);
	assert.equal(key.metrics.viewP95, 8);
	assert.equal(key.metrics.viewMax, 6);
	assert.ok(Math.abs(key.metrics.cpuMs - 39) < 1e-9);
	assert.ok(Math.abs(key.metrics.share - 39 / 20000) < 1e-12);
	const tick = out.by_type.find((r) => r.key === "tick");
	assert.equal(tick.metrics.updP50, 0.25);
	assert.equal(tick.metrics.updP95, 128);
	assert.equal(tick.metrics.updMax, 70);
});

test("tui mode: window summary counts each window once", () => {
	const out = run(["tui", "--dir", FIXTURES, "--sha", "aaaa1111", "--json"]);
	const [w] = out.windows;
	assert.equal(w.n, 2);
	assert.equal(w.metrics.wallS, 20);
	assert.ok(Math.abs(w.metrics.overlayOpen - 0.1) < 1e-12);
	assert.ok(Math.abs(w.metrics.busy - 147 / 20000) < 1e-12);
	assert.equal(w.metrics.subagentsMax, 3);
	assert.equal(w.metrics.transcriptLinesMax, 700);
	assert.equal(w.metrics.droppedWindows, 1);
});

test("tui mode: --since excludes records before the date and table output prints", () => {
	const out = run(["tui", "--dir", FIXTURES, "--since", "2026-09-02", "--json"]);
	assert.equal(out.by_type.length, 0);
	const text = execFileSync("node", [SCRIPT, "tui", "--dir", FIXTURES], { encoding: "utf8" });
	assert.match(text, /upd_p95_ms/);
	assert.match(text, /overlay_open/);
});

test("tui mode --compare reports a delta between build_sha values", () => {
	const out = run(["tui", "--dir", FIXTURES, "--compare", "aaaa1111", "bbbb2222", "--json"]);
	const key = out.sections["tui by message type"].find((r) => r.key === "key");
	assert.equal(key.n_a, 20);
	assert.equal(key.n_b, 10);
	assert.equal(key.upd_p95_ms.a, 2);
	assert.equal(key.upd_p95_ms.b, 0.5);
	assert.equal(key.upd_p95_ms.delta, -1.5);
	const text = execFileSync("node", [SCRIPT, "tui", "--dir", FIXTURES, "--compare", "aaaa1111", "bbbb2222"], { encoding: "utf8" });
	assert.match(text, /aaaa1111 vs bbbb2222/);
	assert.match(text, /share\(delta\)/);
});

// More than --top message types: a type seen in one window must survive, sort
// first when it costs most, and appear on both sides of a comparison.
test("tui mode lists every message type, costliest first, in both compare columns", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-tui-"));
	try {
		const hist = (i) => Array.from({ length: 15 }, (_, j) => (j === i ? 1 : 0));
		const rec = (sha, window, type, ms) =>
			JSON.stringify({
				ts: `2026-09-01T10:00:${String(window).padStart(2, "0")}Z`, run_id: `run-${sha}`, build_sha: sha, kind: "tui", seq: window,
				payload: {
					type, window_id: window, window_ms: 10000, update_count: 1, update_sum_ms: ms, update_max_ms: ms, update_hist: hist(3),
					view_count: 1, view_sum_ms: 0, view_max_ms: 0, view_hist: hist(0), overlay_open_ms: 0, subagents_running_max: 0,
					transcript_lines: 10, width: 80, height: 24, dropped_windows: 0,
				},
			});
		const lines = [];
		for (const sha of ["aaaa1111", "bbbb2222"]) {
			for (let w = 1; w <= 3; w++) {
				for (let t = 0; t < 25; t++) lines.push(rec(sha, w, `cheap_${t}`, 0.1));
			}
			lines.push(rec(sha, 1, "rare_costly", 500));
		}
		writeFileSync(join(dir, "tui.jsonl"), lines.join("\n") + "\n");

		const single = run(["tui", "--dir", dir, "--sha", "aaaa1111", "--json"]);
		assert.equal(single.by_type.length, 26);
		assert.equal(single.by_type[0].key, "rare_costly");

		const cmp = run(["tui", "--dir", dir, "--compare", "aaaa1111", "bbbb2222", "--json"]);
		const rare = cmp.sections["tui by message type"].find((r) => r.key === "rare_costly");
		assert.equal(rare.n_a, 1);
		assert.equal(rare.n_b, 1);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

// --------------------------------------------------------------- peak mode

const peak = (files, extra = []) => run(["peak", ...files, "--json", ...extra]);

// Pinned counts, computed by hand from the fixture files:
//   run-old:  main 120000, 240000; child-1 900000
//   run-new:  main 150000, 1000000; child-2 2000000
//   run-mid:  main 199999; child-3 600000
//   run-tel:  telemetry parent 500000, sub_agent 900000
//   unattributed: one telemetry parent line, 20000, with no run_id
// 8 attributed groups over 4 runs plus 1 unattributed observation. run-mid's
// 199999 sits one token under the 200k bucket on purpose, and
// 500000/1000000 sit exactly on their buckets.
test("peak: combines rotated generations and telemetry lines into run/agent groups", () => {
	const out = peak(PEAK_FILES);
	assert.equal(out.files, 3);
	assert.equal(out.runs, 4);
	assert.equal(out.by_run_agent.length, 8);

	const group = (key) => out.by_run_agent.find((r) => r.key === key);
	assert.deepEqual(group("run-old/main"), { key: "run-old/main", n: 2, metrics: { peakPromptTokens: 240000 } });
	assert.deepEqual(group("run-new/main"), { key: "run-new/main", n: 2, metrics: { peakPromptTokens: 1000000 } });
	assert.equal(group("run-new/child-2").metrics.peakPromptTokens, 2000000);
	assert.equal(group("run-mid/child-3").metrics.peakPromptTokens, 600000);
	// Telemetry has no agent id: source stands in for it, and parent is main.
	assert.deepEqual(group("run-tel/main"), { key: "run-tel/main", n: 1, metrics: { peakPromptTokens: 500000 } });
	assert.equal(group("run-tel/sub_agent").metrics.peakPromptTokens, 900000);
	// Without STEINER_USAGE_TELEMETRY_RUN the line has no run id: it is an
	// unattributed observation, not a run of its own.
	assert.equal(group("(unattributed)/main"), undefined);
	assert.equal(out.unattributed_observations, 1);
	assert.deepEqual(out.unattributed, [{ key: "(unattributed)/main", n: 1, metrics: { peakPromptTokens: 20000 } }]);
});

test("peak: telemetry without a run id is reported separately, never as runs", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-"));
	try {
		writeFileSync(
			join(dir, "telemetry.jsonl"),
			[
				{ kind: "usage", ts: "2026-09-04T10:00:00Z", seq: 1, source: "parent", prompt_tokens: 300000 },
				{ kind: "usage", ts: "2026-09-04T10:01:00Z", seq: 2, source: "parent", prompt_tokens: 400000 },
			]
				.map((line) => JSON.stringify(line))
				.join("\n") + "\n",
		);
		const out = peak([join(dir, "telemetry.jsonl")]);
		assert.equal(out.runs, 0);
		assert.deepEqual(out.by_run_agent, []);
		assert.deepEqual(out.main_peak_at_least, { 200000: 0, 500000: 0, 1000000: 0 });
		assert.equal(out.unattributed_observations, 2);
		assert.deepEqual(out.unattributed, [{ key: "(unattributed)/main", n: 2, metrics: { peakPromptTokens: 400000 } }]);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: a single rotated generation stands on its own", () => {
	const old = peak([PEAK_SESSION_OLD]);
	assert.equal(old.runs, 1);
	assert.equal(old.by_run_agent.length, 2);
	assert.deepEqual(old.main_peak_at_least, { 200000: 1, 500000: 0, 1000000: 0 });

	const active = peak([PEAK_SESSION]);
	assert.equal(active.runs, 2);
	assert.deepEqual(active.main_peak_at_least, { 200000: 1, 500000: 1, 1000000: 1 });
	assert.deepEqual(active.unattributed, []);
});

test("peak: main-agent buckets count at-or-above and ignore sub-agent peaks", () => {
	const out = peak(PEAK_FILES);
	// run-old 240000, run-new 1000000, run-tel 500000 clear 200k; run-mid
	// (199999) does not. child-1/child-2/child-3 and run-tel/sub_agent are
	// above their run's main peak but never counted, and the unattributed
	// 20000 line is not a run at all.
	assert.deepEqual(out.main_peak_at_least, { 200000: 3, 500000: 2, 1000000: 1 });
});

test("peak: --top caps the group listing, not the run summary", () => {
	const out = peak(PEAK_FILES, ["--top", "2"]);
	assert.equal(out.by_run_agent.length, 2);
	assert.equal(out.runs, 4);
	assert.equal(out.unattributed.length, 1);
	assert.equal(out.unattributed_observations, 1);
	assert.deepEqual(out.main_peak_at_least, { 200000: 3, 500000: 2, 1000000: 1 });
});

test("peak: --top must be a non-negative integer and no files is a usage error", () => {
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", "--top", "--json"], { encoding: "utf8", stdio: "pipe" }),
		/--top requires a non-negative integer/,
	);
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, "--top", "abc"], { encoding: "utf8", stdio: "pipe" }),
		/--top requires a non-negative integer/,
	);
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, "--top", "-1"], { encoding: "utf8", stdio: "pipe" }),
		/--top requires a non-negative integer/,
	);
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", "--json"], { encoding: "utf8", stdio: "pipe" }),
		/USAGE_TELEMETRY/,
	);
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak"], { encoding: "utf8", stdio: "pipe" }),
		/usage: node scripts\/diagnostics\.mjs peak/,
	);
});

test("peak: a nonexistent or non-file input is rejected, not read as empty", () => {
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, join(PEAK_FIXTURES, "missing.jsonl")], { encoding: "utf8", stdio: "pipe" }),
		(err) => err.stderr.includes("peak mode: no such file:") && err.stderr.includes("missing.jsonl"),
	);
	// A directory exists but is not a readable input.
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", PEAK_FIXTURES], { encoding: "utf8", stdio: "pipe" }),
		(err) => err.stderr.includes("peak mode: not a regular file:") && err.stderr.includes(PEAK_FIXTURES),
	);
});

// A mistyped path usually points at something readable instead of nothing: a
// raw stream or a plain JSON file parses to zero records and would otherwise
// report zero runs, exit 0, and look like an experiment with no peak calls.
test("peak: a non-empty input with no JSONL records is rejected by name", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-nojsonl-"));
	try {
		const raw = join(dir, "raw.json");
		writeFileSync(raw, JSON.stringify({ nodes: [{ kind: "tool_call" }], edges: [] }, null, 2));
		assert.throws(
			() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, raw], { encoding: "utf8", stdio: "pipe" }),
			(err) =>
				err.status === 1 &&
				err.stderr.includes("peak mode: ") &&
				err.stderr.includes("raw.json") &&
				err.stderr.includes("no JSONL records") &&
				!err.stderr.includes("    at "),
		);
		// Blank lines are not records either: whitespace-only content is not empty.
		const blank = join(dir, "blank.jsonl");
		writeFileSync(blank, "\n \n\n");
		assert.throws(
			() => execFileSync("node", [SCRIPT, "peak", blank], { encoding: "utf8", stdio: "pipe" }),
			(err) => err.status === 1 && err.stderr.includes("blank.jsonl") && err.stderr.includes("no JSONL records"),
		);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

// A minified raw.json, a null line, and an array line all parse to a value, but
// none has a record shape: peak counts them as malformed, so a file holding only
// them is rejected instead of reporting zero runs at exit 0.
test("peak: a parsed line without a record shape is malformed, not a record", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-shape-"));
	try {
		const cases = [
			["raw-min.json", JSON.stringify({ nodes: [{ kind: "tool_call" }], edges: [] })],
			["null.jsonl", "null\n"],
			["array.jsonl", "[1,2]\n"],
		];
		for (const [name, content] of cases) {
			const file = join(dir, name);
			writeFileSync(file, content);
			assert.throws(
				() => execFileSync("node", [SCRIPT, "peak", file], { encoding: "utf8", stdio: "pipe" }),
				(err) => err.status === 1 && err.stderr.includes(name) && err.stderr.includes("no JSONL records"),
				`${name} alone must be rejected`,
			);
		}
		// Mixed with a valid log the same lines are skipped and counted, not fatal.
		const valid = JSON.stringify({ type: "model_call_finished", payload: { prompt_tokens: 250000 }, run_id: "run-shape" });
		const mixed = join(dir, "mixed.jsonl");
		writeFileSync(mixed, [valid, '{"nodes":[],"edges":[]}', "null", "[1,2]"].join("\n") + "\n");
		const out = peak([mixed]);
		assert.equal(out.runs, 1);
		assert.equal(out.skipped_malformed_lines, 3);
		assert.deepEqual(out.by_run_agent, [{ key: "run-shape/main", n: 1, metrics: { peakPromptTokens: 250000 } }]);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: an empty input is accepted, as a rotated generation can be empty", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-empty-"));
	try {
		const empty = join(dir, "session.jsonl.2");
		writeFileSync(empty, "");
		const alone = peak([empty]);
		assert.equal(alone.files, 1);
		assert.equal(alone.runs, 0);
		assert.equal(alone.skipped_malformed_lines, 0);
		assert.deepEqual(alone.main_peak_at_least, { 200000: 0, 500000: 0, 1000000: 0 });
		// The empty generation changes no count when combined with real inputs.
		const combined = peak([...PEAK_FILES, empty]);
		assert.equal(combined.files, 4);
		assert.equal(combined.runs, 4);
		assert.deepEqual(combined.main_peak_at_least, { 200000: 3, 500000: 2, 1000000: 1 });
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: malformed lines are skipped, counted, and never abort the run", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-malformed-"));
	try {
		const file = join(dir, "session.jsonl");
		const lines = [
			JSON.stringify({ type: "model_call_finished", payload: { prompt_tokens: 250000 }, run_id: "run-1" }),
			"not json at all",
			"",
			JSON.stringify({ type: "model_call_finished", payload: { prompt_tokens: 300000 }, run_id: "run-1" }),
			'{"type": "model_call_finished", "payload": {"prompt_tok',
		];
		writeFileSync(file, lines.join("\n") + "\n");
		const out = peak([file]);
		assert.equal(out.runs, 1);
		assert.equal(out.skipped_malformed_lines, 2);
		assert.deepEqual(out.by_run_agent, [{ key: "run-1/main", n: 2, metrics: { peakPromptTokens: 300000 } }]);
		assert.deepEqual(out.main_peak_at_least, { 200000: 1, 500000: 0, 1000000: 0 });
		const text = execFileSync("node", [SCRIPT, "peak", file], { encoding: "utf8" });
		assert.match(text, /skipped 2 malformed lines \(not JSONL records\)/);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: a large input is not mistaken for a read error", () => {
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-large-"));
	try {
		// Comfortably past the engine's argument limit, so spreading the records
		// into push would raise RangeError and surface as "cannot read".
		const line = JSON.stringify({ type: "model_call_finished", payload: { prompt_tokens: 1000 }, run_id: "run-big" });
		writeFileSync(join(dir, "big.jsonl"), Array.from({ length: 150000 }, () => line).join("\n") + "\n");
		const out = peak([join(dir, "big.jsonl")]);
		assert.equal(out.runs, 1);
		assert.deepEqual(out.by_run_agent, [{ key: "run-big/main", n: 150000, metrics: { peakPromptTokens: 1000 } }]);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: an unreadable input is a clean error, not a stack trace", (t) => {
	if (process.getuid?.() === 0) {
		t.skip("root ignores mode 0o000");
		return;
	}
	const dir = mkdtempSync(join(tmpdir(), "diagnostics-peak-unreadable-"));
	const file = join(dir, "unreadable.jsonl");
	try {
		writeFileSync(file, JSON.stringify({ type: "model_call_finished", payload: { prompt_tokens: 1000 }, run_id: "run-1" }) + "\n");
		chmodSync(file, 0o000);
		assert.throws(
			() => execFileSync("node", [SCRIPT, "peak", file], { encoding: "utf8", stdio: "pipe" }),
			(err) =>
				err.stderr.includes("peak mode: cannot read") &&
				err.stderr.includes("unreadable.jsonl") &&
				err.stderr.includes("EACCES") &&
				!err.stderr.includes("    at "),
		);
	} finally {
		chmodSync(file, 0o600);
		rmSync(dir, { recursive: true, force: true });
	}
});

test("peak: unsupported flags are rejected rather than silently ignored", () => {
	for (const [flag, value] of [
		["--since", "2026-09-01"],
		["--dir", FIXTURES],
		["--sha", "aaaa1111"],
		["--min-n", "5"],
	]) {
		assert.throws(
			() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, flag, value], { encoding: "utf8", stdio: "pipe" }),
			(err) => err.stderr.includes(`peak mode: does not accept ${flag}`),
		);
	}
});

test("peak: --compare is refused rather than silently ignored", () => {
	assert.throws(
		() => execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES, "--compare", "a", "b"], { encoding: "utf8", stdio: "pipe" }),
		/--compare does not apply to peak mode/,
	);
});

test("peak: the text tables print the summary and per-group peak", () => {
	const out = execFileSync("node", [SCRIPT, "peak", ...PEAK_FILES], { encoding: "utf8" });
	assert.match(out, /peak main-agent threshold counts/);
	assert.match(out, /main peak >= 200k\s+3\s+75\.0%/);
	assert.match(out, /excluded unattributed observations\s+1\s+-/);
	assert.match(out, /peak prompt tokens by run\/agent \(3 files\)/);
	assert.match(out, /run-new\/main\s+2\s+1000000/);
	assert.match(out, /peak unattributed observations \(no run_id\)/);
	assert.match(out, /\(unattributed\)\/main\s+1\s+20000/);
	// Clean fixtures skip nothing, so the malformed notice stays silent.
	assert.doesNotMatch(out, /malformed/);
});
