// Unit tests for scripts/diagnostics_mutate.mjs. They drive the exported
// analysis functions directly with the testdata/diagnostics/mutate fixtures
// and a stubbed git ancestry resolver, so no repository is needed, and then
// run the wired-up `diagnostics.mjs mutate` mode end to end.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { readFileSync } from "node:fs";

import {
	analyze,
	callFailed,
	collectSamples,
	groupMessages,
	groupMessagesExact,
	groupMessagesLegacy,
	taxonomyBucket,
	windowMetrics,
} from "./diagnostics_mutate.mjs";

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCRIPT = join(__dirname, "diagnostics.mjs");
const FIXTURES = join(__dirname, "..", "testdata", "diagnostics", "mutate");

function readJsonl(path) {
	return readFileSync(path, "utf8")
		.split("\n")
		.filter(Boolean)
		.map((line) => JSON.parse(line));
}

const toolRecords = readJsonl(join(FIXTURES, "tool.jsonl"));
const providerRecords = readJsonl(join(FIXTURES, "provider.jsonl"));
const forRun = (records, runID) => records.filter((r) => r.run_id === runID);

// --------------------------------------------------------------- grouping

test("exact grouping separates interleaved agents and a turn reset", () => {
	const messages = groupMessagesExact(forRun(toolRecords, "run-attr"), providerRecords);
	const count = (source, agentID, turn) =>
		messages.filter((m) => m.source === source && m.agentID === agentID && m.turn === turn).length;

	// parent t1, child-1 t1, child-2 t1, child-1 t2, parent t2, parent t3,
	// parent t1 (after /clear), parent t2.
	assert.equal(messages.length, 8);
	assert.equal(count("parent", "", 1), 2);
	assert.equal(count("parent", "", 2), 2);
	assert.equal(count("parent", "", 3), 1);
	assert.equal(count("sub_agent", "child-1", 1), 1);
	assert.equal(count("sub_agent", "child-1", 2), 1);
	assert.equal(count("sub_agent", "child-2", 1), 1);
	// The two parent turn-1 messages are the pre-reset and post-/clear ones,
	// not a merge: their call counts differ (3 before, 1 after).
	const parentT1 = messages.filter((m) => m.source === "parent" && m.turn === 1);
	assert.deepEqual(parentT1.map((m) => m.calls.length).sort(), [1, 3]);
});

test("exact grouping keeps several mutate calls in one message", () => {
	const messages = groupMessagesExact(forRun(toolRecords, "run-attr"), providerRecords);
	const first = messages.find((m) => m.source === "parent" && m.turn === 1 && m.calls.length === 3);
	assert.ok(first, "parent turn 1 should hold three mutate calls");
});

test("legacy grouping merges a pre-attribution run into one message", () => {
	const messages = groupMessagesLegacy(forRun(toolRecords, "run-legacy"), providerRecords);
	assert.equal(messages.length, 1);
	assert.equal(messages[0].calls.length, 2);
	assert.equal(messages[0].grouping, "legacy");
	// No payload.model on the records, so attribution falls back to the run's
	// most recent provider record.
	assert.equal(messages[0].model, "gpt-5.6-terra");
});

test("groupMessages picks the rule per run", () => {
	const legacy = groupMessages(forRun(toolRecords, "run-legacy"), providerRecords);
	assert.deepEqual(legacy.map((m) => m.grouping), ["legacy"]);
	const exact = groupMessages(forRun(toolRecords, "run-attr"), providerRecords);
	assert.ok(exact.every((m) => m.grouping === "exact"));
});

// ---------------------------------------------------------------- metrics

const call = (opsTotal, opsFailed, failures, outcome) => ({
	kind: "tool",
	run_id: "r",
	seq: 1,
	source: "parent",
	turn: 1,
	payload: { tool: "mutate", outcome: outcome ?? (opsFailed > 0 ? "error" : "ok"), ops_total: opsTotal, ops_failed: opsFailed, failures: failures ?? [] },
});

const message = (calls) => ({ runID: "r", source: "parent", agentID: "", agentType: "code", turn: 1, model: "m", grouping: "exact", calls });

test("per-call reason count divides by calls; raw count stays secondary", () => {
	const metrics = windowMetrics([
		message([call(1, 1, [{ reason: "no_match" }, { reason: "no_match" }])]),
		message([call(1, 1, [{ reason: "no_match" }])]),
		message([call(1, 0)]),
	]);
	const row = metrics.reasons.find((r) => r.key === "no_match");
	assert.equal(row.calls, 2);
	assert.equal(row.raw, 3);
	assert.ok(Math.abs(row.pct - 2 / 3) < 1e-9);
});

test("discarded-ops arithmetic is all-or-nothing per failed call", () => {
	const metrics = windowMetrics([
		message([call(3, 1, [{ reason: "no_match" }])]),
		message([call(1, 2, [{ reason: "no_match" }, { reason: "no_match" }])]),
		message([call(2, 0)]),
	]);
	// discarded = 3 + 1 = 4 over 2 failed calls.
	assert.equal(metrics.opsDiscardedPerFailedCall, 2);
	// applied ops = 2, so 4 / 2 = 2.
	assert.equal(metrics.opsDiscardedPerAppliedOp, 2);
	assert.equal(metrics.failedCallsGt1Failure, 1);
});

test("per-op hazard compounds the call failure rate over ops per call", () => {
	const metrics = windowMetrics([
		message([call(2, 1, [{ reason: "no_match" }])]),
		message([call(2, 0)]),
	]);
	const bucket = metrics.opsBuckets.find((b) => b.key === "2-3");
	assert.equal(bucket.calls, 2);
	assert.equal(bucket.ops, 4);
	assert.equal(bucket.callFailRate, 0.5);
	assert.ok(Math.abs(bucket.hazard - (1 - Math.sqrt(0.5))) < 1e-9);
});

test("failure chains run per agent stream", () => {
	const failing = message([call(1, 1, [{ reason: "no_match" }])]);
	const ok = message([call(1, 0)]);
	failing.agentID = "child-1";
	ok.agentID = "child-1";
	const metrics = windowMetrics([failing, failing, ok, failing]);
	assert.equal(metrics.chainCount, 2);
	assert.equal(metrics.chainMean, 1.5);
	assert.equal(metrics.chainsGe2Share, 0.5);
});

// -------------------------------------------------------------- taxonomy

const defaultMatch = () => ({
	old_bytes: 10,
	old_lines: 2,
	nonblank_lines: 2,
	lines_found: 1,
	longest_run: 1,
	exact_prefix_lines: 0,
	trim_prefix_lines: 0,
	ws_kind: "none",
	indent_delta_max: 0,
	tabs_vs_spaces: false,
	crlf_mismatch: false,
	unescape_matches: false,
	line_prefix: false,
	matches_original: false,
	match_count: 0,
	read_state: "unchanged",
	turns_since_read: 1,
	in_read_range: "yes",
	locus_line: 5,
	file_lines: 40,
	file_hash_supplied: false,
	truncated: false,
});

const failure = (overrides = {}, reason = "no_match") => ({
	reason,
	match: { ...defaultMatch(), ...overrides },
});

test("taxonomyBucket assigns the first matching bucket", () => {
	const cases = [
		["matches_original", failure({ matches_original: true }), "forgot_own_edit_in_call"],
		["self_mutated", failure({ read_state: "self_mutated" }), "stale_after_own_edit"],
		["external_change", failure({ read_state: "external_change" }), "changed_externally"],
		["never_read", failure({ read_state: "never_read" }), "never_read"],
		["pruned", failure({ read_state: "pruned" }), "read_pruned"],
		// A concrete difference outranks the read state that also matches.
		["self_mutated_whitespace", failure({ read_state: "self_mutated", ws_kind: "internal_spacing" }), "whitespace:internal_spacing"],
		["self_mutated_crlf", failure({ read_state: "self_mutated", crlf_mismatch: true }), "encoding:crlf"],
		["self_mutated_escaped", failure({ read_state: "self_mutated", unescape_matches: true }), "encoding:escaped"],
		["self_mutated_line_prefix", failure({ read_state: "self_mutated", line_prefix: true }), "encoding:line_prefix"],
		["whitespace_over_crlf", failure({ ws_kind: "internal_spacing", crlf_mismatch: true }), "whitespace:internal_spacing"],
		["whitespace", failure({ ws_kind: "internal_spacing" }), "whitespace:internal_spacing"],
		["crlf", failure({ crlf_mismatch: true }), "encoding:crlf"],
		["escaped", failure({ unescape_matches: true }), "encoding:escaped"],
		["line_prefix", failure({ line_prefix: true }), "encoding:line_prefix"],
		["outside_read_range", failure({ in_read_range: "no" }), "outside_read_range"],
		["real_sequence", failure({ lines_found: 2, nonblank_lines: 2 }), "lines_real_sequence_wrong"],
		["fabricated", failure({ lines_found: 0 }), "fabricated_or_wrong_file"],
		["partial_recall", failure({ lines_found: 1, nonblank_lines: 3 }), "partial_recall"],
		["ambiguous", failure({ old_lines: 2 }, "ambiguous_match"), "ambiguous:2-3"],
		["ambiguous_short", failure({ old_lines: 1 }, "ambiguous_match"), "ambiguous:1"],
		["no_match_block", { reason: "no_match" }, null],
	];
	for (const [name, f, want] of cases) {
		assert.equal(taxonomyBucket(f), want, name);
	}
});

// -------------------------------------------------- filters and baseline

test("baseline windows use the injected ancestry resolver and cache per sha", () => {
	const seen = [];
	const resolveAncestor = (baseline, sha) => {
		seen.push(sha);
		if (sha === "aaaa1111") return false;
		if (sha === "bbbb2222") return true;
		return null;
	};
	const { report } = analyze({ toolRecords, providerRecords, baselineSha: "base", resolveAncestor });
	assert.equal(report.windows.pre_baseline.calls, 2);
	assert.equal(report.windows.post_baseline.calls, 16);
	assert.equal(report.windows.unknown.calls, 0);
	// Two distinct shas, one resolver call each, despite 18 records.
	assert.deepEqual([...new Set(seen)].sort(), ["aaaa1111", "bbbb2222"]);
	assert.equal(seen.length, 2);
});

test("a sha git does not know lands in the unknown window", () => {
	const tool = forRun(toolRecords, "run-attr").map((r) => ({ ...r, build_sha: "deadbeef" }));
	const { report } = analyze({ toolRecords: tool, providerRecords, baselineSha: "base", resolveAncestor: () => null });
	assert.equal(report.windows.unknown.calls, 16);
	assert.equal(report.windows.post_baseline.calls, 0);
});

test("--exclude-run and --model filter before grouping", () => {
	const excluded = analyze({ toolRecords, providerRecords, excludeRun: "run-legacy" });
	assert.ok(excluded.messages.every((m) => m.runID === "run-attr"));
	assert.equal(excluded.report.windows.all.calls, 16);

	const matched = analyze({ toolRecords, providerRecords, model: "gpt-5.6" });
	assert.equal(matched.report.windows.all.calls, 18);
	const none = analyze({ toolRecords, providerRecords, model: "zzz" });
	assert.equal(none.report.windows.all.calls, 0);
});

test("the featured subset keeps only calls carrying a match block", () => {
	const { report, messages } = analyze({ toolRecords, providerRecords });
	assert.equal(report.windows.all.calls, 18);
	assert.equal(report.windows.featured.calls, 14);
	assert.equal(report.windows.featured.features.total, 15);
	assert.equal(messages.flatMap((m) => m.calls).filter((c) => (c.payload.failures ?? []).some((f) => f.match)).length, 14);
});

test("samples come from capture_bodies records and filter by cause", () => {
	const { messages } = analyze({ toolRecords, providerRecords });
	const all = collectSamples(messages, { limit: 10 });
	assert.equal(all.length, 1);
	assert.equal(all[0].cause, "stale_after_own_edit");
	assert.equal(all[0].sample.path, "internal/tool/mutate.go");
	assert.equal(collectSamples(messages, { cause: "encoding:crlf", limit: 10 }).length, 0);
});

// ------------------------------------------------------------------- CLI

function run(args) {
	return JSON.parse(execFileSync("node", [SCRIPT, ...args], { encoding: "utf8" }));
}

test("CLI mutate --json parses fixtures and reports both groupings", () => {
	const out = run(["mutate", "--dir", FIXTURES, "--json"]);
	assert.equal(out.grouping, "mixed");
	assert.equal(out.windows.all.calls, 18);
	assert.equal(out.windows.featured.calls, 14);
	const terra = out.windows.all.by_model.find((r) => r.key === "gpt-5.6-terra");
	assert.equal(terra.n, 18);
});

test("CLI mutate --samples --cause prints the raw body", () => {
	const out = execFileSync("node", [SCRIPT, "mutate", "--dir", FIXTURES, "--samples", "5", "--cause", "stale_after_own_edit"], { encoding: "utf8" });
	assert.match(out, /internal\/tool\/mutate\.go/);
	assert.match(out, /stale_after_own_edit/);
});

test("CLI mutate refuses --compare", () => {
	assert.throws(
		() => execFileSync("node", [SCRIPT, "mutate", "--dir", FIXTURES, "--compare", "a", "b"], { encoding: "utf8", stdio: "pipe" }),
		(err) => err.status === 1,
	);
});

test("callFailed treats a non-ok outcome as a failure", () => {
	assert.equal(callFailed({ payload: { outcome: "error", ops_total: 1, ops_failed: 0 } }), true);
	assert.equal(callFailed({ payload: { outcome: "ok", ops_total: 1, ops_failed: 0 } }), false);
});
