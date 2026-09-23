// `mutate` mode for scripts/diagnostics.mjs: call/ops metrics, message
// grouping, failure reasons, the feature report and the cause taxonomy over
// the tool and provider diagnostics streams.
//
// The analysis functions are exported so scripts/diagnostics_mutate_test.mjs
// can drive them directly with fixture records and a stubbed git ancestry
// resolver; nothing here needs a real repository. Stdlib only.

import { execFileSync } from "node:child_process";

// REASONS are the three mutate replace failure reasons that carry a `match`
// feature block, and therefore the reasons the feature report splits on.
export const REASONS = ["no_match", "ambiguous_match", "stale_read"];

// COUNT_BUCKETS bucket both ops-per-call and ambiguous_match old_lines. The
// edges are the ones the #673 re-measure used, so numbers stay comparable.
export const COUNT_BUCKETS = ["1", "2-3", "4-5", "6-9", "10-19", "20+"];

export function countBucket(n) {
	if (n <= 1) return "1";
	if (n <= 3) return "2-3";
	if (n <= 5) return "4-5";
	if (n <= 9) return "6-9";
	if (n <= 19) return "10-19";
	return "20+";
}

// --------------------------------------------------------------- windows

// resolveAncestryViaGit answers whether baseline is an ancestor of sha. git
// exit 1 means "not an ancestor"; anything else (128) means git does not know
// the revision, which is an unknown window rather than a false.
export function resolveAncestryViaGit(baseline, sha) {
	try {
		execFileSync("git", ["merge-base", "--is-ancestor", baseline, sha], { stdio: "ignore" });
		return true;
	} catch (err) {
		return err && err.status === 1 ? false : null;
	}
}

// buildWindowResolver caches one ancestry answer per distinct sha, so a run of
// records sharing a build_sha costs a single git call.
export function buildWindowResolver(baselineSha, resolveAncestor = resolveAncestryViaGit) {
	const cache = new Map();
	const of = (sha) => {
		if (!baselineSha) return "all";
		if (!sha) return "unknown";
		if (cache.has(sha)) return cache.get(sha);
		const ancestor = resolveAncestor(baselineSha, sha);
		const name = ancestor === true ? "post_baseline" : ancestor === false ? "pre_baseline" : "unknown";
		cache.set(sha, name);
		return name;
	};
	return { of };
}

// -------------------------------------------------------------- grouping

const isMutate = (rec) => rec.kind === "tool" && rec.payload?.tool === "mutate";

// groupByRun merges every stream's records and orders each run by seq. The
// legacy rule needs provider records interleaved with tool records, so both
// are passed in here.
function groupByRun(recordSets) {
	const byRun = new Map();
	for (const records of recordSets) {
		for (const rec of records) {
			const runID = rec.run_id ?? "";
			if (!byRun.has(runID)) byRun.set(runID, []);
			byRun.get(runID).push(rec);
		}
	}
	for (const list of byRun.values()) list.sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
	return byRun;
}

// providerTimeline indexes each run's provider records by seq so model
// attribution can find the most recent model before a given tool record.
function providerTimeline(providerRecords) {
	const byRun = new Map();
	for (const rec of providerRecords) {
		if (rec.kind !== "provider") continue;
		const runID = rec.run_id ?? "";
		if (!byRun.has(runID)) byRun.set(runID, []);
		byRun.get(runID).push(rec);
	}
	for (const list of byRun.values()) list.sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
	return byRun;
}

function modelBefore(timeline, runID, seq) {
	let model = "";
	for (const rec of timeline.get(runID) ?? []) {
		if ((rec.seq ?? 0) > seq) break;
		if (rec.payload?.model) model = rec.payload.model;
	}
	return model;
}

function newMessage(runID, rec, grouping) {
	return {
		runID,
		source: rec.source ?? "",
		agentID: rec.agent_id ?? "",
		agentType: rec.agent_type ?? "",
		turn: rec.turn ?? 0,
		model: "",
		grouping,
		calls: [],
	};
}

// exactRunMessages applies the 5.1(6) rule: an assistant message is a maximal
// run of tool records with the same (run_id, source, agent_id), in seq order,
// with the same turn. A turn that repeats after /clear starts a new message
// rather than rejoining the earlier one.
function exactRunMessages(runID, records) {
	const messages = [];
	let current = null;
	let key = null;
	const flush = () => {
		if (current && current.calls.length) messages.push(current);
		current = null;
	};
	for (const rec of records) {
		if (rec.kind !== "tool") continue;
		const nextKey = `${rec.source ?? ""}\u0000${rec.agent_id ?? ""}\u0000${rec.turn ?? 0}`;
		if (!current || nextKey !== key) {
			flush();
			current = newMessage(runID, rec, "exact");
			key = nextKey;
		}
		if (isMutate(rec)) current.calls.push(rec);
	}
	flush();
	return messages;
}

// legacyRunMessages is the pre-attribution rule: the mutate records between
// two provider records form one message. Concurrent agents interleave in the
// same run, so this can merge their calls; it is approximate by construction.
function legacyRunMessages(runID, records) {
	const messages = [];
	let current = null;
	const flush = () => {
		if (current && current.calls.length) messages.push(current);
		current = null;
	};
	for (const rec of records) {
		if (rec.kind === "provider") {
			flush();
			continue;
		}
		if (!isMutate(rec)) continue;
		if (!current) current = newMessage(runID, rec, "legacy");
		current.calls.push(rec);
	}
	flush();
	return messages;
}

function assignModels(messages, timeline) {
	for (const m of messages) {
		const first = m.calls[0];
		m.model = first?.payload?.model || modelBefore(timeline, m.runID, first?.seq ?? 0);
	}
	return messages;
}

// groupMessagesExact forces the exact rule for every run.
export function groupMessagesExact(toolRecords, providerRecords) {
	const timeline = providerTimeline(providerRecords);
	const messages = [];
	for (const [runID, records] of groupByRun([toolRecords, providerRecords])) {
		messages.push(...exactRunMessages(runID, records));
	}
	return assignModels(messages, timeline);
}

// groupMessagesLegacy forces the legacy rule for every run.
export function groupMessagesLegacy(toolRecords, providerRecords) {
	const timeline = providerTimeline(providerRecords);
	const messages = [];
	for (const [runID, records] of groupByRun([toolRecords, providerRecords])) {
		messages.push(...legacyRunMessages(runID, records));
	}
	return assignModels(messages, timeline);
}

// groupMessages picks the rule per run: a run whose tool records carry `turn`
// gets exact grouping, one without it keeps the legacy rule. A fixture set
// can hold both, so the choice is made per run rather than per invocation.
export function groupMessages(toolRecords, providerRecords) {
	const timeline = providerTimeline(providerRecords);
	const messages = [];
	for (const [runID, records] of groupByRun([toolRecords, providerRecords])) {
		const attributed = records.some((rec) => rec.kind === "tool" && typeof rec.turn === "number");
		const runMessages = attributed ? exactRunMessages(runID, records) : legacyRunMessages(runID, records);
		messages.push(...runMessages);
	}
	return assignModels(messages, timeline);
}

// --------------------------------------------------------------- metrics

export function callFailed(rec) {
	const p = rec.payload ?? {};
	return (p.ops_failed ?? 0) > 0 || p.outcome !== "ok";
}

function ratio(n, d) {
	return d > 0 ? n / d : null;
}

function quantile(values, q) {
	const s = [...values].sort((a, b) => a - b);
	if (!s.length) return null;
	return s[Math.min(s.length - 1, Math.floor(q * s.length))];
}

// failureChains counts runs of consecutive mutate-messages containing any
// failure, per agent stream. One stream is (run_id, source, agent_id): a
// parent and two sub-agents can share a run and a turn number.
export function failureChains(messages) {
	const streams = new Map();
	for (const m of messages) {
		const key = `${m.runID}\u0000${m.source}\u0000${m.agentID}`;
		if (!streams.has(key)) streams.set(key, []);
		streams.get(key).push(m);
	}
	const chains = [];
	for (const list of streams.values()) {
		let len = 0;
		for (const m of list) {
			if (m.calls.some(callFailed)) {
				len++;
			} else if (len) {
				chains.push(len);
				len = 0;
			}
		}
		if (len) chains.push(len);
	}
	return chains;
}

// windowMetrics aggregates one set of messages. Reasons are counted per call
// (calls with at least one failure of the reason) because since #722 a single
// call emits several reasons, and raw occurrence counts mislead; the raw count
// is kept as a secondary column.
export function windowMetrics(messages) {
	const calls = messages.flatMap((m) => m.calls);
	const opsList = calls.map((c) => c.payload?.ops_total ?? 0);
	const ops = opsList.reduce((a, b) => a + b, 0);
	const failed = calls.filter(callFailed);
	const appliedOps = calls.filter((c) => !callFailed(c)).reduce((a, c) => a + (c.payload?.ops_total ?? 0), 0);
	const discarded = failed.reduce((a, c) => a + (c.payload?.ops_total ?? 0), 0);

	const callsWithReason = new Map();
	const rawReasons = new Map();
	for (const c of calls) {
		const seen = new Set();
		for (const f of c.payload?.failures ?? []) {
			const reason = f.reason ?? "unknown";
			rawReasons.set(reason, (rawReasons.get(reason) ?? 0) + 1);
			seen.add(reason);
		}
		for (const reason of seen) callsWithReason.set(reason, (callsWithReason.get(reason) ?? 0) + 1);
	}

	const buckets = new Map();
	for (const c of calls) {
		const key = countBucket(c.payload?.ops_total ?? 0);
		const b = buckets.get(key) ?? { calls: 0, ops: 0, failedCalls: 0, failedOps: 0 };
		b.calls++;
		b.ops += c.payload?.ops_total ?? 0;
		if (callFailed(c)) {
			b.failedCalls++;
			b.failedOps += c.payload?.ops_failed ?? 0;
		}
		buckets.set(key, b);
	}

	const cpmHistogram = {};
	for (const m of messages) cpmHistogram[m.calls.length] = (cpmHistogram[m.calls.length] ?? 0) + 1;
	const multiCallMessages = messages.filter((m) => m.calls.length > 1).length;

	const chains = failureChains(messages);

	return {
		calls: calls.length,
		ops,
		opsPerCallMean: ratio(ops, calls.length),
		opsP50: quantile(opsList, 0.5),
		opsP90: quantile(opsList, 0.9),
		opsP99: quantile(opsList, 0.99),
		opsMax: opsList.length ? Math.max(...opsList) : 0,
		callsGe20: opsList.filter((n) => n >= 20).length,
		failedCalls: failed.length,
		callFailRate: ratio(failed.length, calls.length),
		messages: messages.length,
		mutateCallsPerMessage: ratio(calls.length, messages.length),
		msgsWithMultipleCalls: multiCallMessages,
		msgsWithMultipleCallsShare: ratio(multiCallMessages, messages.length),
		cpmHistogram,
		opsDiscardedPerFailedCall: ratio(discarded, failed.length),
		opsDiscardedPerAppliedOp: ratio(discarded, appliedOps),
		failedCallsGt1Failure: failed.filter((c) => (c.payload?.ops_failed ?? 0) > 1).length,
		mutateMessagesPerAppliedOp: ratio(messages.length, appliedOps),
		chainCount: chains.length,
		chainMean: ratio(chains.reduce((a, b) => a + b, 0), chains.length),
		chainsGe2Share: ratio(chains.filter((c) => c >= 2).length, chains.length),
		opsBuckets: COUNT_BUCKETS.filter((key) => buckets.has(key)).map((key) => {
			const b = buckets.get(key);
			const callFailRate = ratio(b.failedCalls, b.calls);
			return {
				key,
				calls: b.calls,
				ops: b.ops,
				callFailRate,
				hazard: callFailRate === null || b.ops === 0 ? null : 1 - Math.pow(1 - callFailRate, b.calls / b.ops),
			};
		}),
		reasons: [...new Set([...callsWithReason.keys(), ...rawReasons.keys()])]
			.map((reason) => ({
				key: reason,
				calls: callsWithReason.get(reason) ?? 0,
				pct: ratio(callsWithReason.get(reason) ?? 0, calls.length),
				raw: rawReasons.get(reason) ?? 0,
			}))
			.sort((a, b) => b.calls - a.calls || b.raw - a.raw),
	};
}

// -------------------------------------------------------- feature report

function histogram(values) {
	const out = {};
	for (const v of values) out[v] = (out[v] ?? 0) + 1;
	return out;
}

function bucketed(values, bucketFn) {
	const out = {};
	for (const v of values) {
		const key = bucketFn(v);
		out[key] = (out[key] ?? 0) + 1;
	}
	return out;
}

function rate(rows, predicate) {
	return rows.length ? rows.filter(predicate).length / rows.length : null;
}

// turnsBucket keeps -1 (not observed) separate from 0: folding the unknown
// case into the 0 bucket would report never-read failures as read this turn.
function turnsBucket(v) {
	if (v < 0) return "unknown";
	if (v === 0) return "0";
	if (v === 1) return "1";
	if (v <= 5) return "2-5";
	if (v <= 20) return "6-20";
	return ">20";
}

function ratioBucket(r) {
	if (r === 0) return "0";
	if (r < 0.5) return "(0,0.5)";
	if (r < 1) return "[0.5,1)";
	return "1";
}

function exactPrefixBucket(f) {
	const v = f.match.exact_prefix_lines;
	if (v === -1) return "-1";
	if (v === 0) return "0";
	return v >= (f.match.old_lines ?? 0) ? "all" : "partial";
}

function featureSummary(rows) {
	return {
		n: rows.length,
		read_state: histogram(rows.map((f) => f.match.read_state)),
		ws_kind: histogram(rows.map((f) => f.match.ws_kind)),
		in_read_range: histogram(rows.map((f) => f.match.in_read_range)),
		turns_since_read: bucketed(rows.map((f) => f.match.turns_since_read), turnsBucket),
		lines_found_ratio: bucketed(rows, (f) => ratioBucket(ratio(f.match.lines_found, f.match.nonblank_lines) ?? 0)),
		longest_run_ratio: bucketed(rows, (f) => ratioBucket(ratio(f.match.longest_run, f.match.nonblank_lines) ?? 0)),
		exact_prefix_lines: bucketed(rows, exactPrefixBucket),
		rates: {
			tabs_vs_spaces: rate(rows, (f) => f.match.tabs_vs_spaces === true),
			crlf_mismatch: rate(rows, (f) => f.match.crlf_mismatch === true),
			unescape_matches: rate(rows, (f) => f.match.unescape_matches === true),
			line_prefix: rate(rows, (f) => f.match.line_prefix === true),
			matches_original: rate(rows, (f) => f.match.matches_original === true),
			file_hash_supplied: rate(rows, (f) => f.match.file_hash_supplied === true),
			truncated: rate(rows, (f) => f.match.truncated === true),
		},
		match_count_gt0_share: rate(rows, (f) => (f.match.match_count ?? 0) > 0),
		match_count: histogram(rows.map((f) => f.match.match_count)),
		old_lines: histogram(rows.map((f) => f.match.old_lines)),
	};
}

// featureReport covers failures that carry a `match` block only: that block is
// the new-instrumentation marker, so the report is the featured subset.
export function featureReport(calls) {
	const failures = calls.flatMap((c) => (c.payload?.failures ?? []).filter((f) => f.match));
	const byReason = {};
	for (const reason of REASONS) byReason[reason] = featureSummary(failures.filter((f) => f.reason === reason));
	return { total: failures.length, by_reason: byReason };
}

// ------------------------------------------------------------- taxonomy

// taxonomyBucket assigns the first matching bucket, in the plan's order. It is
// deliberately a script concern: the interpretation can change without a new
// binary. Failures without a `match` block have no bucket.
export function taxonomyBucket(failure) {
	const f = failure?.match;
	if (!f) return null;
	if (failure.reason === "ambiguous_match") return `ambiguous:${countBucket(f.old_lines ?? 0)}`;
	if (f.matches_original) return "forgot_own_edit_in_call";
	// Concrete differences outrank read state: a read-state bucket says when the
	// model last saw the file, not why the bytes differ now.
	if (f.ws_kind && f.ws_kind !== "none") return `whitespace:${f.ws_kind}`;
	if (f.crlf_mismatch) return "encoding:crlf";
	if (f.unescape_matches) return "encoding:escaped";
	if (f.line_prefix) return "encoding:line_prefix";
	if (f.read_state === "self_mutated") return "stale_after_own_edit";
	if (f.read_state === "external_change") return "changed_externally";
	if (f.read_state === "never_read") return "never_read";
	if (f.read_state === "pruned") return "read_pruned";
	if (f.in_read_range === "no") return "outside_read_range";
	if (f.lines_found === f.nonblank_lines && f.nonblank_lines > 0) return "lines_real_sequence_wrong";
	if (f.lines_found === 0) return "fabricated_or_wrong_file";
	return "partial_recall";
}

// taxonomyRows keys rows as "<group> | <bucket>" so a bucket's share stays
// readable within its model or agent type, and percentages use that group's
// failure total as the denominator.
export function taxonomyRows(messages, keyFn) {
	const totals = new Map();
	const counts = new Map();
	for (const m of messages) {
		const group = keyFn(m);
		for (const c of m.calls) {
			for (const f of c.payload?.failures ?? []) {
				const bucket = taxonomyBucket(f);
				if (!bucket) continue;
				totals.set(group, (totals.get(group) ?? 0) + 1);
				const key = `${group}\u0000${bucket}`;
				counts.set(key, (counts.get(key) ?? 0) + 1);
			}
		}
	}
	return [...counts.entries()]
		.map(([key, n]) => {
			const [group, bucket] = key.split("\u0000");
			return { key: `${group} | ${bucket}`, n, metrics: { count: n, pct: ratio(n, totals.get(group)) } };
		})
		.sort((a, b) => a.key.localeCompare(b.key));
}

// -------------------------------------------------------------- samples

// collectSamples returns the bounded raw bodies recorded under capture_bodies,
// optionally filtered to one taxonomy bucket. This is how the resume step
// reads raw cases before acting on a derived bucket.
export function collectSamples(messages, { cause = null, limit = 0 } = {}) {
	const rows = [];
	for (const m of messages) {
		for (const c of m.calls) {
			for (const f of c.payload?.failures ?? []) {
				if (!f.sample) continue;
				const bucket = taxonomyBucket(f);
				if (cause && bucket !== cause) continue;
				rows.push({
					run_id: m.runID,
					ts: c.ts,
					model: m.model,
					agent_type: m.agentType,
					reason: f.reason,
					cause: bucket,
					sample: f.sample,
					match: f.match ?? null,
				});
			}
		}
	}
	rows.sort((a, b) => String(a.ts ?? "").localeCompare(String(b.ts ?? "")));
	return limit > 0 ? rows.slice(0, limit) : rows;
}

// ---------------------------------------------------------------- analyze

export function isFeaturedCall(call) {
	return (call.payload?.failures ?? []).some((f) => f.match);
}

export function restrictMessages(messages, predicate) {
	return messages
		.map((m) => ({ ...m, calls: m.calls.filter(predicate) }))
		.filter((m) => m.calls.length > 0);
}

function groupRows(messages, keyFn) {
	const groups = new Map();
	for (const m of messages) {
		const key = keyFn(m);
		if (!groups.has(key)) groups.set(key, []);
		groups.get(key).push(m);
	}
	return [...groups.entries()]
		.sort((a, b) => b[1].flatMap((m) => m.calls).length - a[1].flatMap((m) => m.calls).length)
		.map(([key, msgs]) => ({ key, n: msgs.flatMap((m) => m.calls).length, metrics: windowMetrics(msgs) }));
}

function windowReport(messages) {
	const calls = messages.flatMap((m) => m.calls);
	return {
		messages: messages.length,
		calls: calls.length,
		metrics: windowMetrics(messages),
		by_model: groupRows(messages, (m) => m.model || "(unknown)"),
		by_agent_type: groupRows(messages, (m) => m.agentType || "(top-level)"),
		features: featureReport(calls),
		taxonomy_by_model: taxonomyRows(messages, (m) => m.model || "(unknown)"),
		taxonomy_by_agent_type: taxonomyRows(messages, (m) => m.agentType || "(top-level)"),
	};
}

// analyze builds the report and returns the grouped messages alongside it, so
// the samples listing can reuse the same grouping and filters.
export function analyze({ toolRecords = [], providerRecords = [], baselineSha = null, excludeRun = null, model = null, resolveAncestor = resolveAncestryViaGit }) {
	const tools = toolRecords.filter((r) => isMutate(r) && (!excludeRun || r.run_id !== excludeRun));
	const providers = providerRecords.filter((r) => !excludeRun || r.run_id !== excludeRun);
	let messages = groupMessages(tools, providers);
	if (model) messages = messages.filter((m) => (m.model ?? "").startsWith(model));

	const resolver = buildWindowResolver(baselineSha, resolveAncestor);
	const windowOf = (m) => resolver.of(m.calls[0]?.build_sha ?? "");

	const names = baselineSha ? ["pre_baseline", "post_baseline", "unknown"] : ["all"];
	const windows = {};
	for (const name of names) windows[name] = windowReport(messages.filter((m) => windowOf(m) === name));
	windows.featured = windowReport(restrictMessages(messages, isFeaturedCall));

	const groupings = new Set(messages.map((m) => m.grouping));
	const grouping = groupings.size === 0 ? "exact" : groupings.size === 1 ? [...groupings][0] : "mixed";
	return { report: { baseline_sha: baselineSha, grouping, windows }, messages };
}

// ----------------------------------------------------------------- render

const fnum = (v) => (v === null || v === undefined ? "n/a" : v.toFixed(2));
const fpct = (v) => (v === null || v === undefined ? "n/a" : (v * 100).toFixed(1) + "%");
const fint = (v) => (v === null || v === undefined ? "n/a" : String(v));

const GROUP_COLUMNS = [
	{ name: "calls", value: (r) => r.metrics.calls, fmt: fint },
	{ name: "ops", value: (r) => r.metrics.ops, fmt: fint },
	{ name: "ops/call", value: (r) => r.metrics.opsPerCallMean, fmt: fnum },
	{ name: "fail_rate", value: (r) => r.metrics.callFailRate, fmt: fpct },
	{ name: "calls>=20", value: (r) => r.metrics.callsGe20, fmt: fint },
	{ name: "msgs", value: (r) => r.metrics.messages, fmt: fint },
	{ name: "calls/msg", value: (r) => r.metrics.mutateCallsPerMessage, fmt: fnum },
	{ name: "msgs_multi", value: (r) => r.metrics.msgsWithMultipleCallsShare, fmt: fpct },
	{ name: "disc/fail", value: (r) => r.metrics.opsDiscardedPerFailedCall, fmt: fnum },
	{ name: "disc/applied", value: (r) => r.metrics.opsDiscardedPerAppliedOp, fmt: fnum },
	{ name: "chains>=2", value: (r) => r.metrics.chainsGe2Share, fmt: fpct },
];

const BUCKET_COLUMNS = [
	{ name: "ops", value: (r) => r.metrics.ops, fmt: fint },
	{ name: "call_fail", value: (r) => r.metrics.callFailRate, fmt: fpct },
	{ name: "hazard", value: (r) => r.metrics.hazard, fmt: fpct },
];

const REASON_COLUMNS = [
	{ name: "pct_of_calls", value: (r) => r.metrics.pct, fmt: fpct },
	{ name: "raw_count", value: (r) => r.metrics.raw, fmt: fint },
];

const TAXONOMY_COLUMNS = [
	{ name: "count", value: (r) => r.metrics.count, fmt: fint },
	{ name: "pct", value: (r) => r.metrics.pct, fmt: fpct },
];

function groupingLabel(grouping) {
	if (grouping === "legacy") return "legacy grouping (approximate)";
	if (grouping === "mixed") return "mixed grouping (exact + legacy grouping (approximate))";
	return "exact grouping";
}

function formatHistogram(h) {
	const parts = Object.entries(h ?? {}).map(([k, v]) => `${k}=${v}`);
	return parts.length ? parts.join(" ") : "(none)";
}

function capRows(rows, top) {
	return top > 0 ? rows.slice(0, top) : rows;
}

function renderMetrics(metrics) {
	console.log(`  calls=${metrics.calls} ops=${metrics.ops} ops/call=${fnum(metrics.opsPerCallMean)} p50=${fint(metrics.opsP50)} p90=${fint(metrics.opsP90)} p99=${fint(metrics.opsP99)} max=${metrics.opsMax} calls>=20=${metrics.callsGe20}`);
	console.log(`  call_fail_rate=${fpct(metrics.callFailRate)} failed_calls=${metrics.failedCalls} failed_calls_gt1_failure=${metrics.failedCallsGt1Failure}`);
	console.log(`  messages=${metrics.messages} mutate_calls/message=${fnum(metrics.mutateCallsPerMessage)} msgs_multi_call=${metrics.msgsWithMultipleCalls} (${fpct(metrics.msgsWithMultipleCallsShare)}) cpm_histogram=${formatHistogram(metrics.cpmHistogram)}`);
	console.log(`  ops_discarded/failed_call=${fnum(metrics.opsDiscardedPerFailedCall)} ops_discarded/applied_op=${fnum(metrics.opsDiscardedPerAppliedOp)} mutate_msgs/applied_op=${fnum(metrics.mutateMessagesPerAppliedOp)}`);
	console.log(`  failure_chains n=${metrics.chainCount} mean=${fnum(metrics.chainMean)} chains>=2=${fpct(metrics.chainsGe2Share)}`);
}

function renderFeatures(title, features) {
	console.log(`\n== ${title} feature report (featured subset only)`);
	for (const reason of REASONS) {
		const s = features.by_reason[reason];
		console.log(`\n  ${reason}  n=${s.n}`);
		if (s.n === 0) {
			console.log("    (no features)");
			continue;
		}
		console.log(`    read_state           ${formatHistogram(s.read_state)}`);
		console.log(`    ws_kind              ${formatHistogram(s.ws_kind)}`);
		console.log(`    in_read_range        ${formatHistogram(s.in_read_range)}`);
		console.log(`    turns_since_read     ${formatHistogram(s.turns_since_read)}`);
		console.log(`    lines_found/nonblank ${formatHistogram(s.lines_found_ratio)}`);
		console.log(`    longest_run/nonblank ${formatHistogram(s.longest_run_ratio)}`);
		console.log(`    exact_prefix_lines   ${formatHistogram(s.exact_prefix_lines)}`);
		console.log(`    rates                ${Object.entries(s.rates).map(([k, v]) => `${k}=${fpct(v)}`).join(" ")}`);
		if (reason === "stale_read") console.log(`    match_count>0        ${fpct(s.match_count_gt0_share)}`);
		if (reason === "ambiguous_match") {
			console.log(`    match_count          ${formatHistogram(s.match_count)}`);
			console.log(`    old_lines            ${formatHistogram(s.old_lines)}`);
		}
	}
}

export function renderReport(report, { printTable, top = 20 } = {}) {
	console.log(`mutate analysis (${groupingLabel(report.grouping)}${report.baseline_sha ? `, baseline ${report.baseline_sha}` : ""})`);
	for (const [name, w] of Object.entries(report.windows)) {
		console.log(`\n== mutate ${name}`);
		renderMetrics(w.metrics);
		printTable(`mutate ${name} by model`, capRows(w.by_model, top), GROUP_COLUMNS);
		printTable(`mutate ${name} by agent_type`, capRows(w.by_agent_type, top), GROUP_COLUMNS);
		printTable(`mutate ${name} ops buckets`, w.metrics.opsBuckets.map((b) => ({ key: b.key, n: b.calls, metrics: b })), BUCKET_COLUMNS);
		printTable(`mutate ${name} reasons (per call; raw_count secondary)`, w.metrics.reasons.map((r) => ({ key: r.key, n: r.calls, metrics: r })), REASON_COLUMNS);
		renderFeatures(`mutate ${name}`, w.features);
		console.log(`\nderived buckets; read raw samples (\`sample\`) before acting`);
		printTable(`mutate ${name} taxonomy by model`, capRows(w.taxonomy_by_model, top), TAXONOMY_COLUMNS);
		printTable(`mutate ${name} taxonomy by agent_type`, capRows(w.taxonomy_by_agent_type, top), TAXONOMY_COLUMNS);
	}
}

export function renderSamples(rows, { printTable } = {}) {
	console.log(`\n== mutate samples (${rows.length})`);
	if (rows.length === 0) {
		console.log("(no capture_bodies samples)");
		return;
	}
	for (const row of rows) {
		const s = row.sample ?? {};
		console.log(`\n  [${row.ts ?? "?"}] ${row.run_id} ${row.model || "(unknown)"} ${row.agent_type || "(top-level)"} ${row.reason} -> ${row.cause}`);
		console.log(`    path: ${s.path ?? "?"}  region_start_line=${s.region_start_line ?? 0}`);
		console.log(`    old_string: ${JSON.stringify(s.old_string ?? "")}${s.old_truncated ? " (truncated)" : ""}`);
		console.log(`    region: ${JSON.stringify(s.region ?? "")}${s.region_truncated ? " (truncated)" : ""}`);
		const m = row.match ?? {};
		console.log(`    match: read_state=${m.read_state} ws_kind=${m.ws_kind} in_read_range=${m.in_read_range} lines_found=${m.lines_found}/${m.nonblank_lines} match_count=${m.match_count}`);
	}
	void printTable;
}

// ------------------------------------------------------------- entrypoint

export function parseMutateArgs(args) {
	const argOf = (name, fallback) => {
		const i = args.indexOf(name);
		return i >= 0 && args[i + 1] ? args[i + 1] : fallback;
	};
	return {
		baselineSha: argOf("--baseline-sha", null),
		excludeRun: argOf("--exclude-run", null),
		model: argOf("--model", null),
		samples: Number.parseInt(argOf("--samples", "0"), 10),
		cause: argOf("--cause", null),
	};
}

export function runMutate({ toolRecords = [], providerRecords = [], args = [], asJson = false, top = 20, printTable, resolveAncestor = resolveAncestryViaGit }) {
	const opts = parseMutateArgs(args);
	const { report, messages } = analyze({
		toolRecords,
		providerRecords,
		baselineSha: opts.baselineSha,
		excludeRun: opts.excludeRun,
		model: opts.model,
		resolveAncestor,
	});
	if (opts.samples > 0) report.samples = collectSamples(messages, { cause: opts.cause, limit: opts.samples });

	if (asJson) {
		console.log(JSON.stringify(report, null, 1));
		return report;
	}
	renderReport(report, { printTable, top });
	if (opts.samples > 0) renderSamples(report.samples, { printTable });
	return report;
}
