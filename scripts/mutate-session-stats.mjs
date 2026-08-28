#!/usr/bin/env node
// Mines steiner session transcripts for `mutate` tool-call outcomes.
//
// Usage:
//   node measure.mjs [--dir <sessions dir>] [--since YYYY-MM-DD] [--json]
//
// Defaults to ~/.config/steiner/sessions. Use --since to restrict to sessions
// created on or after a date, which is how a before/after comparison is drawn:
// the fix lands, then --since <fix date> gives the "after" window.
//
// Metric definitions are deliberately explicit so before/after numbers are
// comparable. A call is a FAILURE if its result carries operations_failed > 0,
// or the result is an error envelope (pre-execution decode error). Op types are
// read from the CALL's operations[].type; the failing op type is read from the
// RESULT's "mutate: operation <N> <type>" prefix.

import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

const argv = process.argv.slice(2);
const argOf = (name, fallback) => {
	const i = argv.indexOf(name);
	return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
};
const DIR = argOf("--dir", join(homedir(), ".config", "steiner", "sessions"));
const SINCE = argOf("--since", null);
const AS_JSON = argv.includes("--json");

const FAIL_CLASSES = [
	["no_match_whitespace_variant_exists", (s) => s.includes("no match for old_string") && s.includes("normalized whitespace match exists")],
	["no_match_other", (s) => s.includes("no match for old_string")],
	["ambiguous_match", (s) => s.includes("ambiguous match")],
	["line_replace_guard_mismatch", (s) => s.includes("contains old_string") || s.includes("want exactly 1")],
	["line_replace_newline_in_old_string", (s) => s.includes("contains newline characters")],
	["line_replace_missing_old_string", (s) => s.includes("requires old_string for safety")],
	["empty_old_string", (s) => s.includes("old_string is empty")],
	["wrong_field_for_op", (s) => s.includes("is not valid for this operation type")],
	["unknown_op_type", (s) => s.includes("unsupported type")],
	["line_out_of_range", (s) => s.includes("is outside file with") || s.includes("exceeds file length")],
	["bad_line_number", (s) => s.includes("line must be >=")],
	["file_hash_stale", (s) => s.includes("file_hash")],
	["parent_dir_missing", (s) => s.includes("parent directory")],
	["assertion_failed", (s) => s.includes("assert_present failed") || s.includes("assert_absent failed")],
	["target_exists", (s) => s.includes("already exists")],
	["target_missing", (s) => s.includes("does not exist")],
	["empty_content_guard", (s) => s.includes("content is empty but")],
];

const classify = (text) => {
	const s = text.toLowerCase();
	for (const [name, test] of FAIL_CLASSES) if (test(s)) return name;
	return "other";
};

const bump = (obj, key, by = 1) => {
	obj[key] = (obj[key] || 0) + by;
};

// Classifies a "normalized whitespace match exists" failure by what actually
// differs between old_string and the file text steiner found. This matters
// because the existing detector (normalizedWhitespaceMatchExists) collapses ALL
// whitespace via strings.Fields, so it reports a "whitespace variant" even when
// the real difference is indentation DEPTH — which a conservative matcher must
// not silently fix. Buckets:
//   trailing_or_unicode  — recoverable by trailing-whitespace/Unicode normalisation
//   indent_uniform       — every line shifted by the SAME leading-whitespace delta
//                          (safe to re-indent and apply)
//   indent_nonuniform    — lines shifted by DIFFERENT deltas, i.e. the model got the
//                          nesting structure wrong (must NOT be auto-applied)
//   other                — anything else
const leading = (line) => (line.match(/^[ \t]*/) || [""])[0];
const rstrip = (s) => s.split("\n").map((l) => l.replace(/[ \t]+$/, "")).join("\n").replace(/\n+$/, "");
const unicodeFold = (s) =>
	s.normalize("NFKC")
		.replace(/[‘’‚‛]/g, "'")
		.replace(/[“”„‟]/g, '"')
		.replace(/[‐-―−]/g, "-")
		.replace(/[  -   　]/g, " ");

const classifyWhitespaceVariant = (oldString, fileText) => {
	if (unicodeFold(rstrip(oldString)) === unicodeFold(rstrip(fileText))) return "trailing_or_unicode";
	// Blank-line count differences are distinguishable only BEFORE filtering blanks out.
	const rawA = oldString.split("\n");
	const rawB = fileText.split("\n");
	const a = rawA.filter((l) => l.trim() !== "");
	const b = rawB.filter((l) => l.trim() !== "");
	if (a.length === 0 || a.length !== b.length) return "other";
	if (a.some((l, i) => l.trim() !== b[i].trim())) {
		// Same content once ALL whitespace is collapsed => the difference is internal
		// (e.g. table-column padding), not leading indentation.
		const collapse = (s) => s.split(/\s+/).filter(Boolean).join(" ");
		return collapse(oldString) === collapse(fileText) ? "internal_whitespace" : "other";
	}
	if (rawA.length !== rawB.length) return "blank_line_count";
	const deltas = a.map((l, i) => leading(b[i]).length - leading(l).length);
	if (!deltas.every((d) => d === deltas[0])) return "indent_nonuniform";
	// Mixed tabs/spaces cannot be re-indented reliably; do not count as safe.
	const homogeneous = [...a, ...b].every((l) => /^\t*$/.test(leading(l)) || /^ *$/.test(leading(l)));
	return homogeneous ? "indent_uniform" : "indent_mixed";
};

// Pulls the matched file region out of a mutate error body, from the "file text
// that matches after whitespace normalization" block emitted by
// buildNoMatchDiagnostics.
//
// Deliberately does NOT fall back to the "context:" anchor block. That block is a
// ±1-line window around the anchor, not the matched region, so comparing a
// multi-line old_string against it yields wrong line counts and misclassifies.
// Cases lacking the normalization block are reported as `unclassifiable` and must
// be read by hand — see the note on the breakdown below.
const extractShownFileText = (body) => {
	const lines = body.split("\n");
	const start = lines.findIndex((l) => l.includes("file text that matches after whitespace normalization"));
	if (start < 0) return null;
	const out = [];
	for (let i = start + 1; i < lines.length; i++) {
		if (!/^\s*\|/.test(lines[i])) break;
		out.push(lines[i].replace(/^\s*\|\s?/, ""));
	}
	return out.length ? out.join("\n") : null;
};

const stats = {
	sessions_scanned: 0,
	// `unclassifiable`: the error carried no normalization block (only the truncated
	// anchor context), so old_string cannot be compared automatically. These must be
	// read by hand. On the 2026-08-28 baseline there were 4 such cases; manual
	// inspection classified them as 2 uniform-indent, 1 blank-line-count, and 1
	// internal-whitespace. So the TRUE safely-applicable count on that baseline is 2,
	// not the 0 this script reports — `safely_auto_applicable_share_of_failures`
	// undercounts whenever `unclassifiable` is non-zero. Treat it as a lower bound.
	whitespace_variant_breakdown: {
		trailing_or_unicode: 0,
		indent_uniform: 0,
		indent_nonuniform: 0,
		indent_mixed: 0,
		blank_line_count: 0,
		internal_whitespace: 0,
		other: 0,
		unclassifiable: 0,
	},
	sessions_using_mutate: 0,
	calls: 0,
	operations: 0,
	failed_calls: 0,
	ops_never_attempted: 0,
	op_type_usage: {},
	failing_op_type: {},
	failure_class: {},
	per_model: {},
	optional_fields: { file_hash: 0, assert_present: 0, assert_absent: 0, dry_run: 0, allow_empty: 0, replace_all: 0 },
	batching: { single_op: 0, multi_op: 0, multi_file: 0 },
	retry_chains: {},
};

for (const file of readdirSync(DIR)) {
	if (!file.endsWith(".json") || file === "index.json") continue;
	let session;
	try {
		session = JSON.parse(readFileSync(join(DIR, file), "utf8"));
	} catch {
		continue;
	}
	if (SINCE && String(session.created_at || "") < SINCE) continue;
	stats.sessions_scanned++;
	const model = session.model || "unknown";
	let sessionUsedMutate = false;

	for (const generation of session.lineage?.generations || []) {
		const pending = new Map();
		let consecutiveFailures = 0;

		for (const message of generation.messages || []) {
			if (message.role === "assistant" && message.tool_calls) {
				for (const call of message.tool_calls) {
					if (call.name !== "mutate") continue;
					sessionUsedMutate = true;
					let args = call.arguments;
					if (typeof args === "string") {
						try {
							args = JSON.parse(args);
						} catch {
							args = {};
						}
					}
					const ops = args?.operations || [];
					pending.set(call.id, ops);
					stats.calls++;
					stats.operations += ops.length;
					stats.per_model[model] ??= { calls: 0, failed: 0 };
					stats.per_model[model].calls++;

					if (args?.dry_run) stats.optional_fields.dry_run++;
					for (const op of ops) {
						if (!op) continue;
						bump(stats.op_type_usage, op.type ?? "<missing>");
						if (op.file_hash) stats.optional_fields.file_hash++;
						if (op.assert_present) stats.optional_fields.assert_present++;
						if (op.assert_absent) stats.optional_fields.assert_absent++;
						if (op.allow_empty) stats.optional_fields.allow_empty++;
						if (op.replace_all) stats.optional_fields.replace_all++;
					}
					if (ops.length <= 1) stats.batching.single_op++;
					else {
						stats.batching.multi_op++;
						const paths = new Set(ops.map((o) => o && (o.path || o.from)).filter(Boolean));
						if (paths.size > 1) stats.batching.multi_file++;
					}
				}
			}

			if (message.role === "tool" && message.name === "mutate" && pending.has(message.tool_call_id)) {
				const body = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
				const failedCount = body.match(/"operations_failed":(\d+)/);
				const isEnvelopeError = !failedCount && (/^mutate: /.test(body.trim()) || /"error"/.test(body));
				const failed = (failedCount && Number(failedCount[1]) > 0) || isEnvelopeError;

				if (failed) {
					stats.failed_calls++;
					stats.per_model[model].failed++;
					consecutiveFailures++;
					const failClass = classify(body);
					bump(stats.failure_class, failClass);
					if (failClass === "no_match_whitespace_variant_exists") {
						// The result body is a JSON envelope; the human-readable error lives in
						// .output or .error. Parse it or the escaping makes classification wrong.
						let errorText = body;
						try {
							const parsed = JSON.parse(body);
							const candidate = parsed.output ?? parsed.error;
							if (typeof candidate === "string") errorText = candidate;
						} catch {
							/* body was already plain text */
						}
						const shown = extractShownFileText(errorText);
						const op = (pending.get(message.tool_call_id) || []).find((o) => o && typeof o.old_string === "string");
						if (shown && op) bump(stats.whitespace_variant_breakdown, classifyWhitespaceVariant(op.old_string, shown));
						else bump(stats.whitespace_variant_breakdown, "unclassifiable");
					}
					const named = body.match(/operation \d+ (\w+)/);
					bump(stats.failing_op_type, named ? named[1] : "<unknown>");
					const skipped = body.match(/"operations_skipped":(\d+)/);
					if (skipped) stats.ops_never_attempted += Number(skipped[1]);
				} else if (consecutiveFailures > 0) {
					bump(stats.retry_chains, consecutiveFailures);
					consecutiveFailures = 0;
				}
			}
		}
		if (consecutiveFailures > 0) bump(stats.retry_chains, consecutiveFailures);
	}
	if (sessionUsedMutate) stats.sessions_using_mutate++;
}

// ---- headline metrics the plan and issue gate on ----
const pct = (n, d) => (d ? ((100 * n) / d).toFixed(1) + "%" : "n/a");
const noMatchTotal = (stats.failure_class.no_match_whitespace_variant_exists || 0) + (stats.failure_class.no_match_other || 0);
const lineOps = ["line_replace", "delete_line", "insert_before", "insert_after"];
const lineOpFailures = lineOps.reduce((sum, t) => sum + (stats.failing_op_type[t] || 0), 0);
const schemaShapeFailures = (stats.failure_class.wrong_field_for_op || 0) + (stats.failure_class.unknown_op_type || 0);

const wv = stats.whitespace_variant_breakdown;
const safelyAutoApplicable = wv.trailing_or_unicode + wv.indent_uniform;

stats.headline = {
	call_failure_rate: pct(stats.failed_calls, stats.calls),
	// NOTE: this counts what steiner's LOOSE detector flags, which collapses all
	// whitespace. It is NOT the share a conservative matcher can recover — see
	// safely_auto_applicable_share below, which is the number to gate on.
	whitespace_variant_share_of_no_match: pct(stats.failure_class.no_match_whitespace_variant_exists || 0, noMatchTotal),
	// LOWER BOUND — undercounts by however many cases land in `unclassifiable`
	// (those need manual reading). See the note on whitespace_variant_breakdown.
	safely_auto_applicable_share_of_failures: pct(safelyAutoApplicable, stats.failed_calls),
	line_op_share_of_failures: pct(lineOpFailures, stats.failed_calls),
	schema_shape_share_of_failures: pct(schemaShapeFailures, stats.failed_calls),
	line_op_share_of_operations: pct(
		lineOps.reduce((sum, t) => sum + (stats.op_type_usage[t] || 0), 0),
		stats.operations,
	),
};
// NOTE ON DENOMINATORS: every headline share is failing-CALLS over failing-CALLS,
// or OPERATIONS over OPERATIONS — never mixed. An earlier draft divided failing
// calls naming line_replace by line_replace operation count; that ratio is
// meaningless and was removed. If you add a metric, keep both sides on the same
// unit or the before/after comparison is worthless.

if (AS_JSON) {
	console.log(JSON.stringify(stats, null, 2));
} else {
	const sorted = (o) => Object.entries(o).sort((a, b) => b[1] - a[1]);
	console.log(`sessions scanned: ${stats.sessions_scanned} (${stats.sessions_using_mutate} used mutate)${SINCE ? `  [since ${SINCE}]` : ""}`);
	console.log(`calls: ${stats.calls}  operations: ${stats.operations}  failed calls: ${stats.failed_calls} (${stats.headline.call_failure_rate})`);
	console.log(`operations never attempted (batch abort): ${stats.ops_never_attempted}`);
	console.log("\n== HEADLINE (the numbers the plan and issue gate on) ==");
	for (const [k, v] of Object.entries(stats.headline)) console.log(`  ${k.padEnd(42)} ${v}`);
	console.log("\n== op type usage ==");
	for (const [k, v] of sorted(stats.op_type_usage)) console.log(`  ${k.padEnd(22)} ${String(v).padEnd(6)} ${pct(v, stats.operations)}`);
	console.log("\n== failing op type (from result text) ==");
	for (const [k, v] of sorted(stats.failing_op_type)) console.log(`  ${k.padEnd(22)} ${v}`);
	console.log("\n== whitespace-variant failures, by what actually differs ==");
	for (const [k, v] of Object.entries(stats.whitespace_variant_breakdown)) console.log(`  ${k.padEnd(24)} ${v}`);
	console.log("  (only trailing_or_unicode + indent_uniform are safe to auto-apply;");
	console.log("   indent_nonuniform means the model got the NESTING wrong, which must not be auto-fixed)");
	console.log("  CAVEAT: steiner editing its own mutate diagnostics code puts these marker");
	console.log("  strings into successful diffs. Classification is gated on a failed call, so");
	console.log("  those do not leak in — do not remove that gate when editing this script.");
	console.log("\n== failure class ==");
	for (const [k, v] of sorted(stats.failure_class)) console.log(`  ${k.padEnd(38)} ${String(v).padEnd(5)} ${pct(v, stats.failed_calls)}`);
	console.log("\n== per model (>=8 calls) ==");
	for (const [k, v] of sorted(stats.per_model).filter(([, v]) => v.calls >= 8)) console.log(`  ${k.padEnd(22)} calls ${String(v.calls).padEnd(5)} failed ${String(v.failed).padEnd(4)} ${pct(v.failed, v.calls)}`);
	console.log("\n== optional field use (operations, except dry_run = calls) ==");
	for (const [k, v] of Object.entries(stats.optional_fields)) console.log(`  ${k.padEnd(22)} ${v}`);
	console.log("\n== batching ==");
	console.log(`  single-op ${stats.batching.single_op}  multi-op ${stats.batching.multi_op}  multi-file ${stats.batching.multi_file} (${pct(stats.batching.multi_file, stats.calls)} of all calls)`);
	console.log("\n== consecutive-failure chains (length: count) ==");
	for (const [k, v] of sorted(stats.retry_chains)) console.log(`  ${k}: ${v}`);
}
