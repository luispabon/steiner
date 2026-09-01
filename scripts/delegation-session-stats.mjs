#!/usr/bin/env node
// Mines steiner session transcripts for sub-agent (delegation) wall-clock and
// token behaviour. Companion to mutate-session-stats.mjs.
//
// Usage:
//   node scripts/delegation-session-stats.mjs [--dir <sessions dir>] [--since YYYY-MM-DD] [--json]
//
// Defaults to ~/.config/steiner/sessions.
//
// A DELEGATION is one assistant tool_call whose name is a sub-agent tool
// (explore, code, review, ...) paired by tool_call_id with the following
// role:"tool" message whose content parses as a delegation result envelope
// (has agent_id + trace).
//
// Wall clock comes from the result's trace[] timestamps: first entry (phase
// "start") to last entry. Phase deltas isolate what sits between the child
// finishing (child_run_complete) and the parent getting its result
// (result_final) -- that span is fixed overhead on every delegation.
//
// BATCH SIZE is the number of sub-agent tool_calls in the SAME assistant
// message. Batch size 1 means the parent paid the full duration serially;
// batch size N means N children could overlap.

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
const UNTIL = argOf("--until", null);
const AS_JSON = argv.includes("--json");

const SUB_TOOLS = new Set([
	"explore",
	"code",
	"review",
	"research",
	"verify",
	"evaluate",
	"sanity_check",
	"plan",
	"vision",
	"follow_up",
]);

const rows = [];
const batches = [];

for (const file of readdirSync(DIR)) {
	if (!file.endsWith(".json")) continue;
	let session;
	try {
		session = JSON.parse(readFileSync(join(DIR, file), "utf8"));
	} catch {
		continue;
	}
	if (Array.isArray(session)) continue;
	const created = session.created_at ?? "";
	if (SINCE && created.slice(0, 10) < SINCE) continue;
	if (UNTIL && created.slice(0, 10) > UNTIL) continue;
	const model = session.model ?? "unknown";

	for (const gen of session.lineage?.generations ?? []) {
		const messages = gen.messages ?? [];
		const calls = new Map(); // tool_call_id -> {name, batchSize, argLen}
		for (const m of messages) {
			if (!m || typeof m !== "object") continue;
			const tcs = (m.tool_calls ?? []).filter((tc) => SUB_TOOLS.has(tc?.function?.name ?? tc?.name));
			if (tcs.length === 0) continue;
			const names = tcs.map((tc) => tc?.function?.name ?? tc?.name);
			batches.push({ session: file, model, size: tcs.length, names });
			for (const tc of tcs) {
				const name = tc?.function?.name ?? tc?.name;
				const args = tc?.arguments ?? tc?.function?.arguments;
				calls.set(tc.id, {
					name,
					batchSize: tcs.length,
					argChars: typeof args === "string" ? args.length : JSON.stringify(args ?? {}).length,
				});
			}
		}
		for (const m of messages) {
			if (!m || m.role !== "tool" || !calls.has(m.tool_call_id)) continue;
			let r;
			try {
				r = JSON.parse(m.content);
			} catch {
				continue;
			}
			if (!r || typeof r !== "object" || !Array.isArray(r.trace)) continue;
			const call = calls.get(m.tool_call_id);

			const t = (phase) => {
				const e = r.trace.find((x) => x.phase === phase);
				return e ? Date.parse(e.time) : null;
			};
			const first = Date.parse(r.trace[0].time);
			const last = Date.parse(r.trace[r.trace.length - 1].time);
			const childDone = t("child_run_complete");
			const extensions = r.trace.filter((x) => x.phase === "extension").length;

			rows.push({
				session: file,
				created,
				model,
				agent: call.name,
				batchSize: call.batchSize,
				status: r.status,
				turns: r.turn_count ?? 0,
				toolCalls: r.tool_call_count ?? 0,
				outTokens: r.token_count ?? 0,
				inTokens: r.input_tokens ?? 0,
				cacheRead: r.cache_read_tokens ?? 0,
				extensions,
				followUps: r.follow_up_count ?? 0,
				worktree: Boolean(r.worktree_path),
				taskChars: call.argChars,
				outputChars: (r.output ?? "").length,
				summaryChars: (r.summary ?? "").length,
				wallMs: last - first,
				// Everything after the child's last provider turn: summarisation,
				// extension checks, status mapping, projection.
				postMs: childDone != null ? last - childDone : null,
			});
		}
	}
}

// ---------------------------------------------------------------- aggregation

const num = (xs) => xs.filter((x) => typeof x === "number" && Number.isFinite(x));
const sum = (xs) => num(xs).reduce((a, b) => a + b, 0);
const mean = (xs) => (num(xs).length ? sum(xs) / num(xs).length : 0);
const pct = (xs, p) => {
	const s = num(xs).sort((a, b) => a - b);
	return s.length ? s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))] : 0;
};
const groupBy = (xs, key) => {
	const m = new Map();
	for (const x of xs) {
		const k = key(x);
		if (!m.has(k)) m.set(k, []);
		m.get(k).push(x);
	}
	return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
};

const table = (title, entries, cols) => {
	const header = ["key", "n", ...cols.map((c) => c[0])];
	const body = entries.map(([k, xs]) => [String(k), String(xs.length), ...cols.map((c) => c[1](xs))]);
	const widths = header.map((h, i) => Math.max(h.length, ...body.map((r) => r[i].length)));
	const line = (r) => r.map((v, i) => v.padEnd(widths[i])).join("  ");
	console.log(`\n== ${title}`);
	console.log(line(header));
	console.log(widths.map((w) => "-".repeat(w)).join("  "));
	for (const r of body) console.log(line(r));
};

const s1 = (n) => (n / 1000).toFixed(1) + "s";
const COLS = [
	["wall_mean", (xs) => s1(mean(xs.map((r) => r.wallMs)))],
	["wall_p50", (xs) => s1(pct(xs.map((r) => r.wallMs), 50))],
	["wall_p90", (xs) => s1(pct(xs.map((r) => r.wallMs), 90))],
	["post_mean", (xs) => s1(mean(xs.map((r) => r.postMs)))],
	["turns", (xs) => mean(xs.map((r) => r.turns)).toFixed(1)],
	["s/turn", (xs) => (mean(xs.map((r) => r.wallMs)) / Math.max(0.01, mean(xs.map((r) => r.turns))) / 1000).toFixed(1)],
	["tools", (xs) => mean(xs.map((r) => r.toolCalls)).toFixed(1)],
	["ext%", (xs) => ((100 * xs.filter((r) => r.extensions > 0).length) / xs.length).toFixed(0)],
	["in_tok", (xs) => Math.round(mean(xs.map((r) => r.inTokens))).toString()],
	["cache%", (xs) => {
		const i = sum(xs.map((r) => r.inTokens));
		return i ? ((100 * sum(xs.map((r) => r.cacheRead))) / i).toFixed(0) : "-";
	}],
	["out_tok", (xs) => Math.round(mean(xs.map((r) => r.outTokens))).toString()],
];

if (AS_JSON) {
	console.log(JSON.stringify({ rows, batches }, null, 1));
} else {
	console.log(`delegations: ${rows.length}   sessions with any: ${new Set(rows.map((r) => r.session)).size}`);
	console.log(`window: ${SINCE ?? "all"} .. ${UNTIL ?? "now"}   dir: ${DIR}`);
	console.log(`total child wall clock: ${(sum(rows.map((r) => r.wallMs)) / 60000).toFixed(1)} min`);
	console.log(
		`post-child overhead: ${(sum(rows.map((r) => r.postMs)) / 60000).toFixed(1)} min ` +
			`(${((100 * sum(rows.map((r) => r.postMs))) / Math.max(1, sum(rows.map((r) => r.wallMs)))).toFixed(1)}% of child wall clock)`,
	);

	table("by agent type", groupBy(rows, (r) => r.agent), COLS);
	table("by parent model", groupBy(rows, (r) => r.model).filter(([, xs]) => xs.length >= 5), COLS);
	table("by status", groupBy(rows, (r) => r.status), COLS);
	table("by extension count", groupBy(rows, (r) => `ext=${r.extensions}`), COLS);
	table("by month", groupBy(rows, (r) => r.created.slice(0, 7)), COLS);

	// Dispatch shape: how many children were launched per assistant message.
	const bySize = groupBy(batches, (b) => `batch=${b.size}`);
	console.log("\n== dispatch batch size (sub-agent calls per assistant message)");
	const totalCalls = sum(batches.map((b) => b.size));
	for (const [k, xs] of bySize) {
		const calls = sum(xs.map((b) => b.size));
		console.log(`${k}  messages=${xs.length}  calls=${calls}  (${((100 * calls) / totalCalls).toFixed(1)}% of all child calls)`);
	}

	// Serialisation cost: wall clock the parent actually waited, assuming a batch
	// runs concurrently (max) vs. what it would cost serially (sum).
	const byMsg = new Map();
	for (const r of rows) {
		const k = `${r.session}|${r.agent}|${r.batchSize}`;
		if (!byMsg.has(k)) byMsg.set(k, []);
		byMsg.get(k).push(r);
	}
	console.log("\n== task prompt / result sizes (chars)");
	for (const [agent, xs] of groupBy(rows, (r) => r.agent)) {
		console.log(
			`${agent.padEnd(14)} n=${String(xs.length).padStart(4)}  task=${Math.round(mean(xs.map((r) => r.taskChars)))}` +
				`  output=${Math.round(mean(xs.map((r) => r.outputChars)))}  summary=${Math.round(mean(xs.map((r) => r.summaryChars)))}`,
		);
	}

	// Children that burned turns without producing tool calls -- pure talk.
	const idle = rows.filter((r) => r.turns >= 3 && r.toolCalls === 0);
	console.log(`\nchildren with >=3 turns and 0 tool calls: ${idle.length} (${((100 * idle.length) / rows.length).toFixed(1)}%)`);
	const zeroCache = rows.filter((r) => r.inTokens > 0 && r.cacheRead === 0);
	console.log(
		`children with input tokens but zero cache read: ${zeroCache.length}/${rows.filter((r) => r.inTokens > 0).length}` +
			`  (mean in_tok ${Math.round(mean(zeroCache.map((r) => r.inTokens)))})`,
	);
}
